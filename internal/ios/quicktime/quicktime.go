// Package quicktime captures the iPhone screen over USB with Apple's QuickTime
// screen-capture protocol: the H.264 stream
// QuickTime Player records on a Mac, at native resolution and up to 60 fps, without the
// bit-rate cap of DisplayService. Based on quicktime_video_hack
// (github.com/danielpaulus/quicktime_video_hack, MIT License, Copyright (c) 2019 danielpaulus). The USB side
// (WinUSB) is in quicktime_windows.go; other systems have no capture.
package quicktime

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

// Every packet and value is a u32le length (including itself) and a FourCC stored as a
// little-endian integer, so "dict" travels as "tcid".
func qtTag(fourcc string) []byte {
	return []byte{fourcc[3], fourcc[2], fourcc[1], fourcc[0]}
}

func qtAtom(fourcc string, payload ...[]byte) []byte {
	n := 8
	for _, p := range payload {
		n += len(p)
	}
	b := append(binary.LittleEndian.AppendUint32(nil, uint32(n)), qtTag(fourcc)...)
	for _, p := range payload {
		b = append(b, p...)
	}
	return b
}

type qtEntry struct {
	fourcc string
	body   []byte
}

func qtAtoms(buf []byte) ([]qtEntry, error) {
	var out []qtEntry
	for len(buf) >= 8 {
		n := binary.LittleEndian.Uint32(buf)
		if n < 8 || int64(n) > int64(len(buf)) {
			return out, fmt.Errorf("bad atom length %d of %d", n, len(buf))
		}
		out = append(out, qtEntry{string(qtTag(string(buf[4:8]))), buf[8:n]})
		buf = buf[n:]
	}
	return out, nil
}

// qtKV keeps the order of dictionary keys, as QuickTime sends them.
type qtKV struct {
	key   string
	value any
}

func qtValue(v any) []byte {
	switch v := v.(type) {
	case bool:
		if v {
			return qtAtom("bulv", []byte{1})
		}
		return qtAtom("bulv", []byte{0})
	case float64:
		return qtAtom("nmbv", []byte{6}, binary.LittleEndian.AppendUint64(nil, math.Float64bits(v)))
	case int:
		return qtAtom("nmbv", []byte{3}, binary.LittleEndian.AppendUint32(nil, uint32(v)))
	case string:
		return qtAtom("strv", []byte(v))
	case []byte:
		return qtAtom("datv", v)
	case []qtKV:
		return qtDict(v)
	}
	panic(fmt.Sprintf("quicktime: unsupported value %T", v))
}

func qtDict(d []qtKV) []byte {
	var body []byte
	for _, kv := range d {
		body = append(body, qtAtom("keyv", qtAtom("strk", []byte(kv.key)), qtValue(kv.value))...)
	}
	return qtAtom("dict", body)
}

// qtParseValue reads one value; dictionary keys are strings or uint16 indexes.
func qtParseValue(fourcc string, body []byte) any {
	switch fourcc {
	case "strv":
		return string(body)
	case "datv":
		return body
	case "bulv":
		return len(body) > 0 && body[0] == 1
	case "nmbv":
		if len(body) >= 9 && body[0] == 6 {
			return math.Float64frombits(binary.LittleEndian.Uint64(body[1:]))
		}
		if len(body) >= 9 && body[0] == 4 {
			return binary.LittleEndian.Uint64(body[1:])
		}
		if len(body) >= 5 {
			return uint64(binary.LittleEndian.Uint32(body[1:]))
		}
	case "dict", "extn":
		return qtParseDict(body)
	case "fdsc":
		return qtParseFormat(body)
	}
	return nil
}

func qtParseDict(body []byte) map[any]any {
	out := map[any]any{}
	entries, _ := qtAtoms(body)
	for _, e := range entries {
		if e.fourcc != "keyv" {
			continue
		}
		pair, _ := qtAtoms(e.body)
		if len(pair) < 2 {
			continue
		}
		var key any = string(pair[0].body)
		if pair[0].fourcc != "strk" && len(pair[0].body) >= 2 {
			key = binary.LittleEndian.Uint16(pair[0].body)
		}
		out[key] = qtParseValue(pair[1].fourcc, pair[1].body)
	}
	return out
}

// Format is a video format description: codec, size and its avcC/hvcC record.
type Format struct {
	Codec         string
	Width, Height int
	Record        []byte
}

func (f Format) HEVC() bool {
	return len(f.Codec) >= 3 && (f.Codec[:3] == "hvc" || f.Codec[:3] == "hev")
}

func qtParseFormat(body []byte) Format {
	var f Format
	entries, _ := qtAtoms(body)
	for _, e := range entries {
		switch {
		case e.fourcc == "codc" && len(e.body) >= 4:
			f.Codec = string(qtTag(string(e.body[:4])))
		case e.fourcc == "vdim" && len(e.body) >= 8:
			f.Width, f.Height = int(binary.LittleEndian.Uint32(e.body)), int(binary.LittleEndian.Uint32(e.body[4:]))
		case e.fourcc == "extn":
			// 49 = SampleDescriptionExtensionAtoms, 105 = its avcC/hvcC entry.
			if atoms, ok := qtParseDict(e.body)[uint16(49)].(map[any]any); ok {
				f.Record, _ = atoms[uint16(105)].([]byte)
			}
		}
	}
	return f
}

// Sample splits a sample buffer into its data and, when it carries one, a new format.
func Sample(sbuf []byte) (data []byte, format *Format, err error) {
	outer, err := qtAtoms(sbuf)
	if err != nil || len(outer) == 0 {
		return nil, nil, errors.New("empty sample buffer")
	}
	entries, err := qtAtoms(outer[0].body)
	for _, e := range entries {
		switch e.fourcc {
		case "sdat":
			data = e.body
		case "fdsc":
			f := qtParseFormat(e.body)
			format = &f
		}
	}
	return data, format, err
}

var annexBStart = []byte{0, 0, 0, 1}

// AnnexBParameters returns the SPS/PPS (and VPS for HEVC) of an avcC/hvcC record as
// Annex B units, and the size of the NAL length prefix of the samples.
func AnnexBParameters(record []byte, hevc bool) ([]byte, int, error) {
	bad := errors.New("invalid decoder configuration record")
	var out []byte
	take := func(b []byte, count int) ([]byte, error) {
		for range count {
			if len(b) < 2 || len(b) < 2+int(binary.BigEndian.Uint16(b)) {
				return nil, bad
			}
			n := int(binary.BigEndian.Uint16(b))
			out = append(append(out, annexBStart...), b[2:2+n]...)
			b = b[2+n:]
		}
		return b, nil
	}
	if hevc {
		if len(record) < 23 {
			return nil, 0, bad
		}
		rest, arrays := record[23:], int(record[22])
		for range arrays {
			if len(rest) < 3 {
				return nil, 0, bad
			}
			var e error
			if rest, e = take(rest[3:], int(binary.BigEndian.Uint16(rest[1:]))); e != nil {
				return nil, 0, e
			}
		}
		return out, int(record[21]&3) + 1, nil
	}
	if len(record) < 7 {
		return nil, 0, bad
	}
	rest, e := take(record[6:], int(record[5]&0x1f))
	if e != nil || len(rest) < 1 {
		return nil, 0, bad
	}
	if _, e = take(rest[1:], int(rest[0])); e != nil {
		return nil, 0, e
	}
	return out, int(record[4]&3) + 1, nil
}

// annexB rewrites length-prefixed NAL units with start codes, appending to out.
func AnnexB(out, data []byte, lengthSize int) ([]byte, error) {
	for len(data) > 0 {
		if len(data) < lengthSize {
			return out, errors.New("truncated NAL length")
		}
		n := 0
		for _, b := range data[:lengthSize] {
			n = n<<8 | int(b)
		}
		data = data[lengthSize:]
		if n > len(data) {
			return out, errors.New("truncated NAL unit")
		}
		out = append(append(out, annexBStart...), data[:n]...)
		data = data[n:]
	}
	return out, nil
}

func qtAsyn(ref uint64, fourcc string, payload []byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(20+len(payload)))
	b = append(b, qtTag("asyn")...)
	b = binary.LittleEndian.AppendUint64(b, ref)
	return append(append(b, qtTag(fourcc)...), payload...)
}

func qtReply(correlation uint64, payload []byte) []byte {
	b := binary.LittleEndian.AppendUint32(nil, uint32(20+len(payload)))
	b = append(b, qtTag("rply")...)
	b = binary.LittleEndian.AppendUint64(b, correlation)
	return append(binary.LittleEndian.AppendUint32(b, 0), payload...)
}

const qtEmptyRef = 1

// Host capabilities, as QuickTime sends them (quicktime_video_hack values). The device
// keeps its native size whatever DisplaySize says.
var (
	qtHPD1 = qtDict([]qtKV{{"Valeria", true}, {"HEVCDecoderSupports444", true}, {"DisplaySize", []qtKV{{"Width", 1920.0}, {"Height", 1200.0}}}})
	qtHPA1 = func() []byte {
		asbd := binary.LittleEndian.AppendUint64(nil, math.Float64bits(48000))
		asbd = append(asbd, qtTag("lpcm")...)
		for _, v := range []uint32{12, 4, 1, 4, 2, 16, 0} {
			asbd = binary.LittleEndian.AppendUint32(asbd, v)
		}
		asbd = binary.LittleEndian.AppendUint64(asbd, math.Float64bits(48000))
		asbd = binary.LittleEndian.AppendUint64(asbd, math.Float64bits(48000))
		return qtDict([]qtKV{{"BufferAheadInterval", 0.07300000000000001}, {"deviceUID", "Valeria"}, {"ScreenLatency", 0.04}, {"formats", asbd}, {"EDIDAC3Support", 0}, {"deviceName", "Valeria"}})
	}()
)

// qtTransport is the claimed capture interface: read returns 0 bytes on its timeout.
type qtTransport interface {
	read(buf []byte) (int, error)
	write(data []byte) error
}

type qtAudioSample struct {
	ticks uint64
	at    time.Time
}

// Session answers the device's protocol and hands every video sample buffer to Samples.
type Session struct {
	t          qtTransport
	writeMu    sync.Mutex
	Samples    chan []byte
	halt       chan struct{}
	haltOnce   sync.Once
	released   chan struct{}
	relOnce    sync.Once
	done       chan struct{}
	need       []byte
	audioRef   atomic.Uint64
	clockStart time.Time
	audio      [2]*qtAudioSample // first and last device audio clock samples
	err        atomic.Value      // string: why the session ended
	Received   atomic.Int64      // bytes from the device, read and reset by diagnostics
	log        func(format string, args ...any)
}

func newQTSession(t qtTransport, log func(string, ...any)) *Session {
	return &Session{t: t, Samples: make(chan []byte, 64), halt: make(chan struct{}), released: make(chan struct{}), done: make(chan struct{}), clockStart: time.Now(), log: log}
}

func (q *Session) write(b []byte) error {
	q.writeMu.Lock()
	defer q.writeMu.Unlock()
	return q.t.write(b)
}

func (q *Session) Running() bool {
	select {
	case <-q.done:
		return false
	default:
		return true
	}
}

func (q *Session) Failure() string {
	s, _ := q.err.Load().(string)
	return s
}

// readLoop runs until stop or a transport failure; it closes Samples on the way out.
func (q *Session) readLoop() {
	defer close(q.done)
	defer close(q.Samples)
	buf := make([]byte, 1<<20)
	var pending []byte
	fail := func(e error) {
		select {
		case <-q.halt:
		default:
			q.err.Store(e.Error())
			q.log("QuickTime capture ended: %v", e)
		}
	}
	for {
		select {
		case <-q.halt:
			return
		default:
		}
		n, e := q.t.read(buf)
		if e != nil {
			fail(e)
			return
		}
		if n == 0 {
			continue
		}
		q.Received.Add(int64(n))
		pending = append(pending, buf[:n]...)
		for len(pending) >= 4 {
			size := binary.LittleEndian.Uint32(pending)
			if size < 4 {
				fail(fmt.Errorf("bad packet length %d", size))
				return
			}
			if int64(len(pending)) < int64(size) {
				break
			}
			packet := bytes.Clone(pending[4:size])
			pending = pending[size:]
			if e = q.handle(packet); e != nil {
				fail(e)
				return
			}
		}
	}
}

func (q *Session) handle(p []byte) error {
	if len(p) < 4 {
		return nil
	}
	kind := string(qtTag(string(p[:4])))
	if kind == "ping" {
		b := append(binary.LittleEndian.AppendUint32(nil, 16), qtTag("ping")...)
		return q.write(binary.LittleEndian.AppendUint64(b, 0x0000000100000000))
	}
	if len(p) < 16 {
		return nil
	}
	sub := string(qtTag(string(p[12:16])))
	if kind == "asyn" {
		switch sub {
		case "feed":
			// Ask for the next frame before decoding this one.
			if q.need != nil {
				if e := q.write(q.need); e != nil {
					return e
				}
			}
			select {
			case q.Samples <- p[16:]:
			case <-q.halt:
			}
		case "eat!":
			q.audioTime(p[16:])
		case "rels":
			q.relOnce.Do(func() { close(q.released) })
		}
		return nil
	}
	if kind != "sync" || len(p) < 24 {
		return nil
	}
	ref := binary.LittleEndian.Uint64(p[4:])
	correlation := binary.LittleEndian.Uint64(p[16:])
	u64 := func(v uint64) []byte { return binary.LittleEndian.AppendUint64(nil, v) }
	switch sub {
	case "cwpa":
		if len(p) < 32 {
			return nil
		}
		deviceRef := binary.LittleEndian.Uint64(p[24:])
		q.audioRef.Store(deviceRef)
		for _, b := range [][]byte{qtAsyn(qtEmptyRef, "hpd1", qtHPD1), qtAsyn(qtEmptyRef, "hpd1", qtHPD1), qtReply(correlation, u64(deviceRef+1000)), qtAsyn(deviceRef, "hpa1", qtHPA1)} {
			if e := q.write(b); e != nil {
				return e
			}
		}
	case "cvrp":
		if len(p) < 32 {
			return nil
		}
		deviceRef := binary.LittleEndian.Uint64(p[24:])
		if entries, _ := qtAtoms(p[32:]); len(entries) > 0 {
			if f, ok := qtParseDict(entries[0].body)["FormatDescription"].(Format); ok {
				q.log("QuickTime stream: %s %dx%d", f.Codec, f.Width, f.Height)
			}
		}
		q.need = qtAsyn(deviceRef, "need", nil)
		if e := q.write(q.need); e != nil {
			return e
		}
		return q.write(qtReply(correlation, u64(deviceRef+0x1000AF)))
	case "clok":
		q.clockStart = time.Now()
		return q.write(qtReply(correlation, u64(ref+0x10000)))
	case "time":
		b := u64(uint64(time.Since(q.clockStart).Nanoseconds()))
		b = binary.LittleEndian.AppendUint32(b, 1_000_000_000)
		b = binary.LittleEndian.AppendUint32(b, 1)
		return q.write(qtReply(correlation, append(b, u64(0)...)))
	case "afmt":
		return q.write(qtReply(correlation, qtDict([]qtKV{{"Error", 0}})))
	case "skew":
		return q.write(qtReply(correlation, u64(math.Float64bits(q.skew()))))
	case "go! ", "stop":
		return q.write(qtReply(correlation, []byte{0, 0, 0, 0}))
	}
	return nil
}

// audioTime records the device audio clock against ours, for SKEW replies.
func (q *Session) audioTime(sbuf []byte) {
	outer, _ := qtAtoms(sbuf)
	if len(outer) == 0 {
		return
	}
	entries, _ := qtAtoms(outer[0].body)
	for _, e := range entries {
		if e.fourcc == "opts" && len(e.body) >= 8 {
			s := &qtAudioSample{binary.LittleEndian.Uint64(e.body), time.Now()}
			if q.audio[0] == nil {
				q.audio[0] = s
			}
			q.audio[1] = s
			return
		}
	}
}

// skew is the device audio ticks per second of our clock (48000 when unknown).
func (q *Session) skew() float64 {
	first, last := q.audio[0], q.audio[1]
	if first != nil && last != nil {
		if elapsed := last.at.Sub(first.at).Seconds(); elapsed > 1 {
			return float64(last.ticks-first.ticks) / elapsed
		}
	}
	return 48000
}

// stop asks the device to stop sending and ends the read loop.
func (q *Session) stop() {
	if q.Running() {
		_ = q.write(qtAsyn(q.audioRef.Load(), "hpa0", nil))
		_ = q.write(qtAsyn(qtEmptyRef, "hpd0", nil))
		select {
		case <-q.released:
		case <-q.done:
		case <-time.After(2 * time.Second):
		}
	}
	q.haltOnce.Do(func() { close(q.halt) })
	select {
	case <-q.done:
	case <-time.After(3 * time.Second):
	}
}
