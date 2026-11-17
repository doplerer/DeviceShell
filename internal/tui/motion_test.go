package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func (app *Interface) testMotionMenuAndRetiredCommands(t *testing.T) {
	m := app.testModel(t)
	m.setInput("/")
	v := app.values(m.sugs)
	if _, ok := v["/motion"]; !ok {
		t.Fatal("motion missing")
	}
	for _, name := range []string{"tap", "swipe", "key", "text"} {
		if _, ok := v["/"+name]; ok || app.lookupCommand(name) != nil {
			t.Fatal("retired command still registered", name)
		}
		if _, err := m.parse("/" + name); err == nil || !strings.Contains(err.Error(), "/motion "+name) {
			t.Fatal(name, err)
		}
	}
	m.setInput("/motion")
	seen := map[string]bool{}
	for _, s := range m.sugs {
		if seen[s.value] {
			t.Fatal("duplicate suggestion", s.value)
		}
		seen[s.value] = true
	}
	for _, name := range []string{"tap", "swipe", "key", "text"} {
		if !seen["/motion "+name] {
			t.Fatal("missing child", name)
		}
	}
	m.setInput("/deslizar")
	if _, ok := app.values(m.sugs)["/motion swipe"]; !ok {
		t.Fatal(m.sugs)
	}
	if c, err := m.parse("/motion nonexistent @all"); err == nil || c.cmd != nil {
		t.Fatal("unknown action accepted")
	}
}
func (app *Interface) testMotionChooserAndBack(t *testing.T) {
	m := app.testModel(t)
	app.typeLine(m, "/motion @pruebas")
	if m.form == nil || len(m.sugs) != 4 {
		t.Fatal("chooser not opened", m.sugs)
	}
	app.answer(m, "tap")
	if m.form.cmd.name != "motion tap" || m.form.sel.Group != "pruebas" {
		t.Fatal("action or target lost")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.form.cmd.name != "motion" {
		t.Fatal("cannot return to action chooser")
	}
	app.answer(m, "text")
	app.answer(m, "Hello, ma\u00f1ana \U0001f60a --flag @unknown")
	if out := app.printed(m); m.form != nil || !strings.Contains(out, "2/2 correctas") {
		t.Fatal("text did not run", out)
	}
	app.typeLine(m, "/motion")
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.form != nil || m.task != nil {
		t.Fatal("cancel failed")
	}
}
func (app *Interface) testMotionRawTextAndHelp(t *testing.T) {
	m := app.testModel(t)
	text := "Hello  ma\u00f1ana \U0001f60a --dry-run @unknown"
	c, err := m.parse("/motion text " + text + " @DEV-001")
	if err != nil || c.raw != text || len(c.flags) != 0 || c.sel.Devices != "DEV-001" {
		t.Fatal(c, err)
	}
	for _, lang := range []string{"es", "en"} {
		app.useLanguage(t, lang)
		app.typeLine(m, "/help motion")
		out := app.printed(m)
		for _, action := range []string{"tap", "swipe", "key", "text"} {
			if !strings.Contains(out, "/motion "+action) {
				t.Fatal(out)
			}
			app.typeLine(m, "/help motion "+action)
			if !strings.Contains(app.printed(m), "/motion "+action) {
				t.Fatal("missing action help", action)
			}
		}
	}
}
