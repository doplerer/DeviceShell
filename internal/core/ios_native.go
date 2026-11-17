package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/doplerer/deviceshell/internal/ios/coredevice"
)

// iosConnector controls one iPhone: the Go engine (iosNative), or a stand-in in tests.
type iosConnector interface {
	alive() bool
	close()
	info() iosStatus
	supports(Spec) bool
	do(c context.Context, app *Service, d Device, s Spec) (string, error)
	liveTouch(c context.Context, phase string, x, y float64) error
	wedged(c context.Context) bool
}

const (
	safariBundle = "com.apple.mobilesafari"
	// Touch stream retry: doubling up to the maximum, slow on purpose because
	// starting streams too often wedges the phone's video service.
	streamRetryMin, streamRetryMax = 5 * time.Second, 60 * time.Second
	// Consecutive start timeouts after which the window says to restart the phone.
	streamWedged = 3
)

// The services each capability needs, as published over RSD.
var iosServiceNames = map[string]string{
	"touch":   "com.apple.coredevice.hid.universalhidservice",
	"buttons": "com.apple.coredevice.hid.indigo",
	"capture": "com.apple.coredevice.screencaptureservice",
	"apps":    "com.apple.coredevice.appservice",
	"display": "com.apple.coredevice.displayservice",
	"dvt":     "com.apple.instruments.dtservicehub",
}

// iosNative is the Go engine: tunnel, HID, the DisplayService touch stream and the
// developer services, all in this process.
type iosNative struct {
	tun      *coredevice.Tunnel
	hid      *coredevice.HID
	shots    *coredevice.Screenshots
	status   iosStatus
	input    sync.Mutex // actions run one at a time; live touches bypass it
	mu       sync.Mutex
	display  *coredevice.Display
	timeouts int
	stop     chan struct{}
	wg       sync.WaitGroup
	logMu    sync.Mutex
	logFile  *os.File
	// onDisplay, when set before begin, sees every DisplayService stream started;
	// showVideo asks for the codec that decodes clean (H.264), as its picture is shown.
	onDisplay func(*coredevice.Display)
	showVideo bool
}

func (app *Service) iosImageDir() string {
	return app.IOSToolsPath("ddi")
}

// connectIOSNative opens the Go engine, mounting the developer image first when the
// phone rebooted since it was last mounted. touchStream opens the DisplayService
// stream that touches need (windows and actions); a status check skips it. When
// video takes its picture from DisplayService, the stream is asked in H.264 and fed
// to it (otherwise its video is discarded, as with QuickTime).
func (app *Service) connectIOSNative(c context.Context, d Device, touchStream bool, video *iosVideo) (*iosNative, error) {
	udid := d.NativeID()
	n := app.newIOSNative(udid)
	if video != nil && video.display != nil {
		n.showVideo, n.onDisplay = true, video.attach
	}
	fail := func(e error) (*iosNative, error) {
		n.logf("connect failed: %v", e)
		n.closeLog()
		return nil, e
	}
	mounted, e := coredevice.Mounted(udid)
	if e != nil {
		return fail(iosConnectError(e))
	}
	if !mounted {
		n.logf("mounting the developer image")
		if e = coredevice.Mount(udid, app.iosImageDir()); e != nil {
			return fail(fmt.Errorf("developer disk image: %w", iosConnectError(e)))
		}
	}
	if c.Err() != nil {
		return fail(c.Err())
	}
	started := time.Now()
	tun, e := coredevice.Open(udid)
	if e != nil {
		return fail(iosConnectError(e))
	}
	n.logf("tunnel up in %v: iOS %s, %d services", time.Since(started), tun.Version, len(tun.Services))
	if e = n.begin(tun, touchStream); e != nil {
		return fail(e)
	}
	return n, nil
}

// begin runs the engine over an open tunnel (USB or Wi-Fi), closing it on failure.
func (n *iosNative) begin(tun *coredevice.Tunnel, touchStream bool) error {
	n.tun = tun
	version := strings.TrimSuffix(n.tun.Version, ".0")
	if !iosSupportedVersion(version) {
		n.tun.Close()
		return fmt.Errorf("se requiere iOS %d o posterior (el dispositivo tiene iOS %s)", iosMinimumMajor, version)
	}
	has := func(name string) bool { return n.tun.Services[iosServiceNames[name]].Port != 0 }
	if !has("capture") && !has("apps") {
		n.tun.Close()
		return errIOSDeveloperImage
	}
	n.status = iosStatus{IOS: version, Capabilities: nativeCapabilities(has), Video: "waiting"}
	n.hid, n.shots = n.tun.HID(), n.tun.Screenshots()
	n.wg.Add(1)
	go n.keepAwake()
	if touchStream && has("display") {
		n.startStream()
		n.wg.Add(1)
		go n.streamLoop()
	}
	return nil
}

// newIOSNative is an engine not connected yet, with its log open.
func (app *Service) newIOSNative(udid string) *iosNative {
	n := &iosNative{stop: make(chan struct{})}
	app.iosLibraryLogOnce.Do(func() {
		if f, e := os.OpenFile(app.LogsPath("ios-goios.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); e == nil {
			coredevice.SetLog(f)
		}
	})
	if f, e := os.OpenFile(app.LogsPath("ios-control-"+udid+".log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); e == nil {
		n.logFile = f
	}
	return n
}

// nativeCapabilities mirrors what each published service allows.
func nativeCapabilities(has func(string) bool) []string {
	var caps []string
	if has("touch") {
		caps = append(caps, "tap", "swipe", "text", "key:ENTER", "key:DEL", "live-touch")
	}
	if has("buttons") {
		for name := range coredevice.Buttons {
			caps = append(caps, "key:"+name)
		}
	}
	if has("capture") || has("dvt") {
		caps = append(caps, "screenshot")
	}
	if has("apps") {
		caps = append(caps, "app-start", "app-stop")
		if has("touch") {
			caps = append(caps, "url")
		}
	}
	slices.Sort(caps)
	return caps
}

// Lockdown failures the preparation flow handles on its own (iosConnectError).
var (
	errIOSLocked        = errors.New("unlock the device and try again")
	errIOSDeveloperMode = errors.New("turn on Developer Mode on the device")
	errIOSNotOnUSB      = errors.New("the device is not connected by USB")
)

// iosConnectError turns the phone's lockdown errors into the sentinel errors above.
func iosConnectError(e error) error {
	text := e.Error()
	switch {
	case strings.Contains(text, "PasswordProtected") || strings.Contains(text, "DeviceLocked"):
		return errIOSLocked
	case strings.Contains(text, "DeveloperMode"):
		return errIOSDeveloperMode
	case strings.Contains(text, "not found") || strings.Contains(text, "DeviceNotFound"):
		return errIOSNotOnUSB
	}
	return e
}

// iosConnectMessage is the text shown for a connection error, in the active language.
func (app *Service) iosConnectMessage(e error) string {
	switch {
	case errors.Is(e, errIOSLocked):
		return app.L("desbloquea el dispositivo y vuelve a intentarlo", errIOSLocked.Error())
	case errors.Is(e, errIOSDeveloperMode):
		return app.L("activa el modo de desarrollador en el dispositivo", errIOSDeveloperMode.Error())
	case errors.Is(e, errIOSNotOnUSB):
		return app.L("el dispositivo no está conectado por USB", errIOSNotOnUSB.Error())
	}
	return e.Error()
}

func (n *iosNative) logf(format string, args ...any) {
	n.logMu.Lock()
	defer n.logMu.Unlock()
	if n.logFile != nil {
		fmt.Fprintf(n.logFile, "%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, args...))
	}
}

func (n *iosNative) closeLog() {
	n.logMu.Lock()
	defer n.logMu.Unlock()
	if n.logFile != nil {
		n.logFile.Close()
		n.logFile = nil
	}
}

// keepAwake stops auto-lock while connected: a locked phone stops its video and
// refuses a new stream.
func (n *iosNative) keepAwake() {
	defer n.wg.Done()
	for {
		if e := n.tun.KeepAwake(300); e != nil {
			n.logf("keep-awake failed: %v", e)
		}
		select {
		case <-n.stop:
			return
		case <-n.tun.Done():
			return
		case <-time.After(2 * time.Minute):
		}
	}
}

// startStream opens the DisplayService stream after stopping any stream left on the
// phone, and reconnects HID to it. Returns whether the stream is up.
func (n *iosNative) startStream() bool {
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if ids, e := n.tun.DisplaySessions(c); e == nil && len(ids) > 0 {
		n.logf("stopping %d stream(s) left on the phone: %v", len(ids), n.tun.StopDisplaySession(c, ids[0]))
	}
	d, e := n.tun.StartDisplayCodec(c, n.showVideo)
	n.mu.Lock()
	defer n.mu.Unlock()
	if e != nil {
		if errors.Is(e, context.DeadlineExceeded) {
			n.timeouts++
		} else {
			n.timeouts = 0
		}
		n.logf("touch stream unavailable (%d timeouts): %v", n.timeouts, e)
		if strings.Contains(e.Error(), "in progress") {
			if ids, err := n.tun.DisplaySessions(c); err == nil && len(ids) > 0 {
				n.tun.StopDisplaySession(c, ids[0])
			}
		}
		return false
	}
	n.timeouts, n.display = 0, d
	if n.onDisplay != nil {
		n.onDisplay(d) // the Wi-Fi picture comes from this stream
	}
	// The phone matches HID input against the live stream.
	n.hid.Reconnect()
	n.logf("touch stream ready: session %s", d.Session)
	return true
}

// streamLoop restarts the touch stream when it fails or the phone stops sending.
func (n *iosNative) streamLoop() {
	defer n.wg.Done()
	delay := streamRetryMin
	for {
		n.mu.Lock()
		d := n.display
		n.mu.Unlock()
		wait := 2 * time.Second
		if d == nil {
			wait = delay
		}
		select {
		case <-n.stop:
			return
		case <-n.tun.Done():
			return
		case <-time.After(wait):
		}
		if d != nil {
			// The phone closes a stream it stopped feeding; 30 s covers its 20 s timeout.
			if d.Idle() < 30*time.Second {
				continue
			}
			n.logf("touch stream stopped sending; restarting it")
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			d.Stop(c)
			cancel()
			n.mu.Lock()
			n.display = nil
			n.mu.Unlock()
		}
		if n.startStream() {
			delay = streamRetryMin
		} else {
			delay = min(streamRetryMax, delay*2)
		}
	}
}

func (n *iosNative) alive() bool {
	select {
	case <-n.tun.Done():
		return false
	default:
		return true
	}
}

// close stops the touch stream (waiting for the phone's answer) before the tunnel.
func (n *iosNative) close() {
	close(n.stop)
	n.mu.Lock()
	d := n.display
	n.display = nil
	n.mu.Unlock()
	if d != nil {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		n.logf("touch stream stopped: %v", d.Stop(c))
		cancel()
	}
	n.hid.Close()
	n.shots.Close()
	n.tun.Close()
	n.wg.Wait()
	n.closeLog()
}

func (n *iosNative) info() iosStatus { return n.status }

func (n *iosNative) supports(s Spec) bool {
	return slices.Contains(n.status.Capabilities, iosCapability(s))
}

func (n *iosNative) wedged(context.Context) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.timeouts >= streamWedged
}

func (n *iosNative) liveTouch(_ context.Context, phase string, x, y float64) error {
	x, y = max(0, min(1, x)), max(0, min(1, y))
	switch phase {
	case "down", "move":
		return n.hid.Touch(true, x, y)
	case "up":
		return n.hid.Touch(false, x, y)
	}
	return fmt.Errorf("unknown touch phase: %s", phase)
}

// sleep waits d unless c ends first.
func sleep(c context.Context, d time.Duration) error {
	select {
	case <-c.Done():
		return c.Err()
	case <-time.After(d):
		return nil
	}
}

func (n *iosNative) tap(c context.Context, x, y float64) error {
	if e := n.hid.Touch(true, x, y); e != nil {
		return e
	}
	sleep(c, 50*time.Millisecond)
	return n.hid.Touch(false, x, y)
}

// swipe interpolates about 60 samples a second (8 to 120) and lifts at the end.
func (n *iosNative) swipe(c context.Context, x1, y1, x2, y2 float64, ms int) error {
	duration := max(50*time.Millisecond, time.Duration(ms)*time.Millisecond)
	steps := max(8, min(120, int(duration.Seconds()*60)))
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		if e := n.hid.Touch(true, x1+(x2-x1)*t, y1+(y2-y1)*t); e != nil {
			return e
		}
		if e := sleep(c, duration/time.Duration(steps)); e != nil {
			n.hid.Touch(false, x2, y2)
			return e
		}
	}
	return n.hid.Touch(false, x2, y2)
}

func (n *iosNative) press(c context.Context, usages ...byte) error {
	if e := n.hid.Keys(usages...); e != nil {
		return e
	}
	sleep(c, 40*time.Millisecond)
	e := n.hid.Keys()
	sleep(c, 20*time.Millisecond)
	return e
}

// chord holds modifier while usage is pressed: a single report with both is read as
// the bare key.
func (n *iosNative) chord(c context.Context, modifier, usage byte) error {
	for _, keys := range [][]byte{{modifier}, {modifier, usage}, {modifier}, {}} {
		if e := n.hid.Keys(keys...); e != nil {
			return e
		}
		sleep(c, 30*time.Millisecond)
	}
	return nil
}

// hidUsage is the Keyboard page usage of a letter, digit or space, and whether it
// needs Shift (USB HID Usage Tables).
func hidUsage(ch rune) (byte, bool, bool) {
	switch {
	case ch >= 'a' && ch <= 'z':
		return byte(0x04 + ch - 'a'), false, true
	case ch >= 'A' && ch <= 'Z':
		return byte(0x04 + ch - 'A'), true, true
	case ch >= '1' && ch <= '9':
		return byte(0x1E + ch - '1'), false, true
	case ch == '0':
		return 0x27, false, true
	case ch == ' ':
		return 0x2C, false, true
	}
	return 0, false, false
}

// text types letters, digits and spaces, which sit on the same keys in the common
// layouts; anything else is pasted, which needs no layout.
func (n *iosNative) text(c context.Context, value string) error {
	typable := value != ""
	for _, ch := range value {
		if _, _, ok := hidUsage(ch); !ok {
			typable = false
			break
		}
	}
	if typable {
		for _, ch := range value {
			usage, shift, _ := hidUsage(ch)
			var e error
			if shift {
				e = n.chord(c, coredevice.KeyLeftShift, usage)
			} else {
				e = n.press(c, usage)
			}
			if e != nil {
				return e
			}
		}
		return nil
	}
	if e := n.tun.Paste(value); e != nil {
		return e
	}
	return n.chord(c, coredevice.KeyLeftGUI, coredevice.KeyV)
}

// url uses Safari's shortcuts, as it ignores launch arguments: Cmd+T leaves the tab
// overview with a new tab, Cmd+L focuses its address bar, then the URL and Enter.
func (n *iosNative) url(c context.Context, url string) error {
	if e := n.tun.Launch(safariBundle, nil); e != nil {
		return e
	}
	steps := []func() error{
		func() error { return sleep(c, 1200*time.Millisecond) },
		func() error { return n.chord(c, coredevice.KeyLeftGUI, coredevice.KeyT) },
		func() error { return sleep(c, 800*time.Millisecond) },
		func() error { return n.chord(c, coredevice.KeyLeftGUI, coredevice.KeyL) },
		func() error { return sleep(c, 400*time.Millisecond) },
		func() error { return n.text(c, url) },
		func() error { return n.press(c, coredevice.KeyEnter) },
	}
	for _, step := range steps {
		if e := step(); e != nil {
			return e
		}
	}
	return nil
}

func (n *iosNative) key(c context.Context, name string) error {
	name = strings.ToUpper(name)
	if b, ok := coredevice.Buttons[name]; ok {
		if e := n.hid.Button(b, true); e != nil {
			return e
		}
		sleep(c, b.Hold)
		return n.hid.Button(b, false)
	}
	switch name {
	case "ENTER":
		return n.press(c, coredevice.KeyEnter)
	case "DEL":
		return n.press(c, coredevice.KeyBackspace)
	}
	return fmt.Errorf("unsupported key: %s", name)
}

func (n *iosNative) do(c context.Context, app *Service, d Device, s Spec) (string, error) {
	n.input.Lock()
	defer n.input.Unlock()
	switch s.Action {
	case "tap":
		return "", n.tap(c, s.X, s.Y)
	case "swipe":
		ms := s.Duration
		if ms <= 0 {
			ms = 300
		}
		return "", n.swipe(c, s.X, s.Y, s.X2, s.Y2, ms)
	case "text":
		return "", n.text(c, s.Args[0])
	case "key":
		return "", n.key(c, s.Args[0])
	case "url":
		return "", n.url(c, app.NormalizeURL(s.Args[0]))
	case "app-start":
		return "", n.tun.Launch(s.Args[0], nil)
	case "app-stop":
		_, e := n.tun.Kill(s.Args[0])
		return "", e
	case "screenshot":
		png, e := n.shots.Take()
		if e != nil {
			return "", e
		}
		return app.saveIOSScreenshot(d, png)
	}
	return "", errors.New("iOS action not implemented")
}
