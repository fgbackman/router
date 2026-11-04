package dhcp

import (
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fgbackman/router/internal/config"
)

// Lease is a committed or static address assignment.
type Lease struct {
	MAC      string     `json:"mac"`
	IP       netip.Addr `json:"ip"`
	Hostname string     `json:"hostname,omitempty"`
	Expires  time.Time  `json:"expires"`
	Static   bool       `json:"static,omitempty"`
}

const (
	offerHold   = time.Minute
	declineHold = 10 * time.Minute
)

var ErrPoolFull = errors.New("dhcp: address pool exhausted")

// Pool allocates addresses. It is safe for concurrent use.
type Pool struct {
	mu        sync.Mutex
	start     netip.Addr
	end       netip.Addr
	router    netip.Addr
	leaseTime time.Duration
	static    map[string]config.StaticLease // by normalized MAC
	staticIPs map[netip.Addr]string         // ip -> MAC
	leases    map[string]*Lease             // by MAC
	offers    map[string]*Lease             // by MAC, tentative
	declined  map[netip.Addr]time.Time
	path      string
	// Now is the clock; tests override it.
	Now func() time.Time
	// OnChange is called (without the lock held) after leases change.
	OnChange func()
}

// NewPool builds a pool from cfg and loads persisted leases from path ("" for none).
func NewPool(cfg *config.Config, path string) (*Pool, error) {
	start, err := netip.ParseAddr(cfg.DHCP.RangeStart)
	if err != nil {
		return nil, err
	}
	end, err := netip.ParseAddr(cfg.DHCP.RangeEnd)
	if err != nil {
		return nil, err
	}
	p := &Pool{
		start: start, end: end, router: cfg.RouterIP(),
		leaseTime: cfg.DHCP.LeaseTime.Duration,
		static:    map[string]config.StaticLease{},
		staticIPs: map[netip.Addr]string{},
		leases:    map[string]*Lease{},
		offers:    map[string]*Lease{},
		declined:  map[netip.Addr]time.Time{},
		path:      path,
		Now:       time.Now,
	}
	for _, s := range cfg.DHCP.StaticLeases {
		mac := normMAC(s.MAC)
		ip, _ := netip.ParseAddr(s.IP)
		p.static[mac] = s
		p.staticIPs[ip] = mac
	}
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			var ls []*Lease
			if json.Unmarshal(b, &ls) == nil {
				for _, l := range ls {
					if l.IP.Is4() && !l.Static {
						p.leases[normMAC(l.MAC)] = l
					}
				}
			}
		}
	}
	return p, nil
}

func normMAC(s string) string {
	if hw, err := net.ParseMAC(s); err == nil {
		return hw.String()
	}
	return strings.ToLower(s)
}

func (p *Pool) inRange(ip netip.Addr) bool {
	return ip.Is4() && !ip.Less(p.start) && !p.end.Less(ip)
}

// availableLocked reports whether mac may hold ip right now.
// strict also refuses addresses whose expired lease belongs to someone else,
// so returning clients keep their old address when the pool isn't full.
func (p *Pool) availableLocked(ip netip.Addr, mac string, strict bool) bool {
	now := p.Now()
	if ip == p.router {
		return false
	}
	if owner, ok := p.staticIPs[ip]; ok && owner != mac {
		return false
	}
	if until, ok := p.declined[ip]; ok && now.Before(until) {
		return false
	}
	for m, l := range p.leases {
		if m != mac && l.IP == ip && (strict || now.Before(l.Expires)) {
			return false
		}
	}
	for m, o := range p.offers {
		if m != mac && o.IP == ip && now.Before(o.Expires) {
			return false
		}
	}
	return true
}

// Offer picks an address for mac, preferring its static, previous, then requested address.
func (p *Pool) Offer(mac string, requested netip.Addr, hostname string) (netip.Addr, error) {
	mac = normMAC(mac)
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.Now()

	pick := func() netip.Addr {
		if s, ok := p.static[mac]; ok {
			ip, _ := netip.ParseAddr(s.IP)
			return ip
		}
		if l, ok := p.leases[mac]; ok && p.inRange(l.IP) && p.availableLocked(l.IP, mac, false) {
			return l.IP
		}
		if o, ok := p.offers[mac]; ok && p.availableLocked(o.IP, mac, false) {
			return o.IP
		}
		if p.inRange(requested) && p.availableLocked(requested, mac, true) {
			return requested
		}
		for _, strict := range []bool{true, false} {
			for ip := p.start; p.inRange(ip); ip = ip.Next() {
				if p.availableLocked(ip, mac, strict) {
					return ip
				}
			}
		}
		return netip.Addr{}
	}
	ip := pick()
	if !ip.IsValid() {
		return ip, ErrPoolFull
	}
	p.offers[mac] = &Lease{MAC: mac, IP: ip, Hostname: hostname, Expires: now.Add(offerHold)}
	return ip, nil
}

// Commit binds ip to mac. It returns false when the server must NAK.
func (p *Pool) Commit(mac string, ip netip.Addr, hostname string) (Lease, bool) {
	mac = normMAC(mac)
	p.mu.Lock()
	var l Lease
	ok := func() bool {
		if s, isStatic := p.static[mac]; isStatic {
			sip, _ := netip.ParseAddr(s.IP)
			if ip != sip {
				return false
			}
			if s.Hostname != "" {
				hostname = s.Hostname
			}
		} else if !p.inRange(ip) {
			return false
		}
		if !p.availableLocked(ip, mac, false) {
			return false
		}
		l = Lease{MAC: mac, IP: ip, Hostname: config.SanitizeHostname(hostname), Expires: p.Now().Add(p.leaseTime)}
		if _, isStatic := p.static[mac]; isStatic {
			l.Static = true
		}
		p.leases[mac] = &l
		delete(p.offers, mac)
		return true
	}()
	p.mu.Unlock()
	if ok {
		p.changed()
	}
	return l, ok
}

// CancelOffer drops a tentative offer (client picked another server).
func (p *Pool) CancelOffer(mac string) {
	p.mu.Lock()
	delete(p.offers, normMAC(mac))
	p.mu.Unlock()
}

// Release ends mac's lease on ip early.
func (p *Pool) Release(mac string, ip netip.Addr) {
	mac = normMAC(mac)
	p.mu.Lock()
	l, ok := p.leases[mac]
	if ok && l.IP == ip {
		l.Expires = p.Now()
	}
	p.mu.Unlock()
	if ok {
		p.changed()
	}
}

// Decline marks ip as in use by an unknown host.
func (p *Pool) Decline(mac string, ip netip.Addr) {
	mac = normMAC(mac)
	p.mu.Lock()
	p.declined[ip] = p.Now().Add(declineHold)
	if l, ok := p.leases[mac]; ok && l.IP == ip {
		delete(p.leases, mac)
	}
	delete(p.offers, mac)
	p.mu.Unlock()
	p.changed()
}

// Active returns unexpired leases plus all static leases, sorted by IP.
func (p *Pool) Active() []Lease {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.Now()
	var out []Lease
	seen := map[string]bool{}
	for _, l := range p.leases {
		if now.Before(l.Expires) {
			out = append(out, *l)
			seen[l.MAC] = true
		}
	}
	for mac, s := range p.static {
		if !seen[mac] {
			ip, _ := netip.ParseAddr(s.IP)
			out = append(out, Lease{MAC: mac, IP: ip, Hostname: s.Hostname, Static: true})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP.Less(out[j].IP) })
	return out
}

// LookupHost resolves a lease hostname (case-insensitive) to its address.
func (p *Pool) LookupHost(name string) (netip.Addr, bool) {
	name = strings.ToLower(name)
	for _, l := range p.Active() {
		if l.Hostname != "" && strings.ToLower(l.Hostname) == name {
			return l.IP, true
		}
	}
	return netip.Addr{}, false
}

func (p *Pool) changed() {
	if p.path != "" {
		_ = p.save()
	}
	if p.OnChange != nil {
		p.OnChange()
	}
}

func (p *Pool) save() error {
	p.mu.Lock()
	ls := make([]*Lease, 0, len(p.leases))
	for _, l := range p.leases {
		if !l.Static {
			ls = append(ls, l)
		}
	}
	sort.Slice(ls, func(i, j int) bool { return ls[i].IP.Less(ls[j].IP) })
	b, err := json.MarshalIndent(ls, "", "  ")
	p.mu.Unlock()
	if err != nil {
		return err
	}
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

// ReverseLookup returns the hostname holding ip, if any.
func (p *Pool) ReverseLookup(ip netip.Addr) (string, bool) {
	for _, l := range p.Active() {
		if l.IP == ip && l.Hostname != "" {
			return l.Hostname, true
		}
	}
	return "", false
}
