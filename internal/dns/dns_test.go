package dns

import (
	"context"
	"encoding/binary"
	"net"
	"net/netip"
	"strings"
	"testing"
)

type fakeLeases map[string]netip.Addr

func (f fakeLeases) LookupHost(n string) (netip.Addr, bool) { a, ok := f[n]; return a, ok }
func (f fakeLeases) ReverseLookup(a netip.Addr) (string, bool) {
	for n, v := range f {
		if v == a {
			return n, true
		}
	}
	return "", false
}

func query(id uint16, name string, typ uint16) []byte {
	b := []byte{byte(id >> 8), byte(id), 0x01, 0x00, 0, 1, 0, 0, 0, 0, 0, 0}
	b = append(b, encodeName(name)...)
	b = binary.BigEndian.AppendUint16(b, typ)
	return binary.BigEndian.AppendUint16(b, ClassIN)
}

func rcode(resp []byte) int               { return int(resp[3] & 0xF) }
func ancount(resp []byte) int             { return int(binary.BigEndian.Uint16(resp[6:8])) }
func lastRdata(resp []byte, n int) []byte { return resp[len(resp)-n:] }

func newServer(t *testing.T, upstream netip.AddrPort) *Server {
	s := &Server{
		Listen: netip.MustParseAddr("192.168.50.1"), Domain: "lan",
		Subnet: netip.MustParsePrefix("192.168.50.0/24"),
		Leases: fakeLeases{"laptop": netip.MustParseAddr("192.168.50.100")},
	}
	bl := NewBlocklist()
	if err := bl.Parse(strings.NewReader("# ads\n0.0.0.0 ads.example.com\ntracker.net\n||adblock.org^\n127.0.0.1 localhost\n")); err != nil {
		t.Fatal(err)
	}
	var ups []netip.AddrPort
	if upstream.IsValid() {
		ups = []netip.AddrPort{upstream}
	}
	s.Update(ups, map[string]netip.Addr{"nas": netip.MustParseAddr("192.168.50.20")}, bl)
	return s
}

func TestLocalNames(t *testing.T) {
	s := newServer(t, netip.AddrPort{})
	ctx := context.Background()
	cases := []struct {
		name  string
		typ   uint16
		rcode int
		ip    string
	}{
		{"laptop.lan", TypeA, RcodeOK, "192.168.50.100"},
		{"LAPTOP", TypeA, RcodeOK, "192.168.50.100"},
		{"router.lan", TypeA, RcodeOK, "192.168.50.1"},
		{"nas.lan", TypeA, RcodeOK, "192.168.50.20"},
		{"nas", TypeA, RcodeOK, "192.168.50.20"},
		{"laptop.lan", TypeAAAA, RcodeOK, ""},
		{"missing.lan", TypeA, RcodeNXDomain, ""},
		{"missing", TypeA, RcodeNXDomain, ""},
	}
	for _, c := range cases {
		resp := s.Answer(ctx, query(7, c.name, c.typ), false)
		if rcode(resp) != c.rcode {
			t.Errorf("%s: rcode %d want %d", c.name, rcode(resp), c.rcode)
			continue
		}
		if c.ip != "" {
			if ancount(resp) != 1 || netip.AddrFrom4([4]byte(lastRdata(resp, 4))).String() != c.ip {
				t.Errorf("%s: wrong answer %v", c.name, resp)
			}
		} else if ancount(resp) != 0 {
			t.Errorf("%s: expected no answers", c.name)
		}
	}
}

func TestReverse(t *testing.T) {
	s := newServer(t, netip.AddrPort{})
	resp := s.Answer(context.Background(), query(1, "100.50.168.192.in-addr.arpa", TypePTR), false)
	if ancount(resp) != 1 || !strings.HasSuffix(string(resp), string(encodeName("laptop.lan"))) {
		t.Fatalf("ptr: %v", resp)
	}
	resp = s.Answer(context.Background(), query(1, "77.50.168.192.in-addr.arpa", TypePTR), false)
	if rcode(resp) != RcodeNXDomain {
		t.Fatal("unknown LAN PTR must be NXDOMAIN, not forwarded")
	}
}

func TestBlocking(t *testing.T) {
	s := newServer(t, netip.AddrPort{})
	for _, n := range []string{"ads.example.com", "x.ads.example.com", "tracker.net", "cdn.adblock.org"} {
		resp := s.Answer(context.Background(), query(2, n, TypeA), false)
		if ancount(resp) != 1 || string(lastRdata(resp, 4)) != "\x00\x00\x00\x00" {
			t.Errorf("%s not sinkholed", n)
		}
	}
	if s.BlockedCount() != 3 {
		t.Errorf("blocklist size %d (localhost must be skipped)", s.BlockedCount())
	}
}

func TestForwarding(t *testing.T) {
	// Fake upstream echoes the query back with QR set and one A record.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 512)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			q, _ := ParseQuery(buf[:n])
			pc.WriteTo(buildResponse(buf[:n], q, RcodeOK, []rr{addrRR(netip.MustParseAddr("93.184.216.34"), 30)}), from)
		}
	}()
	up := netip.MustParseAddrPort(pc.LocalAddr().String())
	s := newServer(t, up)
	resp := s.Answer(context.Background(), query(0x4242, "example.com", TypeA), false)
	if binary.BigEndian.Uint16(resp) != 0x4242 || ancount(resp) != 1 {
		t.Fatalf("forward failed: %v", resp)
	}
	if s.Forwarded.Load() != 1 {
		t.Fatal("forward counter")
	}
}

func TestNoUpstreamServfail(t *testing.T) {
	s := newServer(t, netip.AddrPort{})
	if rcode(s.Answer(context.Background(), query(1, "example.com", TypeA), false)) != RcodeServFail {
		t.Fatal("want SERVFAIL")
	}
}

func TestMalformed(t *testing.T) {
	s := newServer(t, netip.AddrPort{})
	if r := s.Answer(context.Background(), []byte{1, 2, 1, 0, 0, 1, 0, 0, 0, 0, 0, 0, 5, 'a'}, false); r == nil || rcode(r) != 1 {
		t.Fatalf("want FORMERR, got %v", r)
	}
	if s.Answer(context.Background(), []byte{1}, false) != nil {
		t.Fatal("tiny packet should be dropped")
	}
}
