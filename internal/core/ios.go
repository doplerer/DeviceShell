package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doplerer/deviceshell/internal/filelock"
)

type iosBackend struct{ app *Service }
type iosOptions struct {
	Connector map[string]iosConnectorInfo `json:"connector,omitempty"`
	// WiFi is the registry of iPhones paired for or seen over Wi-Fi, by UDID.
	WiFi map[string]iosWiFiInfo `json:"wifi,omitempty"`
}
type iosDeviceSession struct {
	viewRefs   int
	views      atomic.Int32 // viewRefs, readable without mu (held during actions)
	controller bool
	mu         sync.Mutex
	lock       *os.File
	conn       iosConnector
	video      *iosVideo
	// idle closes the connector a while after the last window closed.
	idle *time.Timer
	// stalls counts watchdog restarts in a row; each doubles the next grace, so a phone
	// that stopped sending is not restarted again and again.
	stalls    int
	videoFrom time.Time
}

// Keys the connector can press: hardware buttons plus ENTER/DEL on its virtual keyboard.
var iosKeys = []string{"HOME", "ENTER", "DEL", "POWER", "LOCK", "VOLUME_UP", "VOLUME_DOWN", "MUTE"}

func (app *Service) iosConfigPath() string {
	return app.IOSToolsPath("config.json")
}
func (app *Service) readIOSOptions() iosOptions {
	var cfg iosOptions
	_ = app.ReadJSON(app.iosConfigPath(), &cfg)
	return cfg
}
func (app *Service) updateIOSOptions(change func(*iosOptions)) error {
	return app.WithStore(func() error {
		var cfg iosOptions
		if e := app.ReadJSON(app.iosConfigPath(), &cfg); e != nil && !os.IsNotExist(e) {
			return e
		}
		change(&cfg)
		return app.WriteJSON(app.iosConfigPath(), cfg)
	})
}
func (app *Service) iosCommand(c context.Context, args ...string) (string, error) {
	if app.RunIOS != nil {
		return app.RunIOS(c, args...)
	}
	return goIOS(c, args)
}

// Discover lists the iPhones. What each one is (model, iOS, Developer Mode) is asked
// over lockdown only when usbmux shows it under a new id (it was plugged in or
// re-enumerated) or the preparation changed it; every other scan only lists devices.
func (b iosBackend) Discover(c context.Context) ([]Device, error) {
	out, e := b.app.iosCommand(c, "list")
	if e != nil {
		return nil, e
	}
	var result struct {
		DeviceList []string `json:"deviceList"`
		DeviceIDs  []int    `json:"deviceIDs"`
	}
	if e = json.Unmarshal([]byte(out), &result); e != nil {
		return nil, fmt.Errorf("go-ios list: %w", e)
	}
	usbmux := func(i int) int {
		if i < len(result.DeviceIDs) {
			return result.DeviceIDs[i]
		}
		return 0
	}
	ds := make([]Device, len(result.DeviceList))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for w := 0; w < min(4, len(ds)); w++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				id := result.DeviceList[i]
				if known, ok := b.app.iosInfo.get(id, usbmux(i)); ok {
					ds[i] = known
					continue
				}
				d := Device{Serial: "ios:" + id, Platform: "ios", State: "device", Model: "iPhone", Transport: "usb"}
				cc, cancel := context.WithTimeout(c, 5*time.Second)
				info, err := b.app.iosCommand(cc, "--udid="+id, "info")
				cancel()
				if err != nil {
					d.State = "unauthorized"
				} else {
					var v struct{ ProductType, ProductVersion, DeviceName string }
					if json.Unmarshal([]byte(info), &v) != nil {
						d.State = "offline"
					} else {
						d.Model = v.ProductType
						d.OSVersion = v.ProductVersion
						if !iosSupportedVersion(v.ProductVersion) {
							d.State = iosOutdated
						} else if on, ok := b.app.iosDeveloperMode(c, id); !on {
							d.State = iosDeveloperOff
						} else if b.app.iosIsLocked(d.Serial) {
							d.State = iosLocked // asked again after iosInfoRetry
						} else if !ok {
							// Developer Mode unread: not kept, so the next scan asks again
							// instead of taking it as on until the phone re-enumerates.
							ds[i] = d
							continue
						}
					}
				}
				ds[i] = d
				b.app.iosInfo.put(id, usbmux(i), d)
			}
		}()
	}
	for i := range ds {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	usb, waiting := map[string]bool{}, map[string]bool{}
	for i := range ds {
		if ds[i].State == "device" && b.app.iosIsLocked(ds[i].Serial) {
			ds[i].State = iosLocked
		}
		ds[i].Transport = "usb"
		usb[ds[i].NativeID()] = true
		if iosAwaitingActivation(ds[i]) {
			// Also for a state read from the cache: the next step may be due.
			b.app.activateIOS(ds[i].NativeID(), usbmux(i), ds[i].State, ds[i].Model)
			waiting[ds[i].NativeID()] = true
		}
	}
	b.app.forgetActivations(waiting)
	// iPhones without a cable that answer over Wi-Fi (paired once over USB).
	ds = append(ds, b.app.wifiDevices(usb)...)
	for _, d := range ds {
		b.app.iosTransport.Store(d.Serial, d.Transport)
	}
	return ds, nil
}

// iosLocked marks an iPhone that refused its last connection because it is locked
// with its passcode: windows, @all and actions leave it out until a connection
// succeeds (automatic preparation retries it every 30 s).
const iosLocked = "locked"

func (app *Service) iosIsLocked(serial string) bool {
	_, ok := app.iosLocks.Load(serial)
	return ok
}

// noteIOSConnect remembers whether a connection to d failed because it is locked.
// A locked device leaves the lists; discovery reads its state again every
// iosInfoRetry and clears the mark once lockdown answers (iosDeveloperMode).
func (app *Service) noteIOSConnect(serial string, e error) {
	if errors.Is(e, errIOSLocked) {
		app.iosLocks.Store(serial, true)
		app.iosInfo.forget(strings.TrimPrefix(serial, "ios:"))
	} else if e == nil {
		app.iosLocks.Delete(serial)
	}
}

// iosDeveloperOff and iosOutdated mark connected, trusted devices that cannot be
// controlled yet: without Developer Mode, or older than iOS 27. They are kept in
// the inventory but hidden from listings and never selected by @all.
const (
	iosDeveloperOff = "developer-off"
	iosOutdated     = "ios-outdated"
)

// iosAwaitingActivation reports a USB iPhone that DeviceShell is still preparing for the
// farm (iosActivation): it never trusted this PC, or has Developer Mode off. Until it
// is ready it is neither registered nor listed.
func iosAwaitingActivation(d Device) bool {
	return d.OS() == "ios" && d.Transport != "wifi" && (d.State == "unauthorized" || d.State == iosDeveloperOff)
}

// iosMinimumMajor is the first iOS whose devices grant the live screen stream;
// DeviceShell supports only those, so every iPhone has video and live touch.
const iosMinimumMajor = 27

func iosSupportedVersion(version string) bool {
	major, e := strconv.Atoi(strings.Split(version, ".")[0])
	return e == nil && major >= iosMinimumMajor
}

// Listed returns the devices shown to the user: only the available ones. Devices
// that are disconnected, unauthorized, offline, locked, without Developer Mode or
// on an old iOS stay out of lists; doctor explains them.
func Listed(d []Device) []Device {
	out := make([]Device, 0, len(d))
	for _, v := range d {
		if v.State == "device" {
			out = append(out, v)
		}
	}
	return out
}

// iosDeveloperMode reports Developer Mode and whether the phone answered. An
// unreadable answer counts as enabled, so a transient failure never hides a working
// device, but it is not known: discovery does not keep it and asks again.
func (app *Service) iosDeveloperMode(c context.Context, udid string) (enabled, known bool) {
	cc, cancel := context.WithTimeout(c, 5*time.Second)
	defer cancel()
	raw, e := app.iosCommand(cc, "--udid="+udid, "devmode", "get")
	var mode struct {
		Enabled *bool `json:"DeveloperModeEnabled"`
	}
	if e != nil {
		// A locked device refuses the image mounter: mark it before any window opens.
		app.noteIOSConnect("ios:"+udid, iosConnectError(e))
	} else {
		app.iosLocks.Delete("ios:" + udid) // lockdown answered: it is unlocked
	}
	if e == nil && json.Unmarshal([]byte(raw), &mode) == nil && mode.Enabled != nil {
		return *mode.Enabled, true
	}
	// Unknown: taken as on (it is not switched on blindly), but asked again next scan.
	if e != nil && !app.iosIsLocked("ios:"+udid) {
		app.activationLog("%s: developer mode unknown: %v", udid, e)
	}
	return true, false
}

// connectorAction reports whether the CoreDevice connector performs the step.
func connectorAction(action string) bool {
	switch action {
	case "tap", "swipe", "text", "key", "url", "app-start", "app-stop", "screenshot":
		return true
	}
	return false
}

func (b iosBackend) Supports(d Device, s Spec) error {
	supported := false
	switch s.Action {
	case "tap", "swipe", "text", "screenshot", "wait", "app-list", "app-start", "app-stop", "app-uninstall", "url":
		supported = true
	case "key":
		supported = len(s.Args) == 1 && slices.Contains(iosKeys, strings.ToUpper(s.Args[0]))
	case "reboot":
		supported = len(s.Args) == 1 && s.Args[0] == "system"
	case "app-install":
		supported = len(s.Args) == 1 && strings.EqualFold(filepath.Ext(s.Args[0]), ".ipa")
	}
	if !supported {
		return fmt.Errorf("%s is not supported on iPhone/iOS", s.Action)
	}
	if d.Transport == "wifi" {
		switch s.Action {
		case "app-list", "app-install", "app-uninstall", "reboot":
			return fmt.Errorf("%s is not available over Wi-Fi: connect the iPhone by cable", s.Action)
		}
	}
	if d.OSVersion != "" && !iosSupportedVersion(d.OSVersion) {
		return fmt.Errorf("se requiere iOS %d o posterior (el dispositivo tiene iOS %s)", iosMinimumMajor, d.OSVersion)
	}
	// Each iOS version publishes different developer services; the connector
	// records what this device offers the first time it connects.
	// A device updated since then is checked again when the connector starts.
	if info, ok := b.app.readIOSOptions().Connector[d.NativeID()]; ok && info.IOS == d.OSVersion && connectorAction(s.Action) && !slices.Contains(info.Capabilities, iosCapability(s)) {
		return fmt.Errorf("%s is not available on iOS %s without signing apps", iosCapability(s), info.IOS)
	}
	return nil
}
func (app *Service) iosSession(d Device) *iosDeviceSession {
	app.iosMu.Lock()
	defer app.iosMu.Unlock()
	if app.iosSessions == nil {
		app.iosSessions = map[string]*iosDeviceSession{}
	}
	s := app.iosSessions[d.Serial]
	if s == nil {
		s = &iosDeviceSession{}
		app.iosSessions[d.Serial] = s
	}
	return s
}

// CloseDevices releases the iPhones first: turning QuickTime off must happen before
// the process ends, and closing the Android connectors only removes their forwards.
func (app *Service) CloseDevices() {
	app.iosMu.Lock()
	for _, s := range app.iosSessions {
		s.mu.Lock()
		s.close(app)
		s.mu.Unlock()
	}
	app.iosMu.Unlock()
	app.closeAndroidSessions()
}
func (s *iosDeviceSession) close(app *Service) {
	s.stopConnector()
	if s.lock != nil {
		filelock.Unlock(s.lock)
		_ = s.lock.Close()
	}
	s.lock = nil
}

// stopConnector closes the connector and then the video: turning QuickTime off
// re-enumerates the device, so it waits until the tunnel is closed.
func (s *iosDeviceSession) stopConnector() {
	if s.conn != nil {
		s.conn.close()
	}
	if s.video != nil {
		s.video.close(true)
	}
	s.conn, s.video = nil, nil
}

// goVideo reports whether the session captures the picture: a connector replaced in
// tests never touches the USB device.
func (app *Service) goVideo() bool {
	return app.StartIOSConnector == nil
}
func (s *iosDeviceSession) acquire(app *Service, d Device) error {
	if s.lock != nil {
		return nil
	}
	// Use the UDID as bytes encoded in hex, never as an unchecked filesystem path.
	lockPath := app.DataPath(fmt.Sprintf(".ios-%x.lock", []byte(d.NativeID())))
	f, e := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	if e = filelock.Lock(f, true); e != nil {
		f.Close()
		return errors.New(app.L("iPhone ocupado por otra instancia de DeviceShell", "iPhone in use by another DeviceShell instance"))
	}
	s.lock = f
	return nil
}

// start keeps one connector per device for the life of the session; a connector
// that exited (phone unplugged, tunnel lost) is replaced on the next use.
// Caller holds s.mu.
func (s *iosDeviceSession) start(c context.Context, app *Service, d Device) error {
	stalled := s.video != nil && s.video.stalled.Load()
	if s.conn != nil && s.conn.alive() && !stalled {
		return nil
	}
	// A capture that ran well for 2 minutes starts the count again.
	switch {
	case s.video != nil && time.Since(s.videoFrom) > 2*time.Minute:
		s.stalls = 0
	case stalled:
		s.stalls++
	}
	s.stopConnector()
	if e := s.acquire(app, d); e != nil {
		return e
	}
	// The phone may have moved between cable and Wi-Fi since the window opened.
	if t, ok := app.iosTransport.Load(d.Serial); ok {
		d.Transport = t.(string)
	}
	var video *iosVideo
	if app.goVideo() {
		if app.iosVideoSource(d) == IPhoneVideoQuickTime {
			video = app.openIOSVideo(d.NativeID(), iosStallGrace(s.stalls))
		} else {
			video = app.openDisplayVideo(d.NativeID())
		}
		s.videoFrom = time.Now()
	}
	conn, e := app.connectIOS(c, d, video)
	app.noteIOSConnect(d.Serial, e)
	if e != nil {
		if video != nil {
			video.close(true)
		}
		return e
	}
	s.conn, s.video = conn, video
	return nil
}

// cableVideo is the picture chosen for iPhones on a cable (settings, iphoneVideo).
func (app *Service) cableVideo() string {
	c, _, e := app.ReadSettings()
	if e != nil || c.IPhoneVideo != IPhoneVideoQuickTime {
		return IPhoneVideoDisplayService
	}
	return IPhoneVideoQuickTime
}

// iosVideoSource is where d's picture comes from: DisplayService over Wi-Fi, and on a
// cable unless QuickTime is chosen in the settings. Read at every connection, so a
// change applies to the next one.
func (app *Service) iosVideoSource(d Device) string {
	if d.Transport == "wifi" {
		return IPhoneVideoDisplayService
	}
	return app.cableVideo()
}

// iosStallGrace is how long a capture may send nothing before the watchdog restarts
// it: 5 s, doubling with each restart in a row up to 5 minutes.
func iosStallGrace(stalls int) time.Duration {
	return min(5*time.Second<<min(stalls, 6), 5*time.Minute)
}

// run performs a connector action. Caller holds s.mu.
func (s *iosDeviceSession) run(c context.Context, app *Service, d Device, spec Spec) (string, error) {
	if e := s.start(c, app, d); e != nil {
		return "", e
	}
	if !s.conn.supports(spec) {
		return "", fmt.Errorf("%s is not available on iOS %s without signing apps", iosCapability(spec), s.conn.info().IOS)
	}
	out, e := s.conn.do(c, app, d, spec)
	if e != nil && !s.conn.alive() {
		s.stopConnector()
	}
	return out, e
}

func (b iosBackend) Execute(c context.Context, d Device, spec Spec) (string, error) {
	s := b.app.iosSession(d)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.controller {
		return "", errors.New("iPhone controlled by view/sync; close it before running actions")
	}
	if e := c.Err(); e != nil {
		return "", e
	}
	if e := s.acquire(b.app, d); e != nil {
		return "", e
	}
	args := []string{"--udid=" + d.NativeID()}
	switch spec.Action {
	case "app-list":
		out, e := b.app.iosCommand(c, append(args, "apps")...)
		if e != nil {
			return out, e
		}
		return normalizeIOSApps(out)
	case "app-install":
		return b.app.iosCommand(c, append(args, "install", "--path="+spec.Args[0])...)
	case "app-uninstall":
		return b.app.iosCommand(c, append(args, "uninstall", spec.Args[0])...)
	case "reboot":
		s.close(b.app)
		if e := s.acquire(b.app, d); e != nil {
			return "", e
		}
		defer s.close(b.app)
		return b.app.iosCommand(c, append(args, "reboot")...)
	}
	return s.run(c, b.app, d, spec)
}

func (app *Service) BackendDiagnostics(c context.Context) map[string]string {
	out := map[string]string{}
	for _, p := range []string{"android", "ios"} {
		probe, stop := context.WithTimeout(c, 10*time.Second)
		ds, e := app.backend(Device{Platform: p}).Discover(probe)
		stop()
		if e != nil {
			out[p] = e.Error()
			continue
		}
		out[p] = fmt.Sprintf("USB: %d devices", len(ds))
		if p != "ios" {
			continue
		}
		wifi := 0
		for _, d := range ds {
			if d.Transport == "wifi" {
				wifi++
			}
		}
		out[p] = fmt.Sprintf("USB: %d devices; Wi-Fi: %d", len(ds)-wifi, wifi)
		out[p] += "; engine: Go"
		cfg := app.readIOSOptions()
		// The iPhones still being activated are only counted, by stage.
		if pending := app.PendingIOS(); len(pending) > 0 {
			stages := map[string]int{}
			var order []string
			for _, v := range pending {
				if stages[v.Stage] == 0 {
					order = append(order, v.Stage)
				}
				stages[v.Stage]++
			}
			var parts []string
			for _, s := range order {
				parts = append(parts, fmt.Sprintf("%s %d", s, stages[s]))
			}
			out[p] += fmt.Sprintf("; waiting for activation: %d (%s)", len(pending), strings.Join(parts, ", "))
		}
		for _, d := range ds {
			if iosAwaitingActivation(d) {
				continue
			}
			out[p] += "; " + d.Serial + ": " + d.State + " / iOS " + d.OSVersion
			if d.Transport == "wifi" {
				// Developer Mode is read over usbmux, which a Wi-Fi phone is not on.
				out[p] += " / Wi-Fi"
				continue
			}
			modeCtx, modeStop := context.WithTimeout(c, 3*time.Second)
			modeRaw, modeErr := app.iosCommand(modeCtx, "--udid="+d.NativeID(), "devmode", "get")
			modeStop()
			var mode struct {
				Enabled *bool `json:"DeveloperModeEnabled"`
			}
			if modeErr != nil || json.Unmarshal([]byte(modeRaw), &mode) != nil || mode.Enabled == nil {
				out[p] += "; developer mode: unchecked"
			} else if *mode.Enabled {
				out[p] += "; developer mode: enabled"
			} else {
				out[p] += "; developer mode: disabled (enable on device)"
			}
			if info, ok := cfg.Connector[d.NativeID()]; ok {
				out[p] += "; connector: " + strings.Join(info.Capabilities, ",") + "; video: " + info.Video
			}
		}
	}
	return out
}

// Match the existing app-list contract, including interactive package completion.
func normalizeIOSApps(raw string) (string, error) {
	var value any
	if e := json.Unmarshal([]byte(raw), &value); e != nil {
		return "", fmt.Errorf("go-ios apps: %w", e)
	}
	ids := map[string]bool{}
	var visit func(any)
	visit = func(v any) {
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				visit(item)
			}
		case map[string]any:
			for key, item := range x {
				if key == "CFBundleIdentifier" {
					if id, ok := item.(string); ok {
						ids[id] = true
					}
				} else {
					visit(item)
				}
			}
		}
	}
	visit(value)
	var lines []string
	for id := range ids {
		lines = append(lines, "package:"+id)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n"), nil
}

// Only an authoritative scan can invalidate sessions; transport failures are reported offline.
func (app *Service) invalidateDisconnectedIOS(live []Device, issues map[string]error) {
	if issues["ios"] != nil {
		return
	}
	connected := map[string]bool{}
	for _, d := range live {
		if d.OS() == "ios" && d.State == "device" {
			connected[d.Serial] = true
		}
	}
	app.iosMu.Lock()
	defer app.iosMu.Unlock()
	for serial, s := range app.iosSessions {
		if !connected[serial] {
			s.mu.Lock()
			s.close(app)
			s.mu.Unlock()
		}
	}
}
