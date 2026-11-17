package coredevice

import (
	"encoding/binary"
	"net"
	"os"
	"syscall"
	"unsafe"
)

var getExtendedTCPTable = syscall.NewLazyDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

// ownConnection reports whether the loopback client is this process: any program on
// the PC could otherwise reach the phone's developer services through the tunnel.
func ownConnection(c net.Conn) bool {
	local, ok1 := c.LocalAddr().(*net.TCPAddr)
	remote, ok2 := c.RemoteAddr().(*net.TCPAddr)
	if !ok1 || !ok2 {
		return false
	}
	const tcpTableOwnerPIDConnections, afInet = 4, 2
	var size uint32
	getExtendedTCPTable.Call(0, uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPIDConnections, 0)
	for range 3 {
		buf := make([]byte, size+4096)
		size = uint32(len(buf))
		r, _, _ := getExtendedTCPTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0, afInet, tcpTableOwnerPIDConnections, 0)
		if r == 122 { // ERROR_INSUFFICIENT_BUFFER: the table grew
			continue
		}
		if r != 0 {
			return false
		}
		// MIB_TCPTABLE_OWNER_PID: count, then rows of six uint32 (state, local address,
		// local port, remote address, remote port, PID); ports in network order.
		n := binary.LittleEndian.Uint32(buf)
		port := func(v uint32) int { return int(v&0xff)<<8 | int(v>>8&0xff) }
		for i := range n {
			row := buf[4+i*24 : 4+(i+1)*24]
			if port(binary.LittleEndian.Uint32(row[8:])) == remote.Port && port(binary.LittleEndian.Uint32(row[16:])) == local.Port {
				return binary.LittleEndian.Uint32(row[20:]) == uint32(os.Getpid())
			}
		}
		return false
	}
	return false
}
