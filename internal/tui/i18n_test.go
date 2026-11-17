package tui

import (
	"os"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/doplerer/deviceshell/internal/core"
)

func (app *Interface) useLanguage(t *testing.T, l string) {
	t.Helper()
	app.Service.Lang = l
	app.RebuildTexts()
	t.Cleanup(func() {
		app.Service.Lang = "es"
		app.RebuildTexts()
	})
}
func (app *Interface) testLanguagePairsAndSaving(t *testing.T) {
	m := app.testModel(t)
	os.WriteFile(app.Service.ConfigPath(), []byte("\ufeff{\"expectedDevices\": 20, \"useCxtKey\": true}"), 0600)
	if app.Service.L("hola", "hello") != "hola" {
		t.Fatal("Spanish is not the default")
	}
	t.Cleanup(func() {
		app.Service.Lang = "es"
		app.RebuildTexts()
	})
	app.setTestLanguage(t, m, "en")
	if app.Service.Lang != "en" || app.Service.L("hola", "hello") != "hello" || !strings.Contains(m.footer(), "shortcuts") {
		t.Fatal("language not switched", app.Service.Lang)
	}
	var config map[string]any
	if e := app.Service.ReadJSON(app.Service.ConfigPath(), &config); e != nil || config["language"] != "en" || config["expectedDevices"] != float64(20) || config["useCxtKey"] != true {
		t.Fatal(config, e)
	}
	app.Service.Lang = "es"
	app.Service.LoadLanguage()
	if app.Service.Lang != "en" {
		t.Fatal("saved language not loaded")
	}
	os.Setenv("DEVICESHELL_LANG", "es")
	app.Service.LoadLanguage()
	os.Unsetenv("DEVICESHELL_LANG")
	if app.Service.Lang != "es" {
		t.Fatal("DEVICESHELL_LANG ignored")
	}
	if e := app.Service.SaveLanguage("fr"); e == nil {
		t.Fatal("unknown language accepted")
	}
}
func (app *Interface) testOldSpanishNamesStillWork(t *testing.T) {
	m := app.testModel(t)
	for line, want := range map[string]string{"/ver @all": "view"} {
		c, e := m.parse(line)
		if e != nil || c.cmd.name != want {
			t.Fatal(line, e)
		}
	}
	m.setInput("/")
	for _, s := range m.sugs {
		for _, old := range []string{"/ver", "/guia", "cerrar", "simulacion", "ninguno", "solo-ver"} {
			if strings.Contains(s.value, old) {
				t.Fatalf("menu shows %s", s.value)
			}
		}
	}
	for _, c := range app.tuiRegistry {
		for _, old := range []string{"/ver", "/guia", "cerrar", "simulacion", "ninguno", "solo-ver", "NOMBRE", "DESTINO", "TEMA"} {
			if strings.Contains(c.usage+" "+c.example, old) {
				t.Fatalf("/%s usage or example in Spanish: %s %s", c.name, c.usage, c.example)
			}
		}
	}
}

var spanishWords = map[string]bool{"placa": true, "placas": true, "conectada": true, "conectadas": true, "pantalla": true, "pantallas": true, "destino": true, "lote": true, "lotes": true, "réplica": true, "rutina": true, "seguidora": true, "seguidoras": true, "principal": true, "guía": true, "mayús": true, "atajos": true, "ahora": true, "todas": true, "para": true, "con": true, "que": true, "los": true, "las": true, "del": true, "una": true, "sin": true, "pasos": true, "grupo": true, "elige": true, "esc cancela": true}

func (app *Interface) spanishLeaks(text string) []string {
	var leaks []string
	words := strings.FieldsFunc(strings.ToLower(ansi.Strip(text)), func(r rune) bool {
		return !unicode.IsLetter(r)
	})
	for _, w := range words {
		if spanishWords[w] {
			leaks = append(leaks, w)
		}
	}
	return leaks
}
func (app *Interface) testEnglishHasNoSpanishLeftovers(t *testing.T) {
	m := app.testModel(t)
	app.useLanguage(t, "en")
	check := func(what, text string) {
		t.Helper()
		if leaks := app.spanishLeaks(text); len(leaks) > 0 {
			t.Errorf("%s has Spanish words %v:\n%s", what, leaks, ansi.Strip(text))
		}
	}
	check("welcome", m.welcomeView())
	app.typeLine(m, "/help")
	check("/help", app.printed(m))
	for _, c := range app.tuiRegistry {
		app.typeLine(m, "/help "+c.name)
		check("/help "+c.name, app.printed(m))
		m.setInput("/" + c.name)
		check("menu /"+c.name, strings.Join(app.frame(m), "\n"))
	}
	m.setInput("/")
	check("menu /", strings.Join(app.frame(m), "\n"))
	m.setInput("?")
	check("shortcuts", strings.Join(app.frame(m), "\n"))
	m.setInput("")
	check("footer", m.footer())
	m.panel = &devicesPanel{app: app, marked: map[string]bool{}}
	check("devices panel", strings.Join(app.frame(m), "\n"))
	m.panel = &devicesPanel{app: app, marked: map[string]bool{}, onPick: func(core.Selection) tea.Cmd {
		return nil
	}}
	check("picker", strings.Join(app.frame(m), "\n"))
	m.panel = nil
	app.typeLine(m, "/motion tap")
	check("assistant", strings.Join(app.frame(m), "\n"))
	app.answer(m, "7")
	check("assistant error", m.form.err)
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	app.typeLine(m, "/routine preparacion mensaje=hi @all")
	check("batch summary", app.printed(m))
	m.task = &taskState{title: "tap", sel: "@all", started: time.Now(), targets: m.devices, steps: make([]core.Spec, 2), state: []int{2, 1, 0}, step: []int{1, 1, 0}, cancel: func() {
	}}
	check("progress", strings.Join(app.frame(m), "\n"))
	m.task = nil
	app.typeLine(m, "/screenshot @all")
	check("screenshot run", app.printed(m))
	check("doctor", m.renderDoctor("Android Debug Bridge version 1.0.41\nVersion 37.0.1", nil, m.devices, nil))
	check("groups", m.renderGroups())
	app.typeLine(m, "/nonexistent")
	check("unknown command", app.printed(m))
}
