// Package dns is a caching-free forwarding resolver for the LAN with local
// hostnames (from DHCP leases and static records) and domain blocking.
package dns

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"strings"
)

const (
	TypeA    = 1
	TypePTR  = 12
	TypeAAAA = 28
	ClassIN  = 1

	RcodeOK       = 0
	RcodeServFail = 2
	RcodeNXDomain = 3
	RcodeNotImp   = 4
	RcodeRefused  = 5
)

const headerLen = 12

// Question is the single question of a standard query.
type Question struct {
	ID    uint16
	Name  string // lowercase, no trailing dot
	Type  uint16
	Class uint16
	end   int // offset just past the question section
}

var errFormat = errors.New("dns: malformed query")

// ParseQuery parses the header and first question of a query.
func ParseQuery(b []byte) (Question, error) {
	var q Question
	if len(b) < headerLen {
		return q, errFormat
	}
	q.ID = binary.BigEndian.Uint16(b[0:2])
	flags := binary.BigEndian.Uint16(b[2:4])
	if flags&0x8000 != 0 { // QR set: not a query
		return q, errFormat
	}
	if binary.BigEndian.Uint16(b[4:6]) != 1 {
		return q, errFormat
	}
	var labels []string
	i := headerLen
	for {
		if i >= len(b) {
			return q, errFormat
		}
		n := int(b[i])
		i++
		if n == 0 {
			break
		}
		if n > 63 || i+n > len(b) { // compression pointers are not valid in a lone question
			return q, errFormat
		}
		labels = append(labels, strings.ToLower(string(b[i:i+n])))
		i += n
	}
	if i+4 > len(b) {
		return q, errFormat
	}
	q.Name = strings.Join(labels, ".")
	if len(q.Name) > 253 {
		return q, errFormat
	}
	q.Type = binary.BigEndian.Uint16(b[i : i+2])
	q.Class = binary.BigEndian.Uint16(b[i+2 : i+4])
	q.end = i + 4
	return q, nil
}

// Opcode returns the query's opcode.
func Opcode(b []byte) int { return int(b[2]>>3) & 0xF }

type rr struct {
	typ   uint16
	ttl   uint32
	rdata []byte
}

// buildResponse answers query (whose question ends at q.end) with rcode and
// answers. EDNS and other additional records are dropped.
func buildResponse(query []byte, q Question, rcode int, answers []rr) []byte {
	out := make([]byte, 0, q.end+len(answers)*32)
	out = append(out, query[:q.end]...)
	rd := query[2] & 0x01
	out[2] = 0x80 | (query[2] & 0x78) | 0x04 | rd // QR, opcode, AA, RD
	out[3] = 0x80 | byte(rcode&0xF)               // RA
	binary.BigEndian.PutUint16(out[4:6], 1)
	binary.BigEndian.PutUint16(out[6:8], uint16(len(answers)))
	binary.BigEndian.PutUint16(out[8:10], 0)
	binary.BigEndian.PutUint16(out[10:12], 0)
	for _, a := range answers {
		out = append(out, 0xC0, 0x0C) // pointer to the question name
		out = binary.BigEndian.AppendUint16(out, a.typ)
		out = binary.BigEndian.AppendUint16(out, ClassIN)
		out = binary.BigEndian.AppendUint32(out, a.ttl)
		out = binary.BigEndian.AppendUint16(out, uint16(len(a.rdata)))
		out = append(out, a.rdata...)
	}
	return out
}

// errorResponse answers a query that couldn't be parsed past the header.
func errorResponse(query []byte, rcode int) []byte {
	if len(query) < headerLen {
		return nil
	}
	out := append([]byte(nil), query[:headerLen]...)
	out[2] = 0x80 | (query[2] & 0x79)
	out[3] = 0x80 | byte(rcode&0xF)
	for i := 4; i < 12; i++ {
		out[i] = 0
	}
	return out
}

func addrRR(a netip.Addr, ttl uint32) rr {
	if a.Is4() {
		v := a.As4()
		return rr{TypeA, ttl, v[:]}
	}
	v := a.As16()
	return rr{TypeAAAA, ttl, v[:]}
}

func encodeName(name string) []byte {
	var out []byte
	for _, l := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		if l == "" {
			continue
		}
		out = append(out, byte(len(l)))
		out = append(out, l...)
	}
	return append(out, 0)
}

// parseReverse turns "4.3.2.1.in-addr.arpa" into 1.2.3.4.
func parseReverse(name string) (netip.Addr, bool) {
	rest, ok := strings.CutSuffix(name, ".in-addr.arpa")
	if !ok {
		return netip.Addr{}, false
	}
	p := strings.Split(rest, ".")
	if len(p) != 4 {
		return netip.Addr{}, false
	}
	a, err := netip.ParseAddr(p[3] + "." + p[2] + "." + p[1] + "." + p[0])
	return a, err == nil
}
