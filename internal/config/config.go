// Package config defines the router's on-disk configuration.
package config

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// DefaultPath is where the config lives unless -config is given.
const DefaultPath = "/usr/local/etc/router/config.json"

// DefaultStateDir holds leases, pf rules and teardown state.
const DefaultStateDir = "/var/db/router"

type Config struct {
	WAN      WAN      `json:"wan"`
	LAN      LAN      `json:"lan"`
	DHCP     DHCP     `json:"dhcp"`
	DNS      DNS      `json:"dns"`
	Firewall Firewall `json:"firewall"`
	Web      Web      `json:"web"`
	StateDir string   `json:"stateDir"`
}

type WAN struct {
	// Interface faces the internet/upstream network, e.g. "en0".
	Interface string `json:"interface"`
}

type LAN struct {
	// Interface serves the local network, e.g. "en5" (USB/Thunderbolt Ethernet).
	Interface string `json:"interface"`
	// Address is the router's LAN address in CIDR form, e.g. "192.168.50.1/24".
	Address string `json:"address"`
	// ConfigureAddress adds Address to Interface as an alias on start and
	// removes it on stop. Disable it if you set a manual IP in System Settings.
	ConfigureAddress bool `json:"configureAddress"`
}

type DHCP struct {
	Enabled      bool          `json:"enabled"`
	RangeStart   string        `json:"rangeStart"`
	RangeEnd     string        `json:"rangeEnd"`
	LeaseTime    Duration      `json:"leaseTime"`
	StaticLeases []StaticLease `json:"staticLeases"`
}

type StaticLease struct {
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`
}

type DNS struct {
	Enabled bool `json:"enabled"`
	// Domain is the local search domain handed out by DHCP ("lan").
	Domain string `json:"domain"`
	// Upstreams are "ip:port" resolvers. Empty means use /etc/resolv.conf.
	Upstreams []string `json:"upstreams"`
	// Blocklists are local files or http(s) URLs in hosts or one-domain-per-line format.
	Blocklists []string `json:"blocklists"`
	// BlockedDomains are blocked along with all of their subdomains.
	BlockedDomains []string     `json:"blockedDomains"`
	Hosts          []HostRecord `json:"hosts"`
}

type HostRecord struct {
	Name string `json:"name"`
	IP   string `json:"ip"`
}

type Firewall struct {
	// BlockInbound drops unsolicited IPv4 traffic arriving on the WAN interface.
	BlockInbound bool `json:"blockInbound"`
	// AllowPing answers ICMP echo on WAN when BlockInbound is on.
	AllowPing bool `json:"allowPing"`
	// OpenPorts are ports on the Mac itself reachable from WAN, e.g. "tcp/22".
	OpenPorts    []string      `json:"openPorts"`
	PortForwards []PortForward `json:"portForwards"`
}

type PortForward struct {
	Name         string `json:"name"`
	Protocol     string `json:"protocol"` // tcp, udp or both
	ExternalPort int    `json:"externalPort"`
	InternalIP   string `json:"internalIP"`
	InternalPort int    `json:"internalPort"`
}

type Web struct {
	Enabled bool   `json:"enabled"`
	Listen  string `json:"listen"`
	User    string `json:"user"`
	// PasswordHash is set by `router passwd`. An empty hash disables the UI.
	PasswordHash string `json:"passwordHash"`
}

// Duration marshals as a Go duration string such as "12h".
type Duration struct{ time.Duration }

func (d Duration) MarshalJSON() ([]byte, error) { return json.Marshal(d.String()) }

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

// Default returns a working config for the given interfaces.
func Default(wan, lan string) *Config {
	return &Config{
		WAN: WAN{Interface: wan},
		LAN: LAN{Interface: lan, Address: "192.168.50.1/24", ConfigureAddress: true},
		DHCP: DHCP{
			Enabled:    true,
			RangeStart: "192.168.50.100",
			RangeEnd:   "192.168.50.249",
			LeaseTime:  Duration{12 * time.Hour},
		},
		DNS: DNS{
			Enabled: true,
			Domain:  "lan",
		},
		Firewall: Firewall{BlockInbound: true, AllowPing: false},
		Web:      Web{Enabled: true, Listen: "127.0.0.1:8080", User: "admin"},
		StateDir: DefaultStateDir,
	}
}

func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := &Config{}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if c.StateDir == "" {
		c.StateDir = DefaultStateDir
	}
	if c.DNS.Domain == "" {
		c.DNS.Domain = "lan"
	}
	if c.DHCP.LeaseTime.Duration == 0 {
		c.DHCP.LeaseTime.Duration = 12 * time.Hour
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Save writes the config atomically with 0600 permissions (it holds the password hash).
func (c *Config) Save(path string) error {
	if err := c.Validate(); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Clone returns a deep copy.
func (c *Config) Clone() *Config {
	b, _ := json.Marshal(c)
	n := &Config{}
	_ = json.Unmarshal(b, n)
	return n
}

// LANPrefix returns the router address and its network.
func (c *Config) LANPrefix() (netip.Prefix, error) {
	p, err := netip.ParsePrefix(c.LAN.Address)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("lan.address %q: want CIDR like 192.168.50.1/24", c.LAN.Address)
	}
	if !p.Addr().Is4() {
		return netip.Prefix{}, errors.New("lan.address must be IPv4")
	}
	return p, nil
}

// RouterIP is the LAN address without the prefix length.
func (c *Config) RouterIP() netip.Addr {
	p, _ := c.LANPrefix()
	return p.Addr()
}

var ifaceName = func(s string) bool {
	if s == "" || len(s) > 15 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func (c *Config) Validate() error {
	var errs []error
	add := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if !ifaceName(c.WAN.Interface) {
		add("wan.interface %q is not a valid interface name", c.WAN.Interface)
	}
	if !ifaceName(c.LAN.Interface) {
		add("lan.interface %q is not a valid interface name", c.LAN.Interface)
	}
	if c.WAN.Interface == c.LAN.Interface {
		add("wan.interface and lan.interface must differ")
	}
	lan, err := c.LANPrefix()
	if err != nil {
		errs = append(errs, err)
	} else if lan.Addr() == lan.Masked().Addr() {
		add("lan.address %s is the network address; use a host address such as .1", lan)
	}

	if c.DHCP.Enabled && err == nil {
		s, e1 := netip.ParseAddr(c.DHCP.RangeStart)
		e, e2 := netip.ParseAddr(c.DHCP.RangeEnd)
		switch {
		case e1 != nil || e2 != nil:
			add("dhcp.rangeStart/rangeEnd must be IPv4 addresses")
		case !lan.Contains(s) || !lan.Contains(e):
			add("dhcp range %s-%s is outside %s", s, e, lan.Masked())
		case e.Less(s):
			add("dhcp.rangeEnd is before rangeStart")
		}
		if c.DHCP.LeaseTime.Duration < time.Minute {
			add("dhcp.leaseTime must be at least 1m")
		}
		seen := map[string]bool{}
		for i, sl := range c.DHCP.StaticLeases {
			mac, err := net.ParseMAC(sl.MAC)
			if err != nil || len(mac) != 6 {
				add("dhcp.staticLeases[%d]: bad mac %q", i, sl.MAC)
				continue
			}
			ip, err := netip.ParseAddr(sl.IP)
			if err != nil || !lan.Contains(ip) || ip == lan.Addr() {
				add("dhcp.staticLeases[%d]: ip %q must be in %s and not the router", i, sl.IP, lan.Masked())
			}
			if seen[mac.String()] {
				add("dhcp.staticLeases[%d]: duplicate mac %s", i, mac)
			}
			seen[mac.String()] = true
			if sl.Hostname != "" && !validLabel(sl.Hostname) {
				add("dhcp.staticLeases[%d]: bad hostname %q", i, sl.Hostname)
			}
		}
	}

	if c.DNS.Enabled {
		if !validLabel(c.DNS.Domain) {
			add("dns.domain %q must be a single label like \"lan\"", c.DNS.Domain)
		}
		for _, u := range c.DNS.Upstreams {
			if _, err := netip.ParseAddrPort(u); err != nil {
				add("dns.upstreams: %q must be ip:port, e.g. 1.1.1.1:53", u)
			}
		}
		for i, h := range c.DNS.Hosts {
			if _, err := netip.ParseAddr(h.IP); err != nil || h.Name == "" {
				add("dns.hosts[%d]: need a name and an IP", i)
			}
		}
	}

	for _, p := range c.Firewall.OpenPorts {
		if _, _, err := ParseProtoPort(p); err != nil {
			add("firewall.openPorts: %v", err)
		}
	}
	for i, f := range c.Firewall.PortForwards {
		switch f.Protocol {
		case "tcp", "udp", "both":
		default:
			add("firewall.portForwards[%d]: protocol must be tcp, udp or both", i)
		}
		if f.ExternalPort < 1 || f.ExternalPort > 65535 || f.InternalPort < 1 || f.InternalPort > 65535 {
			add("firewall.portForwards[%d]: ports must be 1-65535", i)
		}
		ip, err := netip.ParseAddr(f.InternalIP)
		if err != nil || (lan.IsValid() && !lan.Contains(ip)) {
			add("firewall.portForwards[%d]: internalIP %q must be on the LAN", i, f.InternalIP)
		}
		if strings.ContainsAny(f.Name, "\n\r#\"") {
			add("firewall.portForwards[%d]: name may not contain quotes, # or newlines", i)
		}
	}

	if c.Web.Enabled {
		if _, err := netip.ParseAddrPort(c.Web.Listen); err != nil {
			add("web.listen %q must be ip:port", c.Web.Listen)
		}
	}
	if !filepath.IsAbs(c.StateDir) {
		add("stateDir must be an absolute path")
	}
	return errors.Join(errs...)
}

// ParseProtoPort parses "tcp/22" or "udp/51820".
func ParseProtoPort(s string) (proto string, port int, err error) {
	proto, ps, ok := strings.Cut(s, "/")
	if !ok || (proto != "tcp" && proto != "udp") {
		return "", 0, fmt.Errorf("%q: want tcp/PORT or udp/PORT", s)
	}
	port, err = strconv.Atoi(ps)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("%q: bad port", s)
	}
	return proto, port, nil
}

func validLabel(s string) bool {
	if s == "" || len(s) > 63 || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// SanitizeHostname turns a DHCP-supplied hostname into a DNS label, or "".
func SanitizeHostname(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		case r == ' ' || r == '_' || r == '\'':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = out[:63]
	}
	return out
}

const pbkdfIter = 210000

// HashPassword returns "pbkdf2-sha256$iter$salt$hash".
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, pw, salt, pbkdfIter, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdfIter, hex.EncodeToString(salt), hex.EncodeToString(key)), nil
}

// CheckPassword reports whether pw matches a hash from HashPassword.
func CheckPassword(hash, pw string) bool {
	parts := strings.Split(hash, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1000 {
		return false
	}
	salt, err1 := hex.DecodeString(parts[2])
	want, err2 := hex.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}
