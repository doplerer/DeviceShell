package remotepairing

import (
	"context"
	"encoding/binary"
	"net"
	"strings"
	"time"
)

// Advert is a paired-capable phone found on the LAN: its RemotePairing identifier
// (TXT "identifier") and where its _remotepairing._tcp service listens.
type Advert struct {
	Identifier string
	Host       string
	Port       int
}

const remotePairingService = "_remotepairing._tcp.local"

// mdnsQuery asks for the PTR records of _remotepairing._tcp with the "unicast
// response" bit (RFC 6762 5.4): the phones answer to our port, so Windows needs no
// Bonjour and no listener on 5353.
func mdnsQuery() []byte {
	q := []byte{0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0}
	for _, l := range strings.Split(remotePairingService, ".") {
		q = append(q, byte(len(l)))
		q = append(q, l...)
	}
	return append(q, 0, 0, 12, 0x80, 1)
}

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// Browse looks for phones on the LAN for wait: one multicast query on every IPv4
// interface plus a direct query to each address in hints (networks that drop
// multicast still let the phone answer a unicast query). Answers from the same
// identifier count once; the address is the one the answer came from.
func Browse(c context.Context, hints []string, wait time.Duration) []Advert {
	var conns []*net.UDPConn
	var nets []*net.IPNet // the network of each conn (nil: any)
	locals := localIPv4()
	if len(locals) == 0 {
		locals = []*net.IPNet{nil}
	}
	for _, n := range locals {
		var ip net.IP
		if n != nil {
			ip = n.IP
		}
		if conn, e := net.ListenUDP("udp4", &net.UDPAddr{IP: ip}); e == nil {
			conns, nets = append(conns, conn), append(nets, n)
		}
	}
	// Each direct query leaves from the interface on the address's network.
	from := func(ip net.IP) *net.UDPConn {
		for i, n := range nets {
			if n != nil && n.Contains(ip) {
				return conns[i]
			}
		}
		return conns[0]
	}
	defer func() {
		for _, conn := range conns {
			conn.Close()
		}
	}()
	query := mdnsQuery()
	ask := func() {
		for _, conn := range conns {
			conn.WriteToUDP(query, mdnsGroup)
		}
		if len(conns) > 0 {
			for _, h := range hints {
				if ip := net.ParseIP(h).To4(); ip != nil {
					from(ip).WriteToUDP(query, &net.UDPAddr{IP: ip, Port: 5353})
				}
			}
		}
	}
	// A phone saving power on Wi-Fi can miss a query: ask three times.
	ask()
	stopAsking := make(chan struct{})
	defer close(stopAsking)
	go func() {
		for range 2 {
			select {
			case <-stopAsking:
				return
			case <-time.After(wait / 4):
			}
			ask()
		}
	}()
	found := map[string]Advert{}
	answers := make(chan Advert, 64)
	deadline := time.Now().Add(wait)
	if d, ok := c.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	done := make(chan struct{})
	for _, conn := range conns {
		conn.SetReadDeadline(deadline)
		go func() {
			defer func() { done <- struct{}{} }()
			buf := make([]byte, 9000)
			for {
				n, from, e := conn.ReadFromUDP(buf)
				if e != nil {
					return
				}
				if a, ok := parseAdvert(buf[:n]); ok {
					a.Host = from.IP.String()
					answers <- a
				}
			}
		}()
	}
	for running := len(conns); running > 0; {
		select {
		case a := <-answers:
			found[a.Identifier] = a
		case <-done:
			running--
		}
	}
	for len(answers) > 0 {
		a := <-answers
		found[a.Identifier] = a
	}
	out := make([]Advert, 0, len(found))
	for _, a := range found {
		out = append(out, a)
	}
	return out
}

// localIPv4 lists the IPv4 networks (with this PC's address in each) of the
// interfaces that are up and can multicast.
func localIPv4() []*net.IPNet {
	var out []*net.IPNet
	ifaces, _ := net.Interfaces()
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&net.FlagMulticast == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLinkLocalUnicast() {
				mask := net.CIDRMask(32, 32) // unknown mask: the interface, no network
				if ones, bits := n.Mask.Size(); bits == 32 || bits == 128 && ones >= 96 {
					mask = net.CIDRMask(ones-(bits-32), 32)
				}
				out = append(out, &net.IPNet{IP: n.IP.To4(), Mask: mask})
			}
		}
	}
	return out
}

// LocalHosts lists every address of this PC's private IPv4 networks (RFC 1918) with
// at most limit addresses, to ask each one directly when a phone is not found by
// multicast: networks that drop multicast, or a phone saving power, still answer a
// query sent to its own address.
func LocalHosts(limit int) []string {
	return hostsIn(localIPv4(), limit)
}

// hostsIn lists the addresses of nets that can be hosts (not the network, the
// broadcast or the PC's own), skipping public networks and those over limit.
func hostsIn(nets []*net.IPNet, limit int) []string {
	var out []string
	seen := map[string]bool{}
	for _, n := range nets {
		ip := n.IP.To4()
		ones, bits := n.Mask.Size()
		if ip == nil || bits != 32 || !ip.IsPrivate() || 1<<(32-ones) > limit || ones > 30 {
			continue
		}
		base := binary.BigEndian.Uint32(ip.Mask(n.Mask))
		for i := uint32(1); i < 1<<(32-ones)-1; i++ {
			h := make(net.IP, 4)
			binary.BigEndian.PutUint32(h, base+i)
			if s := h.String(); !h.Equal(ip) && !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// parseAdvert reads an mDNS answer: the SRV record of a _remotepairing._tcp
// instance gives the port, its TXT record the identifier.
func parseAdvert(buf []byte) (Advert, bool) {
	if len(buf) < 12 || buf[2]&0x80 == 0 { // a response
		return Advert{}, false
	}
	skipName := func(i int) int {
		for i < len(buf) {
			l := int(buf[i])
			switch {
			case l == 0:
				return i + 1
			case l&0xc0 == 0xc0:
				return i + 2
			}
			i += 1 + l
		}
		return len(buf)
	}
	var a Advert
	qd := int(binary.BigEndian.Uint16(buf[4:]))
	rr := int(binary.BigEndian.Uint16(buf[6:])) + int(binary.BigEndian.Uint16(buf[8:])) + int(binary.BigEndian.Uint16(buf[10:]))
	i := 12
	for range qd {
		i = skipName(i) + 4
	}
	for range rr {
		i = skipName(i)
		if i+10 > len(buf) {
			break
		}
		typ, rdlen := binary.BigEndian.Uint16(buf[i:]), int(binary.BigEndian.Uint16(buf[i+8:]))
		i += 10
		if i+rdlen > len(buf) {
			break
		}
		data := buf[i : i+rdlen]
		i += rdlen
		switch typ {
		case 33: // SRV: priority, weight, port, target
			if len(data) >= 6 {
				a.Port = int(binary.BigEndian.Uint16(data[4:]))
			}
		case 16: // TXT: length-prefixed key=value strings
			for t := data; len(t) > 0 && int(t[0]) < len(t); t = t[1+int(t[0]):] {
				if kv := string(t[1 : 1+int(t[0])]); strings.HasPrefix(kv, "identifier=") {
					a.Identifier = strings.TrimPrefix(kv, "identifier=")
				}
			}
		}
	}
	return a, a.Identifier != "" && a.Port != 0
}
