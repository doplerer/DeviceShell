package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRoutinePlatformPreflightAndRetryMetadata(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	app.EnsureLayout()
	android := Device{Serial: "S1", Alias: "DEV-001", State: "device"}
	iphone := Device{Serial: "ios:S1", Alias: "DEV-002", State: "device", Platform: "ios", OSVersion: "27.2"}
	for _, platform := range []string{"", "android", "ios", "both", "bad"} {
		p := filepath.Join(app.Root, "rutina.json")
		raw := fmt.Sprintf(`{"platform":%q,"steps":[{"action":"wait","args":["0"]},{"action":"key","args":["HOME"]}]}`, platform)
		os.WriteFile(p, []byte(raw), 0600)
		steps, e := app.LoadRoutine([]string{p})
		if platform == "bad" {
			if e == nil {
				t.Fatal("invalid platform accepted")
			}
			continue
		}
		if e != nil {
			t.Fatal(e)
		}
		calls := map[string]int{}
		app.ExecuteStep = func(_ context.Context, d Device, _ Spec) (string, error) { calls[d.Serial]++; return "", nil }
		job, e := app.ExecuteJob(context.Background(), []Device{android, iphone}, steps, BatchOptions{Parallel: 1, Timeout: time.Second})
		if e != nil {
			t.Fatal(e)
		}
		want := platform
		if want == "" {
			want = "android"
		}
		for _, d := range []Device{android, iphone} {
			expected := 0
			if want == "both" || want == d.OS() {
				expected = 2
			}
			if calls[d.Serial] != expected {
				t.Fatalf("%s %s: %v", platform, d.OS(), calls)
			}
		}
		summary := app.SummarizeJob(job)
		if summary.Steps[0].RoutinePlatform != want {
			t.Fatal("summary lost platform")
		}
		// In-memory retry uses the same specs and must reject the wrong platform again.
		if want != "both" {
			wrong := iphone
			if want == "ios" {
				wrong = android
			}
			if app.CheckDeviceSteps(wrong, job.Steps) == nil {
				t.Fatal("retry bypass")
			}
		}
	}
}
func TestBothRoutineDoesNotPartiallyExecute(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	app.EnsureLayout()
	calls := 0
	app.ExecuteStep = func(context.Context, Device, Spec) (string, error) { calls++; return "", nil }
	steps := []Spec{{Action: "wait", Args: []string{"0"}, RoutinePlatform: "both"}, {Action: "shell", Args: []string{"anything"}, RoutinePlatform: "both", OnError: "continue", Retry: 2}}
	job, e := app.ExecuteJob(context.Background(), []Device{{Serial: "ios:X", Platform: "ios", State: "device"}}, steps, BatchOptions{Parallel: 1, Timeout: time.Second})
	if e != nil || job.Failed() != 1 || calls != 0 {
		t.Fatal(job, e, calls)
	}
}
func TestIOSDiscoveryIdentityAndMissingBackendIsolation(t *testing.T) {
	app := New()
	app.RunIOS = func(_ context.Context, args ...string) (string, error) {
		if args[0] == "list" {
			return `{"deviceList":["A"]}`, nil
		}
		return `{"ProductType":"iPhone18,2","ProductVersion":"27.0"}`, nil
	}
	ds, e := (iosBackend{app}).Discover(context.Background())
	if e != nil || len(ds) != 1 || ds[0].Serial != "ios:A" || ds[0].OSVersion != "27.0" {
		t.Fatal(ds, e)
	}
	old := []Device{{Serial: "A", Alias: "old", State: "device"}}
	merged, e := app.mergeDiscovered(old, ds, map[string]error{"android": fmt.Errorf("missing adb")}, "now")
	if e != nil || len(merged) != 2 {
		t.Fatal(merged, e)
	}
	for _, d := range merged {
		if d.Serial == "A" && (d.Alias != "old" || d.State != "offline") {
			t.Fatal(d)
		}
	}
	selected, e := app.ResolveTargets(merged, Selection{All: true})
	if e != nil || len(selected) != 1 || selected[0].Serial != "ios:A" {
		t.Fatal(selected, e)
	}
}
func TestIOSCapabilitiesAndLocks(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	app.EnsureLayout()
	defer app.CloseDevices()
	d := Device{Serial: "ios:X", Platform: "ios"}
	for _, s := range []Spec{{Action: "key", Args: []string{"BACK"}}, {Action: "app-install", Args: []string{"a.apk"}}, {Action: "shell", Args: []string{"x"}}, {Action: "reboot", Args: []string{"recovery"}}} {
		if app.CheckDeviceSteps(d, []Spec{s}) == nil {
			t.Fatal(s)
		}
	}
	a, b := &iosDeviceSession{}, &iosDeviceSession{}
	if e := a.acquire(app, d); e != nil {
		t.Fatal(e)
	}
	defer a.close(app)
	if e := b.acquire(app, d); e == nil {
		b.close(app)
		t.Fatal("exclusive device lease missing")
	}
	d.Serial = "ios:Y"
	if e := b.acquire(app, d); e != nil {
		t.Fatal("unrelated device blocked", e)
	}
	b.close(app)
}

func TestIOSAppListAndDisconnectCleanup(t *testing.T) {
	out, e := normalizeIOSApps(`[{"CFBundleIdentifier":"com.example.b"},{"CFBundleIdentifier":"com.example.a"}]`)
	if e != nil || out != "package:com.example.a\npackage:com.example.b" {
		t.Fatal(out, e)
	}
	if _, e := normalizeIOSApps("bad JSON"); e == nil {
		t.Fatal("malformed app list accepted")
	}
	app := New()
	app.Root = t.TempDir()
	app.EnsureLayout()
	defer app.CloseDevices()
	d := Device{Serial: "ios:X", Platform: "ios"}
	s := app.iosSession(d)
	if e := s.acquire(app, d); e != nil {
		t.Fatal(e)
	}
	app.invalidateDisconnectedIOS(nil, map[string]error{"ios": fmt.Errorf("scan failed")})
	if s.lock == nil {
		t.Fatal("failed scan closed active session")
	}
	app.invalidateDisconnectedIOS(nil, nil)
	if s.lock != nil {
		t.Fatal("disconnected session retained lease")
	}
}
