package dhcp

import (
	"net"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/fgbackman/router/internal/config"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestServer(t *testing.T, mod func(*config.Config)) (*Server, *clock) {
	t.Helper()
	cfg := config.Default("en0", "en5")
	cfg.DHCP.RangeStart = "192.168.50.100"
	cfg.DHCP.RangeEnd = "192.168.50.102"
	if mod != nil {
		mod(cfg)
	}
	pool, err := NewPool(cfg, filepath.Join(t.TempDir(), "leases.json"))
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	pool.Now = c.now
	lan, _ := cfg.LANPrefix()
	return &Server{
		Pool: pool, Interface: "en5", ServerIP: cfg.RouterIP(), Subnet: lan.Masked(),
		DNS: []netip.Addr{cfg.RouterIP()}, Domain: "lan", LeaseTime: cfg.DHCP.LeaseTime.Duration,
	}, c
}

func mkReq(mac string, typ byte, opts map[byte][]byte) *Packet {
	hw, _ := net.ParseMAC(mac)
	p := &Packet{Op: BootRequest, HType: 1, HLen: 6, XID: 0xdeadbeef, CHAddr: hw,
		CIAddr: netip.IPv4Unspecified(), YIAddr: netip.IPv4Unspecified(), SIAddr: netip.IPv4Unspecified(), GIAddr: netip.IPv4Unspecified()}
	p.SetOption(OptMessageType, []byte{typ})
	for k, v := range opts {
		p.SetOption(k, v)
	}
	// Round-trip through the wire format to exercise the codec.
	d, err := Decode(p.Encode())
	if err != nil {
		panic(err)
	}
	return d
}

func ip(s string) netip.Addr { return netip.MustParseAddr(s) }

func TestFullHandshake(t *testing.T) {
	s, _ := newTestServer(t, nil)
	mac := "aa:bb:cc:00:00:01"
	off, dst := s.Handle(mkReq(mac, Discover, map[byte][]byte{OptHostname: []byte("Fred's Laptop")}))
	if off == nil || off.MessageType() != Offer || off.YIAddr != ip("192.168.50.100") || dst != broadcastDst {
		t.Fatalf("offer = %+v dst %v", off, dst)
	}
	ack, _ := s.Handle(mkReq(mac, Request, map[byte][]byte{
		OptRequestedIP: ipBytes(off.YIAddr), OptServerID: ipBytes(s.ServerIP), OptHostname: []byte("Fred's Laptop"),
	}))
	if ack.MessageType() != Ack || ack.YIAddr != off.YIAddr {
		t.Fatalf("ack = %+v", ack)
	}
	enc, _ := Decode(ack.Encode())
	if r, _ := enc.IPOption(OptRouter); r != s.ServerIP {
		t.Errorf("router option %v", r)
	}
	if got, ok := s.Pool.LookupHost("fred-s-laptop"); !ok || got != off.YIAddr {
		t.Errorf("hostname lookup = %v %v", got, ok)
	}
}

func TestSecondClientGetsDifferentIP(t *testing.T) {
	s, _ := newTestServer(t, nil)
	a, _ := s.Handle(mkReq("aa:00:00:00:00:01", Discover, nil))
	b, _ := s.Handle(mkReq("aa:00:00:00:00:02", Discover, nil))
	if a.YIAddr == b.YIAddr {
		t.Fatal("same address offered twice")
	}
}

func TestStaticLeaseAndNak(t *testing.T) {
	s, _ := newTestServer(t, func(c *config.Config) {
		c.DHCP.StaticLeases = []config.StaticLease{{MAC: "AA-BB-CC-DD-EE-FF", IP: "192.168.50.20", Hostname: "nas"}}
	})
	off, _ := s.Handle(mkReq("aa:bb:cc:dd:ee:ff", Discover, nil))
	if off.YIAddr != ip("192.168.50.20") {
		t.Fatalf("static offer %v", off.YIAddr)
	}
	// INIT-REBOOT asking for some other address must be refused.
	nak, dst := s.Handle(mkReq("aa:bb:cc:dd:ee:ff", Request, map[byte][]byte{OptRequestedIP: ipBytes(ip("192.168.50.101"))}))
	if nak.MessageType() != Nak || dst != broadcastDst {
		t.Fatalf("want NAK, got %v", nak.MessageType())
	}
	// Other clients can never get the static IP.
	if _, ok := s.Pool.Commit("11:11:11:11:11:11", ip("192.168.50.20"), ""); ok {
		t.Fatal("static ip leased to wrong client")
	}
}

func TestPoolExhaustionAndExpiry(t *testing.T) {
	s, c := newTestServer(t, nil)
	for i := 1; i <= 3; i++ {
		mac := net.HardwareAddr{2, 0, 0, 0, 0, byte(i)}.String()
		got, err := s.Pool.Offer(mac, netip.Addr{}, "")
		if err != nil {
			t.Fatal(err)
		}
		s.Pool.Commit(mac, got, "")
	}
	if _, err := s.Pool.Offer("02:00:00:00:00:09", netip.Addr{}, ""); err != ErrPoolFull {
		t.Fatalf("want pool full, got %v", err)
	}
	c.t = c.t.Add(13 * time.Hour)
	if _, err := s.Pool.Offer("02:00:00:00:00:09", netip.Addr{}, ""); err != nil {
		t.Fatalf("expired leases should be reusable: %v", err)
	}
}

func TestReturningClientKeepsAddress(t *testing.T) {
	s, c := newTestServer(t, nil)
	mac := "02:00:00:00:00:01"
	first, _ := s.Pool.Offer(mac, netip.Addr{}, "")
	s.Pool.Commit(mac, first, "")
	c.t = c.t.Add(48 * time.Hour)
	// A new client shouldn't take the expired address while others are free.
	other, _ := s.Pool.Offer("02:00:00:00:00:02", netip.Addr{}, "")
	if other == first {
		t.Fatal("expired address handed to new client while pool had room")
	}
	again, _ := s.Pool.Offer(mac, netip.Addr{}, "")
	if again != first {
		t.Fatalf("returning client got %v, want %v", again, first)
	}
}

func TestOtherServerSelected(t *testing.T) {
	s, _ := newTestServer(t, nil)
	r, _ := s.Handle(mkReq("aa:00:00:00:00:01", Request, map[byte][]byte{
		OptRequestedIP: ipBytes(ip("192.168.50.100")), OptServerID: ipBytes(ip("192.168.50.254")),
	}))
	if r != nil {
		t.Fatal("must stay silent when client chose another server")
	}
}

func TestRenewUnicast(t *testing.T) {
	s, _ := newTestServer(t, nil)
	mac := "aa:00:00:00:00:01"
	got, _ := s.Pool.Offer(mac, netip.Addr{}, "")
	s.Pool.Commit(mac, got, "")
	req := mkReq(mac, Request, nil)
	req.CIAddr = got
	ack, dst := s.Handle(req)
	if ack.MessageType() != Ack || dst != netip.AddrPortFrom(got, 68) {
		t.Fatalf("renew: %v to %v", ack.MessageType(), dst)
	}
}

func TestLeasePersistence(t *testing.T) {
	cfg := config.Default("en0", "en5")
	path := filepath.Join(t.TempDir(), "leases.json")
	p1, _ := NewPool(cfg, path)
	got, _ := p1.Offer("aa:00:00:00:00:01", netip.Addr{}, "")
	p1.Commit("aa:00:00:00:00:01", got, "tv")
	p2, _ := NewPool(cfg, path)
	if a, ok := p2.LookupHost("tv"); !ok || a != got {
		t.Fatalf("lease not restored: %v %v", a, ok)
	}
}

func TestDomainSearchEncoding(t *testing.T) {
	got := encodeDomainSearch("home.lan")
	want := []byte{4, 'h', 'o', 'm', 'e', 3, 'l', 'a', 'n', 0}
	if string(got) != string(want) {
		t.Fatalf("%v", got)
	}
	if lastAddr(netip.MustParsePrefix("192.168.50.0/24")) != ip("192.168.50.255") {
		t.Fatal("broadcast address")
	}
}

func TestDecodeGarbage(t *testing.T) {
	for _, b := range [][]byte{nil, make([]byte, 100), make([]byte, 300)} {
		if _, err := Decode(b); err == nil {
			t.Error("expected error")
		}
	}
}
