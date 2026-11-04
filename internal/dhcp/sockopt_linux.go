package dhcp

import "syscall"

// Linux support exists so the server can be developed and tested off-Mac.
func bindToInterface(iface string) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, c syscall.RawConn) error {
		var serr error
		err := c.Control(func(fd uintptr) {
			if serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); serr != nil {
				return
			}
			if serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1); serr != nil {
				return
			}
			serr = syscall.BindToDevice(int(fd), iface)
		})
		if err != nil {
			return err
		}
		return serr
	}
}
