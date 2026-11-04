package dhcp

import (
	"net"
	"syscall"
)

// bindToInterface pins the socket to one interface so broadcasts to
// 255.255.255.255 leave through the LAN port, not the default route.
func bindToInterface(iface string) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		ifi, err := net.InterfaceByName(iface)
		if err != nil {
			return err
		}
		var serr error
		err = c.Control(func(fd uintptr) {
			if serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); serr != nil {
				return
			}
			if serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1); serr != nil {
				return
			}
			serr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_BOUND_IF, ifi.Index)
		})
		if err != nil {
			return err
		}
		return serr
	}
}
