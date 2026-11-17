package remotepairing

import (
	"bytes"
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// A real answer of an iPhone to the _remotepairing._tcp query (testdata/advert.bin,
// with its identifier, auth tag, host name and addresses replaced by placeholders)
// gives its identifier and port.
func TestParseAdvertReadsRealAnswer(t *testing.T) {
	b, e := os.ReadFile(filepath.Join("testdata", "advert.bin"))
	if e != nil {
		t.Fatal(e)
	}
	a, ok := parseAdvert(b)
	if !ok || a.Identifier != "0A1B2C3D-0000-4000-8000-00000000F00D" || a.Port != 49152 {
		t.Fatalf("%+v %v", a, ok)
	}
	if _, ok := parseAdvert(mdnsQuery()); ok {
		t.Fatal("a query is not an answer")
	}
	for n := range b { // truncated answers never panic
		parseAdvert(b[:n])
	}
}

// The LAN control channel: "RPPairing", 16-bit length, JSON; byte strings in base64.
func TestRPPairingFraming(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() {
		(&rpConn{c: a}).Send(map[string]interface{}{"value": map[string]interface{}{"hello": 1}})
	}()
	head := make([]byte, 11)
	if _, e := b.Read(head); e != nil || string(head[:9]) != "RPPairing" {
		t.Fatalf("%q %v", head, e)
	}
	body := make([]byte, int(head[9])<<8|int(head[10]))
	if _, e := b.Read(body); e != nil || string(body) != `{"hello":1}` {
		t.Fatalf("%q %v", body, e)
	}
	data := []byte{1, 2, 3}
	msg := `{"message":{"plain":{"_0":{"event":{"_0":{"pairingData":{"_0":{"data":"` + base64.StdEncoding.EncodeToString(data) + `"}}}}}}}}`
	go func() {
		var frame bytes.Buffer
		frame.WriteString("RPPairing")
		frame.Write([]byte{byte(len(msg) >> 8), byte(len(msg))})
		frame.WriteString(msg)
		b.Write(frame.Bytes())
	}()
	got, e := (&rpConn{c: a}).ReceiveOnClientServerStream()
	if e != nil {
		t.Fatal(e)
	}
	pd := got["value"].(map[string]interface{})["message"].(map[string]interface{})["plain"].(map[string]interface{})["_0"].(map[string]interface{})["event"].(map[string]interface{})["_0"].(map[string]interface{})["pairingData"].(map[string]interface{})["_0"].(map[string]interface{})
	if !bytes.Equal(pd["data"].([]byte), data) {
		t.Fatalf("data %v", pd["data"])
	}
}

// The addresses asked directly when a phone is not found by multicast: the hosts of
// small private networks, without the PC's own.
func TestHostsInListsSmallPrivateNetworks(t *testing.T) {
	network := func(cidr string) *net.IPNet {
		ip, n, e := net.ParseCIDR(cidr)
		if e != nil {
			t.Fatal(e)
		}
		n.IP = ip.To4()
		return n
	}
	hosts := hostsIn([]*net.IPNet{network("192.168.1.58/24")}, 1024)
	if len(hosts) != 253 || hosts[0] != "192.168.1.1" || hosts[len(hosts)-1] != "192.168.1.254" {
		t.Fatal(len(hosts), hosts[0], hosts[len(hosts)-1])
	}
	for _, h := range hosts {
		if h == "192.168.1.58" {
			t.Fatal("own address asked")
		}
	}
	if n := len(hostsIn([]*net.IPNet{network("10.0.0.2/22")}, 1024)); n != 1021 {
		t.Fatal("/22", n)
	}
	// Too big, public (Tailscale's CGNAT range) or a single address: nothing.
	for _, cidr := range []string{"10.1.0.2/16", "100.101.1.2/10", "8.8.8.8/24", "192.168.1.58/32"} {
		if h := hostsIn([]*net.IPNet{network(cidr)}, 1024); len(h) != 0 {
			t.Fatal(cidr, len(h))
		}
	}
}
