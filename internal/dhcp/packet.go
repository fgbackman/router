// Package dhcp is a small authoritative DHCPv4 server (RFC 2131/2132).
package dhcp

import (
	"encoding/binary"
	"errors"
	"net"
	"net/netip"
)

const (
	BootRequest = 1
	BootReply   = 2
)

// Message types (option 53).
const (
	Discover = 1
	Offer    = 2
	Request  = 3
	Decline  = 4
	Ack      = 5
	Nak      = 6
	Release  = 7
	Inform   = 8
)

// Option codes used by the server.
const (
	OptPad          = 0
	OptSubnetMask   = 1
	OptRouter       = 3
	OptDNS          = 6
	OptHostname     = 12
	OptDomainName   = 15
	OptBroadcast    = 28
	OptRequestedIP  = 50
	OptLeaseTime    = 51
	OptMessageType  = 53
	OptServerID     = 54
	OptParamList    = 55
	OptRenewalT1    = 58
	OptRebindingT2  = 59
	OptClientID     = 61
	OptSearchDomain = 119
	OptEnd          = 255
)

var magicCookie = [4]byte{99, 130, 83, 99}

const headerLen = 236

// Packet is a decoded BOOTP/DHCP message. Options keep their raw bytes.
type Packet struct {
	Op      byte
	HType   byte
	HLen    byte
	Hops    byte
	XID     uint32
	Secs    uint16
	Flags   uint16
	CIAddr  netip.Addr
	YIAddr  netip.Addr
	SIAddr  netip.Addr
	GIAddr  netip.Addr
	CHAddr  net.HardwareAddr
	Options map[byte][]byte
	// order preserves option order on encode for deterministic output.
	order []byte
}

func (p *Packet) MessageType() byte {
	if v := p.Options[OptMessageType]; len(v) == 1 {
		return v[0]
	}
	return 0
}

// SetOption sets or replaces an option.
func (p *Packet) SetOption(code byte, v []byte) {
	if p.Options == nil {
		p.Options = map[byte][]byte{}
	}
	if _, ok := p.Options[code]; !ok {
		p.order = append(p.order, code)
	}
	p.Options[code] = v
}

// IPOption returns an option holding a single IPv4 address.
func (p *Packet) IPOption(code byte) (netip.Addr, bool) {
	v := p.Options[code]
	if len(v) != 4 {
		return netip.Addr{}, false
	}
	return netip.AddrFrom4([4]byte(v)), true
}

// Broadcast reports whether the client asked for broadcast replies.
func (p *Packet) Broadcast() bool { return p.Flags&0x8000 != 0 }

var errShort = errors.New("dhcp: packet too short")

func Decode(b []byte) (*Packet, error) {
	if len(b) < headerLen+4 {
		return nil, errShort
	}
	p := &Packet{
		Op: b[0], HType: b[1], HLen: b[2], Hops: b[3],
		XID:    binary.BigEndian.Uint32(b[4:8]),
		Secs:   binary.BigEndian.Uint16(b[8:10]),
		Flags:  binary.BigEndian.Uint16(b[10:12]),
		CIAddr: netip.AddrFrom4([4]byte(b[12:16])),
		YIAddr: netip.AddrFrom4([4]byte(b[16:20])),
		SIAddr: netip.AddrFrom4([4]byte(b[20:24])),
		GIAddr: netip.AddrFrom4([4]byte(b[24:28])),
	}
	hlen := int(p.HLen)
	if hlen > 16 {
		return nil, errors.New("dhcp: bad hlen")
	}
	p.CHAddr = append(net.HardwareAddr(nil), b[28:28+hlen]...)
	if [4]byte(b[headerLen:headerLen+4]) != magicCookie {
		return nil, errors.New("dhcp: missing magic cookie")
	}
	p.Options = map[byte][]byte{}
	opts := b[headerLen+4:]
	for i := 0; i < len(opts); {
		code := opts[i]
		if code == OptPad {
			i++
			continue
		}
		if code == OptEnd {
			break
		}
		if i+1 >= len(opts) {
			return nil, errors.New("dhcp: truncated option")
		}
		n := int(opts[i+1])
		if i+2+n > len(opts) {
			return nil, errors.New("dhcp: truncated option")
		}
		v := opts[i+2 : i+2+n]
		// RFC 3396: repeated options are concatenated.
		if old, ok := p.Options[code]; ok {
			p.Options[code] = append(append([]byte(nil), old...), v...)
		} else {
			p.SetOption(code, append([]byte(nil), v...))
		}
		i += 2 + n
	}
	return p, nil
}

func (p *Packet) Encode() []byte {
	b := make([]byte, headerLen, 576)
	b[0], b[1], b[2], b[3] = p.Op, p.HType, p.HLen, p.Hops
	binary.BigEndian.PutUint32(b[4:8], p.XID)
	binary.BigEndian.PutUint16(b[8:10], p.Secs)
	binary.BigEndian.PutUint16(b[10:12], p.Flags)
	put4 := func(off int, a netip.Addr) {
		if a.Is4() {
			v := a.As4()
			copy(b[off:off+4], v[:])
		}
	}
	put4(12, p.CIAddr)
	put4(16, p.YIAddr)
	put4(20, p.SIAddr)
	put4(24, p.GIAddr)
	copy(b[28:44], p.CHAddr)
	b = append(b, magicCookie[:]...)
	for _, code := range p.order {
		v, ok := p.Options[code]
		if !ok {
			continue
		}
		for len(v) > 255 {
			b = append(b, code, 255)
			b = append(b, v[:255]...)
			v = v[255:]
		}
		b = append(b, code, byte(len(v)))
		b = append(b, v...)
	}
	b = append(b, OptEnd)
	// Pad to the BOOTP minimum; some clients drop shorter replies.
	for len(b) < 300 {
		b = append(b, OptPad)
	}
	return b
}

func ipBytes(a netip.Addr) []byte { v := a.As4(); return v[:] }

func ipsBytes(as ...netip.Addr) []byte {
	var out []byte
	for _, a := range as {
		out = append(out, ipBytes(a)...)
	}
	return out
}

func u32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }

// encodeDomainSearch encodes option 119 without compression.
func encodeDomainSearch(domain string) []byte {
	var out []byte
	start := 0
	for i := 0; i <= len(domain); i++ {
		if i == len(domain) || domain[i] == '.' {
			out = append(out, byte(i-start))
			out = append(out, domain[start:i]...)
			start = i + 1
		}
	}
	return append(out, 0)
}
