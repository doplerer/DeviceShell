package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func (app *Interface) answer(m *tuiModel, s string) {
	m.setInput(s)
	app.run(m, app.upd(m, tea.KeyMsg{Type: tea.KeyEnter}))
}
func (app *Interface) testFormTapStepByStep(t *testing.T) {
	m := app.testModel(t)
	m.setInput("/motion ta")
	if len(m.sugs) == 0 || m.sugs[0].value != "/motion tap" {
		t.Fatal(m.sugs)
	}
	app.run(m, app.upd(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if p, _ := m.formStep(); m.form == nil || p == nil || p.name != "X" {
		t.Fatal("Assistant not opened at X")
	}
	if !strings.Contains(ansi.Strip(strings.Join(app.frame(m), "\n")), "posición horizontal") {
		t.Fatal("Missing explanation of X")
	}
	app.answer(m, "2")
	if !strings.Contains(m.form.err, "entre 0 y 1") {
		t.Fatal(m.form.err)
	}
	app.answer(m, "0,2")
	if m.form.values[0] != "0.2" {
		t.Fatal(m.form.values)
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if p, _ := m.formStep(); p.name != "X" || m.input.Value() != "0.2" {
		t.Fatal(m.input.Value())
	}
	app.answer(m, "0.2")
	app.answer(m, "")
	if m.form.values[1] != "0.5" {
		t.Fatal(m.form.values)
	}
	if _, dest := m.formStep(); dest == nil || len(m.sugs) == 0 || m.sugs[0].value != "@all" {
		t.Fatal("Destination step without choices", m.sugs)
	}
	app.answer(m, "")
	out := ansi.Strip(app.printed(m))
	if m.form != nil || !strings.Contains(out, "tap 0.20 0.50") {
		t.Fatal("Assistant did not run the batch", out)
	}
	if !strings.Contains(out, "› /motion tap 0.2 0.5 @all") {
		t.Fatal("The composed command is not shown")
	}
}
func (app *Interface) testFormKeepsTypedArgsAndRoutineVars(t *testing.T) {
	m := app.testModel(t)
	app.typeLine(m, "/motion swipe 0.5 0.8")
	if m.form == nil || len(m.form.values) != 2 {
		t.Fatal("Typed arguments lost")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.form != nil {
		t.Fatal("Esc did not cancel")
	}
	app.typeLine(m, "/routine preparacion")
	if p, _ := m.formStep(); p == nil || p.kind != pRoutineVar || p.key != "mensaje" {
		t.Fatal("Routine parameter not asked")
	}
	app.answer(m, "hola mundo")
	app.answer(m, "pruebas")
	if out := ansi.Strip(app.printed(m)); m.form != nil || !strings.Contains(out, "mensaje=hola mundo") {
		t.Fatal(out)
	}
}
func (app *Interface) testFormGroupAndPicker(t *testing.T) {
	m := app.testModel(t)
	app.typeLine(m, "/group create")
	if p, _ := m.formStep(); p == nil || p.name != "Nombre" {
		t.Fatal("Group name not asked")
	}
	app.answer(m, "con espacio")
	if !strings.Contains(m.form.err, "espacios") {
		t.Fatal(m.form.err)
	}
	app.answer(m, "nuevo")
	for i, s := range m.sugs {
		if s.pick {
			m.sugIdx = i
		}
	}
	m.sugMoved = true
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if p, ok := m.panel.(*devicesPanel); !ok || p.onPick == nil {
		t.Fatal("Picker not opened")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" ")})
	app.run(m, app.upd(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if out := ansi.Strip(app.printed(m)); m.form != nil || !strings.Contains(out, "/group create nuevo @DEV-001") {
		t.Fatal(out)
	}
	if g, _ := app.Service.Groups(); len(g["nuevo"]) != 1 {
		t.Fatal("Group was not saved")
	}
}
func (app *Interface) testCommandHintAndDestinationSuggestions(t *testing.T) {
	m := app.testModel(t)
	m.setInput("/motion tap 0.5 ")
	if h := ansi.Strip(m.hint); !strings.Contains(h, "‹Y›") || !strings.Contains(h, "vertical") {
		t.Fatal(h)
	}
	m.setInput("/motion tap 0.5 0.5 ")
	if h := ansi.Strip(m.hint); !strings.Contains(h, "‹destino›") || len(m.sugs) == 0 || m.sugs[0].value != "@all" {
		t.Fatal(h, m.sugs)
	}
	m.setInput("/motion tap 0.2")
	if m.takeSuggestion() {
		t.Fatal("0.2 would be replaced")
	}
	m.setInput("/motion key HO")
	if !m.takeSuggestion() || m.sugs[0].value != "HOME" {
		t.Fatal("Choice prefix not completed")
	}
	m.setInput("/group ")
	if len(m.sugs) != 5 || m.sugs[1].value != "create" || m.sugs[3].value != "rename" || m.sugs[1].detail == "" {
		t.Fatal(m.sugs)
	}
}
func (app *Interface) testFormArrowsStepNumbers(t *testing.T) {
	m := app.testModel(t)
	app.typeLine(m, "/record")
	if p, _ := m.formStep(); m.form == nil || p == nil || p.kind != pNumber {
		t.Fatal("Assistant not asking the seconds")
	}
	press := func(k tea.KeyType, want string) {
		t.Helper()
		app.upd(m, tea.KeyMsg{Type: k})
		if m.input.Value() != want {
			t.Fatalf("got %q, want %q", m.input.Value(), want)
		}
	}
	press(tea.KeyUp, "11")
	press(tea.KeyRight, "21")
	m.setInput("5")
	press(tea.KeyLeft, "1")
	press(tea.KeyDown, "1")
	m.setInput("175")
	press(tea.KeyRight, "180")
	press(tea.KeyLeft, "170")
	app.run(m, app.upd(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if len(m.form.values) != 1 || m.form.values[0] != "170" {
		t.Fatal("Enter did not keep the stepped number", m.form.values)
	}
	if _, dest := m.formStep(); dest == nil {
		t.Fatal("Assistant did not move to the destination")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	app.typeLine(m, "/motion tap")
	if p, _ := m.formStep(); p == nil || p.kind != pCoord {
		t.Fatal("Assistant not asking X")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.input.Value() != "" || m.sugIdx != 1 {
		t.Fatal("arrows must still browse coordinate suggestions", m.input.Value(), m.sugIdx)
	}
}
