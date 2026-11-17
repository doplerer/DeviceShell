package tui

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/doplerer/deviceshell/internal/core"
)

func (app *Interface) testHealthAlertsShown(t *testing.T) {
	m := app.testModel(t)
	app.tuiHealth = func([]core.Device, int) map[string]core.Health {
		return map[string]core.Health{"S1": {Battery: 80, TempC: 47, Charging: true}, "S2": {Battery: 9, TempC: 30}}
	}
	app.typeLine(m, "/doctor")
	if out := ansi.Strip(app.printed(m)); !strings.Contains(out, "! DEV-001 · 47 °C") || !strings.Contains(out, "! DEV-002 · batería 9 %") || !strings.Contains(out, "2 de 2 con avisos") {
		t.Fatal(out)
	}
	if f := ansi.Strip(m.footer()); !strings.Contains(f, "! 2 avisos") {
		t.Fatal(f)
	}
	if m.recommendation != "/doctor" {
		t.Fatal("no recommendation:", m.recommendation)
	}
	app.typeLine(m, "/devices")
	if v := strings.Join(app.frame(m), "\n"); !strings.Contains(v, "80%⚡") || !strings.Contains(v, "!47°") || !strings.Contains(v, "!9%") {
		t.Fatal(v)
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	m.alertTemp, m.alertBattery = 0, 0
	if m.alertCount() != 0 || strings.Contains(ansi.Strip(m.footer()), "aviso") {
		t.Fatal("alerts with the limits off")
	}
}

func (app *Interface) testAlertSettings(t *testing.T) {
	m := app.testModel(t)
	app.typeLine(m, "/settings")
	p, ok := m.panel.(*settingsPanel)
	if !ok {
		t.Fatal("no settings panel")
	}
	for range 3 {
		app.upd(m, tea.KeyMsg{Type: tea.KeyUp})
	}
	if p.cursor != 6 || !strings.Contains(strings.Join(app.frame(m), "\n"), "Alertas") {
		t.Fatal("alerts section missing", p.cursor)
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	p.editor.SetValue("95")
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if p.err == "" {
		t.Fatal("95 °C accepted")
	}
	p.editor.SetValue("38")
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	c, _, e := app.Service.ReadSettings()
	if e != nil || c.AlertTemp != 38 || m.alertTemp != 38 || c.AlertBattery != 15 {
		t.Fatal(c.AlertTemp, m.alertTemp, e)
	}
}

func (app *Interface) testRetryMarks(t *testing.T) {
	m := app.testModel(t)
	j := core.Job{ID: "20261029-120000.000000002", Steps: []core.Spec{{Action: "tap"}}, Results: []core.Result{{Alias: "DEV-001", OK: true, Retries: 2}, {Alias: "DEV-002", OK: true, Warnings: 1, Output: "! paso 1 (tap): no screen\n"}}}
	if out := ansi.Strip(m.renderJob("tap", "@all", j, 0, true, false)); !strings.Contains(out, "↻2") || !strings.Contains(out, "!1") || !strings.Contains(out, "! paso 1 (tap): no screen") {
		t.Fatal(out)
	}
	m.task = &taskState{title: "tap", started: time.Now(), targets: []core.Device{{Alias: "DEV-001"}}, steps: j.Steps, state: []int{1}, step: []int{0}, retry: []int{2}}
	if v := ansi.Strip(m.taskView()); !strings.Contains(v, "DEV-001 paso 1/1 ↻2") {
		t.Fatal(v)
	}
	m.task = nil
}

func (app *Interface) testSystemCommands(t *testing.T) {
	m := app.testModel(t)
	var got []core.Spec
	fake := app.tuiRunJob
	app.tuiRunJob = func(c context.Context, target []core.Device, steps []core.Spec, o core.BatchOptions) (core.Job, error) {
		got = append(got, steps...)
		return fake(c, target, steps, o)
	}
	for line, want := range map[string]string{
		"/reboot @all": "reboot system",
		"/perm grant com.x android.permission.CAMERA @all": "perm-grant com.x android.permission.CAMERA",
		"/wipe com.x @all":                                 "app-clear com.x",
		"/uninstall com.x @all":                            "apk-uninstall com.x",
		"/android-setting system screen_brightness 0 @all": "setting system screen_brightness 0",
		"/logcat @DEV-001":                                 "logcat 500",
		"/record 10 @DEV-001":                              "screenrecord 10",
		"/motion key POWER @all":                           "key POWER",
	} {
		got = nil
		app.typeLine(m, line)
		if len(got) != 1 || got[0].Action+" "+strings.Join(got[0].Args, " ") != want {
			t.Errorf("%s: %+v · %s", line, got, ansi.Strip(app.printed(m)))
		}
	}
	got = nil
	app.typeLine(m, "/wifi maybe @all")
	if len(got) != 0 || !strings.Contains(app.printed(m), "on, off") {
		t.Fatal("invalid Wi-Fi state ran")
	}
	// /clear keeps clearing the screen: the new commands share no prefix with existing ones.
	app.typeLine(m, "/clear")
	if m.form != nil || len(m.transcript) != 0 {
		t.Fatal("/clear opened another command")
	}
	// Built-in variables are filled in by each device, so the assistant never asks for them.
	os.WriteFile(app.Service.RoutinesPath("por-placa.json"), []byte(`{"steps":[{"action":"text","args":["${saludo} ${alias} ${index}"]}]}`), 0600)
	if v := app.routineVars("por-placa"); len(v) != 1 || v[0] != "saludo" {
		t.Fatal(v)
	}
}
