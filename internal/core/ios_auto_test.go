package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAutomaticIOSResumeState(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	d := Device{Alias: "DEV-018", Serial: "ios:A", Platform: "ios"}
	progress := app.setupProgress(d, "developer-mode", "Activa el modo de desarrollador", true, false, nil)
	var loaded IOSSetupProgress
	if e := app.ReadJSON(app.IOSToolsPath("setup", "A.json"), &loaded); e != nil || loaded.Stage != progress.Stage || !loaded.NeedsUser || loaded.Ready {
		t.Fatal(loaded, e)
	}
	if app.IOSSetupSummary([]Device{{Alias: "DEV-018", Serial: "ios:A", State: "device", Platform: "ios"}}) == "" {
		t.Fatal("resume state missing")
	}
	// A device that is not available is explained by its state, not by an old preparation.
	if app.IOSSetupSummary([]Device{{Alias: "DEV-018", Serial: "ios:A", State: iosLocked, Platform: "ios"}}) != "" {
		t.Fatal("stale preparation of an unavailable device shown")
	}
}

func TestAutomaticPreparationCancellationDoesNotInstall(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	c, cancel := context.WithCancel(context.Background())
	cancel()
	app.AutoPrepareIOS(c, []Device{{Alias: "DEV-018", Serial: "ios:A", State: "device", Platform: "ios"}}, nil)
	if _, e := os.Stat(app.ToolsPath()); !os.IsNotExist(e) {
		t.Fatal("cancelled preparation wrote installation files")
	}
}

func TestAutomaticPreparationRunsOncePerConnection(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	if e := os.MkdirAll(app.DataPath(), 0700); e != nil {
		t.Fatal(e)
	}
	fake := &fakeConnector{caps: []string{"tap"}}
	starts := 0
	app.RunIOS = func(_ context.Context, args ...string) (string, error) {
		return `{"DeveloperModeEnabled":true}`, nil
	}
	app.StartIOSConnector = func(context.Context, Device) (iosConnector, error) {
		starts++
		return fake, nil
	}
	d := Device{Alias: "DEV-019", Serial: "ios:U", State: "device", Platform: "ios"}
	var notified []IOSSetupProgress
	notify := func(p IOSSetupProgress) { notified = append(notified, p) }
	if states := app.AutoPrepareIOS(context.Background(), []Device{d}, notify); !states[d.Alias].Ready || starts != 1 {
		t.Fatal(states, starts)
	}
	notified = nil
	if states := app.AutoPrepareIOS(context.Background(), []Device{d}, notify); len(states) != 0 || len(notified) != 0 || starts != 1 {
		t.Fatal("prepared again on the next refresh", states, notified)
	}
	// Unplugged, then plugged back: prepared again.
	app.AutoPrepareIOS(context.Background(), []Device{{Alias: d.Alias, Serial: d.Serial, State: "disconnected", Platform: "ios"}}, notify)
	if states := app.AutoPrepareIOS(context.Background(), []Device{d}, notify); !states[d.Alias].Ready || starts != 2 {
		t.Fatal(states, starts)
	}
}

// Devices that are not available to the farm are never touched: no lockdown
// command, no connector, nothing opened on the device.
func TestAutomaticPreparationLeavesUnavailableDevicesAlone(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	app.RunIOS = func(_ context.Context, args ...string) (string, error) {
		t.Fatal("touched an unavailable device", args)
		return "", nil
	}
	app.StartIOSConnector = func(context.Context, Device) (iosConnector, error) {
		t.Fatal("connector started on an unavailable device")
		return nil, nil
	}
	for _, state := range []string{iosLocked, iosDeveloperOff, iosOutdated, "unauthorized", "offline"} {
		d := Device{Alias: "DEV-018", Serial: "ios:A", State: state, Platform: "ios"}
		for range 2 {
			if states := app.AutoPrepareIOS(context.Background(), []Device{d}, nil); len(states) != 0 {
				t.Fatal(state, states)
			}
		}
	}
}

// A device listed as available that turns out locked is marked and leaves the
// lists; preparation does not retry it, discovery reads it again later.
func TestAutomaticPreparationMarksLockedDevice(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	if e := os.MkdirAll(app.DataPath(), 0700); e != nil {
		t.Fatal(e)
	}
	app.RunIOS = func(context.Context, ...string) (string, error) { return `{"DeveloperModeEnabled":true}`, nil }
	starts := 0
	app.StartIOSConnector = func(context.Context, Device) (iosConnector, error) {
		starts++
		return nil, errIOSLocked
	}
	d := Device{Alias: "DEV-019", Serial: "ios:U", State: "device", Platform: "ios"}
	if states := app.AutoPrepareIOS(context.Background(), []Device{d}, nil); states[d.Alias].Stage != "unlock" || !app.iosIsLocked(d.Serial) {
		t.Fatal(states)
	}
	d.State = iosLocked // what discovery now reports
	app.iosAuto[d.Serial] = iosAutoState{"device", "unlock", time.Now().Add(-iosAutoRetry)}
	app.AutoPrepareIOS(context.Background(), []Device{d}, nil)
	if starts != 1 || !app.iosIsLocked(d.Serial) {
		t.Fatal("a locked device was retried or forgotten while still attached", starts)
	}
}

// Developer Mode found off on a listed device: nothing is opened on it.
func TestAutomaticPreparationDoesNotRevealDeveloperMode(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	var calls []string
	app.RunIOS = func(_ context.Context, args ...string) (string, error) {
		call := strings.Join(args, " ")
		calls = append(calls, call)
		if call == "--udid=A devmode get" {
			return `{"DeveloperModeEnabled":false}`, nil
		}
		t.Fatalf("unexpected command: %s", call)
		return "", nil
	}
	app.StartIOSConnector = func(context.Context, Device) (iosConnector, error) {
		t.Fatal("connector started without developer mode")
		return nil, nil
	}
	d := Device{Alias: "DEV-018", Serial: "ios:A", State: "device", Platform: "ios"}
	states, e := app.prepareIOSTargets(context.Background(), []Device{d}, nil)
	if e != nil || states[d.Alias].Stage != "developer-mode" || len(calls) != 1 {
		t.Fatal(states, calls, e)
	}
}

func TestAutomaticPreparationSkipsWhileAnotherRuns(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	app.RunIOS = func(context.Context, ...string) (string, error) {
		t.Fatal("ran while another preparation held the device")
		return "", nil
	}
	app.iosSetupMu.Lock()
	defer app.iosSetupMu.Unlock()
	if states := app.AutoPrepareIOS(context.Background(), []Device{{Alias: "DEV-018", Serial: "ios:A", State: iosDeveloperOff, Platform: "ios"}}, nil); states != nil {
		t.Fatal(states)
	}
}

func TestIOSPreparationSkipsAndroidAndUnavailable(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	app.RunIOS = func(_ context.Context, args ...string) (string, error) {
		t.Fatal("touched", args)
		return "", nil
	}
	states, e := app.prepareIOSTargets(context.Background(), []Device{
		{Alias: "ANDROID", Serial: "A", State: "device"},
		{Alias: "IPAD", Serial: "ios:B", State: "unauthorized", Platform: "ios"},
	}, nil)
	if e != nil || len(states) != 0 {
		t.Fatal(states, e)
	}
}

func TestIOSOptionsDoNotOverwriteBrokenConfig(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	path := app.iosConfigPath()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	original := []byte(`{"connector":`)
	if e := os.WriteFile(path, original, 0600); e != nil {
		t.Fatal(e)
	}
	if e := app.updateIOSOptions(func(*iosOptions) {}); e == nil {
		t.Fatal("invalid configuration was accepted")
	}
	if actual, _ := os.ReadFile(path); !bytes.Equal(actual, original) {
		t.Fatal("invalid configuration was overwritten")
	}
}

// fakeConnector stands in for the iPhone engine and records the actions it receives.
type fakeConnector struct {
	mu    sync.Mutex
	calls []string
	caps  []string
}

func (f *fakeConnector) alive() bool { return true }
func (f *fakeConnector) close()      {}
func (f *fakeConnector) info() iosStatus {
	return iosStatus{IOS: "27.2", Capabilities: f.caps, Video: "waiting"}
}
func (f *fakeConnector) supports(s Spec) bool { return slices.Contains(f.caps, iosCapability(s)) }
func (f *fakeConnector) do(_ context.Context, _ *Service, _ Device, s Spec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("%s %v %v %v", s.Action, s.Args, s.X, s.Y))
	return "", nil
}
func (f *fakeConnector) liveTouch(context.Context, string, float64, float64) error { return nil }
func (f *fakeConnector) wedged(context.Context) bool                               { return false }

func TestIOSActionsGoThroughConnector(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	if e := os.MkdirAll(app.DataPath(), 0700); e != nil {
		t.Fatal(e)
	}
	fake := &fakeConnector{caps: []string{"tap", "swipe", "text", "key:HOME", "screenshot", "app-start"}}
	app.StartIOSConnector = func(context.Context, Device) (iosConnector, error) { return fake, nil }
	d := Device{Alias: "DEV-019", Serial: "ios:U", State: "device", Platform: "ios", OSVersion: "27.2"}
	for _, s := range []Spec{{Action: "tap", X: 0.25, Y: 0.5}, {Action: "key", Args: []string{"home"}}, {Action: "text", Args: []string{"Año"}}} {
		if _, e := app.Execute(context.Background(), d, s); e != nil {
			t.Fatal(s, e)
		}
	}
	defer app.CloseDevices()
	want := []string{"tap [] 0.25 0.5", "key [home] 0 0", "text [Año] 0 0"}
	fake.mu.Lock()
	got := fake.calls
	fake.mu.Unlock()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatal(got)
	}
	// Capabilities the device lacks are refused before reaching it, also after restart.
	if _, e := app.Execute(context.Background(), d, Spec{Action: "app-stop", Args: []string{"com.apple.mobilesafari"}}); e == nil || !strings.Contains(e.Error(), "iOS 27.2") {
		t.Fatal(e)
	}
	if e := app.CheckDeviceSteps(d, []Spec{{Action: "url", Args: []string{"https://example.com"}}}); e == nil {
		t.Fatal("cached capabilities ignored")
	}
}

func TestIOSConnectErrorsAndReadyMessage(t *testing.T) {
	for raw, want := range map[string]string{
		"lockdown: PasswordProtected":  "unlock the device and try again",
		"DeveloperModeDisabled":        "turn on Developer Mode on the device",
		"device with udid U not found": "the device is not connected by USB",
		"something else":               "something else",
	} {
		if got := iosConnectError(errors.New(raw)).Error(); got != want {
			t.Fatal(raw, got)
		}
	}
	if m := New().iosReadyMessage(iosStatus{IOS: "27.2", Capabilities: []string{"tap"}, Video: "waiting", VideoError: "x."}); !strings.Contains(m, "no se ha iniciado: x;") {
		t.Fatal(m)
	}
}
