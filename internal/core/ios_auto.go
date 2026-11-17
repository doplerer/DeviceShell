package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type IOSSetupProgress struct {
	Alias     string `json:"alias"`
	Stage     string `json:"stage"`
	Message   string `json:"message"`
	NeedsUser bool   `json:"needsUser"`
	Ready     bool   `json:"ready"`
	Updated   string `json:"updated"`
}

func (app *Service) setupProgress(d Device, stage, message string, manual, ready bool, notify func(IOSSetupProgress)) IOSSetupProgress {
	p := IOSSetupProgress{d.Alias, stage, message, manual, ready, time.Now().Format(time.RFC3339)}
	_ = app.WriteJSON(app.IOSToolsPath("setup", d.NativeID()+".json"), p)
	if notify != nil {
		notify(p)
	}
	return p
}

// iosAutoState is what automatic preparation last did for a connected device.
type iosAutoState struct {
	state string // the device state it saw
	stage string
	at    time.Time
}

// iosAutoRetry spaces new attempts on a device that is locked, busy or failed.
const iosAutoRetry = 30 * time.Second

// iosAutoFinal stages wait for the device to change state (trust, Developer
// Mode, an update) or are done; every other stage is retried.
func iosAutoFinal(stage string) bool {
	switch stage {
	case "ready", "developer-mode", "update", "connect":
		return true
	}
	return false
}

// AutoPrepareIOS prepares the available iPhones without an Apple account as
// discovery reports them. Each device is handled once per connection and again
// when its state changes. Devices that are not available to the farm are not
// prepared here: discovery itself asks an untrusted one to trust this PC and turns
// on Developer Mode (iosActivation), and leaves a locked or old one alone; doctor
// explains them. A call while another runs returns at once: the next refresh
// catches up.
func (app *Service) AutoPrepareIOS(c context.Context, devices []Device, notify func(IOSSetupProgress)) map[string]IOSSetupProgress {
	if !app.iosSetupMu.TryLock() {
		return nil
	}
	defer app.iosSetupMu.Unlock()
	if app.iosAuto == nil {
		app.iosAuto = map[string]iosAutoState{}
	}
	now := time.Now()
	attached := map[string]bool{}
	var due []Device
	fresh, control := false, false
	for _, d := range devices {
		if d.OS() != "ios" || d.State == "disconnected" {
			continue
		}
		attached[d.Serial] = true
		// Over Wi-Fi there is nothing to prepare: the scanner already checked it.
		if d.State != "device" || d.Transport == "wifi" {
			continue
		}
		last, seen := app.iosAuto[d.Serial]
		if seen && last.state == d.State && (iosAutoFinal(last.stage) || now.Sub(last.at) < iosAutoRetry) {
			continue
		}
		due = append(due, d)
		fresh = fresh || !seen
		control = control || d.State == "device"
	}
	// A device that left is prepared again when it comes back.
	for serial := range app.iosAuto {
		if !attached[serial] {
			delete(app.iosAuto, serial)
		}
	}
	app.iosLocks.Range(func(serial, _ any) bool {
		if !attached[serial.(string)] {
			app.iosLocks.Delete(serial)
		}
		return true
	})
	if len(due) == 0 || c.Err() != nil {
		return nil
	}
	result := map[string]IOSSetupProgress{}
	// The QuickTime driver (an administrator prompt) is only needed when QuickTime is
	// the cable picture; a test's stand-in connector needs none.
	if control && app.StartIOSConnector == nil && app.cableVideo() == IPhoneVideoQuickTime {
		// Windows' administrator prompt comes back only when another iPhone connects.
		if fresh || !app.iosDriverTried {
			app.iosDriverTried = true
			p := IOSSetupProgress{Alias: iosDriverAlias, Stage: "components", Ready: true, Message: "instalado", Updated: time.Now().Format(time.RFC3339)}
			if e := app.ensureIOSQuickTimeDriver(c, notify); e != nil && c.Err() == nil {
				p.Ready, p.Message = false, e.Error()+app.L("; las ventanas del iPhone no tendrán imagen hasta instalarlo (reconecta el iPhone para volver a intentarlo).", "; iPhone windows show no picture until it is installed (reconnect the iPhone to try again).")
			}
			// Kept for doctor; nothing is printed while the user works.
			_ = app.WriteJSON(app.IOSToolsPath("setup", iosDriverFile), p)
			if notify != nil && !p.Ready {
				notify(p)
			}
		}
	}
	states, e := app.prepareIOSTargets(c, due, notify)
	if c.Err() != nil {
		return states
	}
	for _, d := range due {
		p, ok := states[d.Alias]
		if !ok {
			// The device stopped answering go-ios before reporting a stage.
			p = app.setupProgress(d, "connector-error", fmt.Sprint(e), false, false, notify)
		}
		result[d.Alias] = p
		app.iosAuto[d.Serial] = iosAutoState{d.State, p.Stage, now}
		if p.Ready {
			// Ready over USB: pair it for Wi-Fi once, in the background.
			app.pairForWiFi(d)
		}
	}
	return result
}

func (app *Service) prepareIOSTargets(c context.Context, targets []Device, notify func(IOSSetupProgress)) (map[string]IOSSetupProgress, error) {
	result := map[string]IOSSetupProgress{}
	failed := false
	for _, d := range targets {
		if e := c.Err(); e != nil {
			return result, e
		}
		report := func(stage, msg string, user, ready bool) {
			result[d.Alias] = app.setupProgress(d, stage, msg, user, ready, notify)
		}
		// Preparing may change what the phone is (Developer Mode, the developer image).
		defer app.iosInfo.forget(d.NativeID())
		if d.OS() != "ios" || d.State != "device" {
			// Only available iPhones are prepared; the rest are left alone.
			continue
		}
		raw, e := app.iosCommand(c, "--udid="+d.NativeID(), "devmode", "get")
		if e != nil {
			if e = iosConnectError(e); errors.Is(e, errIOSLocked) {
				app.noteIOSConnect(d.Serial, e)
				report("unlock", app.iosConnectMessage(e), true, false)
				continue
			}
			return result, e
		}
		var mode struct {
			Enabled bool `json:"DeveloperModeEnabled"`
		}
		if e = json.Unmarshal([]byte(raw), &mode); e != nil {
			return result, e
		}
		if !mode.Enabled {
			// Nothing is opened on the device: the next scan sees Developer Mode off
			// (the cache is forgotten above) and the device leaves the lists.
			report("developer-mode", "Activa Modo de desarrollador en Ajustes > Privacidad y seguridad y confirma el reinicio.", true, false)
			continue
		}
		report("connector", "Conectando; si hace falta se monta la imagen de desarrollo (requiere Internet). Conserva el dispositivo desbloqueado...", false, false)
		session := app.iosSession(d)
		session.mu.Lock()
		if session.viewRefs > 0 {
			session.mu.Unlock()
			report("busy", app.L("En uso por una pantalla o réplica; se comprobará cuando la cierres", "In use by a screen or replica; it is checked once you close it"), false, false)
			continue
		}
		e = session.start(c, app, d)
		var status iosStatus
		if e == nil {
			status = session.conn.info()
		}
		// Release the device so actions and windows can open their own session.
		session.close(app)
		session.mu.Unlock()
		if e != nil {
			if errors.Is(e, errIOSLocked) || errors.Is(e, errIOSDeveloperMode) {
				report("unlock", app.iosConnectMessage(e), true, false)
				continue
			}
			report("connector-error", app.iosConnectMessage(e), false, false)
			failed = true
			continue
		}
		report("ready", app.iosReadyMessage(status), false, true)
	}
	if failed {
		return result, fmt.Errorf("preparation incomplete; check the state of the devices")
	}
	return result, nil
}

// iosReadyMessage confirms the connection and flags anything the device withheld.
func (app *Service) iosReadyMessage(s iosStatus) string {
	message := app.L("Preparado (iOS ", "Ready (iOS ") + s.IOS + app.L("): vídeo en directo, toques, texto, botones y apps.", "): live video, touch, text, buttons and apps.")
	if !slices.Contains(s.Capabilities, "tap") {
		message = app.L("Conectado (iOS ", "Connected (iOS ") + s.IOS + app.L(") pero sin servicio de toques; desconéctalo y vuelve a conectarlo.", ") but without the touch service; unplug it and plug it in again.")
	}
	if s.Video != "stream" {
		message += app.L(" El vídeo no se ha iniciado", " The video has not started")
		if s.VideoError != "" {
			message += ": " + strings.TrimSuffix(s.VideoError, ".")
		}
		message += app.L("; la ventana mostrará el error.", "; the window will show the error.")
	}
	return message
}

// iosDriverFile keeps the last QuickTime driver installation result for doctor.
const (
	iosDriverFile  = "_quicktime-driver.json"
	iosDriverAlias = "Controlador QuickTime"
)

// IOSSetupSummary is the last preparation of each available iPhone among devices,
// and the QuickTime driver's result. Unavailable devices are explained by their
// state instead, so stale preparations of a device that left are not shown.
func (app *Service) IOSSetupSummary(devices []Device) string {
	available := map[string]bool{}
	for _, d := range devices {
		if d.OS() == "ios" && d.State == "device" {
			available[d.Alias] = true
		}
	}
	entries, _ := filepath.Glob(app.IOSToolsPath("setup", "*.json"))
	var lines []string
	quickTime := app.cableVideo() == IPhoneVideoQuickTime
	if len(available) > 0 {
		if quickTime {
			lines = append(lines, app.L("vídeo por cable: QuickTime (cámbialo en /settings)", "video on cable: QuickTime (change it in /settings)"))
		} else {
			lines = append(lines, app.L("vídeo por cable: DisplayService (cámbialo en /settings)", "video on cable: DisplayService (change it in /settings)"))
		}
	}
	for _, path := range entries {
		var p IOSSetupProgress
		if app.ReadJSON(path, &p) != nil || p.Alias == "" {
			continue
		}
		if available[p.Alias] || p.Alias == iosDriverAlias && len(available) > 0 && quickTime {
			lines = append(lines, p.Alias+": "+p.Stage+" - "+p.Message)
		}
	}
	lines = append(lines, app.WiFiStatus(devices)...)
	return strings.Join(lines, "\n")
}
