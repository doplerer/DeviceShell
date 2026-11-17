package core

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/doplerer/deviceshell/internal/ios/coredevice"
)

// A DisplayService recording (.cache/wifi-probe/usb.rtp: 4-byte length + RTP
// packet) decodes into pictures through the assembler and the FFmpeg decoder.
func TestDisplayServiceRecordingDecodes(t *testing.T) {
	path, _ := filepath.Abs(filepath.Join("..", "..", ".cache", "wifi-probe", "usb.rtp"))
	data, e := os.ReadFile(path)
	if e != nil {
		t.Skip("no recording:", path)
	}
	dec, e := newVideoDecoder(scrcpyToolDir(t), true)
	if e != nil {
		t.Fatal(e)
	}
	defer dec.close()
	var a coredevice.HEVCAssembler
	pictures, units, errors := 0, 0, 0
	var w, h int
	for len(data) >= 4 {
		n := int(binary.BigEndian.Uint32(data))
		p := data[4 : 4+n]
		data = data[4+n:]
		au, _ := a.Push(p)
		if au == nil {
			continue
		}
		units++
		if e := dec.decode(au, func(v yuvView) { pictures++; w, h = v.w, v.h }); e != nil {
			errors++
		}
	}
	t.Logf("%d access units, %d pictures of %dx%d, %d decode errors, %d packets lost", units, pictures, w, h, errors, a.Lost)
	if pictures == 0 || pictures < units*9/10 {
		t.Fatalf("only %d pictures from %d access units", pictures, units)
	}
}

// The Wi-Fi picture source decodes the phone's H.264 DisplayService stream (a
// recording, .cache/wifi-probe/h264-motion.rtp) into the windows' frame hub.
func TestDisplayVideoSourceDecodesH264Recording(t *testing.T) {
	path, _ := filepath.Abs(filepath.Join("..", "..", ".cache", "wifi-probe", "h264-motion.rtp"))
	data, e := os.ReadFile(path)
	if e != nil {
		t.Skip("no recording:", path)
	}
	app := New()
	app.Root = t.TempDir()
	if e := app.EnsureLayout(); e != nil {
		t.Fatal(e)
	}
	app.ToolDir = scrcpyToolDir(t)
	v := app.openDisplayVideo("U1")
	packets := 0
	for len(data) >= 4 {
		n := int(binary.BigEndian.Uint32(data))
		v.display.packets <- displayPacket{gen: 1, h264: true, data: append([]byte(nil), data[4:4+n]...)}
		data = data[4+n:]
		packets++
		v.display.bytes.Add(int64(n))
	}
	for wait := time.Now(); v.decoded.Load() < 300 && time.Since(wait) < 20*time.Second; time.Sleep(50 * time.Millisecond) {
	}
	decoded := v.decoded.Load()
	v.close(true)
	if decoded < 300 || !v.live() || v.received() == 0 {
		t.Fatalf("%d pictures from %d packets, live %v, failure %q", decoded, packets, v.live(), v.failed())
	}
}
