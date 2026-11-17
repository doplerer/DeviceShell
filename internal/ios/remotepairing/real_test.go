package remotepairing

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/doplerer/deviceshell/internal/ios/coredevice"
)

// With a real iPhone (opt-in): DEVICESHELL_RP_UDID=<udid> [DEVICESHELL_RP_HOST=<ip>] go test
// ./internal/ios/remotepairing -run TestRealPhone -v. Over USB it verifies (or makes) the
// pairing and reads the identifier; then it looks for the phone on the LAN and opens
// its Wi-Fi tunnel. Saves the raw mDNS answer to testdata/advert.bin.
func TestRealPhone(t *testing.T) {
	udid := os.Getenv("DEVICESHELL_RP_UDID")
	if udid == "" {
		t.Skip("set DEVICESHELL_RP_UDID")
	}
	pm, e := NewPairRecordManager(filepath.Join("..", "..", "..", "dist", "tools", "ios", "remotepairing"))
	if e != nil {
		t.Fatal(e)
	}
	var identifier string
	if d, e := ios.GetDevice(udid); e == nil {
		tun, e := coredevice.OpenDevice(d)
		if e != nil {
			t.Fatal(e)
		}
		c, cancel := context.WithTimeout(context.Background(), pairTimeout)
		start := time.Now()
		identifier, e = Pair(c, tun, pm)
		cancel()
		tun.Close()
		t.Logf("USB pair/verify: %q %v (%v)", identifier, e, time.Since(start).Round(time.Millisecond))
	}
	var hints []string
	if h := os.Getenv("DEVICESHELL_RP_HOST"); h != "" {
		hints = append(hints, h)
	}
	start := time.Now()
	ads := Browse(context.Background(), hints, 1500*time.Millisecond)
	t.Logf("browse (%v): %+v", time.Since(start).Round(time.Millisecond), ads)
	for _, a := range ads {
		if identifier != "" && a.Identifier != identifier {
			continue
		}
		// Keep one real answer for the parser test.
		if conn, e := net.ListenUDP("udp4", nil); e == nil {
			conn.WriteToUDP(mdnsQuery(), &net.UDPAddr{IP: net.ParseIP(a.Host), Port: 5353})
			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			buf := make([]byte, 9000)
			if n, _, e := conn.ReadFromUDP(buf); e == nil {
				os.MkdirAll("testdata", 0o755)
				os.WriteFile(filepath.Join("testdata", "advert.bin"), buf[:n], 0o644)
			}
			conn.Close()
		}
		start = time.Now()
		conn, id, e := Dial(a.Host, a.Port, pm)
		if e != nil {
			t.Fatalf("Dial %s: %v", a.Host, e)
		}
		tun, e := coredevice.OpenConn(udid, "27", conn)
		if e != nil {
			t.Fatal(e)
		}
		t.Logf("Wi-Fi tunnel in %v: identifier %q, RSD udid %q, iOS %q, %d services, props %v", time.Since(start).Round(time.Millisecond), id, tun.UDID, tun.Version, len(tun.Services), tun.Properties)
		tun.Close()
		return
	}
	t.Fatal("phone not found on the LAN")
}
