package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/doplerer/deviceshell/internal/android/scrcpy"
)

func scrcpySession(w, h int) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint32(b, 1<<31)
	binary.BigEndian.PutUint32(b[4:], uint32(w))
	binary.BigEndian.PutUint32(b[8:], uint32(h))
	return b
}

func scrcpyPacket(config bool, data []byte) []byte {
	b := make([]byte, 12)
	flags := uint64(123456)
	if config {
		flags = scrcpy.FlagConfig
	}
	binary.BigEndian.PutUint64(b, flags)
	binary.BigEndian.PutUint32(b[8:], uint32(len(data)))
	return append(b, data...)
}

func TestScrcpyControlMessages(t *testing.T) {
	app := New()
	down := scrcpy.Touch(scrcpy.MotionDown, 1, 1, 1080, 2340)
	want := []byte{2, 0, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xfe, 0, 0, 0x04, 0x37, 0, 0, 0x09, 0x23, 0x04, 0x38, 0x09, 0x24, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0}
	if !bytes.Equal(down, want) {
		t.Fatalf("touch down % x", down)
	}
	if up := scrcpy.Touch(scrcpy.MotionUp, 0, 0, 1080, 2340); up[1] != scrcpy.MotionUp || binary.BigEndian.Uint32(up[10:]) != 0 || binary.BigEndian.Uint16(up[22:]) != 0 {
		t.Fatalf("touch up % x", up)
	}
	for steps, v := range map[int]uint16{1: 0x0800, -1: 0xf800, 100: 0x7fff, -100: 0x8000} {
		if s := scrcpy.Scroll(.5, .5, 1080, 2340, steps); binary.BigEndian.Uint16(s[15:]) != v || binary.BigEndian.Uint16(s[13:]) != 0 {
			t.Errorf("scroll %d: % x", steps, s)
		}
	}
	if k := scrcpy.Key(1, 66); !bytes.Equal(k, []byte{0, 1, 0, 0, 0, 66, 0, 0, 0, 0, 0, 0, 0, 0}) {
		t.Fatalf("key % x", k)
	}
	// Every message must have the length the pinned wire format gives its type.
	for _, m := range [][]byte{down, scrcpy.Scroll(0, 0, 10, 10, 1), scrcpy.Key(0, 3), scrcpy.Text("a"), scrcpy.Paste("Año €"), scrcpy.Back(0)} {
		if n, e := app.FrameLength(m); e != nil || n != len(m) {
			t.Errorf("type %d: length %d, want %d (%v)", m[0], n, len(m), e)
		}
	}
}

func TestAndroidVideoSizeAndBitRate(t *testing.T) {
	r := Rect{0, 0, 238, 423}
	if s := androidVideoSize(r); s != 846 {
		t.Fatal(s)
	}
	if s := androidVideoSize(Rect{0, 0, 100, 200}); s != 720 {
		t.Fatal(s)
	}
	if s := androidVideoSize(Rect{0, 0, 1080, 2340}); s != 1600 {
		t.Fatal(s)
	}
	// 846x476 at 60 fps, a quarter bit per pixel.
	if b := androidBitRate(r, 846, 60); b != 846*476*60/4 {
		t.Fatal(b)
	}
}

// fakeDecoder turns every unit into one picture and records what it was given.
type fakeDecoder struct{ units chan []byte }

func (f *fakeDecoder) decode(data []byte, emit func(yuvView)) error {
	f.units <- bytes.Clone(data)
	img := solidImage(4, 8, 128, 128, 128, false)
	emit(yuvView{w: img.w, h: img.h, cw: img.cw, sx: 1, sy: 1, y: img.y, u: img.u, v: img.v, ys: img.w, cs: img.cw})
	return nil
}
func (f *fakeDecoder) close() {}

func TestAndroidVideoStreamAndTouch(t *testing.T) {
	device, video := net.Pipe()
	controlDevice, control := net.Pipe()
	v := &androidVideo{video: video, control: control, done: make(chan struct{})}
	v.init("scrcpy", nil)
	dec := &fakeDecoder{units: make(chan []byte, 4)}
	go v.readLoop(dec)
	go func() {
		device.Write(scrcpySession(496, 1072))
		device.Write(scrcpyPacket(true, []byte{0, 0, 0, 1, 0x67}))
		device.Write(scrcpyPacket(false, []byte{0, 0, 0, 1, 0x65}))
		device.Write(scrcpyPacket(false, []byte{0, 0, 0, 1, 0x41}))
	}()
	// The config packet goes in front of the next picture only.
	if u := <-dec.units; !bytes.Equal(u, []byte{0, 0, 0, 1, 0x67, 0, 0, 0, 1, 0x65}) {
		t.Fatalf("first unit % x", u)
	}
	if u := <-dec.units; !bytes.Equal(u, []byte{0, 0, 0, 1, 0x41}) {
		t.Fatalf("second unit % x", u)
	}
	if w, h, e := v.videoSize(); e != nil || w != 496 || h != 1072 {
		t.Fatal(w, h, e)
	}
	for deadline := time.Now().Add(time.Second); v.decoded.Load() < 2 && time.Now().Before(deadline); {
		time.Sleep(time.Millisecond)
	}
	if v.decoded.Load() != 2 || !v.live() {
		t.Fatal("pictures must reach the hub")
	}

	written := make(chan []byte, 8)
	go func() {
		buf := make([]byte, 64)
		for {
			n, e := controlDevice.Read(buf)
			if e != nil {
				return
			}
			written <- bytes.Clone(buf[:n])
		}
	}()
	if e := v.touch("move", .5, .5); e != nil {
		t.Fatal(e)
	}
	if e := v.touch("down", .25, .5); e != nil {
		t.Fatal(e)
	}
	select {
	case m := <-written:
		if m[1] != scrcpy.MotionDown || binary.BigEndian.Uint32(m[10:]) != 124 || binary.BigEndian.Uint16(m[18:]) != 496 {
			t.Fatalf("a move without a finger down must not be sent, then down: % x", m)
		}
	case <-time.After(time.Second):
		t.Fatal("no touch sent")
	}
	if e := v.key(4); e != nil {
		t.Fatal(e)
	}
	if back := <-written; !bytes.Equal(back, []byte{4, 0}) {
		t.Fatalf("BACK % x", back)
	}
	<-written

	// Closing lifts the finger where it was and ends the stream.
	go v.close()
	if up := <-written; up[1] != scrcpy.MotionUp || binary.BigEndian.Uint32(up[10:]) != 124 {
		t.Fatalf("close must lift the finger: % x", up)
	}
	select {
	case <-v.done:
	case <-time.After(time.Second):
		t.Fatal("the stream must end")
	}
	if v.live() || v.flags() != videoSourceFailed {
		t.Fatal("a stopped picture must say why")
	}
	device.Close()
	controlDevice.Close()
	io.Copy(io.Discard, device)
}

// Opt-in: a real device streams through the scrcpy server and FFmpeg, without input.
// DEVICESHELL_ANDROID_SERIAL picks the device, DEVICESHELL_ROOT the DeviceShell folder with its tools.
func TestAndroidVideoLive(t *testing.T) {
	serial, root := os.Getenv("DEVICESHELL_ANDROID_SERIAL"), os.Getenv("DEVICESHELL_ROOT")
	if serial == "" || root == "" {
		t.Skip("set DEVICESHELL_ANDROID_SERIAL and DEVICESHELL_ROOT")
	}
	app := New()
	app.Root = t.TempDir()
	app.EnsureLayout()
	app.ToolDir = filepath.Join(root, "tools", "scrcpy-win64-v5.0.1")
	app.Adb = filepath.Join(app.ToolDir, "adb.exe")
	started := time.Now()
	v, e := app.openAndroidVideo(context.Background(), serial, Rect{0, 0, 238, 423}, viewFPS)
	if e != nil {
		t.Fatal(e)
	}
	defer v.close()
	ready := time.Since(started)
	var first time.Duration
	for v.decoded.Load() == 0 && time.Since(started) < 10*time.Second {
		time.Sleep(10 * time.Millisecond)
	}
	first = time.Since(started)
	n0 := v.decoded.Load()
	time.Sleep(3 * time.Second)
	w, h, e := v.videoSize()
	t.Logf("connected in %v, first frame after %v, video %dx%d (%v), %.1f fps while still, %.1f Mbit/s", ready, first, w, h, e, float64(v.decoded.Load()-n0)/3, float64(v.received())*8/1e6/time.Since(started).Seconds())
	if n0 == 0 || e != nil {
		t.Fatal("no picture; server: " + v.server.log.String())
	}
}
