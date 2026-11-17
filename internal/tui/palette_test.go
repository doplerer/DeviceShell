package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doplerer/deviceshell/internal/core"
)

func (app *Interface) values(s []suggestion) map[string]suggestion {
	out := map[string]suggestion{}
	for _, x := range s {
		out[x.value] = x
	}
	return out
}
func (app *Interface) testPaletteListsActions(t *testing.T) {
	m := app.testModel(t)
	m.setInput("/")
	if len(m.sugs) != len(app.tuiRegistry) {
		t.Fatalf("/ lists %d entries", len(m.sugs))
	}
	for _, s := range m.sugs {
		if s.indent {
			t.Fatalf("ready-made action %s under plain /", s.value)
		}
	}
	for typed, wants := range map[string][]string{"/view": {"/view @all", "/view @all --read-only"}, "/vi": {"/view @all"}, "/motion key": {"/motion key HOME", "/motion key POWER"}, "/motion swipe": {"/motion swipe 0.5 0.8 0.5 0.2"}, "/shell": {"!getprop ro.product.model"}, "/routine": {"/routine preparacion"}, "/group": {"/group create"}, "/stop": {"/stop com.android.chrome"}, "/captura": {"/screenshot", "/screenshot @all"}} {
		m.setInput(typed)
		v := app.values(m.sugs)
		for _, want := range wants {
			if _, ok := v[want]; !ok {
				t.Fatalf("%s does not show %s: %v", typed, want, m.sugs)
			}
		}
	}
	m.setInput("/s")
	for _, s := range m.sugs {
		if s.indent {
			t.Fatal("/s unfolds actions of many commands")
		}
	}
	for _, unwanted := range []string{"/view close", "/sync pause"} {
		m.setInput(strings.Fields(unwanted)[0])
		if _, ok := app.values(m.sugs)[unwanted]; ok {
			t.Fatalf("%s offered without reason", unwanted)
		}
	}
}
func (app *Interface) testPaletteContextWithoutSections(t *testing.T) {
	m := app.testModel(t)
	done := make(chan struct{})
	close(done)
	m.wall = &screenWall{Devices: m.devices[:2], Layout: []core.Rect{{X: 1, Y: 31, W: 200, H: 434}}, cancel: func() {
	}, done: done, ports: map[string]int{}}
	m.wall.alive.Store(2)
	m.sync = &syncState{s: &core.LiveSession{Errors: map[string]string{}}, master: m.devices[0]}
	m.target = core.Selection{Group: "pruebas"}
	m.lastFailed = []string{"DEV-003"}
	m.setInput("/")
	if len(m.sugs) != len(app.tuiRegistry) {
		t.Fatalf("/ should contain only commands: %v", m.sugs)
	}
	for i, c := range app.tuiRegistry {
		if m.sugs[i].value != "/"+c.name {
			t.Fatalf("unexpected contextual action in /: %v", m.sugs)
		}
	}
	view := strings.Join(app.frame(m), "\n")
	if strings.Contains(view, "Ahora · lo que tiene sentido") || strings.Contains(view, "  Todo\n") {
		t.Fatal("contextual section still shown")
	}
	m.setInput("/screenshot ")
	failed := false
	for _, s := range m.sugs {
		failed = failed || (s.label == "@fallidas" && s.value == "@DEV-003" && strings.Contains(s.detail, "fallaron"))
	}
	if !failed {
		t.Fatal(m.sugs)
	}
	m.setInput("/view ")
	if len(m.sugs) == 0 || m.sugs[0].value != "close" {
		t.Fatal(m.sugs)
	}
	m.sync = nil
	m.setInput("/view")
	for i, s := range m.sugs {
		if s.value == "/view close" {
			m.sugIdx = i
		}
	}
	app.run(m, app.upd(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if !strings.Contains(app.printed(m), "/view close") {
		t.Fatal("/view close not run")
	}
}
func (app *Interface) testPaletteEnterOpensAssistant(t *testing.T) {
	m := app.testModel(t)
	m.setInput("/motion key HO")
	for i, s := range m.sugs {
		if s.value == "/motion key HOME" {
			m.sugIdx = i
		}
	}
	app.run(m, app.upd(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if _, dest := m.formStep(); m.form == nil || dest == nil || m.form.values[0] != "HOME" {
		t.Fatal("/motion key HOME did not ask for the devices")
	}
}
func (app *Interface) testSearchByMeaning(t *testing.T) {
	m := app.testModel(t)
	for typed, want := range map[string]string{"/captura": "/screenshot", "/instalar": "/apk", "/replicar": "/sync", "/deslizar": "/motion swipe", "/salir": "/exit"} {
		m.setInput(typed)
		if _, ok := app.values(m.sugs)[want]; !ok {
			t.Fatalf("%s does not find %s: %v", typed, want, m.sugs)
		}
	}
	m.setInput("/teléfonos")
	if _, ok := app.values(m.sugs)["/devices"]; !ok {
		t.Fatal("accents not ignored")
	}
	m.setInput("/start chrome")
	if _, ok := app.values(m.sugs)["com.android.chrome"]; !ok {
		t.Fatal(m.sugs)
	}
}
func (app *Interface) testDeeperArgumentSuggestions(t *testing.T) {
	m := app.testModel(t)
	m.setInput("/view ")
	v := app.values(m.sugs)
	if _, ok := v["--read-only"]; !ok {
		t.Fatal("flag not offered", m.sugs)
	}
	if _, ok := v["@all"]; !ok {
		t.Fatal("devices not offered")
	}
	m.setInput("/view --")
	if len(m.sugs) != 1 || m.sugs[0].value != "--read-only" {
		t.Fatal(m.sugs)
	}
	m.setInput("/motion key ")
	if _, ok := app.values(m.sugs)["POWER"]; !ok {
		t.Fatal("power key missing")
	}
	m.setInput("!")
	if len(m.sugs) == 0 || m.sugs[0].value != "getprop ro.product.model" {
		t.Fatal(m.sugs)
	}
	m.setInput("!dum")
	if len(m.sugs) != 1 || m.sugs[0].value != "dumpsys battery" {
		t.Fatal(m.sugs)
	}
	m.applySuggestion(m.sugs[0])
	if m.input.Value() != "!dumpsys battery " {
		t.Fatalf("%q", m.input.Value())
	}
	m.setInput("/sync start DEV-001 --views master ")
	if _, ok := app.values(m.sugs)["@all"]; !ok {
		t.Fatal(m.sugs)
	}
}
