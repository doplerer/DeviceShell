package tui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/doplerer/deviceshell/internal/core"
)

func (app *Interface) testRecommendationContextPriority(t *testing.T) {
	m := app.testModel(t)
	check := func(want string) {
		t.Helper()
		m.layout()
		if m.recommendation != want {
			t.Fatalf("recommendation = %q, want %q", m.recommendation, want)
		}
	}
	check("")
	m.target = core.Selection{Group: "pruebas"}
	check("/settings")
	m.sync = &syncState{s: &core.LiveSession{}}
	m.sync.info.Paused = true
	check("/sync resume")
	m.sync.info.Paused = false
	check("/sync pause")
	m.wall = &screenWall{}
	m.wall.alive.Store(2)
	check("/view close")
	m.lastFailed = []string{"DEV-003"}
	check("/retry")
	m.inventory[0].State = "unauthorized"
	check("/doctor")
}
func (app *Interface) testRecommendationSubmitAfterTab(t *testing.T) {
	m := app.testModel(t)
	m.target = core.Selection{Group: "pruebas"}
	m.layout()
	app.upd(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.input.Value() != "/settings" || m.panel != nil {
		t.Fatal("Tab did not leave recommendation ready to submit")
	}
	app.run(m, app.upd(m, tea.KeyMsg{Type: tea.KeyEnter}))
	if _, ok := m.panel.(*settingsPanel); !ok {
		t.Fatal("Enter did not submit accepted recommendation")
	}
}
func (app *Interface) testRecommendationTabOnly(t *testing.T) {
	m := app.testModel(t)
	m.lastFailed = []string{"DEV-003"}
	m.layout()
	before := append([]string(nil), m.history...)
	transcript := strings.Join(m.transcript, "\n")
	if !m.recommendationVisible() || !strings.Contains(ansi.Strip(m.inputView()), "/retry   Tab o → para aceptar") {
		t.Fatal("missing inline recommendation")
	}
	if m.input.Value() != "" || len(m.sugs) != 0 {
		t.Fatal("recommendation leaked into input or menu")
	}
	if cmd := app.upd(m, tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil || m.input.Value() != "" || m.task != nil {
		t.Fatal("empty Enter accepted recommendation")
	}
	if cmd := app.upd(m, tea.KeyMsg{Type: tea.KeyTab}); cmd != nil || m.input.Value() != "/retry" || m.input.Position() != len([]rune("/retry")) {
		t.Fatal("Tab did not fill the complete command")
	}
	if m.task != nil || m.form != nil || !reflect.DeepEqual(before, m.history) || transcript != strings.Join(m.transcript, "\n") {
		t.Fatal("Tab executed or recorded the command")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" x")})
	if m.input.Value() != "/retry x" || m.recommendationVisible() {
		t.Fatal("accepted recommendation cannot be edited")
	}
}
func (app *Interface) testRecommendationRightArrow(t *testing.T) {
	m := app.testModel(t)
	m.lastFailed = []string{"DEV-003"}
	m.layout()
	before := append([]string(nil), m.history...)
	if cmd := app.upd(m, tea.KeyMsg{Type: tea.KeyRight}); cmd != nil || m.input.Value() != "/retry" || m.input.Position() != len([]rune("/retry")) {
		t.Fatal("Right did not fill the complete command")
	}
	if m.task != nil || !reflect.DeepEqual(before, m.history) {
		t.Fatal("Right executed or recorded the command")
	}
	m.setInput("ab")
	app.upd(m, tea.KeyMsg{Type: tea.KeyLeft})
	app.upd(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.input.Value() != "ab" || m.input.Position() != 2 {
		t.Fatal("Right no longer moves the cursor while typing")
	}
}
func (app *Interface) testRecommendationDismissAndRefresh(t *testing.T) {
	m := app.testModel(t)
	m.lastFailed = []string{"DEV-003"}
	m.layout()
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	app.upd(m, devicesMsg{d: m.devices})
	if m.recommendationVisible() {
		t.Fatal("poll restored dismissed recommendation")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.input.Value() != "" {
		t.Fatal("Tab accepted hidden recommendation")
	}
	app.typeLine(m, "/help")
	if !m.recommendationVisible() {
		t.Fatal("submitted command did not restore recommendation")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if m.recommendationVisible() || len(m.sugs) != len(app.tuiRegistry) {
		t.Fatal("typing did not restore regular menu")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if !m.recommendationVisible() {
		t.Fatal("empty input did not restore recommendation")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	devices := append([]core.Device(nil), m.devices...)
	devices[0].State = "unauthorized"
	app.upd(m, devicesMsg{d: devices})
	if !m.recommendationVisible() || m.recommendation != "/doctor" {
		t.Fatal("new recommendation did not replace dismissed retry")
	}
	m.history = []string{"/help"}
	m.hidx = len(m.history)
	app.upd(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.input.Value() != "/help" {
		t.Fatal("recommendation captured history navigation")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.input.Value() != "" || !m.recommendationVisible() {
		t.Fatal("history did not return to empty draft")
	}
	m.lastFailed = nil
	devices[0].State = "device"
	app.upd(m, devicesMsg{d: devices})
	if m.recommendationVisible() || m.recommendation != "" {
		t.Fatal("stale recommendation")
	}
}
func (app *Interface) testRecommendationVisibilityAndLayout(t *testing.T) {
	m := app.testModel(t)
	m.lastFailed = []string{"DEV-003"}
	for _, state := range []string{"normal", "form", "panel", "busy", "task", "exit", "reset"} {
		m.form, m.panel, m.task = nil, nil, nil
		m.busy, m.exiting, m.resetPending = "", false, false
		switch state {
		case "form":
			m.form = &formState{}
		case "panel":
			m.panel = &devicesPanel{app: app}
		case "busy":
			m.busy = "loading"
		case "task":
			m.task = &taskState{}
		case "exit":
			m.exiting = true
		case "reset":
			m.resetPending = true
		}
		m.refreshRecommendation()
		if m.recommendationVisible() != (state == "normal") {
			t.Fatalf("visibility in %s", state)
		}
	}
	m.resetPending = false
	profile := lipgloss.ColorProfile()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(profile)
	})
	lipgloss.SetColorProfile(termenv.TrueColor)
	for _, width := range []int{24, 40, 80, 160} {
		app.upd(m, tea.WindowSizeMsg{Width: width, Height: 30})
		view := m.View()
		if len(strings.Split(view, "\n")) != 30 {
			t.Fatal("recommendation shifted input")
		}
		for _, row := range strings.Split(view, "\n") {
			if lipgloss.Width(row) != width {
				t.Fatalf("overflow at width %d", width)
			}
		}
		if !strings.Contains(m.inputView(), app.stDim.Render(app.clip("retry   Tab o → para aceptar", m.input.Width-1))) {
			t.Fatal("recommendation is not rendered in gray")
		}
	}
}
