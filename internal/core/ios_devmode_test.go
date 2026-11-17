package core

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestIOSWithoutDeveloperModeIsHiddenAndSkippedByAll(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	app.RunIOS = func(_ context.Context, args ...string) (string, error) {
		switch strings.Join(args, " ") {
		case "list":
			return `{"deviceList":["PHONE","PAD","OLD"]}`, nil
		case "--udid=PHONE info", "--udid=PAD info":
			return `{"ProductType":"iPhone18,2","ProductVersion":"27.2"}`, nil
		case "--udid=OLD info":
			return `{"ProductType":"iPad14,3","ProductVersion":"26.6.2"}`, nil
		case "--udid=PHONE devmode get":
			return `{"DeveloperModeEnabled":true}`, nil
		case "--udid=PAD devmode get":
			return `{"DeveloperModeEnabled":false}`, nil
		case "--udid=PAD devmode enable":
			return "", errDeveloperModeRevealed
		}
		t.Fatal("unexpected", args)
		return "", nil
	}
	ds, e := (iosBackend{app}).Discover(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	app.WaitIOSActivation(context.Background())
	states := map[string]string{}
	for _, d := range ds {
		states[d.NativeID()] = d.State
	}
	// An outdated device is not even asked for Developer Mode.
	if states["PHONE"] != "device" || states["PAD"] != iosDeveloperOff || states["OLD"] != iosOutdated {
		t.Fatal(states)
	}
	if listed := Listed(ds); len(listed) != 1 || listed[0].NativeID() != "PHONE" {
		t.Fatal(listed)
	}
	all, e := app.ResolveTargets(ds, Selection{All: true})
	if e != nil || len(all) != 1 || all[0].NativeID() != "PHONE" {
		t.Fatal(all, e)
	}
	// An explicit choice still reaches the device and explains why it cannot run.
	byID := map[string]Device{}
	for _, d := range ds {
		byID[d.NativeID()] = d
	}
	app.EnsureLayout()
	job, _ := app.ExecuteJob(context.Background(), []Device{byID["PAD"], byID["OLD"]}, []Spec{{Action: "wait", Args: []string{"1"}}}, BatchOptions{Parallel: 1})
	if job.Results[0].OK || !strings.Contains(job.Results[0].Error, "desarrollador") {
		t.Fatal(job.Results[0])
	}
	if job.Results[1].OK || !strings.Contains(job.Results[1].Error, "iOS 27") {
		t.Fatal(job.Results[1])
	}
}

func TestIOSVersionsBefore27AreRejected(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	for version, ok := range map[string]bool{"27.0": true, "27.2": true, "28.1": true, "26.6.2": false, "17.4": false, "": false} {
		if iosSupportedVersion(version) != ok {
			t.Fatal(version)
		}
	}
	old := Device{Serial: "ios:OLD", Platform: "ios", State: "device", OSVersion: "26.6.2"}
	if e := app.CheckDeviceSteps(old, []Spec{{Action: "tap", X: .5, Y: .5}}); e == nil || !strings.Contains(e.Error(), "iOS 27") {
		t.Fatal(e)
	}
	// An outdated device is not available: preparation leaves it alone.
	states, _ := app.prepareIOSTargets(context.Background(), []Device{{Alias: "OLD", Serial: "ios:OLD", Platform: "ios", State: iosOutdated, OSVersion: "26.6.2"}}, nil)
	if len(states) != 0 {
		t.Fatal(states)
	}
}

func TestUnreadableDeveloperModeDoesNotHideDevice(t *testing.T) {
	app := New()
	app.RunIOS = func(context.Context, ...string) (string, error) { return "not json", nil }
	if on, known := app.iosDeveloperMode(context.Background(), "X"); !on || known {
		t.Fatal("transient failure hid a device", on, known)
	}
}

// A phone whose Developer Mode could not be read is listed, but not kept as such:
// the next scan asks again, and finds it off.
func TestUnreadDeveloperModeIsAskedAgain(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	phone := &fakeActivationPhone{id: 2, devmodeErr: errors.New("IsDevModeEnabled: failed connecting to image mounter service")}
	app.RunIOS = phone.run
	if ds := scanAndWait(t, app); len(ds) != 1 || ds[0].State != "device" {
		t.Fatal(ds)
	}
	phone.set(func() { phone.devmodeErr, phone.devmode = nil, `{"DeveloperModeEnabled":false}` })
	if ds := scanAndWait(t, app); len(ds) != 1 || ds[0].State != iosDeveloperOff {
		t.Fatal("unread answer kept", ds)
	}
	if n := phone.count("--udid=NEW devmode get"); n != 2 {
		t.Fatal("devmode get", n)
	}
}

func TestGestureFinishTapOrSwipe(t *testing.T) {
	g := gestureState{x: .5, y: .5, started: time.Now(), active: true}
	if s := g.finish(.5, .5); s.Action != "tap" {
		t.Fatal(s)
	}
	if s := g.finish(.5, .9); s.Action != "swipe" || s.Y2 != .9 {
		t.Fatal(s)
	}
}
