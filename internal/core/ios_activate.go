package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// iosActivation brings the iPhones plugged in by USB that the farm cannot use yet up
// to Developer Mode: one that never trusted this PC is asked to («¿Confiar en este
// ordenador?»), and on one that trusts it Developer Mode is switched on (without a
// passcode: it restarts and confirms by itself) or, with a passcode, its toggle is
// revealed in Settings. Until then the phone is not registered (MergeInventory) and is
// only counted (PendingIOS).
type iosActivation struct {
	mu      sync.Mutex
	logMu   sync.Mutex // ios-activation.log
	entries map[string]*iosActivating
	running sync.WaitGroup
}

type iosActivating struct {
	usbmux  int // the id usbmux listed it under when last attempted
	stage   string
	model   string
	running bool
	next    time.Time // zero: not again while usbmux keeps the same id
}

// Activation stages, as PendingIOS reports them.
const (
	iosStageTrust    = "trust" // waiting for «Confiar» on the phone
	iosStageDenied   = "trust-denied"
	iosStageUnlock   = "unlock"   // locked: it cannot be asked
	iosStageEnabling = "enabling" // switching Developer Mode on (it restarts)
	iosStageManual   = "manual"   // passcode: the toggle was revealed in Settings (again every iosActivationRetry)
	iosStageRetry    = "retry"    // the last attempt failed; it is tried again
)

// iosTrustRetry spaces the pairing requests while the phone shows the trust prompt or
// is locked; iosActivationRetry spaces a failed attempt, and each time the toggle is
// revealed again (it may have been locked, or Settings already open).
// iosActivationLimit bounds one step (a restart into Developer Mode takes about a minute).
const (
	iosTrustRetry      = 5 * time.Second
	iosActivationRetry = 30 * time.Second
	iosActivationLimit = 5 * time.Minute
)

// activateIOS starts, in the background, the next step for a USB iPhone that
// discovery found untrusted ("unauthorized") or without Developer Mode. A step runs
// once per usbmux id unless it asked to be retried: a phone the user said no to is
// left alone until it re-enumerates (it is plugged in again, or it restarts when
// Developer Mode is switched on). One whose toggle was revealed gets it revealed
// again every iosActivationRetry, without trying to switch it on again.
func (app *Service) activateIOS(udid string, usbmux int, state, model string) {
	a := &app.iosActivation
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.entries == nil {
		a.entries = map[string]*iosActivating{}
	}
	e, ok := a.entries[udid]
	if ok && (e.running || e.usbmux == usbmux && (e.next.IsZero() || time.Now().Before(e.next))) {
		return
	}
	if !ok {
		e = &iosActivating{}
		a.entries[udid] = e
	}
	if model != "" {
		e.model = model
	}
	revealOnly := e.stage == iosStageManual && e.usbmux == usbmux && state == iosDeveloperOff
	e.usbmux, e.running, e.next = usbmux, true, time.Time{}
	switch {
	case state == "unauthorized":
		e.stage = iosStageTrust
	case !revealOnly:
		e.stage = iosStageEnabling
	}
	ctx := app.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	a.running.Add(1)
	go func() {
		defer a.running.Done()
		c, cancel := context.WithTimeout(ctx, iosActivationLimit)
		defer cancel()
		stage, retry, changed := app.activateStep(c, udid, state, revealOnly)
		// The step may have restarted the phone: "once per id" counts from the id it
		// came back with, so a restart into Developer Mode does not start it again.
		id := app.iosListedID(c, udid)
		if changed {
			// Trusted, or in Developer Mode: the next scan reads it again.
			app.iosInfo.forget(udid)
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		e.running, e.stage = false, stage
		if id != 0 {
			e.usbmux = id
		}
		if retry > 0 {
			e.next = time.Now().Add(retry)
		}
	}()
}

// iosListedID is the id usbmux lists udid under now, or 0.
func (app *Service) iosListedID(c context.Context, udid string) int {
	out, e := app.iosCommand(c, "list")
	if e != nil {
		return 0
	}
	var result struct {
		DeviceList []string `json:"deviceList"`
		DeviceIDs  []int    `json:"deviceIDs"`
	}
	if json.Unmarshal([]byte(out), &result) != nil {
		return 0
	}
	for i, id := range result.DeviceList {
		if id == udid && i < len(result.DeviceIDs) {
			return result.DeviceIDs[i]
		}
	}
	return 0
}

// activateStep runs one step, logs it, and returns the stage it leaves the phone in,
// when to try again (0: only after it re-enumerates) and whether the phone changed.
func (app *Service) activateStep(c context.Context, udid, state string, revealOnly bool) (string, time.Duration, bool) {
	args := []string{"devmode", "enable"}
	switch {
	case state == "unauthorized":
		args = []string{"pair"}
	case revealOnly:
		args = []string{"devmode", "reveal"}
	}
	_, e := app.iosCommand(c, append([]string{"--udid=" + udid}, args...)...)
	stage, retry, changed := activationResult(state, revealOnly, e)
	result := "ok"
	if e != nil {
		result = e.Error()
	}
	app.activationLog("%s (%s): %s: %s -> %s", udid, state, strings.Join(args, " "), result, stage)
	return stage, retry, changed
}

// activationResult turns the answer to a step into what activateStep returns.
func activationResult(state string, revealOnly bool, e error) (string, time.Duration, bool) {
	if state == "unauthorized" {
		switch {
		case e == nil:
			return iosStageTrust, time.Nanosecond, true // trusted: read it again at once
		case strings.Contains(e.Error(), "UserDeniedPairing"):
			return iosStageDenied, 0, false
		case strings.Contains(e.Error(), "PasswordProtected"):
			return iosStageUnlock, iosTrustRetry, false
		case strings.Contains(e.Error(), "PairingDialog"):
			return iosStageTrust, iosTrustRetry, false
		}
		return iosStageRetry, iosActivationRetry, false
	}
	if revealOnly {
		// Shown again; whether it worked or not, it is the user's turn.
		return iosStageManual, iosActivationRetry, false
	}
	switch {
	case e == nil:
		return iosStageEnabling, 0, true
	case strings.Contains(e.Error(), errDeveloperModeRevealed.Error()):
		return iosStageManual, iosActivationRetry, false
	case strings.Contains(e.Error(), "confirma"):
		// Switched on and restarted, but the confirmation was not answered.
		return iosStageManual, 0, true
	}
	return iosStageRetry, iosActivationRetry, false
}

// activationLog appends a line to output/logs/ios-activation.log: each step and what
// the phone answered, which is the only trace of why one is not activated.
func (app *Service) activationLog(format string, args ...any) {
	a := &app.iosActivation
	a.logMu.Lock()
	defer a.logMu.Unlock()
	path := app.LogsPath("ios-activation.log")
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, args...))
}

// forgetActivations drops the iPhones that are no longer waiting: unplugged, or
// usable. A step still running is kept: the phone leaves usbmux while it restarts.
func (app *Service) forgetActivations(waiting map[string]bool) {
	a := &app.iosActivation
	a.mu.Lock()
	defer a.mu.Unlock()
	for udid, e := range a.entries {
		if !waiting[udid] && !e.running {
			delete(a.entries, udid)
		}
	}
}

// WaitIOSActivation waits for the steps under way (a short-lived command must not
// end while a phone restarts into Developer Mode), or until c ends.
func (app *Service) WaitIOSActivation(c context.Context) {
	done := make(chan struct{})
	go func() {
		app.iosActivation.running.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-c.Done():
	}
}

// IOSPending is an iPhone plugged in by USB that is not available yet because it is
// being activated; it is neither registered nor listed.
type IOSPending struct {
	Model   string `json:"model,omitempty"`
	Stage   string `json:"stage"`
	Running bool   `json:"running"`
	Message string `json:"message"`
}

// PendingIOS lists the iPhones waiting to be activated, ordered by stage.
func (app *Service) PendingIOS() []IOSPending {
	a := &app.iosActivation
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]IOSPending, 0, len(a.entries))
	for _, e := range a.entries {
		out = append(out, IOSPending{Model: e.model, Stage: e.stage, Running: e.running, Message: app.iosStageMessage(e.stage)})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Stage < out[j].Stage || out[i].Stage == out[j].Stage && out[i].Model < out[j].Model
	})
	return out
}

// IOSActivating reports whether any activation step is under way.
func (app *Service) IOSActivating() bool {
	for _, p := range app.PendingIOS() {
		if p.Running {
			return true
		}
	}
	return false
}

func (app *Service) iosStageMessage(stage string) string {
	switch stage {
	case iosStageTrust:
		return app.L("pulsa «Confiar» en el iPhone e introduce su código", "tap «Trust» on the iPhone and enter its passcode")
	case iosStageDenied:
		return app.L("se rechazó «Confiar»: desconéctalo y vuelve a conectarlo", "«Trust» was declined: unplug it and plug it in again")
	case iosStageUnlock:
		return app.L("desbloquea el iPhone para que pida «Confiar»", "unlock the iPhone so it asks to «Trust»")
	case iosStageEnabling:
		return app.L("activando el modo de desarrollador; el iPhone se reinicia", "turning Developer Mode on; the iPhone restarts")
	case iosStageManual:
		return app.L("cierra Ajustes del todo y ábrelo: Privacidad y seguridad > Modo de desarrollador (al final); actívalo, deja que se reinicie, desbloquéalo y confirma «Activar»", "close Settings completely and open it: Privacy & Security > Developer Mode (at the bottom); turn it on, let it restart, unlock it and confirm «Turn On»")
	}
	return app.L("no se pudo preparar; se vuelve a intentar", "could not be prepared; trying again")
}
