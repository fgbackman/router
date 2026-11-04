package dns

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Resolver supplies LAN names; the DHCP pool implements it.
type Resolver interface {
	LookupHost(name string) (netip.Addr, bool)
}

// Server forwards LAN queries upstream and answers local names itself.
type Server struct {
	Listen     netip.Addr
	RouterName string // "router" by default
	Domain     string
	Subnet     netip.Prefix
	Leases     Resolver
	Log        *log.Logger

	mu        sync.RWMutex
	upstreams []netip.AddrPort
	hosts     map[string]netip.Addr // fqdn and short name -> addr
	reverse   map[netip.Addr]string
	block     *Blocklist

	Queries   atomic.Uint64
	Blocked   atomic.Uint64
	Local     atomic.Uint64
	Forwarded atomic.Uint64
	Failed    atomic.Uint64
}

const (
	localTTL   = 60
	blockedTTL = 300
	upstreamTO = 2 * time.Second
)

// Update swaps upstreams, static hosts and the blocklist atomically.
func (s *Server) Update(upstreams []netip.AddrPort, hosts map[string]netip.Addr, block *Blocklist) {
	h := map[string]netip.Addr{}
	rev := map[netip.Addr]string{}
	for name, a := range hosts {
		name = strings.ToLower(strings.TrimSuffix(name, "."))
		h[name] = a
		if !strings.Contains(name, ".") {
			h[name+"."+s.Domain] = a
			rev[a] = name + "." + s.Domain
		} else if _, ok := rev[a]; !ok {
			rev[a] = name
		}
	}
	s.mu.Lock()
	s.upstreams = upstreams
	s.hosts = h
	s.reverse = rev
	s.block = block
	s.mu.Unlock()
}

// BlockedCount is the number of domains in the active blocklist.
func (s *Server) BlockedCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.block == nil {
		return 0
	}
	return s.block.Len()
}

func (s *Server) Upstreams() []netip.AddrPort {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]netip.AddrPort(nil), s.upstreams...)
}

func (s *Server) logf(format string, a ...any) {
	if s.Log != nil {
		s.Log.Printf(format, a...)
	}
}

// Serve listens on UDP and TCP port 53 of s.Listen until ctx ends.
func (s *Server) Serve(ctx context.Context) error {
	addr := netip.AddrPortFrom(s.Listen, 53).String()
	var lc net.ListenConfig
	pc, err := lc.ListenPacket(ctx, "udp", addr)
	if err != nil {
		return fmt.Errorf("dns: %w", err)
	}
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		pc.Close()
		return fmt.Errorf("dns: %w", err)
	}
	go func() { <-ctx.Done(); pc.Close(); ln.Close() }()
	s.logf("dns: listening on %s", addr)

	go s.serveTCP(ctx, ln)
	buf := make([]byte, 4096)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			continue
		}
		q := append([]byte(nil), buf[:n]...)
		go func() {
			if resp := s.Answer(ctx, q, false); resp != nil {
				pc.WriteTo(resp, from)
			}
		}()
	}
}

func (s *Server) serveTCP(ctx context.Context, ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			r := bufio.NewReader(c)
			for {
				c.SetDeadline(time.Now().Add(10 * time.Second))
				q, err := readTCPMsg(r)
				if err != nil {
					return
				}
				resp := s.Answer(ctx, q, true)
				if resp == nil {
					return
				}
				if _, err := c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(resp))), resp...)); err != nil {
					return
				}
			}
		}()
	}
}

func readTCPMsg(r io.Reader) ([]byte, error) {
	var l [2]byte
	if _, err := io.ReadFull(r, l[:]); err != nil {
		return nil, err
	}
	b := make([]byte, binary.BigEndian.Uint16(l[:]))
	_, err := io.ReadFull(r, b)
	return b, err
}

// Answer produces the response for a raw query, or nil to drop it.
func (s *Server) Answer(ctx context.Context, query []byte, tcp bool) []byte {
	s.Queries.Add(1)
	q, err := ParseQuery(query)
	if err != nil {
		if len(query) >= headerLen && query[2]&0x80 == 0 {
			return errorResponse(query, 1) // FORMERR
		}
		return nil
	}
	if Opcode(query) != 0 {
		return buildResponse(query, q, RcodeNotImp, nil)
	}
	if q.Class != ClassIN {
		return s.forward(ctx, query, q, tcp)
	}
	if resp, ok := s.answerLocal(query, q); ok {
		s.Local.Add(1)
		return resp
	}
	s.mu.RLock()
	bl := s.block
	s.mu.RUnlock()
	if bl.Match(q.Name) {
		s.Blocked.Add(1)
		switch q.Type {
		case TypeA:
			return buildResponse(query, q, RcodeOK, []rr{addrRR(netip.IPv4Unspecified(), blockedTTL)})
		case TypeAAAA:
			return buildResponse(query, q, RcodeOK, []rr{addrRR(netip.IPv6Unspecified(), blockedTTL)})
		default:
			return buildResponse(query, q, RcodeNXDomain, nil)
		}
	}
	return s.forward(ctx, query, q, tcp)
}

// answerLocal handles the router's own names, static hosts, DHCP clients and
// reverse lookups for the LAN. Names under the local domain never leave the LAN.
func (s *Server) answerLocal(query []byte, q Question) ([]byte, bool) {
	name := q.Name
	routerName := s.RouterName
	if routerName == "" {
		routerName = "router"
	}

	if a, ok := parseReverse(name); ok && s.Subnet.Contains(a) {
		host := ""
		s.mu.RLock()
		host = s.reverse[a]
		s.mu.RUnlock()
		if a == s.Listen {
			host = routerName + "." + s.Domain
		}
		if host == "" {
			host = s.leaseNameFor(a)
		}
		if host == "" {
			return buildResponse(query, q, RcodeNXDomain, nil), true
		}
		if q.Type != TypePTR {
			return buildResponse(query, q, RcodeOK, nil), true
		}
		return buildResponse(query, q, RcodeOK, []rr{{TypePTR, localTTL, encodeName(host)}}), true
	}

	short, underDomain := strings.CutSuffix(name, "."+s.Domain)
	single := !strings.Contains(name, ".")
	if name == s.Domain {
		return buildResponse(query, q, RcodeOK, nil), true
	}

	var addr netip.Addr
	found := false
	s.mu.RLock()
	if a, ok := s.hosts[name]; ok {
		addr, found = a, true
	}
	s.mu.RUnlock()
	if !found && (underDomain || single) {
		label := short
		if single {
			label = name
		}
		if strings.Contains(label, ".") {
			// e.g. "foo.bar.lan": not something we hand out.
		} else if label == routerName {
			addr, found = s.Listen, true
		} else if s.Leases != nil {
			addr, found = s.Leases.LookupHost(label)
		}
		if !found {
			return buildResponse(query, q, RcodeNXDomain, nil), true
		}
	}
	if !found {
		return nil, false
	}
	if (q.Type == TypeA && addr.Is4()) || (q.Type == TypeAAAA && addr.Is6() && !addr.Is4In6()) {
		return buildResponse(query, q, RcodeOK, []rr{addrRR(addr, localTTL)}), true
	}
	return buildResponse(query, q, RcodeOK, nil), true // NODATA
}

func (s *Server) leaseNameFor(a netip.Addr) string {
	type reverser interface {
		ReverseLookup(netip.Addr) (string, bool)
	}
	if r, ok := s.Leases.(reverser); ok {
		if h, ok := r.ReverseLookup(a); ok {
			return h + "." + s.Domain
		}
	}
	return ""
}

func (s *Server) forward(ctx context.Context, query []byte, q Question, tcp bool) []byte {
	ups := s.Upstreams()
	if len(ups) == 0 {
		s.Failed.Add(1)
		return buildResponse(query, q, RcodeServFail, nil)
	}
	for _, up := range ups {
		resp, err := exchange(ctx, up, query, tcp)
		if err != nil {
			continue
		}
		if len(resp) >= 2 && binary.BigEndian.Uint16(resp[0:2]) == q.ID {
			s.Forwarded.Add(1)
			return resp
		}
	}
	s.Failed.Add(1)
	return buildResponse(query, q, RcodeServFail, nil)
}

func exchange(ctx context.Context, up netip.AddrPort, query []byte, tcp bool) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, upstreamTO)
	defer cancel()
	network := "udp"
	if tcp {
		network = "tcp"
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, network, up.String())
	if err != nil {
		return nil, err
	}
	defer c.Close()
	dl, _ := ctx.Deadline()
	c.SetDeadline(dl)
	if tcp {
		if _, err := c.Write(append(binary.BigEndian.AppendUint16(nil, uint16(len(query))), query...)); err != nil {
			return nil, err
		}
		return readTCPMsg(c)
	}
	if _, err := c.Write(query); err != nil {
		return nil, err
	}
	buf := make([]byte, 65535)
	n, err := c.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

// SystemUpstreams reads nameservers from resolv.conf (which macOS keeps in
// sync with the WAN's DHCP settings), skipping our own address.
func SystemUpstreams(path string, self netip.Addr) []netip.AddrPort {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []netip.AddrPort
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) >= 2 && fs[0] == "nameserver" {
			a, err := netip.ParseAddr(strings.Split(fs[1], "%")[0])
			if err == nil && a != self && !a.IsLoopback() && !a.IsLinkLocalUnicast() {
				out = append(out, netip.AddrPortFrom(a, 53))
			}
		}
	}
	return out
}
