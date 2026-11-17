package quicktime

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"testing"
	"time"
)

// fakeQTDevice feeds scripted packets to a Session and records what it writes.
type fakeQTDevice struct {
	in      chan []byte
	written chan []byte
}

func (f *fakeQTDevice) read(buf []byte) (int, error) {
	select {
	case p, ok := <-f.in:
		if !ok {
			return 0, errors.New("unplugged")
		}
		return copy(buf, p), nil
	case <-time.After(20 * time.Millisecond):
		return 0, nil
	}
}

func (f *fakeQTDevice) write(b []byte) error {
	f.written <- bytes.Clone(b)
	return nil
}

func qtPacket(kind string, body ...[]byte) []byte {
	payload := append(qtTag(kind), bytes.Join(body, nil)...)
	return append(binary.LittleEndian.AppendUint32(nil, uint32(4+len(payload))), payload...)
}

func qtSync(sub string, ref, correlation uint64, extra ...[]byte) []byte {
	u64 := func(v uint64) []byte { return binary.LittleEndian.AppendUint64(nil, v) }
	return qtPacket("sync", append([][]byte{u64(ref), qtTag(sub), u64(correlation)}, extra...)...)
}

// avcC record with one SPS and one PPS and 4-byte NAL lengths.
var testAVCC = []byte{1, 0x64, 0, 0x1f, 0xff, 0xe1, 0, 3, 0x67, 0x64, 0x1f, 1, 0, 2, 0x68, 0xee}

func testFormat() []byte {
	extn := qtAtom("dict", qtAtom("keyv", qtAtom("idxk", []byte{49, 0}), qtAtom("dict", qtAtom("keyv", qtAtom("idxk", []byte{105, 0}), qtAtom("datv", testAVCC)))))
	return qtAtom("fdsc", qtAtom("mdia", []byte("ediv")), qtAtom("codc", qtTag("avc1")), qtAtom("vdim", []byte{0x28, 5, 0, 0, 0x34, 0x0b, 0, 0}), qtAtom("extn", extn[8:]))
}

func waitWrite(t *testing.T, d *fakeQTDevice, kind, sub string) []byte {
	t.Helper()
	for {
		select {
		case b := <-d.written:
			if string(qtTag(string(b[4:8]))) == kind && (sub == "" || string(qtTag(string(b[16:20]))) == sub) {
				return b
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("no %s %s written", kind, sub)
		}
	}
}

func TestQuickTimeSessionHandshakeAndSamples(t *testing.T) {
	d := &fakeQTDevice{in: make(chan []byte, 16), written: make(chan []byte, 64)}
	q := newQTSession(d, func(string, ...any) {})
	go q.readLoop()

	ping := qtPacket("ping", binary.LittleEndian.AppendUint64(nil, 0))
	d.in <- ping[:5] // packets may arrive split across reads
	d.in <- ping[5:]
	if b := waitWrite(t, d, "ping", ""); len(b) != 16 || binary.LittleEndian.Uint64(b[8:]) != 0x0000000100000000 {
		t.Fatalf("ping reply % x", b)
	}

	d.in <- qtSync("cwpa", 7, 99, binary.LittleEndian.AppendUint64(nil, 500))
	waitWrite(t, d, "asyn", "hpd1")
	if b := waitWrite(t, d, "rply", ""); binary.LittleEndian.Uint64(b[8:]) != 99 || binary.LittleEndian.Uint64(b[20:]) != 1500 {
		t.Fatalf("cwpa reply % x", b)
	}
	waitWrite(t, d, "asyn", "hpa1")

	format := qtDict([]qtKV{{"FormatDescription", []byte{}}})
	d.in <- qtSync("cvrp", 7, 100, binary.LittleEndian.AppendUint64(nil, 600), format)
	if b := waitWrite(t, d, "asyn", "need"); binary.LittleEndian.Uint64(b[8:]) != 600 {
		t.Fatalf("need % x", b)
	}
	waitWrite(t, d, "rply", "")

	nal := []byte{0, 0, 0, 2, 0x65, 0x88}
	sbuf := qtAtom("sbuf", testFormat(), qtAtom("sdat", nal))
	d.in <- qtPacket("asyn", binary.LittleEndian.AppendUint64(nil, 600), qtTag("feed"), sbuf)
	waitWrite(t, d, "asyn", "need") // the next frame is asked for before this one is decoded
	select {
	case got := <-q.Samples:
		data, f, e := Sample(got)
		if e != nil || !bytes.Equal(data, nal) || f == nil || f.Codec != "avc1" || f.Width != 1320 || f.Height != 2868 || !bytes.Equal(f.Record, testAVCC) {
			t.Fatalf("sample = % x, %+v, %v", data, f, e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no sample")
	}

	d.in <- qtSync("skew", 7, 101)
	if b := waitWrite(t, d, "rply", ""); math.Float64frombits(binary.LittleEndian.Uint64(b[20:])) != 48000 {
		t.Fatalf("skew reply % x", b)
	}

	close(d.in)
	select {
	case <-q.done:
	case <-time.After(2 * time.Second):
		t.Fatal("read loop did not end")
	}
	if q.Running() || q.Failure() != "unplugged" {
		t.Fatalf("running=%v error=%q", q.Running(), q.Failure())
	}
}

func TestAnnexBFromAVCC(t *testing.T) {
	params, lengthSize, e := AnnexBParameters(testAVCC, false)
	if e != nil || lengthSize != 4 || !bytes.Equal(params, []byte{0, 0, 0, 1, 0x67, 0x64, 0x1f, 0, 0, 0, 1, 0x68, 0xee}) {
		t.Fatalf("params % x, %d, %v", params, lengthSize, e)
	}
	out, e := AnnexB(nil, []byte{0, 0, 0, 2, 0x65, 0x88, 0, 0, 0, 1, 0x41}, 4)
	if e != nil || !bytes.Equal(out, []byte{0, 0, 0, 1, 0x65, 0x88, 0, 0, 0, 1, 0x41}) {
		t.Fatalf("annexB % x, %v", out, e)
	}
	if _, e = AnnexB(nil, []byte{0, 0, 0, 9, 1}, 4); e == nil {
		t.Fatal("a truncated NAL unit must fail")
	}
	if _, _, e = AnnexBParameters([]byte{1, 2}, false); e == nil {
		t.Fatal("a short record must fail")
	}
}
