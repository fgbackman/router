package dhcp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"time"
)

// Server answers DHCP on one interface.
type Server struct {
	Pool      *Pool
	Interface string
	ServerIP  netip.Addr
	Subnet    netip.Prefix // masked LAN network
	DNS       []netip.Addr
	Domain    string
	LeaseTime time.Duration
	Log       *log.Logger
}

// Serve listens on UDP 67 until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	lc := net.ListenConfig{Control: bindToInterface(s.Interface)}
	conn, err := lc.ListenPacket(ctx, "udp4", "0.0.0.0:67")
	if err != nil {
		return fmt.Errorf("dhcp: listen on %s:67: %w (is macOS Internet Sharing on? it runs its own DHCP server)", s.Interface, err)
	}
	go func() { <-ctx.Done(); conn.Close() }()
	s.logf("dhcp: serving %s on %s", s.Subnet, s.Interface)

	buf := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(err, net.ErrClosed) {
				return err
			}
			s.logf("dhcp: read: %v", err)
			continue
		}
		req, err := Decode(buf[:n])
		if err != nil {
			continue
		}
		resp, dst := s.Handle(req)
		if resp == nil {
			continue
		}
		if _, err := conn.WriteTo(resp.Encode(), net.UDPAddrFromAddrPort(dst)); err != nil {
			s.logf("dhcp: send to %s: %v", dst, err)
		}
	}
}

func (s *Server) logf(format string, a ...any) {
	if s.Log != nil {
		s.Log.Printf(format, a...)
	}
}

var broadcastDst = netip.AddrPortFrom(netip.AddrFrom4([4]byte{255, 255, 255, 255}), 68)

// Handle processes one request and returns the reply and where to send it.
// A nil reply means stay silent.
func (s *Server) Handle(req *Packet) (*Packet, netip.AddrPort) {
	if req.Op != BootRequest || req.HType != 1 || req.HLen != 6 {
		return nil, netip.AddrPort{}
	}
	mac := req.CHAddr.String()
	host := string(req.Options[OptHostname])
	requested, _ := req.IPOption(OptRequestedIP)
	serverID, hasServerID := req.IPOption(OptServerID)

	switch req.MessageType() {
	case Discover:
		ip, err := s.Pool.Offer(mac, requested, host)
		if err != nil {
			s.logf("dhcp: no address for %s: %v", mac, err)
			return nil, netip.AddrPort{}
		}
		return s.reply(req, Offer, ip)

	case Request:
		if hasServerID && serverID != s.ServerIP {
			s.Pool.CancelOffer(mac)
			return nil, netip.AddrPort{}
		}
		ip := requested
		if !ip.IsValid() || ip.IsUnspecified() {
			ip = req.CIAddr // RENEWING / REBINDING
		}
		if !ip.IsValid() || ip.IsUnspecified() {
			return nil, netip.AddrPort{}
		}
		if l, ok := s.Pool.Commit(mac, ip, host); ok {
			s.logf("dhcp: ack %s -> %s %s", mac, ip, l.Hostname)
			return s.reply(req, Ack, ip)
		}
		s.logf("dhcp: nak %s for %s", mac, ip)
		return s.reply(req, Nak, netip.Addr{})

	case Decline:
		if hasServerID && serverID == s.ServerIP && requested.IsValid() {
			s.logf("dhcp: %s declined %s (address conflict)", mac, requested)
			s.Pool.Decline(mac, requested)
		}
		return nil, netip.AddrPort{}

	case Release:
		s.Pool.Release(mac, req.CIAddr)
		return nil, netip.AddrPort{}

	case Inform:
		return s.reply(req, Ack, netip.Addr{})
	}
	return nil, netip.AddrPort{}
}

func (s *Server) reply(req *Packet, typ byte, yiaddr netip.Addr) (*Packet, netip.AddrPort) {
	zero := netip.AddrFrom4([4]byte{})
	r := &Packet{
		Op: BootReply, HType: 1, HLen: 6, XID: req.XID, Flags: req.Flags,
		CIAddr: zero, YIAddr: zero, SIAddr: zero, GIAddr: req.GIAddr,
		CHAddr: req.CHAddr,
	}
	if !r.GIAddr.IsValid() {
		r.GIAddr = zero
	}
	r.SetOption(OptMessageType, []byte{typ})
	r.SetOption(OptServerID, ipBytes(s.ServerIP))

	if typ != Nak {
		inform := !yiaddr.IsValid()
		if inform {
			r.CIAddr = req.CIAddr
		} else {
			r.YIAddr = yiaddr
			secs := uint32(s.LeaseTime / time.Second)
			r.SetOption(OptLeaseTime, u32(secs))
			r.SetOption(OptRenewalT1, u32(secs/2))
			r.SetOption(OptRebindingT2, u32(secs*7/8))
		}
		mask := net.CIDRMask(s.Subnet.Bits(), 32)
		r.SetOption(OptSubnetMask, mask)
		r.SetOption(OptRouter, ipBytes(s.ServerIP))
		if len(s.DNS) > 0 {
			r.SetOption(OptDNS, ipsBytes(s.DNS...))
		}
		if s.Domain != "" {
			r.SetOption(OptDomainName, []byte(s.Domain))
			r.SetOption(OptSearchDomain, encodeDomainSearch(s.Domain))
		}
		r.SetOption(OptBroadcast, ipBytes(lastAddr(s.Subnet)))
	}

	// RFC 2131 §4.1 reply addressing.
	switch {
	case req.GIAddr.IsValid() && !req.GIAddr.IsUnspecified():
		return r, netip.AddrPortFrom(req.GIAddr, 67)
	case typ == Nak:
		return r, broadcastDst
	case req.CIAddr.IsValid() && !req.CIAddr.IsUnspecified():
		return r, netip.AddrPortFrom(req.CIAddr, 68)
	default:
		// Unicasting to yiaddr needs a manual ARP entry; broadcast always works.
		return r, broadcastDst
	}
}

func lastAddr(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().As4()
	hostBits := 32 - p.Bits()
	v := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	v |= (1 << hostBits) - 1
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}
