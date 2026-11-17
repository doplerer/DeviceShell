package tui

import (
	"strings"

	"github.com/doplerer/deviceshell/internal/core"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Restore our base after nested styles reset, so plain text and gaps never
// inherit a light terminal's default background or foreground.
func (app *Interface) renderScreen(content string, width, height int) string {
	frame := app.stScreen.Width(width).Height(height).Render(content)
	base, _, _ := strings.Cut(app.stScreen.Render(" "), " ")
	if base != "" {
		frame = strings.ReplaceAll(frame, "\x1b[0m", "\x1b[0m"+base) + "\x1b[0m"
	}
	return frame
}

const (
	glyphOK      = "✓"
	glyphFail    = "✗"
	glyphWarn    = "!"
	glyphInfo    = "›"
	glyphDot     = "●"
	glyphDotOff  = "○"
	glyphRule    = "│"
	glyphPointer = "▸"
)

func (app *Interface) stateLabel(state string) string {
	switch state {
	case "device":
		return app.Service.L("conectada", "connected")
	case "unauthorized":
		return app.Service.L("sin autorizar", "unauthorized")
	case "developer-off":
		return app.Service.L("sin modo desarrollador", "developer mode off")
	case "ios-outdated":
		return app.Service.L("requiere iOS 27", "needs iOS 27")
	case "locked":
		return app.Service.L("bloqueado", "locked")
	case "needs-usb":
		return app.Service.L("necesita cable", "needs cable")
	case "disconnected":
		return app.Service.L("desconectada", "disconnected")
	}
	return state
}
func (app *Interface) stateStyle(state string) lipgloss.Style {
	switch state {
	case "device":
		return app.stStateOn
	case "unauthorized", "developer-off", "ios-outdated", "locked", "needs-usb":
		return app.stStateWarn
	case "disconnected", "":
		return app.stFaint
	}
	return app.stStateErr
}
func (app *Interface) stateGlyph(state string) string {
	if state == "disconnected" || state == "" {
		return glyphDotOff
	}
	return glyphDot
}
func (app *Interface) stateDot(state string) string {
	return app.stateStyle(state).Render(app.stateGlyph(state) + " " + app.stateLabel(state))
}

// deviceDot is stateDot, but a connected device says how it reaches the PC: USB or Wi-Fi.
func (app *Interface) deviceDot(d core.Device) string {
	if d.State != "device" {
		return app.stateDot(d.State)
	}
	link := "USB"
	if d.Link() == "wifi" {
		link = "Wi-Fi"
	}
	return app.stateStyle(d.State).Render(app.stateGlyph(d.State) + " " + link)
}

// pad fits s to exactly n visible cells, truncating with an ellipsis.
func (app *Interface) pad(s string, n int) string {
	w := lipgloss.Width(s)
	if w > n {
		return ansi.Truncate(s, n, "…")
	}
	return s + strings.Repeat(" ", n-w)
}
func (app *Interface) clip(s string, n int) string {
	if n < 1 {
		return ""
	}
	return ansi.Truncate(s, n, "…")
}

// tree renders a result block: a status header and its body behind a thin left rule.
func (app *Interface) tree(header string, body ...string) string {
	var b strings.Builder
	b.WriteString(header)
	rule := app.stFaint.Render(glyphRule)
	for _, l := range body {
		b.WriteString("\n  " + rule + " " + l)
	}
	return b.String()
}

// head builds a block header from a status glyph and a bold title.
func (app *Interface) head(st lipgloss.Style, glyph, title string) string {
	return st.Render(glyph) + " " + app.stBold.Render(title)
}

// progressBar shows correct, failed and pending devices as one segmented line.
func (app *Interface) progressBar(ok, fail, total, width int) string {
	if total < 1 || width < 1 {
		return ""
	}
	o, f := ok*width/total, fail*width/total
	if fail > 0 && f == 0 {
		f = 1
	}
	o = min(o, width-f)
	return app.stOK.Render(strings.Repeat("━", o)) + app.stErr.Render(strings.Repeat("×", f)) + app.stFaint.Render(strings.Repeat("─", width-o-f))
}
