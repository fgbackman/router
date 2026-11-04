//go:build !darwin && !linux

package dhcp

import (
	"errors"
	"syscall"
)

func bindToInterface(string) func(string, string, syscall.RawConn) error {
	return func(string, string, syscall.RawConn) error {
		return errors.New("dhcp: interface binding unsupported on this OS")
	}
}
