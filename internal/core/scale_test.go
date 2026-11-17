package core

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/doplerer/deviceshell/internal/android/scrcpy"
)

func TestStartLimiterBoundsConcurrentStarts(t *testing.T) {
	l := newStartLimiter(4)
	var running, most atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := l.acquire(context.Background()); e != nil {
				t.Error(e)
				return
			}
			n := running.Add(1)
			for m := most.Load(); n > m && !most.CompareAndSwap(m, n); m = most.Load() {
			}
			time.Sleep(5 * time.Millisecond)
			running.Add(-1)
			l.release()
		}()
	}
	wg.Wait()
	if most.Load() != 4 {
		t.Fatal("at most 4 at once, saw", most.Load())
	}
	// A cancelled start stops waiting.
	full := newStartLimiter(1)
	full.acquire(context.Background())
	c, cancel := context.WithCancel(context.Background())
	cancel()
	if full.acquire(c) == nil {
		t.Fatal("a cancelled wait must fail")
	}
}

func TestWatchStallWithFakeClock(t *testing.T) {
	run := func(counter func() int64, active func() bool, ticks int) bool {
		start := time.Unix(0, 0)
		tick := make(chan time.Time)
		done := make(chan struct{})
		stalled := make(chan struct{}, 1)
		finished := make(chan struct{})
		go func() {
			watchStall(done, tick, func() time.Time { return start }, counter, active, 5*time.Second, func() { stalled <- struct{}{} })
			close(finished)
		}()
		for i := 1; i <= ticks; i++ {
			select {
			case tick <- start.Add(time.Duration(i) * time.Second):
			case <-finished:
			}
		}
		close(done)
		<-finished
		select {
		case <-stalled:
			return true
		default:
			return false
		}
	}
	still := func() int64 { return 42 }
	var n atomic.Int64
	moving := func() int64 { return n.Add(1) }
	on, off := func() bool { return true }, func() bool { return false }
	if run(still, on, 4) {
		t.Error("4 s without data is not a stall yet")
	}
	if !run(still, on, 6) {
		t.Error("6 s without data is a stall")
	}
	if run(moving, on, 20) {
		t.Error("data keeps arriving: no stall")
	}
	if run(still, off, 20) {
		t.Error("an inactive source is never a stall")
	}
}

func TestIOSStallGraceGrows(t *testing.T) {
	for stalls, want := range map[int]time.Duration{0: 5 * time.Second, 1: 10 * time.Second, 3: 40 * time.Second, 6: 5 * time.Minute, 20: 5 * time.Minute} {
		if got := iosStallGrace(stalls); got != want {
			t.Errorf("%d stalls: %v, want %v", stalls, got, want)
		}
	}
}

// The inventory asks lockdown about an iPhone only when usbmux lists it under a new id.
func TestIOSDiscoverCachesWhileUsbmuxIDStays(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	var infos, modes atomic.Int32
	var usbmux atomic.Int32
	usbmux.Store(7)
	devmode := `{"DeveloperModeEnabled":true}`
	var devmodeMu sync.Mutex
	app.RunIOS = func(_ context.Context, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "list":
			return `{"deviceList":["U1"],"deviceIDs":[` + string(rune('0'+usbmux.Load())) + `]}`, nil
		case "--udid=U1 info":
			infos.Add(1)
			return `{"ProductType":"iPhone18,1","ProductVersion":"27.0","DeviceName":"x"}`, nil
		case "--udid=U1 devmode get":
			modes.Add(1)
			devmodeMu.Lock()
			defer devmodeMu.Unlock()
			return devmode, nil
		case "--udid=U1 devmode enable":
			return "", errDeveloperModeRevealed // a passcode: only the toggle is shown
		}
		return "", nil
	}
	discover := func() Device {
		defer app.WaitIOSActivation(context.Background())
		ds, e := iosBackend{app}.Discover(context.Background())
		if e != nil || len(ds) != 1 {
			t.Fatal(ds, e)
		}
		return ds[0]
	}
	for range 3 {
		if d := discover(); d.State != "device" || d.Model != "iPhone18,1" {
			t.Fatal(d)
		}
	}
	if infos.Load() != 1 || modes.Load() != 1 {
		t.Fatal("same usbmux id: asked again", infos.Load(), modes.Load())
	}
	// DeviceShell's own QuickTime switch: the same phone under a new id.
	app.iosInfo.rebind("U1", 7, 8)
	usbmux.Store(8)
	discover()
	if infos.Load() != 1 {
		t.Fatal("a rebound phone must not be asked again")
	}
	// Plugged in again: a new id it did not expect.
	usbmux.Store(9)
	discover()
	if infos.Load() != 2 {
		t.Fatal("a re-enumerated phone must be asked again")
	}
	// The preparation changed it.
	devmodeMu.Lock()
	devmode = `{"DeveloperModeEnabled":false}`
	devmodeMu.Unlock()
	app.iosInfo.forget("U1")
	if d := discover(); d.State != iosDeveloperOff || infos.Load() != 3 {
		t.Fatal(d, infos.Load())
	}
	// Developer Mode off is kept only for a while, then asked again.
	discover()
	if infos.Load() != 3 {
		t.Fatal("asked again within the retry time")
	}
	app.iosInfo.mu.Lock()
	e := app.iosInfo.entries["U1"]
	e.until = time.Now().Add(-time.Second)
	app.iosInfo.entries["U1"] = e
	app.iosInfo.mu.Unlock()
	discover()
	if infos.Load() != 4 {
		t.Fatal("Developer Mode off must be asked again after the retry time")
	}
	// Without usbmux ids (older answers) nothing is kept.
	app.RunIOS = func(_ context.Context, args ...string) (string, error) {
		if strings.Join(args, " ") == "list" {
			return `{"deviceList":["U2"]}`, nil
		}
		if args[len(args)-1] == "info" {
			infos.Add(1)
		}
		return `{"ProductType":"iPhone18,1","ProductVersion":"27.0","DeveloperModeEnabled":true}`, nil
	}
	before := infos.Load()
	discover()
	discover()
	if infos.Load() != before+2 {
		t.Fatal("unknown ids must not be cached")
	}
}

func TestScrcpyResetVideoAndOptions(t *testing.T) {
	app := New()
	if m := scrcpy.ResetVideo(); !bytes.Equal(m, []byte{17}) {
		t.Fatalf("RESET_VIDEO % x", m)
	}
	if n, e := app.FrameLength(scrcpy.ResetVideo()); e != nil || n != 1 {
		t.Fatal("RESET_VIDEO is one byte on the wire", n, e)
	}
	if got := scrcpyOptions("audio=false max_fps=60"); got != "audio=false max_fps=60 cleanup=false" {
		t.Fatal(got)
	}
	if got := scrcpyOptions("video=false cleanup=true control=true"); got != "video=false cleanup=false control=true" {
		t.Fatal(got)
	}
}

// With no window showing it, a connector drops the packets undecoded; a window coming
// back sends RESET_VIDEO and decoding resumes from the next config.
func TestAndroidVideoPausesWithoutWindows(t *testing.T) {
	old := androidIdleDecode
	androidIdleDecode = 50 * time.Millisecond
	defer func() { androidIdleDecode = old }()
	device, video := net.Pipe()
	controlDevice, control := net.Pipe()
	v := &androidVideo{video: video, control: control, done: make(chan struct{})}
	v.init("scrcpy", nil)
	dec := &fakeDecoder{units: make(chan []byte, 16)}
	go v.readLoop(dec)
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
	send := func(b []byte) {
		if _, e := device.Write(b); e != nil {
			t.Fatal(e)
		}
	}
	send(scrcpySession(496, 1072))
	send(scrcpyPacket(true, []byte{0, 0, 0, 1, 0x67}))
	send(scrcpyPacket(false, []byte{0, 0, 0, 1, 0x65}))
	<-dec.units
	time.Sleep(80 * time.Millisecond)
	// Nobody watches: dropped.
	send(scrcpyPacket(false, []byte{0, 0, 0, 1, 0x41}))
	send(scrcpyPacket(false, []byte{0, 0, 0, 1, 0x41}))
	select {
	case u := <-dec.units:
		t.Fatalf("decoded without windows: % x", u)
	case <-time.After(50 * time.Millisecond):
	}
	// A window: RESET_VIDEO, then frames before the new config are still dropped.
	v.watch(true)
	send(scrcpyPacket(false, []byte{0, 0, 0, 1, 0x41}))
	select {
	case m := <-written:
		if !bytes.Equal(m, []byte{17}) {
			t.Fatalf("expected RESET_VIDEO, got % x", m)
		}
	case <-time.After(time.Second):
		t.Fatal("no RESET_VIDEO")
	}
	send(scrcpyPacket(false, []byte{0, 0, 0, 1, 0x41}))
	send(scrcpySession(496, 1072))
	send(scrcpyPacket(true, []byte{0, 0, 0, 1, 0x67}))
	send(scrcpyPacket(false, []byte{0, 0, 0, 1, 0x65}))
	select {
	case u := <-dec.units:
		if !bytes.Equal(u, []byte{0, 0, 0, 1, 0x67, 0, 0, 0, 1, 0x65}) {
			t.Fatalf("decoding must resume at the new config: % x", u)
		}
	case <-time.After(time.Second):
		t.Fatal("decoding did not resume")
	}
	v.watch(false)
	device.Close()
	controlDevice.Close()
	<-v.done
	io.Copy(io.Discard, device)
}

// An iPhone that refused its connection because it is locked is not offered for
// windows or @all. Discovery only reads it again after iosInfoRetry, and lists it
// once lockdown answers; unplugging it forgets the mark.
func TestIOSLockedIsNotAvailable(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	var locked atomic.Bool
	var reads atomic.Int32
	app.RunIOS = func(_ context.Context, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "list":
			return `{"deviceList":["U1"],"deviceIDs":[3]}`, nil
		case "--udid=U1 info":
			reads.Add(1)
			return `{"ProductType":"iPhone18,1","ProductVersion":"27.0","DeviceName":"x"}`, nil
		case "--udid=U1 devmode get":
			if locked.Load() {
				return "", errors.New("Could not start service:com.apple.mobile.mobile_image_mounter with reason:'PasswordProtected'")
			}
			return `{"DeveloperModeEnabled":true}`, nil
		}
		return "", nil
	}
	state := func() string {
		ds, e := iosBackend{app}.Discover(context.Background())
		if e != nil || len(ds) != 1 {
			t.Fatal(ds, e)
		}
		return ds[0].State
	}
	if s := state(); s != "device" {
		t.Fatal(s)
	}
	// A window's connection finds it locked.
	locked.Store(true)
	app.noteIOSConnect("ios:U1", iosConnectError(errors.New("lockdown: PasswordProtected")))
	if s := state(); s != iosLocked {
		t.Fatal("locked:", s)
	}
	before := reads.Load()
	if s := state(); s != iosLocked || reads.Load() != before {
		t.Fatal("a locked device must be read again only after the retry time", s)
	}
	// Unlocked: listed again once the retry time passes.
	locked.Store(false)
	app.iosInfo.mu.Lock()
	e := app.iosInfo.entries["U1"]
	e.until = time.Now().Add(-time.Second)
	app.iosInfo.entries["U1"] = e
	app.iosInfo.mu.Unlock()
	if s := state(); s != "device" {
		t.Fatal("unlocked:", s)
	}
	app.noteIOSConnect("ios:U1", iosConnectError(errors.New("DeviceLocked")))
	app.AutoPrepareIOS(context.Background(), nil, nil)
	if app.iosIsLocked("ios:U1") {
		t.Fatal("unplugged: the mark must go")
	}
}

// A locked device is known from the first listing: lockdown refuses the image
// mounter that the Developer Mode check needs.
func TestIOSDiscoverSeesLockedDevice(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	app.RunIOS = func(_ context.Context, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "list":
			return `{"deviceList":["U1"],"deviceIDs":[3]}`, nil
		case "--udid=U1 info":
			return `{"ProductType":"iPad14,3","ProductVersion":"27.2","DeviceName":"x"}`, nil
		case "--udid=U1 devmode get":
			return "", errors.New("IsDevModeEnabled: failed connecting to image mounter service with err: Could not start service:com.apple.mobile.mobile_image_mounter with reason:'PasswordProtected'.")
		}
		return "", nil
	}
	ds, e := iosBackend{app}.Discover(context.Background())
	if e != nil || len(ds) != 1 || ds[0].State != iosLocked {
		t.Fatal(ds, e)
	}
}
