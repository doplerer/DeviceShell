package remotepairing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/http"
	"github.com/doplerer/deviceshell/internal/ios/coredevice"
)

// Pair pairs this PC with the phone behind tun (a USB tunnel) for Wi-Fi, or just
// verifies the pairing it already has, and returns the phone's RemotePairing
// identifier. A new pairing shows "Trust this computer?" on the phone: Pair waits
// for the answer until c ends.
func Pair(c context.Context, tun *coredevice.Tunnel, pm PairRecordManager) (string, error) {
	port := tun.Services[untrustedTunnelServiceName].Port
	if port == 0 {
		return "", errors.New("the iPhone does not advertise the pairing service")
	}
	conn, e := tun.DialTCP(c, int(port))
	if e != nil {
		return "", e
	}
	// Reading the phone's answer has no deadline of its own: closing the
	// connection when c ends stops it.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-c.Done():
			conn.Close()
		case <-done:
		}
	}()
	h, e := http.NewHttpConnection(conn)
	if e != nil {
		conn.Close()
		return "", e
	}
	x, e := ios.CreateXpcConnection(h)
	if e != nil {
		h.Close()
		return "", e
	}
	ts := newTunnelServiceWithXpc(x, h, pm)
	defer ts.Close()
	if e = ts.ManualPair(); e != nil {
		if c.Err() != nil {
			return "", fmt.Errorf("pairing got no answer: %w", c.Err())
		}
		return "", e
	}
	if ts.deviceIdentifier == "" {
		return "", errors.New("paired, but the iPhone did not give its identifier")
	}
	return ts.deviceIdentifier, nil
}

// pairTimeout is how long Pair waits by default for "Trust" on the phone.
const pairTimeout = 2 * time.Minute
