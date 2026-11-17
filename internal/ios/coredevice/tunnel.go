// Package coredevice talks to iPhones (iOS 27) without helpers: the CoreDevice tunnel over
// USB, an in-process IPv6 stack and the developer services DeviceShell uses. go-ios (MIT)
// provides usbmux, lockdown, RemoteXPC and several services; the iOS 27 services it
// lacks are written from captured traffic, documented in docs/iphone-protocol.md.
package coredevice

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/http"
	"github.com/danielpaulus/go-ios/ios/tunnel"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv6"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

const coreDeviceProxy = "com.apple.internal.devicecompute.CoreDeviceProxy"

// Tunnel is the CoreDevice tunnel to one iPhone. Device is ready for go-ios services:
// they reach the tunnel through a loopback listener that only accepts this process.
type Tunnel struct {
	Device  ios.DeviceEntry
	Version string // iOS version, e.g. "27.2"
	// Services published over RSD, by name.
	Services map[string]ios.RsdServiceEntry
	UDID     string // the phone's UDID, as RSD reports it
	// Properties of the RSD handshake (UniqueDeviceID, OSVersion, ProductType...).
	Properties map[string]any

	deviceIP, localIP tcpip.Address
	stack             *stack.Stack
	endpoint          *tunnel.Endpoint
	conn              io.Closer
	listener          net.Listener
	closeOnce         sync.Once
	done              chan struct{}
}

// tunnelParameters is the device's answer to the CDTunnel handshake.
type tunnelParameters struct {
	ServerAddress    string
	ServerRSDPort    uint64
	ClientParameters struct {
		Address string
		Netmask string
		Mtu     uint64
	}
}

// exchangeTunnelParameters runs the CDTunnel handshake: magic, one length byte and a
// JSON body each way. Adapted from go-ios (tunnel.exchangeCoreTunnelParameters, MIT,
// Copyright (c) 2019 danielpaulus).
func exchangeTunnelParameters(rw io.ReadWriter) (tunnelParameters, error) {
	request, _ := json.Marshal(map[string]any{"type": "clientHandshakeRequest", "mtu": 1280})
	var b bytes.Buffer
	b.WriteString("CDTunnel\x00")
	b.WriteByte(byte(len(request)))
	b.Write(request)
	if _, e := rw.Write(b.Bytes()); e != nil {
		return tunnelParameters{}, e
	}
	head := make([]byte, len("CDTunnel")+2)
	if _, e := io.ReadFull(rw, head); e != nil {
		return tunnelParameters{}, fmt.Errorf("CDTunnel header: %w", e)
	}
	body := make([]byte, head[len(head)-1])
	if _, e := io.ReadFull(rw, body); e != nil {
		return tunnelParameters{}, fmt.Errorf("CDTunnel body: %w", e)
	}
	var p tunnelParameters
	return p, json.Unmarshal(body, &p)
}

// packetConn returns one IPv6 packet per Read: the lockdown connection is a byte stream
// of back-to-back packets, and the gVisor link endpoint expects whole packets.
type packetConn struct {
	r *bufio.Reader
	io.WriteCloser
}

func (p *packetConn) Read(b []byte) (int, error) {
	const headerLen = 40
	if len(b) < headerLen {
		return 0, io.ErrShortBuffer
	}
	if _, e := io.ReadFull(p.r, b[:headerLen]); e != nil {
		return 0, e
	}
	if b[0]>>4 != 6 {
		return 0, fmt.Errorf("tunnel: IP version %d, expected 6", b[0]>>4)
	}
	total := headerLen + int(binary.BigEndian.Uint16(b[4:6]))
	if total > len(b) {
		return 0, fmt.Errorf("tunnel: packet of %d bytes over the %d-byte MTU", total, len(b))
	}
	if _, e := io.ReadFull(p.r, b[headerLen:total]); e != nil {
		return 0, e
	}
	return total, nil
}

// Open brings up the tunnel to udid and reads the services it publishes. The developer
// image must be mounted for the CoreDevice services to appear.
func Open(udid string) (*Tunnel, error) {
	device, e := ios.GetDevice(udid)
	if e != nil {
		return nil, e
	}
	return OpenDevice(device)
}

// OpenDevice brings up the tunnel over the usbmux entry given: a USB entry, or the
// "Network" entry usbmuxd lists for a phone reachable over Wi-Fi.
func OpenDevice(device ios.DeviceEntry) (*Tunnel, error) {
	version, e := ios.GetProductVersion(device)
	if e != nil {
		return nil, fmt.Errorf("iOS version: %w", e)
	}
	conn, e := ios.ConnectToService(device, coreDeviceProxy)
	if e != nil {
		return nil, fmt.Errorf("CoreDevice tunnel: %w", e)
	}
	return openOver(device, version.String(), conn)
}

// OpenConn brings up the tunnel over conn, a transport that already speaks CDTunnel
// (for example the RemotePairing TLS-PSK tunnel over Wi-Fi).
func OpenConn(udid, version string, conn io.ReadWriteCloser) (*Tunnel, error) {
	device := ios.DeviceEntry{Properties: ios.DeviceProperties{SerialNumber: udid, ConnectionType: "Network"}}
	return openOver(device, version, conn)
}

// openOver runs the CDTunnel handshake on conn and brings up the tunnel and RSD.
func openOver(device ios.DeviceEntry, version string, conn io.ReadWriteCloser) (*Tunnel, error) {
	params, e := exchangeTunnelParameters(conn)
	if e != nil {
		conn.Close()
		return nil, fmt.Errorf("CoreDevice tunnel: %w", e)
	}
	t := &Tunnel{Version: version, conn: conn, done: make(chan struct{})}
	if e = t.startStack(conn, params); e != nil {
		conn.Close()
		return nil, e
	}
	if e = t.listen(); e != nil {
		t.Close()
		return nil, e
	}
	device.Address = params.ServerAddress
	device.UserspaceTUN = true
	device.UserspaceTUNHost = "127.0.0.1"
	device.UserspaceTUNPort = t.listener.Addr().(*net.TCPAddr).Port
	handshake, props, e := t.rsdHandshake(int(params.ServerRSDPort))
	if e != nil {
		t.Close()
		return nil, fmt.Errorf("RSD: %w", e)
	}
	device.Rsd = handshake
	t.Properties = props
	if v, ok := props["OSVersion"].(string); ok && v != "" {
		t.Version = v
	}
	t.Device, t.Services, t.UDID = device, handshake.Services, handshake.Udid
	return t, nil
}

func (t *Tunnel) startStack(conn io.ReadWriteCloser, p tunnelParameters) error {
	deviceIP, localIP := net.ParseIP(p.ServerAddress), net.ParseIP(p.ClientParameters.Address)
	if deviceIP == nil || localIP == nil || p.ClientParameters.Mtu == 0 {
		return fmt.Errorf("CoreDevice tunnel: invalid parameters %+v", p)
	}
	t.deviceIP, t.localIP = tcpip.AddrFromSlice(deviceIP.To16()), tcpip.AddrFromSlice(localIP.To16())
	endpoint, e := tunnel.RWCEndpointNew(&packetConn{bufio.NewReaderSize(conn, 1<<16), conn}, uint32(p.ClientParameters.Mtu), 0)
	if e != nil {
		return e
	}
	t.endpoint = endpoint
	t.stack = stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv6.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	const nic = 1
	if err := t.stack.CreateNIC(nic, endpoint); err != nil {
		return fmt.Errorf("tunnel: %v", err)
	}
	address := t.localIP.WithPrefix()
	address.PrefixLen = 64
	if err := t.stack.AddProtocolAddress(nic, tcpip.ProtocolAddress{Protocol: ipv6.ProtocolNumber, AddressWithPrefix: address}, stack.AddressProperties{}); err != nil {
		return fmt.Errorf("tunnel: %v", err)
	}
	t.stack.SetRouteTable([]tcpip.Route{{Destination: header.IPv6EmptySubnet, NIC: nic}})
	go func() {
		// The data plane ends when the phone is unplugged or the connection drops.
		endpoint.Wait()
		t.Close()
	}()
	return nil
}

// DialTCP connects to a TCP port of the phone inside the tunnel.
func (t *Tunnel) DialTCP(c context.Context, port int) (net.Conn, error) {
	return gonet.DialContextTCP(c, t.stack, tcpip.FullAddress{NIC: 1, Addr: t.deviceIP, Port: uint16(port)}, ipv6.ProtocolNumber)
}

// ListenUDP opens a UDP socket on our tunnel address (port 0 picks one).
func (t *Tunnel) ListenUDP(port int) (*gonet.UDPConn, error) {
	return gonet.DialUDP(t.stack, &tcpip.FullAddress{NIC: 1, Addr: t.localIP, Port: uint16(port)}, nil, ipv6.ProtocolNumber)
}

// DeviceIP and LocalIP are the two ends of the tunnel.
func (t *Tunnel) DeviceIP() net.IP { return net.IP(t.deviceIP.AsSlice()) }
func (t *Tunnel) LocalIP() net.IP  { return net.IP(t.localIP.AsSlice()) }

// listen serves go-ios's userspace preamble (16-byte IPv6 address, uint32le port) on
// loopback, for connections made by this process only.
func (t *Tunnel) listen() error {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	t.listener = l
	go func() {
		for {
			client, e := l.Accept()
			if e != nil {
				return
			}
			go t.serve(client)
		}
	}()
	return nil
}

func (t *Tunnel) serve(client net.Conn) {
	defer client.Close()
	if !ownConnection(client) {
		return
	}
	preamble := make([]byte, 20)
	if _, e := io.ReadFull(client, preamble); e != nil {
		return
	}
	if !net.IP(preamble[:16]).Equal(t.DeviceIP()) {
		return
	}
	remote, e := t.DialTCP(context.Background(), int(binary.LittleEndian.Uint32(preamble[16:])))
	if e != nil {
		return
	}
	defer remote.Close()
	done := make(chan struct{}, 2)
	go func() { io.Copy(remote, client); done <- struct{}{} }()
	go func() { io.Copy(client, remote); done <- struct{}{} }()
	select {
	case <-done:
	case <-t.done:
	}
}

// timeout is a context that ends after d or with the tunnel.
func (t *Tunnel) timeout(d time.Duration) (context.Context, context.CancelFunc) {
	c, cancel := context.WithTimeout(context.Background(), d)
	go func() {
		select {
		case <-t.done:
			cancel()
		case <-c.Done():
		}
	}()
	return c, cancel
}

// Done is closed when the tunnel ends.
func (t *Tunnel) Done() <-chan struct{} { return t.done }

func (t *Tunnel) Close() error {
	var e error
	t.closeOnce.Do(func() {
		close(t.done)
		if t.listener != nil {
			t.listener.Close()
		}
		e = t.conn.Close()
		if t.stack != nil {
			t.stack.Close()
		}
	})
	return e
}

// rsdHandshake reads the RSD handshake on port inside the tunnel: the services and
// the phone's properties (go-ios keeps only the UDID).
func (t *Tunnel) rsdHandshake(port int) (ios.RsdHandshakeResponse, map[string]any, error) {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, e := t.DialTCP(c, port)
	if e != nil {
		return ios.RsdHandshakeResponse{}, nil, e
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	h, e := http.NewHttpConnection(conn)
	if e != nil {
		return ios.RsdHandshakeResponse{}, nil, e
	}
	defer h.Close()
	x, e := ios.CreateXpcConnection(h)
	if e != nil {
		return ios.RsdHandshakeResponse{}, nil, e
	}
	m, e := x.ReceiveOnClientServerStream()
	if e != nil {
		return ios.RsdHandshakeResponse{}, nil, e
	}
	props, _ := m["Properties"].(map[string]any)
	udid, _ := props["UniqueDeviceID"].(string)
	services, _ := m["Services"].(map[string]any)
	if udid == "" || m["MessageType"] != "Handshake" {
		return ios.RsdHandshakeResponse{}, nil, errors.New("respuesta RSD inesperada")
	}
	out := ios.RsdHandshakeResponse{Udid: udid, Services: map[string]ios.RsdServiceEntry{}}
	for name, v := range services {
		entry, _ := v.(map[string]any)
		port, _ := entry["Port"].(string)
		if p, e := strconv.ParseUint(port, 10, 32); e == nil {
			out.Services[name] = ios.RsdServiceEntry{Port: uint32(p)}
		}
	}
	return out, props, nil
}
