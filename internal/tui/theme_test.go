package tui

import (
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func (app *Interface) testMonochromeOnLightAndDarkTerminals(t *testing.T) {
	profile, dark := lipgloss.ColorProfile(), lipgloss.HasDarkBackground()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(profile)
		lipgloss.SetHasDarkBackground(dark)
	})
	lipgloss.SetColorProfile(termenv.TrueColor)
	rgb := regexp.MustCompile(`(?:38|48);2;(\d+);(\d+);(\d+)`)
	stateRGB := map[string]bool{"63,185,80": true, "210,153,34": true, "248,81,73": true}
	m := app.testModel(t)
	for _, width := range []int{40, 80, 160} {
		app.upd(m, tea.WindowSizeMsg{Width: width, Height: 30})
		for _, state := range []string{"welcome", "suggestions", "panel", "settings"} {
			m.setInput("")
			m.panel = nil
			switch state {
			case "settings":
				m.cmdSettings(call{})
			case "suggestions":
				m.setInput("/s")
			case "panel":
				m.panel = &devicesPanel{app: app, marked: map[string]bool{"S1": true}}
			}
			m.layout()
			lipgloss.SetHasDarkBackground(false)
			light := m.View()
			lipgloss.SetHasDarkBackground(true)
			darkView := m.View()
			if light != darkView {
				t.Fatalf("%s: theme depends on terminal background", state)
			}
			if !strings.Contains(light, "48;2;0;0;0") {
				t.Fatal("Missing fixed black background")
			}
			for _, color := range rgb.FindAllStringSubmatch(light, -1) {
				if stateRGB[color[1]+","+color[2]+","+color[3]] {
					continue
				}
				if color[1] != color[2] || color[2] != color[3] {
					t.Fatalf("Chromatic color in %s: %s", state, color[0])
				}
			}
			rows := strings.Split(light, "\n")
			if len(rows) != 30 {
				t.Fatalf("%s: wrong height: %d", state, len(rows))
			}
			for _, row := range rows {
				if lipgloss.Width(row) != width {
					t.Fatalf("%s: canvas does not fill width %d", state, width)
				}
			}
		}
	}
	base, _, _ := strings.Cut(app.stScreen.Render(" "), " ")
	painted := app.renderScreen("before "+app.stDim.Render("secondary")+" after", 40, 2)
	if !strings.Contains(painted, "\x1b[0m"+base+" after") {
		t.Fatal("Nested style reset did not restore canvas colors")
	}
	if !strings.Contains(app.progressBar(1, 1, 3, 9), "×") {
		t.Fatal("Failed progress has no distinct marker")
	}
}
