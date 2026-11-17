package tui

import (
	"os"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func (app *Interface) setTestLanguage(t *testing.T, m *tuiModel, l string) {
	t.Helper()
	app.typeLine(m, "/settings")
	p, ok := m.panel.(*settingsPanel)
	if !ok {
		t.Fatal("settings not opened", m.transcript)
	}
	p.cursor = 0
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if l == "en" {
		p.option = 1
	} else {
		p.option = 0
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
}
func (app *Interface) testSettingsPersistenceAndValidation(t *testing.T) {
	m := app.testModel(t)
	t.Cleanup(func() {
		app.Service.Lang = "es"
		app.RebuildTexts()
	})
	path := app.Service.ConfigPath()
	os.WriteFile(path, []byte("\ufeff{\"expectedDevices\":20,\"useCxtKey\":true,\"other\":{\"kept\":1}}"), 0600)
	app.typeLine(m, "/settings")
	p := m.panel.(*settingsPanel)
	p.cursor = 1
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	p.editor.SetValue("0")
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if p.err == "" || m.parallel != 4 {
		t.Fatal("invalid value accepted")
	}
	p.editor.SetValue("8")
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.parallel != 8 || p.editing {
		t.Fatal("parallel not applied", p.err)
	}
	p.cursor = 2
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	p.editor.SetValue("90s")
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.timeout != 90*time.Second {
		t.Fatal("timeout not applied")
	}
	p.cursor = 1
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	p.editor.SetValue("33")
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.parallel != 8 {
		t.Fatal("cancel applied edit")
	}
	p.cursor = 3
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	p.option = 1
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !m.target.All {
		t.Fatal("session values not applied")
	}
	next := app.newTUIModel()
	if next.parallel != 8 || next.timeout != 90*time.Second || !next.target.Empty() {
		t.Fatal("restart defaults")
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "other") || !strings.Contains(string(b), "kept") {
		t.Fatal("unknown settings lost")
	}
	os.WriteFile(path, []byte("{broken"), 0600)
	p.cursor = 1
	p.beginEdit("12")
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if p.err == "" || m.parallel != 8 || !p.editing {
		t.Fatal("failed save applied")
	}
	b, _ = os.ReadFile(path)
	if string(b) != "{broken" {
		t.Fatal("broken file overwritten")
	}
}
func (app *Interface) testSettingsGeometryAndRetiredCommands(t *testing.T) {
	m := app.testModel(t)
	for _, name := range []string{"language", "idioma", "lang", "mode", "modo", "parallel", "paralelo", "timeout", "target", "destino"} {
		if app.lookupCommand(name) != nil || !strings.Contains(app.unknownCommand(name).Error(), "/settings") {
			t.Fatal("old command retained", name)
		}
	}
	for _, language := range []string{"es", "en"} {
		app.useLanguage(t, language)
		for _, w := range []int{40, 80, 160} {
			for _, h := range []int{10, 12, 30} {
				app.upd(m, tea.WindowSizeMsg{Width: w, Height: h})
				app.typeLine(m, "/settings")
				p := m.panel.(*settingsPanel)
				for i := 0; i < 7; i++ {
					p.cursor = i
					f := app.frame(m)
					if testing.Verbose() && w == 80 && h == 30 && i == 0 {
						t.Log("\n" + strings.Join(f, "\n"))
					}
					if len(f) != h || !strings.Contains(strings.Join(f, "\n"), p.labels()[i]) {
						t.Fatalf("selection hidden %dx%d, %d", w, h, i)
					}
				}
				app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
			}
		}
	}
}
func (app *Interface) testSettingsSelectorsAndWindowArea(t *testing.T) {
	m := app.testModel(t)
	oldCxt := app.Service.StartupCxt
	app.Service.StartupCxt = false
	t.Cleanup(func() {
		app.Service.StartupCxt = oldCxt
	})
	app.typeLine(m, "/settings")
	p := m.panel.(*settingsPanel)
	choose := func(field, option int) {
		p.cursor = field
		app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
		p.option = option
		app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	}
	choose(3, 3)
	if m.target.Group != "pruebas" {
		t.Fatal("group not selected")
	}
	choose(3, 2)
	if _, ok := m.panel.(*devicesPanel); !ok {
		t.Fatal("device picker not opened")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.panel != p {
		t.Fatal("picker cancel did not return to settings")
	}
	choose(3, 2)
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.panel != p || m.target.Devices == "" {
		t.Fatal("device picker did not return selection")
	}
	choose(5, 1)
	if !p.config.UseCxtKey || !strings.Contains(p.values(m)[5], "reiniciar") || app.Service.StartupCxt {
		t.Fatal("CXT restart handling")
	}
	choose(4, 1)
	for _, value := range []string{"-100", "-200", "800", "600"} {
		p.editor.SetValue(value)
		app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	}
	c, _, err := app.Service.ReadSettings()
	if err != nil || len(c.WindowArea) != 4 || c.WindowArea[0] != -100 || c.WindowArea[3] != 600 {
		t.Fatal("area not saved", c, err)
	}
	choose(4, 1)
	p.editor.SetValue("99")
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	c, _, _ = app.Service.ReadSettings()
	if c.WindowArea[0] != -100 {
		t.Fatal("partial window area was saved")
	}
	choose(4, 0)
	c, _, _ = app.Service.ReadSettings()
	if c.WindowArea != nil {
		t.Fatal("automatic area retained customization")
	}
	// The iPhone cable picture: DisplayService by default, QuickTime on demand.
	if !strings.Contains(p.values(m)[8], "DisplayService") {
		t.Fatal("default cable video:", p.values(m)[8])
	}
	choose(8, 1)
	if c, _, _ = app.Service.ReadSettings(); c.IPhoneVideo != "quicktime" || !strings.Contains(p.values(m)[8], "QuickTime") {
		t.Fatal("QuickTime not saved", c.IPhoneVideo)
	}
	choose(8, 0)
	if c, _, _ = app.Service.ReadSettings(); c.IPhoneVideo != "displayservice" {
		t.Fatal("DisplayService not saved", c.IPhoneVideo)
	}
}
func (app *Interface) testSettingsArrowsStepNumbers(t *testing.T) {
	m := app.testModel(t)
	app.typeLine(m, "/settings")
	p := m.panel.(*settingsPanel)
	press := func(k tea.KeyType, want string) {
		t.Helper()
		app.upd(m, tea.KeyMsg{Type: k})
		if p.editor.Value() != want {
			t.Fatalf("got %q, want %q", p.editor.Value(), want)
		}
	}
	p.cursor = 1
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	p.editor.SetValue("4")
	press(tea.KeyUp, "5")
	press(tea.KeyRight, "15")
	press(tea.KeyDown, "14")
	press(tea.KeyLeft, "4")
	press(tea.KeyLeft, "1")
	for range 10 {
		app.upd(m, tea.KeyMsg{Type: tea.KeyRight})
	}
	press(tea.KeyUp, "64")
	press(tea.KeyLeft, "54")
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if m.parallel != 54 || p.editing {
		t.Fatal("stepped value not saved", p.err)
	}
	p.cursor = 7
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	p.editor.SetValue("5")
	press(tea.KeyLeft, "0")
	press(tea.KeyDown, "0")
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	p.cursor = 2
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	p.editor.SetValue("90s")
	press(tea.KeyUp, "1m31s")
	press(tea.KeyLeft, "1m21s")
	p.editor.SetValue("5s")
	press(tea.KeyLeft, "1s")
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	p.cursor = 0
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	app.upd(m, tea.KeyMsg{Type: tea.KeyDown})
	if !p.choosing || p.option != 1 {
		t.Fatal("arrows must still move through choices")
	}
}
