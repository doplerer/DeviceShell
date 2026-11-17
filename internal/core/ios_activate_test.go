package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeActivationPhone answers go-ios for one USB iPhone whose id, answers and
// recorded calls the test changes between scans.
type fakeActivationPhone struct {
	mu         sync.Mutex
	id         int
	info       error  // answer to info (nil: a supported, trusted phone)
	devmode    string // answer to devmode get
	devmodeErr error
	pair       error
	enable     func() error
	calls      map[string]int
}

func (f *fakeActivationPhone) run(_ context.Context, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	cmd := strings.Join(args, " ")
	f.calls[cmd]++
	switch cmd {
	case "list":
		return fmt.Sprintf(`{"deviceList":["NEW"],"deviceIDs":[%d]}`, f.id), nil
	case "--udid=NEW info":
		if f.info != nil {
			return "", f.info
		}
		return `{"ProductType":"iPhone18,2","ProductVersion":"27.2"}`, nil
	case "--udid=NEW devmode get":
		return f.devmode, f.devmodeErr
	case "--udid=NEW devmode reveal":
		return "", nil
	case "--udid=NEW pair":
		if f.pair != nil {
			return "", f.pair
		}
		return `{"paired":true}`, nil
	case "--udid=NEW devmode enable":
		if f.enable != nil {
			return "", f.enable()
		}
		return "", nil
	}
	return "", errors.New("unexpected " + cmd)
}

func (f *fakeActivationPhone) count(cmd string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[cmd]
}

func (f *fakeActivationPhone) set(change func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	change()
}

func scanAndWait(t *testing.T, app *Service) []Device {
	t.Helper()
	ds, e := (iosBackend{app}).Discover(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	app.WaitIOSActivation(context.Background())
	return ds
}

// dueNow lets the next scan retry a step whose pause has not passed yet.
func dueNow(app *Service, udid string) {
	app.iosActivation.mu.Lock()
	defer app.iosActivation.mu.Unlock()
	if e := app.iosActivation.entries[udid]; e != nil && !e.next.IsZero() {
		e.next = time.Now().Add(-time.Second)
	}
}

func TestUntrustedIPhoneIsAskedToTrustOncePerPrompt(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	phone := &fakeActivationPhone{id: 7, info: errors.New("lockdown: InvalidHostID"), pair: errors.New("Please accept the PairingDialog on the device and run pairing again!")}
	app.RunIOS = phone.run
	scanAndWait(t, app)
	if n := phone.count("--udid=NEW pair"); n != 1 {
		t.Fatal("pair", n)
	}
	if p := app.PendingIOS(); len(p) != 1 || p[0].Stage != iosStageTrust || !strings.Contains(p[0].Message, "Confiar") {
		t.Fatal(p)
	}
	// While the prompt is open it is not asked on every scan, only after the pause.
	scanAndWait(t, app)
	if n := phone.count("--udid=NEW pair"); n != 1 {
		t.Fatal("pair again at once", n)
	}
	dueNow(app, "NEW")
	phone.set(func() { phone.pair = errors.New("Lockdown error: UserDeniedPairing") })
	scanAndWait(t, app)
	if n := phone.count("--udid=NEW pair"); n != 2 || app.PendingIOS()[0].Stage != iosStageDenied {
		t.Fatal("denied", n, app.PendingIOS())
	}
	// Declined: not asked again until it is plugged in again.
	dueNow(app, "NEW")
	scanAndWait(t, app)
	if n := phone.count("--udid=NEW pair"); n != 2 {
		t.Fatal("asked after a no", n)
	}
	phone.set(func() { phone.id = 8 })
	scanAndWait(t, app)
	if n := phone.count("--udid=NEW pair"); n != 3 {
		t.Fatal("not asked after replugging", n)
	}
	// Trusted: the phone is read again and goes on to Developer Mode.
	phone.set(func() {
		phone.info, phone.pair, phone.devmode = nil, nil, `{"DeveloperModeEnabled":true}`
	})
	dueNow(app, "NEW")
	scanAndWait(t, app)
	ds := scanAndWait(t, app)
	if len(ds) != 1 || ds[0].State != "device" || len(app.PendingIOS()) != 0 {
		t.Fatal(ds, app.PendingIOS())
	}
}

func TestDeveloperModeIsTurnedOnOncePerConnection(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	phone := &fakeActivationPhone{id: 3, devmode: `{"DeveloperModeEnabled":false}`}
	// With a passcode only the toggle can be shown.
	phone.enable = func() error { return fmt.Errorf("%w: passcode set", errDeveloperModeRevealed) }
	app.RunIOS = phone.run
	ds := scanAndWait(t, app)
	if len(ds) != 1 || ds[0].State != iosDeveloperOff || len(Listed(ds)) != 0 {
		t.Fatal(ds)
	}
	p := app.PendingIOS()
	if phone.count("--udid=NEW devmode enable") != 1 || len(p) != 1 || p[0].Stage != iosStageManual || p[0].Model != "iPhone18,2" || !strings.Contains(p[0].Message, "Modo de desarrollador") {
		t.Fatal(p)
	}
	scanAndWait(t, app)
	if n := phone.count("--udid=NEW devmode enable"); n != 1 || phone.count("--udid=NEW devmode reveal") != 0 {
		t.Fatal("revealed again at once", n)
	}
	// After the pause the toggle is revealed again (the phone may have been locked,
	// or Settings open), without trying to switch it on again.
	dueNow(app, "NEW")
	scanAndWait(t, app)
	if n := phone.count("--udid=NEW devmode enable"); n != 1 || phone.count("--udid=NEW devmode reveal") != 1 || app.PendingIOS()[0].Stage != iosStageManual {
		t.Fatal("reveal", n, phone.count("--udid=NEW devmode reveal"), app.PendingIOS())
	}
	// Every step is in the activation log, with what the phone answered.
	raw, e := os.ReadFile(app.LogsPath("ios-activation.log"))
	if e != nil || !strings.Contains(string(raw), "NEW (developer-off): devmode enable: developer mode revealed in Settings: passcode set -> manual") || !strings.Contains(string(raw), "devmode reveal: ok -> manual") {
		t.Fatal(string(raw), e)
	}
	// Without a passcode it restarts (another usbmux id) into Developer Mode: the
	// restart itself does not start it again.
	phone.set(func() {
		phone.id = 4
		phone.enable = func() error { phone.id = 5; return nil }
	})
	scanAndWait(t, app)
	scanAndWait(t, app)
	if n := phone.count("--udid=NEW devmode enable"); n != 2 || app.PendingIOS()[0].Stage != iosStageEnabling {
		t.Fatal("enable", n, app.PendingIOS())
	}
	// Confirmed: read again after iosInfoRetry, as any phone found without it.
	phone.set(func() { phone.devmode = `{"DeveloperModeEnabled":true}` })
	app.iosInfo.forget("NEW")
	ds = scanAndWait(t, app)
	if len(Listed(ds)) != 1 || len(app.PendingIOS()) != 0 {
		t.Fatal(ds, app.PendingIOS())
	}
	// Unplugged while waiting: forgotten.
	app.activateIOS("GONE", 1, iosDeveloperOff, "")
	app.WaitIOSActivation(context.Background())
	app.forgetActivations(map[string]bool{})
	if len(app.PendingIOS()) != 0 {
		t.Fatal(app.PendingIOS())
	}
}

func TestIPhonesAwaitingActivationAreNotRegistered(t *testing.T) {
	app := New()
	old := []Device{{Serial: "ios:KNOWN", Alias: "DEV-001", Platform: "ios"}}
	live := []Device{
		{Serial: "ios:KNOWN", Platform: "ios", State: iosDeveloperOff, Transport: "usb"},
		{Serial: "ios:UNTRUSTED", Platform: "ios", State: "unauthorized", Transport: "usb"},
		{Serial: "ios:OFF", Platform: "ios", State: iosDeveloperOff, Transport: "usb"},
		{Serial: "ANDROID", Platform: "android", State: "unauthorized"},
	}
	merged, e := app.MergeInventory(old, live, "now")
	if e != nil {
		t.Fatal(e)
	}
	got := map[string]Device{}
	for _, d := range merged {
		got[d.Serial] = d
	}
	// A known iPhone keeps its alias (and stays out of the lists); a new one waits.
	if len(merged) != 2 || got["ios:KNOWN"].Alias != "DEV-001" || got["ios:KNOWN"].State != iosDeveloperOff || got["ANDROID"].Alias != "DEV-002" {
		t.Fatal(merged)
	}
	live[2].State = "device"
	merged, _ = app.MergeInventory(merged, live, "later")
	for _, d := range merged {
		if d.Serial == "ios:OFF" && d.Alias != "DEV-003" {
			t.Fatal(d)
		}
	}
	if len(merged) != 3 {
		t.Fatal(merged)
	}
}
