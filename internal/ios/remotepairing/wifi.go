package remotepairing

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/danielpaulus/go-ios/ios/tunnel/tlspsk"
)

// rpConn carries the RemotePairing control channel over the LAN: each message is
// "RPPairing" + a 16-bit big-endian length + the envelope's value as JSON (byte
// strings in base64), instead of RemoteXPC.
type rpConn struct {
	c   net.Conn
	Log func(string, ...any)
}

func (r *rpConn) Send(data map[string]interface{}, _ ...uint32) error {
	body, e := json.Marshal(data["value"])
	if e != nil {
		return e
	}
	if r.Log != nil {
		r.Log("→ %s", body)
	}
	var b bytes.Buffer
	b.WriteString("RPPairing")
	binary.Write(&b, binary.BigEndian, uint16(len(body)))
	b.Write(body)
	_, e = r.c.Write(b.Bytes())
	return e
}

func (r *rpConn) ReceiveOnClientServerStream() (map[string]interface{}, error) {
	head := make([]byte, 11)
	if _, e := io.ReadFull(r.c, head); e != nil {
		return nil, e
	}
	if string(head[:9]) != "RPPairing" {
		return nil, fmt.Errorf("RPPairing: bad magic %q", head[:9])
	}
	body := make([]byte, binary.BigEndian.Uint16(head[9:]))
	if _, e := io.ReadFull(r.c, body); e != nil {
		return nil, e
	}
	if r.Log != nil {
		r.Log("← %s", body)
	}
	var v map[string]interface{}
	if e := json.Unmarshal(body, &v); e != nil {
		return nil, e
	}
	// The byte strings the codec reads back.
	if m, e := getChildMap(v, "message"); e == nil {
		if pd, e := getChildMap(m, "plain", "_0", "event", "_0", "pairingData", "_0"); e == nil {
			if s, ok := pd["data"].(string); ok {
				pd["data"], _ = base64.StdEncoding.DecodeString(s)
			}
		}
		if se, e := getChildMap(m, "streamEncrypted"); e == nil {
			if s, ok := se["_0"].(string); ok {
				se["_0"], _ = base64.StdEncoding.DecodeString(s)
			}
		}
	}
	return map[string]interface{}{"value": v}, nil
}

// ErrNotPaired: the phone refused the pair-verify, it does not trust this PC.
var ErrNotPaired = errors.New("the iPhone is not paired with this PC")

// Dial verifies the RemotePairing made over USB on the phone's _remotepairing._tcp
// port and returns the TLS-PSK tunnel connection and the phone's identifier: the
// CDTunnel handshake and the IPv6 packets run on it as on CoreDeviceProxy.
func Dial(host string, port int, pm PairRecordManager) (io.ReadWriteCloser, string, error) {
	conn, ts, e := dial(host, port, pm)
	if e != nil {
		return nil, "", e
	}
	return conn, ts.deviceIdentifier, nil
}

func dial(host string, port int, pm PairRecordManager) (io.ReadWriteCloser, *tunnelService, error) {
	var log func(string, ...any)
	c, e := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(port)), 5*time.Second)
	if e != nil {
		return nil, nil, e
	}
	// A phone that stops answering mid-handshake must not hang the connector.
	c.SetDeadline(time.Now().Add(15 * time.Second))
	ts := &tunnelService{c: c, controlChannel: newControlChannelReadWriter(&rpConn{c, log}), pairRecords: pm}
	if e = ts.controlChannel.writeRequest(map[string]interface{}{"handshake": map[string]interface{}{"_0": map[string]interface{}{
		"hostOptions": map[string]interface{}{"attemptPairVerify": true}, "wireProtocolVersion": int64(19)}}}); e != nil {
		c.Close()
		return nil, nil, fmt.Errorf("handshake: %w", e)
	}
	if _, e = ts.controlChannel.read(); e != nil {
		c.Close()
		return nil, nil, fmt.Errorf("handshake: %w", e)
	}
	if e = ts.verifyPair(); e != nil {
		c.Close()
		var refused tlvError
		if errors.As(e, &refused) {
			return nil, nil, fmt.Errorf("%w (%v)", ErrNotPaired, e)
		}
		return nil, nil, fmt.Errorf("pair-verify: %w", e)
	}
	tp, e := ts.createTcpTunnelListener()
	if e != nil {
		c.Close()
		return nil, nil, fmt.Errorf("createListener: %w", e)
	}
	tc, e := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(tp)), 5*time.Second)
	if e != nil {
		c.Close()
		return nil, nil, fmt.Errorf("tunnel port %d: %w", tp, e)
	}
	tc.SetDeadline(time.Now().Add(15 * time.Second))
	tls, e := tlspsk.Client(tc, ts.sharedSecret)
	if e != nil {
		c.Close()
		tc.Close()
		return nil, nil, fmt.Errorf("TLS-PSK: %w", e)
	}
	c.SetDeadline(time.Time{})
	tc.SetDeadline(time.Time{})
	// The control channel stays open while the tunnel lives.
	return &tunnelConn{tls, c}, ts, nil
}

type tunnelConn struct {
	io.ReadWriteCloser
	control net.Conn
}

func (t *tunnelConn) Close() error {
	t.control.Close()
	return t.ReadWriteCloser.Close()
}
