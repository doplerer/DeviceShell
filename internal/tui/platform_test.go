package tui

import (
	"strings"
	"testing"

	"github.com/doplerer/deviceshell/internal/core"
)

func TestSharedAppCommandsAndRoutinePlatformSuggestion(t *testing.T) {
	app := newTestRuntime()
	m := app.testModel(t)
	for _, line := range []string{`/app install "C:\ruta con espacios\test.ipa" @all`, `/app uninstall com.example.app @pruebas`} {
		c, e := m.parse(line)
		if e != nil || !strings.HasPrefix(c.cmd.name, "app ") {
			t.Fatal(c, e)
		}
		ps, dest := app.commandParams(m, c.cmd.name, nil)
		if len(ps) != 1 || dest == nil {
			t.Fatal("missing wizard", c.cmd.name)
		}
	}
	p := app.Service.RoutinesPath("iphone.json")
	if e := app.Service.WriteJSON(p, map[string]any{"platform": "ios", "steps": []any{}}); e != nil {
		t.Fatal(e)
	}
	found := false
	for _, s := range app.routineSuggestions() {
		if s.value == "iphone" {
			found = true
			if !strings.Contains(s.detail, "[ios]") {
				t.Fatal(s)
			}
		}
	}
	if !found {
		t.Fatal("routine missing")
	}
}

func TestIPhoneHasNoSetupCommand(t *testing.T) {
	app := newTestRuntime()
	m := app.testModel(t)
	if _, e := m.parse("/ios setup @DEV-001"); e == nil {
		t.Fatal("/ios still exists")
	}
}

func TestConnectedIPhoneIsPreparedAutomatically(t *testing.T) {
	app := newTestRuntime()
	m := app.testModel(t)
	var prepared []core.Device
	app.tuiPrepareIOS = func(d []core.Device, notify func(core.IOSSetupProgress)) {
		prepared = d
		if notify != nil {
			t.Fatal("the background preparation must not report into the transcript")
		}
	}
	iphone := core.Device{Serial: "ios:U", Alias: "DEV-019", Platform: "ios", State: "device"}
	ipad := core.Device{Serial: "ios:P", Alias: "DEV-018", Platform: "ios", State: "locked"}
	cmd := app.upd(m, devicesMsg{d: []core.Device{iphone, ipad}})
	if cmd == nil {
		t.Fatal("no preparation started")
	}
	if _, ok := cmd().(iosAutoDone); !ok || len(prepared) != 2 {
		t.Fatal(prepared)
	}
	if app.upd(m, devicesMsg{d: []core.Device{iphone}}) != nil {
		t.Fatal("second preparation while the first runs")
	}
	app.upd(m, iosAutoDone{})
	// Nothing is printed, so the main menu stays on screen.
	if len(m.transcript) != 0 || !m.showWelcome() {
		t.Fatal("background preparation hid the main menu", m.transcript)
	}
	if len(m.devices) != 1 || m.devices[0].Alias != "DEV-019" {
		t.Fatal("the locked iPad must not be listed", m.devices)
	}
	if app.upd(m, devicesMsg{d: []core.Device{{Serial: "S1", Alias: "DEV-001", State: "device"}}}) != nil {
		t.Fatal("preparation started without an iPhone")
	}
}

// /doctor is the one place that names the devices left out of the lists.
func TestDoctorNamesUnavailableDevices(t *testing.T) {
	app := newTestRuntime()
	m := app.testModel(t)
	out := m.renderDoctor("Android Debug Bridge version 1.0.41", nil, []core.Device{
		{Serial: "S1", Alias: "DEV-001", State: "device"},
		{Serial: "ios:P", Alias: "DEV-018", State: "locked", Platform: "ios"},
		{Serial: "S17", Alias: "DEV-017", State: "disconnected"},
	}, nil)
	if !strings.Contains(out, "No disponibles: DEV-018 bloqueado · DEV-017 desconectada") {
		t.Fatal(out)
	}
}
