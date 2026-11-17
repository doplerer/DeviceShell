package core

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doplerer/deviceshell/internal/android/scrcpy"
)

// androidVideo is an Android picture handled in Go, as iosVideo is for the iPhone: the
// scrcpy server encodes the screen in H.264, FFmpeg decodes it and every window gets
// the newest frame fitted to its size. The same server injects the windows' touches,
// scrolls and keys through its control socket, without adb input. The wire format
// is in internal/android/scrcpy.

// androidIdleDecode is how long a connector keeps decoding with no window showing it
// (none open, or all minimized) before it only reads and drops the packets.
var androidIdleDecode = 2 * time.Second

// androidStall: a still screen still sends about 10 repeated frames a second, so a
// connector that receives nothing for this long is stuck and is replaced.
const androidStall = 3 * time.Second

// androidVideoSize is the longest side the device encodes for a window of r: twice the
// window, between 720 and 1600, so the PC scales it down with the iPhone's sharpness
// while decoding a whole wall stays cheap.
func androidVideoSize(r Rect) int {
	return min(max(2*max(r.W, r.H), 720), 1600)
}

// androidBitRate is about a quarter of a bit per encoded pixel and frame, as ViewBitRate.
func androidBitRate(r Rect, size, fps int) int {
	short := size * min(r.W, r.H) / max(1, r.W, r.H)
	return min(max(size*short*fps/4, 2_000_000), 8_000_000)
}

type androidVideo struct {
	frameHub
	serial         string
	server         *scrcpyServer
	video, control net.Conn
	writeMu        sync.Mutex
	pressed        bool // a finger is down, at last; guarded by writeMu
	last           [2]float64
	size           atomic.Uint64 // video width<<32 | height
	bytes          atomic.Int64
	failure        atomic.Value  // string: why the picture stopped
	done           chan struct{} // closed when the video stream ends
	logMu          sync.Mutex
	logFile        *os.File
	record         func(unit []byte) // the opt-in recording tests keep the stream
}

var unsafeFileChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// openAndroidVideo starts the server and waits for its stream to begin. The server
// lives until close, not until c ends: c only bounds the start.
func (app *Service) openAndroidVideo(c context.Context, serial string, r Rect, fps int) (*androidVideo, error) {
	v := &androidVideo{serial: serial, done: make(chan struct{})}
	v.init("scrcpy", v.logf)
	if f, e := os.OpenFile(app.LogsPath("android-video-"+unsafeFileChars.ReplaceAllString(serial, "_")+".log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); e == nil {
		v.logFile = f
	}
	fail := func(e error) (*androidVideo, error) {
		v.logf("start failed: %v", e)
		v.close()
		return nil, e
	}
	size := androidVideoSize(r)
	// Only what differs from the server defaults (H.264 video, control, the three metas
	// and cleanup are on): app_process on Samsung Android 14 aborts with "stack
	// corruption detected" when given a dozen or more options.
	options := fmt.Sprintf("audio=false tunnel_forward=true max_size=%d max_fps=%d video_bit_rate=%d clipboard_autosync=false", size, fps, androidBitRate(r, size, fps))
	v.logf("starting scrcpy server: %s", options)
	var e error
	if v.server, e = app.startScrcpyServer(context.Background(), serial, options); e != nil {
		return fail(e)
	}
	if v.video, e = v.server.connect(c); e != nil {
		return fail(e)
	}
	if v.control, e = v.server.dial(); e != nil {
		return fail(e)
	}
	meta := make([]byte, scrcpy.DeviceNameSize+4)
	v.video.SetReadDeadline(time.Now().Add(10 * time.Second))
	if _, e = io.ReadFull(v.video, meta); e != nil {
		return fail(fmt.Errorf("the server sent no video: %w; %s", e, v.server.log.String()))
	}
	v.video.SetReadDeadline(time.Time{})
	if codec := binary.BigEndian.Uint32(meta[scrcpy.DeviceNameSize:]); codec != scrcpy.CodecH264 {
		return fail(fmt.Errorf("the phone cannot encode the video (codec %08x); %s", codec, v.server.log.String()))
	}
	dec, e := newVideoDecoder(app.ToolDir, false)
	if e != nil {
		return fail(fmt.Errorf("video decoder: %w", e))
	}
	v.logf("video source: scrcpy (%s)", strings.TrimRight(string(meta[:scrcpy.DeviceNameSize]), "\x00"))
	if videoRecorder != nil {
		v.record = videoRecorder("android-"+serial, false)
	}
	go v.readLoop(dec)
	go watchStallEvery(v.done, v.bytes.Load, func() bool { return true }, androidStall, func() {
		v.fail(fmt.Sprintf(app.L("vigilante: sin datos del móvil en %v", "watchdog: no data from the phone in %v"), androidStall))
	})
	// The device sends nothing we use on the control socket; it ends with the server.
	go func() {
		io.Copy(io.Discard, v.control)
		v.fail("control cerrado")
	}()
	return v, nil
}

func (v *androidVideo) logf(format string, args ...any) {
	v.logMu.Lock()
	defer v.logMu.Unlock()
	if v.logFile != nil {
		fmt.Fprintf(v.logFile, "%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, args...))
	}
}

// fail records the first reason the picture stopped and ends the stream.
func (v *androidVideo) fail(why string) {
	if v.failure.CompareAndSwap(nil, why) {
		v.logf("video stopped: %s", why)
		if v.video != nil {
			v.video.Close()
		}
	}
}

func (v *androidVideo) readLoop(dec videoDecoder) {
	defer close(v.done)
	defer dec.close()
	defer v.frameHub.close()
	r := bufio.NewReaderSize(v.video, 1<<20)
	head := make([]byte, scrcpy.HeaderSize)
	var config, packet, unit []byte
	failures := 0
	// Without windows the packets are dropped undecoded; when one shows the picture
	// again, RESET_VIDEO makes the server start over with its config and a key frame,
	// and decoding resumes from that config.
	paused, awaitConfig := false, false
	watched := time.Now()
	for {
		if _, e := io.ReadFull(r, head); e != nil {
			v.fail("video interrupted: " + e.Error())
			return
		}
		h, e := scrcpy.ParseHeader(head)
		if e != nil {
			v.fail(e.Error())
			return
		}
		if h.Session {
			v.size.Store(uint64(h.Width)<<32 | uint64(h.Height))
			v.logf("video session %dx%d", h.Width, h.Height)
			continue
		}
		packet = slices.Grow(packet[:0], h.Size)[:h.Size]
		if _, e := io.ReadFull(r, packet); e != nil {
			v.fail("video interrupted: " + e.Error())
			return
		}
		v.bytes.Add(int64(len(head) + h.Size))
		if v.watchers.Load() > 0 {
			watched = time.Now()
			if paused {
				paused, awaitConfig = false, true
				v.writeMu.Lock()
				e := v.write(scrcpy.ResetVideo())
				v.writeMu.Unlock()
				v.logf("window back: video reset (%v)", e)
			}
		} else if !paused && time.Since(watched) > androidIdleDecode {
			paused = true
			v.logf("no window: decoding paused")
		}
		if paused {
			continue
		}
		if h.Config {
			config, awaitConfig = append(config[:0], packet...), false
			continue
		}
		if awaitConfig {
			continue
		}
		data := packet
		if len(config) > 0 {
			unit = append(append(unit[:0], config...), packet...)
			data, config = unit, config[:0]
		}
		if v.record != nil {
			v.record(data)
		}
		if e := timedDecode(dec, data, v.publish); e != nil {
			if failures++; failures <= 10 || failures%100 == 0 {
				v.logf("decode error (%d): %v", failures, e)
			}
		}
	}
}

func (v *androidVideo) alive() bool {
	select {
	case <-v.done:
		return false
	default:
		return v.failure.Load() == nil
	}
}

func (v *androidVideo) live() bool      { return v.alive() }
func (v *androidVideo) received() int64 { return v.bytes.Load() }
func (v *androidVideo) flags() uint32 {
	if v.failure.Load() != nil {
		return videoSourceFailed
	}
	return 0
}

// write sends one control message; a write that does not go through ends the stream.
// Caller holds v.writeMu.
func (v *androidVideo) write(b []byte) error {
	if v.control == nil {
		return errors.New("no control channel")
	}
	v.control.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	if _, e := v.control.Write(b); e != nil {
		v.fail("control: " + e.Error())
		return e
	}
	return nil
}

func (v *androidVideo) videoSize() (int, int, error) {
	s := v.size.Load()
	if s == 0 {
		return 0, 0, errors.New("no video yet")
	}
	return int(s >> 32), int(s & 0xffffffff), nil
}

// touch moves the finger, in 0..1 screen coordinates.
func (v *androidVideo) touch(phase string, x, y float64) error {
	w, h, e := v.videoSize()
	if e != nil {
		return e
	}
	action := map[string]byte{"down": scrcpy.MotionDown, "move": scrcpy.MotionMove, "up": scrcpy.MotionUp}
	a, ok := action[phase]
	if !ok {
		return fmt.Errorf("unknown touch phase: %s", phase)
	}
	v.writeMu.Lock()
	defer v.writeMu.Unlock()
	if a == scrcpy.MotionMove && !v.pressed {
		return nil
	}
	v.pressed, v.last = a != scrcpy.MotionUp, [2]float64{x, y}
	return v.write(scrcpy.Touch(a, x, y, w, h))
}

func (v *androidVideo) scroll(x, y float64, steps int) error {
	w, h, e := v.videoSize()
	if e != nil {
		return e
	}
	v.writeMu.Lock()
	defer v.writeMu.Unlock()
	return v.write(scrcpy.Scroll(x, y, w, h, steps))
}

// key presses and releases an Android key code; BACK goes as the back-or-wake message.
func (v *androidVideo) key(code int) error {
	v.writeMu.Lock()
	defer v.writeMu.Unlock()
	down, up := scrcpy.Key(0, code), scrcpy.Key(1, code)
	if code == 4 {
		down, up = scrcpy.Back(0), scrcpy.Back(1)
	}
	if e := v.write(down); e != nil {
		return e
	}
	return v.write(up)
}

// text types one plain character as keys and anything else through the clipboard.
func (v *androidVideo) text(s string) error {
	v.writeMu.Lock()
	defer v.writeMu.Unlock()
	if len(s) == 1 && s[0] >= 32 && s[0] < 127 {
		return v.write(scrcpy.Text(s))
	}
	if len(s) > 262144-14 {
		return errors.New(v.server.app.L("Texto demasiado largo", "Text too long"))
	}
	return v.write(scrcpy.Paste(s))
}

// close lifts a finger still down and stops the server.
func (v *androidVideo) close() {
	v.writeMu.Lock()
	if v.pressed && v.control != nil {
		if w, h, e := v.videoSize(); e == nil {
			v.control.SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
			v.control.Write(scrcpy.Touch(scrcpy.MotionUp, v.last[0], v.last[1], w, h))
		}
		v.pressed = false
	}
	v.writeMu.Unlock()
	v.fail("cerrado")
	if v.control != nil {
		v.control.Close()
	}
	if v.server != nil {
		v.server.close()
	}
	v.logMu.Lock()
	defer v.logMu.Unlock()
	if v.logFile != nil {
		v.logFile.Close()
		v.logFile = nil
	}
}
