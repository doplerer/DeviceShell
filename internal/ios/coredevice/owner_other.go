//go:build !windows

package coredevice

import "net"

// ownConnection: DeviceShell's iPhone support runs on Windows; elsewhere only loopback binds.
func ownConnection(net.Conn) bool { return true }
