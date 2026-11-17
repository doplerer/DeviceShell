package coredevice

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"howett.net/plist"
)

// DisplayService video stream, kept open only because iOS 27 drops touch input
// without one (docs/iphone-protocol.md, "Sesión de DisplayService"). Its RTP is
// counted and discarded; RTCP receiver reports keep the phone from closing it.

const (
	displayService    = "com.apple.coredevice.displayservice"
	deviceInfoService = "com.apple.coredevice.deviceinfo"
)

// Captured parts of the negotiator offer's media blob (protobuf): the fields around
// our SSRC are the codec capabilities, identical in every session.
var (
	mediaBlobHead, _   = hex.DecodeString("08011001")
	videoAfterSSRC, _  = hex.DecodeString("10001a3f087b120a0801100118c387032000120a0801100218c387032000120a0801100118c387032000120a0801100218c3870320001a09464c533b53573a313b20011a2e0864120a0801100118c387032000120a0801100218c3870320001a10464c533b565241453a303b53573a313b200e3800403f50016001")
	mediaBlobTail, _   = hex.DecodeString("320d56696365726f7920312e372e3040004a0908ea1f1000188080014a0b080010c0d1e123188080204a0a08001080b489131880604a0508101084204a0b08001080dac409188080064a05080410e4324a0b080010809bee02188080084a0b08001080c2d72f188080404a0b080010808ece1c188080104a05080110ab026880c0dd87d2a0c0e9ed017002800100900101")
	remoteEndpointInfo = protoMessage(protoVarint(1, 0), protoVarint(2, 1), protoBytes(3, []byte("Mac15,9")), protoBytes(4, []byte("2205.3.1")), protoBytes(5, []byte("25F80")))
)

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func protoVarint(field int, v uint64) []byte {
	return appendVarint(appendVarint(nil, uint64(field)<<3), v)
}

func protoBytes(field int, v []byte) []byte {
	b := appendVarint(appendVarint(nil, uint64(field)<<3|2), uint64(len(v)))
	return append(b, v...)
}

func protoMessage(fields ...[]byte) []byte { return bytes.Join(fields, nil) }

// hevcCodecEntry is the offer's HEVC codec (payload type 100). Its stream drifts:
// after a few page swipes the phone's encoder predicts from pictures that differ from
// the ones it sent, and FFmpeg and Chrome's hardware decoder both show the same broken
// blocks. The H.264 codec (payload type 123) decodes clean.
var hevcCodecEntry, _ = hex.DecodeString("1a2e0864120a0801100118c387032000120a0801100218c3870320001a10464c533b565241453a303b53573a313b200e3800403f50016001")

// mediaBlob is the uncompressed media blob with our SSRC.
func mediaBlob(ssrc uint32, h264Only bool) []byte {
	video := append(protoVarint(1, uint64(ssrc)), videoAfterSSRC...)
	if h264Only {
		video = bytes.Replace(video, hevcCodecEntry, nil, 1)
	}
	return protoMessage(mediaBlobHead, protoBytes(5, video), mediaBlobTail)
}

// negotiatorOffer is the binary plist sent as negotiatorOffer.
func negotiatorOffer(ssrc uint32, callID string, h264Only bool) ([]byte, error) {
	var z bytes.Buffer
	w, _ := zlib.NewWriterLevel(&z, zlib.BestCompression)
	w.Write(mediaBlob(ssrc, h264Only))
	w.Close()
	return plist.Marshal(map[string]any{
		"avcMediaStreamNegotiatorMediaBlob":      z.Bytes(),
		"avcMediaStreamNegotiatorMode":           5,
		"avcMediaStreamOptionCallID":             callID,
		"avcMediaStreamOptionRemoteEndpointInfo": remoteEndpointInfo,
	}, plist.BinaryFormat)
}

// rtcpReport is the receiver report + empty SDES the phone expects every second.
func rtcpReport(ours, theirs, highestSeq uint32) []byte {
	b := make([]byte, 44)
	copy(b, []byte{0x81, 0xc9, 0x00, 0x07})
	binary.BigEndian.PutUint32(b[4:], ours)
	binary.BigEndian.PutUint32(b[8:], theirs)
	binary.BigEndian.PutUint32(b[16:], highestSeq)
	copy(b[32:], []byte{0x81, 0xca, 0x00, 0x02})
	binary.BigEndian.PutUint32(b[36:], ours)
	b[40] = 0x01
	return b
}

// Display is a running DisplayService stream.
type Display struct {
	Answer      map[string]any // the phone's answer to startmediastream
	PayloadType byte           // RTP payload type of the video: 123 H.264, 100 HEVC
	t           *Tunnel
	udp         *gonet.UDPConn
	Session     uuid.UUID
	ours        uint32
	theirs      uint32
	dest        *net.UDPAddr
	interval    time.Duration
	highest     atomic.Uint32 // extended highest RTP sequence received
	Packets     atomic.Int64
	Bytes       atomic.Int64                 // RTP bytes received
	tap         atomic.Pointer[func([]byte)] // sees each RTP packet (SetTap)
	rtcpTap     atomic.Pointer[func([]byte)] // sees each RTCP packet from the phone
	stop        chan struct{}
	stopOnce    sync.Once
	wg          sync.WaitGroup
	ended       chan struct{}
	endedOnce   sync.Once
	lastPacket  atomic.Int64 // unix nanoseconds
	// The phone's last sender report (middle 32 bits of its NTP time) and when it
	// arrived, echoed as LSR/DLSR so the phone can measure the round trip (RFC 3550 6.4.1).
	lastSR   atomic.Uint32
	lastSRAt atomic.Int64
}

func numberIn(m map[string]any, key string) (uint64, bool) {
	switch v := m[key].(type) {
	case uint64:
		return v, true
	case int64:
		return uint64(v), true
	case float64:
		return uint64(v), true
	}
	return 0, false
}

// StartDisplay opens the stream; it fails with the phone's message (for example
// "call is currently in progress") or the context's error on a timeout.
func (t *Tunnel) StartDisplay(c context.Context) (*Display, error) {
	return t.StartDisplayCodec(c, false)
}

// StartDisplayCodec is StartDisplay offering only H.264 when h264Only: the picture
// is then meant to be shown (Display.PayloadType says which codec the phone chose).
func (t *Tunnel) StartDisplayCodec(c context.Context, h264Only bool) (*Display, error) {
	udp, e := t.ListenUDP(0)
	if e != nil {
		return nil, e
	}
	var r [4]byte
	rand.Read(r[:])
	ours := binary.BigEndian.Uint32(r[:]) | 1<<28
	session := uuid.New()
	offer, e := negotiatorOffer(ours, strings.ToUpper(uuid.New().String()), h264Only)
	if e != nil {
		udp.Close()
		return nil, e
	}
	input := map[string]any{
		"type": "video", "direction": "output", "timeout": uint64(20),
		"senderIP": t.DeviceIP().String(), "receiverIP": t.LocalIP().String(),
		"receiverPort":            uint64(udp.LocalAddr().(*net.UDPAddr).Port),
		"clientSupportedFeatures": uint64(140),
		"negotiatorOffer":         offer,
		"options": map[string]any{
			"AVCMediaStreamNegotiatorAccessNetworkType":     map[string]any{"int": int64(1)},
			"AVCMediaStreamNegotiatorTransportProtocolType": map[string]any{"int": int64(2)},
			"CoreDeviceVideoDisplayMode":                    map[string]any{"string": "DisplayByID"},
			"VideoStreamForDisplayID":                       map[string]any{"int": int64(1)},
			"avcMediaStreamOptionClientSessionID":           map[string]any{"uuid": session},
		},
	}
	out, e := t.invoke(c, displayService, "startmediastream", "mediastreamstart", input)
	if e != nil {
		udp.Close()
		return nil, e
	}
	connection, _ := out["connection"].(map[string]any)
	config, _ := connection["streamConfig"].(map[string]any)
	port, ok1 := numberIn(config, "SourcePort")
	theirs, ok2 := numberIn(config, "LocalSSRC")
	if id, ok := uuidIn(connection, "options", "avcMediaStreamOptionClientSessionID", "uuid"); ok {
		session = id
	}
	d := &Display{Answer: out, t: t, udp: udp, Session: session, ours: ours, theirs: uint32(theirs), interval: time.Second, stop: make(chan struct{}), ended: make(chan struct{})}
	if pt, ok := numberIn(config, "TxPayloadType"); ok {
		d.PayloadType = byte(pt)
	}
	if remote, ok := numberIn(config, "RemoteSSRC"); ok {
		d.ours = uint32(remote)
	}
	if s, ok := config["RTCPSendInterval"].(float64); ok && s > 0 {
		d.interval = time.Duration(s * float64(time.Second))
	}
	if !ok1 || !ok2 {
		// Without the phone's port there are no reports and it closes the stream after 20 s.
		d.Stop(c)
		return nil, fmt.Errorf("startmediastream: reply without SourcePort/LocalSSRC")
	}
	d.dest = &net.UDPAddr{IP: t.DeviceIP(), Port: int(port)}
	d.lastPacket.Store(time.Now().UnixNano())
	d.wg.Add(2)
	go d.receive()
	go d.report()
	return d, nil
}

// receive counts RTP packets and tracks the highest sequence number (RFC 3550 A.1).
func (d *Display) receive() {
	defer d.wg.Done()
	defer d.end()
	buf := make([]byte, 65536)
	var cycles, last uint32
	first := true
	for {
		n, _, e := d.udp.ReadFrom(buf)
		if e != nil {
			return
		}
		if n < 8 || buf[0]>>6 != 2 {
			continue
		}
		if buf[1] >= 200 && buf[1] <= 206 {
			if buf[1] == 200 && n >= 20 {
				d.lastSR.Store(binary.BigEndian.Uint32(buf[10:]))
				d.lastSRAt.Store(time.Now().UnixNano())
			}
			if f := d.rtcpTap.Load(); f != nil {
				(*f)(buf[:n])
			}
			continue // RTCP from the phone
		}
		if n < 12 {
			continue
		}
		seq := uint32(binary.BigEndian.Uint16(buf[2:]))
		if !first && seq < last && last-seq > 0x8000 {
			cycles += 1 << 16
		}
		first, last = false, seq
		if ext := cycles | seq; ext > d.highest.Load() {
			d.highest.Store(ext)
		}
		d.Packets.Add(1)
		d.Bytes.Add(int64(n))
		d.lastPacket.Store(time.Now().UnixNano())
		if f := d.tap.Load(); f != nil {
			(*f)(buf[:n])
		}
	}
}

// SetTap hands every RTP packet to f (nil stops it); f must not keep the slice.
func (d *Display) SetTap(f func(packet []byte)) {
	if f == nil {
		d.tap.Store(nil)
		return
	}
	d.tap.Store(&f)
}

// SetRTCPTap hands every RTCP packet from the phone to f (nil stops it).
func (d *Display) SetRTCPTap(f func(packet []byte)) {
	if f == nil {
		d.rtcpTap.Store(nil)
		return
	}
	d.rtcpTap.Store(&f)
}

// SendRTCP sends a raw RTCP packet to the phone's media port.
func (d *Display) SendRTCP(b []byte) error {
	_, e := d.udp.WriteTo(b, d.dest)
	return e
}

// SSRCs returns ours and the phone's.
func (d *Display) SSRCs() (ours, theirs uint32) { return d.ours, d.theirs }

func (d *Display) report() {
	defer d.wg.Done()
	ticker := time.NewTicker(d.interval)
	defer ticker.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-d.t.done:
			return
		case <-ticker.C:
			r := rtcpReport(d.ours, d.theirs, d.highest.Load())
			if at := d.lastSRAt.Load(); at != 0 {
				binary.BigEndian.PutUint32(r[24:], d.lastSR.Load())
				binary.BigEndian.PutUint32(r[28:], uint32(time.Since(time.Unix(0, at)).Seconds()*65536))
			}
			if _, e := d.udp.WriteTo(r, d.dest); e != nil {
				return
			}
		}
	}
}

func (d *Display) end() { d.endedOnce.Do(func() { close(d.ended) }) }

// Ended is closed when the stream's socket stops (tunnel gone or Stop).
func (d *Display) Ended() <-chan struct{} { return d.ended }

// Idle is how long since the last video packet arrived.
func (d *Display) Idle() time.Duration { return time.Since(time.Unix(0, d.lastPacket.Load())) }

// Stop ends the reports and asks the phone to stop the session, waiting for its
// answer: closing the tunnel first can leave the session running on the phone.
func (d *Display) Stop(c context.Context) error {
	var e error
	d.stopOnce.Do(func() {
		close(d.stop)
		e = d.t.StopDisplaySession(c, d.Session)
		d.udp.Close()
		d.wg.Wait()
	})
	return e
}

// StopDisplaySession stops session and any other stream left on the phone; one
// phone has one DeviceShell stream at a time.
func (t *Tunnel) StopDisplaySession(c context.Context, session uuid.UUID) error {
	// Without stopAll the phone rejects the request, and with stopAll false it calls it
	// invalid; stopAll true also ends sessions an earlier connector left running.
	_, e := t.invoke(c, displayService, "stopmediastream", "mediastreamstop", map[string]any{
		"avcMediaStreamOptionClientSessionID": map[string]any{"uuid": session},
		"stopAll":                             true,
	})
	return e
}

// DisplaySessions lists the session ids the phone's media server reports.
func (t *Tunnel) DisplaySessions(c context.Context) ([]uuid.UUID, error) {
	out, e := t.invoke(c, displayService, "getmediastreamserverstatus", "mediastreamstatus", map[string]any{})
	if e != nil {
		return nil, e
	}
	var ids []uuid.UUID
	sessions, _ := out["sessions"].([]any)
	for _, s := range sessions {
		if id, ok := uuidIn(s, "connection", "options", "avcMediaStreamOptionClientSessionID", "uuid"); ok {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// DisplayInfo is the phone's display description (sizes, orientation).
func (t *Tunnel) DisplayInfo(c context.Context) (map[string]any, error) {
	return t.invoke(c, deviceInfoService, "getdisplayinfo", "", map[string]any{})
}

func (t *Tunnel) HasDisplay() bool { return t.Services[displayService].Port != 0 }
