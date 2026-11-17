package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/doplerer/deviceshell/internal/core"
)

func (m *tuiModel) View() string {
	if m.quitting || m.width == 0 {
		return ""
	}
	content := m.vp.View()
	if m.showWelcome() {
		content = m.welcomeView()
	}
	return m.app.renderScreen(content+"\n"+m.bottom, m.w(), m.h())
}

// bottomView is pinned to the last rows: suggestions open upwards so the input box and the footer never move.
func (m *tuiModel) bottomView() string {
	parts := []string{""}
	if m.sync != nil {
		parts = append(parts, m.syncLine())
	}
	if m.wall != nil {
		parts = append(parts, m.app.stAccent.Render(m.app.Service.L("▣ Pantallas", "▣ Screens"))+m.app.stDim.Render(fmt.Sprintf(m.app.Service.L(" · %d abiertas en cuadrícula · /view close", " · %d open in a grid · /view close"), m.wall.Alive())))
	}
	switch {
	case m.exiting:
		parts = append(parts, m.spin.View()+" "+m.app.stAccent.Render(m.app.Service.L("Cerrando lotes y réplicas…", "Closing batches and replicas…")))
	case m.task != nil:
		parts = append(parts, m.taskView())
	case m.busy != "":
		parts = append(parts, m.spin.View()+" "+m.app.stAccent.Render(m.busy+"…")+m.app.stDim.Render(" ("+m.app.fmtDuration(time.Since(m.busySince))+")"))
	}
	switch {
	case m.panel != nil:
		parts = append(parts, m.panel.view(m))
	default:
		above := len(parts)
		if m.form != nil {
			parts = append(parts, m.formView())
		}
		if m.input.Value() == "?" && m.form == nil {
			parts = append(parts, m.shortcutsView())
		} else if m.sugVisible() {
			parts = append(parts, m.suggestionsView())
		}
		if m.hint != "" && m.form == nil {
			parts = append(parts, m.hint)
		}
		// Whatever opens above the input keeps a blank row of air from it.
		if len(parts) > above {
			parts = append(parts, "")
		}
		parts = append(parts, m.inputView())
	}
	parts = append(parts, "", m.footer())
	lines := strings.Split(strings.Join(parts, "\n"), "\n")
	for i, l := range lines {
		lines[i] = m.app.clip(l, m.w()-1)
	}
	if limit := m.h() - 1; len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return strings.Join(lines, "\n")
}
func (m *tuiModel) showWelcome() bool {
	return len(m.transcript) == 0 && m.task == nil && m.busy == "" && m.panel == nil && m.form == nil && !m.exiting
}

const welcomeLogo = `
██████╗  ███████╗ ██╗   ██╗ ██╗  ██████╗ ███████╗
██╔══██╗ ██╔════╝ ██║   ██║ ██║ ██╔════╝ ██╔════╝
██║  ██║ █████╗   ██║   ██║ ██║ ██║      █████╗  
██║  ██║ ██╔══╝   ╚██╗ ██╔╝ ██║ ██║      ██╔══╝  
██████╔╝ ███████╗  ╚████╔╝  ██║ ╚██████╗ ███████╗
╚═════╝  ╚══════╝   ╚═══╝   ╚═╝  ╚═════╝ ╚══════╝
  ███████╗ ██╗  ██╗ ███████╗ ██╗      ██╗        
  ██╔════╝ ██║  ██║ ██╔════╝ ██║      ██║        
  ███████╗ ███████║ █████╗   ██║      ██║        
  ╚════██║ ██╔══██║ ██╔══╝   ██║      ██║        
  ███████║ ██║  ██║ ███████╗ ███████╗ ███████╗   
  ╚══════╝ ╚═╝  ╚═╝ ╚══════╝ ╚══════╝ ╚══════╝   `

// logoShades run top to bottom through each word of the logo, so the solid blocks read as brushed metal.
var logoShades = []lipgloss.Color{"#FFFFFF", "#E8E8E8", "#CFCFCF", "#B5B5B5", "#9A9A9A", "#808080"}

// logoShineStart and logoShineStep drive the click animation: a diagonal band of light that sweeps the logo.
// From -2 in steps of 3 the position never lands on -1, which marks the logo at rest.
const (
	logoShineStart = -2
	logoShineStep  = 3
)

// screenArea is a rectangle of terminal cells, used to hit-test clicks.
type screenArea struct{ x, y, w, h int }

func (a screenArea) contains(x, y int) bool {
	return x >= a.x && x < a.x+a.w && y >= a.y && y < a.y+a.h
}

// renderLogo paints the solid blocks with a vertical gradient and the box-drawing shadow in dark gray.
// shine >= 0 lights a diagonal band at that position. Cells are grouped in runs of the same look,
// one escape per run instead of one per cell.
func (app *Interface) renderLogo(logo string, shine int) string {
	shadows := []lipgloss.Style{lipgloss.NewStyle().Foreground(lipgloss.Color("#4A4A4A")), lipgloss.NewStyle().Foreground(lipgloss.Color("#7A7A7A")), lipgloss.NewStyle().Foreground(lipgloss.Color("#B5B5B5"))}
	kind := func(r rune) int {
		switch {
		case r == '█':
			return 1
		case r >= '─' && r <= '╿':
			return 2
		}
		return 0
	}
	lines := strings.Split(logo, "\n")
	for y, line := range lines {
		shade := y % len(logoShades)
		faces := []lipgloss.Style{lipgloss.NewStyle().Foreground(logoShades[shade]).Bold(true), lipgloss.NewStyle().Foreground(logoShades[max(shade-2, 0)]).Bold(true), lipgloss.NewStyle().Foreground(logoShades[0]).Bold(true)}
		// look is kind*3 + light, where light is 0 outside the band, 1 in its halo and 2 in its core.
		look := func(x int, r rune) int {
			k := kind(r)
			if k == 0 || shine < 0 {
				return k * 3
			}
			d := x + 2*y - shine
			if d < 0 {
				d = -d
			}
			switch {
			case d <= 1:
				return k*3 + 2
			case d <= 4:
				return k*3 + 1
			}
			return k * 3
		}
		runes := []rune(line)
		var b strings.Builder
		for start := 0; start < len(runes); {
			l, end := look(start, runes[start]), start+1
			for end < len(runes) && look(end, runes[end]) == l {
				end++
			}
			seg := string(runes[start:end])
			switch l / 3 {
			case 1:
				seg = faces[l%3].Render(seg)
			case 2:
				seg = shadows[l%3].Render(seg)
			}
			b.WriteString(seg)
			start = end
		}
		lines[y] = b.String()
	}
	return strings.Join(lines, "\n")
}

func (m *tuiModel) starterView() string {
	w := max(1, m.vp.Width-1)
	row := func(command, description string) string {
		return m.app.clip("  "+m.app.stBold.Render(m.app.pad(command, 10))+m.app.stDim.Render(description), w)
	}
	return strings.Join([]string{"", m.app.clip("  "+m.app.stDim.Render(m.app.Service.L("Para empezar", "To get started")), w), row("/devices", m.app.Service.L("Elegir dispositivos", "Choose devices")), row("/view", m.app.Service.L("Abrir pantallas", "Open screens")), row("/sync", m.app.Service.L("Réplica entre dispositivos", "Replicate between devices")), row("/help", m.app.Service.L("Ver comandos", "Show commands"))}, "\n")
}

// welcomeView is decorative only: it never enters the scrollable transcript.
func (m *tuiModel) welcomeView() string {
	w, h := max(1, m.vp.Width-1), max(1, m.vp.Height)
	connected, unauth := m.botCounts()
	// metric puts the figure first in bold and its label in gray, so the numbers are what the eye catches.
	metric := func(n int, one, many string) string {
		label := many
		if n == 1 {
			label = one
		}
		return m.app.stBold.Render(fmt.Sprint(n)) + " " + m.app.stDim.Render(label)
	}
	stats := []string{m.botDotStyle().Render(glyphDot) + " " + metric(connected, m.app.Service.L("dispositivo", "device"), m.app.Service.L("dispositivos", "devices"))}
	if unauth > 0 {
		stats = append(stats, m.app.stStateWarn.Render(fmt.Sprintf(m.app.Service.L("%d sin autorizar", "%d unauthorized"), unauth)))
	}
	stats = append(stats, metric(len(m.groups), m.app.Service.L("grupo", "group"), m.app.Service.L("grupos", "groups")), metric(m.routineCount, m.app.Service.L("rutina", "routine"), m.app.Service.L("rutinas", "routines")))
	center := func(s string) string {
		return lipgloss.PlaceHorizontal(w, lipgloss.Center, s)
	}
	// The metrics share one airy row when it fits; otherwise they tighten and wrap over several rows.
	sep := func(gap string) string {
		return gap + m.app.stFaint.Render(glyphRule) + gap
	}
	summaryRows := []string{strings.Join(stats, sep("    "))}
	if lipgloss.Width(summaryRows[0]) > w {
		summaryRows = nil
		line := stats[0]
		for _, stat := range stats[1:] {
			if lipgloss.Width(line+sep("  ")+stat) > w {
				summaryRows = append(summaryRows, line)
				line = stat
			} else {
				line += sep("  ") + stat
			}
		}
		summaryRows = append(summaryRows, line)
	}
	var centeredRows []string
	for _, row := range summaryRows {
		for _, wrapped := range strings.Split(ansi.Wrap(row, w, " "), "\n") {
			centeredRows = append(centeredRows, center(wrapped))
		}
	}
	summary := strings.Join(centeredRows, "\n")
	plain := strings.TrimPrefix(welcomeLogo, "\n")
	logo := m.app.renderLogo(lipgloss.NewStyle().Width(lipgloss.Width(plain)).Render(plain), m.logoShine)
	// signature is ruled to the given width so it lines up with the mark above it.
	signature := func(width int) string {
		rule := m.app.stFaint.Render(strings.Repeat("─", max(1, (width-15)/2)))
		return rule + "  " + m.app.stDim.Render("by ") + m.app.stBold.Render("doplerer") + "  " + rule
	}
	block := func(mark string, rule int) string {
		return center(mark) + "\n\n" + center(signature(rule)) + "\n\n" + summary
	}
	compact := block(m.app.stSel.Render("DeviceShell"), min(31, w))
	tips := m.starterView()
	reserved := lipgloss.Height(tips) + 1
	prefix := ""
	if reserved+lipgloss.Height(compact) <= h && lipgloss.Width(compact) <= w {
		prefix = tips + "\n\n"
		h -= reserved
	}
	content := block(logo, lipgloss.Width(plain))
	if lipgloss.Width(logo) > w || lipgloss.Height(content) > h {
		content = compact
	}
	if lipgloss.Width(content) > w || lipgloss.Height(content) > h {
		content = ""
	}
	out := prefix + lipgloss.Place(m.vp.Width, h, lipgloss.Left, lipgloss.Center, content)
	// Remember where the big logo landed so a click on it can start the shine.
	m.logoArea = screenArea{}
	first, _, _ := strings.Cut(plain, "\n")
	for y, row := range strings.Split(ansi.Strip(out), "\n") {
		if i := strings.Index(row, first); i >= 0 {
			m.logoArea = screenArea{lipgloss.Width(row[:i]), y, lipgloss.Width(plain), lipgloss.Height(plain)}
			break
		}
	}
	return out
}
func (m *tuiModel) inputView() string {
	input := &m.input
	if m.recommendationVisible() {
		preview := m.input
		input = &preview
		input.Placeholder = m.recommendation + m.app.Service.L("   Tab o → para aceptar", "   Tab or → to accept")
		input.PlaceholderStyle = m.app.stDim
	}
	return " " + input.View()
}
func (m *tuiModel) suggestionsView() string {
	labelOf := func(s suggestion) string {
		if s.indent {
			return "  " + s.label
		}
		return s.label
	}
	lw := 0
	for _, s := range m.sugs {
		lw = max(lw, lipgloss.Width(labelOf(s)))
	}
	lw = min(lw+2, m.w()/2)
	reserved := 12
	if m.form != nil {
		reserved += lipgloss.Height(m.formView())
	}
	size := min(len(m.sugs), max(4, m.h()-reserved))
	m.sugOffset = m.app.scrollWindow(m.sugIdx, m.sugOffset, len(m.sugs), size)
	var lines []string
	if m.sugOffset > 0 {
		lines = append(lines, m.app.stDim.Render(fmt.Sprintf(m.app.Service.L("  ↑ %d más", "  ↑ %d more"), m.sugOffset)))
	}
	for i := m.sugOffset; i < m.sugOffset+size; i++ {
		s := m.sugs[i]
		label := m.app.pad(labelOf(s), lw)
		var line string
		switch {
		case i == m.sugIdx:
			line = m.app.stAccent.Render(glyphPointer+" ") + m.app.stActive.Render(label) + " " + s.detail
			if s.example != "" {
				line += m.app.stDim.Render("   ej. " + s.example)
			}
		case s.bold:
			line = "  " + m.app.stBold.Render(label) + " " + m.app.stDim.Render(s.detail)
		default:
			line = "  " + label + " " + m.app.stDim.Render(s.detail)
		}
		lines = append(lines, m.app.clip(line, m.w()-1))
	}
	if rest := len(m.sugs) - m.sugOffset - size; rest > 0 {
		lines = append(lines, m.app.stDim.Render(fmt.Sprintf(m.app.Service.L("  ↓ %d más · ↑↓ o rueda para recorrer", "  ↓ %d more · ↑↓ or wheel to browse"), rest)))
	}
	return strings.Join(lines, "\n")
}
func (m *tuiModel) shortcutsView() string {
	rows := [][]string{{m.app.Service.L("/  comandos", "/  commands"), m.app.Service.L("@  elegir placas o grupo", "@  pick devices or group"), m.app.Service.L("!  shell en el destino", "!  shell on the target")}, {m.app.Service.L("tab  completar", "tab  complete"), m.app.Service.L("↑↓  historial de órdenes", "↑↓  command history"), m.app.Service.L("ctrl+l  limpiar resultados", "ctrl+l  clear results")}, {m.app.Service.L("mayús+flechas  seleccionar", "shift+arrows  select"), m.app.Service.L("ctrl+flechas  saltar palabras", "ctrl+arrows  jump words"), m.app.Service.L("ctrl+a  seleccionar todo", "ctrl+a  select all")}, {m.app.Service.L("ctrl+c  copiar selección", "ctrl+c  copy selection"), m.app.Service.L("ctrl+x  cortar", "ctrl+x  cut"), m.app.Service.L("ctrl+v  pegar", "ctrl+v  paste")}, {m.app.Service.L("mayús+arrastrar  copiar de pantalla", "shift+drag  copy from screen"), m.app.Service.L("/copy  copiar último resultado", "/copy  copy last result"), m.app.Service.L("ctrl+o  salida completa", "ctrl+o  full output")}, {m.app.Service.L("esc  cancelar lote", "esc  cancel batch"), m.app.Service.L("repág/avpág  desplazar", "pgup/pgdn  scroll")}, {m.app.Service.L("/settings  ajustes", "/settings  settings"), m.app.Service.L("/help  comandos", "/help  commands"), m.app.Service.L("ctrl+c ×2  salir", "ctrl+c ×2  quit")}}
	cw := max(26, (m.w()-4)/3)
	out := []string{"  " + m.app.stBold.Render(m.app.Service.L("Atajos", "Shortcuts"))}
	for _, r := range rows {
		var cells []string
		for _, c := range r {
			k, v, _ := strings.Cut(c, "  ")
			cells = append(cells, m.app.pad(m.app.stAccent.Render(k)+" "+m.app.stDim.Render(v), cw))
		}
		out = append(out, m.app.clip("  "+strings.Join(cells, ""), m.w()-1))
	}
	return strings.Join(out, "\n")
}
func (m *tuiModel) botCounts() (connected, unauthorized int) {
	for _, d := range m.inventory {
		switch d.State {
		case "device":
			connected++
		case "unauthorized":
			unauthorized++
		}
	}
	return
}
func (app *Interface) formatBotCount(connected int) string {
	if connected == 1 {
		return app.Service.L("1 dispositivo", "1 device")
	}
	return fmt.Sprintf(app.Service.L("%d dispositivos", "%d devices"), connected)
}

// botDotStyle colors the devices dot: green when all is well, amber with unauthorized devices, red with none connected.
func (m *tuiModel) botDotStyle() lipgloss.Style {
	connected, unauth := m.botCounts()
	switch {
	case connected == 0:
		return m.app.stStateErr
	case unauth > 0:
		return m.app.stStateWarn
	}
	return m.app.stStateOn
}

// footer is a quiet status line aligned with the input: muted segments on the left, the current hint on the right.
func (m *tuiModel) footer() string {
	sep := m.app.stFaint.Render("  ·  ")
	connected, unauth := m.botCounts()
	segs := []string{m.botDotStyle().Render(glyphDot) + " " + m.app.stDim.Render(m.app.formatBotCount(connected))}
	if unauth > 0 {
		segs = append(segs, m.app.stStateWarn.Render(fmt.Sprintf(m.app.Service.L("%d sin autorizar", "%d unauthorized"), unauth)))
	}
	if n := m.alertCount(); n > 0 {
		segs = append(segs, m.app.stStateWarn.Render("! "+m.app.plural(n, m.app.Service.L("aviso", "alert"), m.app.Service.L("avisos", "alerts"))))
	}
	if !m.target.Empty() {
		segs = append(segs, m.app.stDim.Render(m.app.Service.L("destino ", "target "))+m.app.stSel.Render(m.target.String()))
	}
	segs = append(segs, m.app.stDim.Render(fmt.Sprintf("×%d", m.parallel)))
	left := " " + strings.Join(segs, sep)
	var right string
	switch {
	case !m.quitArmed.IsZero() && time.Since(m.quitArmed) < 2*time.Second:
		right = m.app.stWarn.Bold(true).Render(m.app.Service.L("Ctrl+C otra vez para salir", "Ctrl+C again to quit"))
	case m.flash != "" && time.Since(m.flashAt) < 2*time.Second:
		right = m.app.stOK.Bold(true).Render(glyphOK + " " + m.flash)
	case m.newBelow:
		right = m.app.stSel.Render(m.app.Service.L("↓ nuevos · fin", "↓ new · end"))
	case !m.vp.AtBottom():
		right = m.app.stDim.Render(m.app.Service.L("↑ historial · fin vuelve", "↑ history · end returns"))
	default:
		right = m.app.stDim.Render(m.app.Service.L("? atajos", "? shortcuts"))
	}
	right += " "
	w := m.w() - 1
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		right = ""
		left = m.app.clip(left, w)
		gap = w - lipgloss.Width(left)
	}
	return left + strings.Repeat(" ", max(0, gap)) + right
}
func (m *tuiModel) taskView() string {
	t := m.task
	counts := [4]int{}
	var running []string
	for i, s := range t.state {
		counts[s]++
		if s == 1 {
			line := fmt.Sprintf(m.app.Service.L("%s paso %d/%d", "%s step %d/%d"), t.targets[i].Alias, t.step[i]+1, len(t.steps))
			if i < len(t.retry) && t.retry[i] > 0 {
				line += fmt.Sprintf(" ↻%d", t.retry[i])
			}
			running = append(running, line)
		}
	}
	hint := m.app.Service.L("esc cancela", "esc cancels")
	if t.canceled {
		hint = m.app.Service.L("cancelando…", "canceling…")
	}
	title := m.spin.View() + " " + m.app.stAccent.Render(m.app.Service.L("Ejecutando ", "Running ")) + m.app.stBold.Render(m.app.clip(t.title, m.w()/2)) + m.app.stDim.Render(" · "+t.sel)
	done := counts[2] + counts[3]
	stats := []string{fmt.Sprintf("%d/%d", done, len(t.targets)), m.app.stOK.Render(fmt.Sprintf("%s %d", glyphOK, counts[2]))}
	if counts[3] > 0 {
		stats = append(stats, m.app.stErr.Render(fmt.Sprintf("%s %d", glyphFail, counts[3])))
	}
	stats = append(stats, m.app.stDim.Render(fmt.Sprintf(m.app.Service.L("%d en curso · %d en cola · %s · %s", "%d running · %d queued · %s · %s"), counts[1], counts[0], m.app.fmtDuration(time.Since(t.started)), hint)))
	lines := []string{title, "  " + m.app.progressBar(counts[2], counts[3], len(t.targets), min(30, max(10, m.w()/4))) + "  " + strings.Join(stats, m.app.stDim.Render(" · "))}
	if len(running) > 0 {
		lines = append(lines, "  "+m.app.stDim.Render(m.app.clip(strings.Join(running, " · "), m.w()-4)))
	}
	return strings.Join(lines, "\n")
}
func (m *tuiModel) syncLine() string {
	st := m.sync
	if st.s == nil {
		return m.spin.View() + " " + m.app.stAccent.Render(m.app.Service.L("Iniciando réplica con ", "Starting the replica with ")+st.master.Alias+"…") + m.app.stDim.Render(" ("+m.app.fmtDuration(time.Since(st.started))+")")
	}
	head := m.app.stOK.Render(m.app.Service.L("◉ Réplica en vivo", "◉ Live replica"))
	if st.info.Paused {
		head = m.app.stWarn.Render(m.app.Service.L("‖ Réplica en pausa", "‖ Replica paused"))
	}
	parts := []string{head, m.app.Service.L("principal ", "master ") + m.app.stBold.Render(st.master.Alias), m.app.plural(st.peers, m.app.Service.L("seguidora", "follower"), m.app.Service.L("seguidoras", "followers")), fmt.Sprintf(m.app.Service.L("%d eventos", "%d events"), st.info.Events)}
	if n := len(st.info.Errors); n > 0 {
		parts = append(parts, m.app.stErr.Render(m.app.plural(n, "error", m.app.Service.L("errores", "errors"))))
	}
	parts = append(parts, m.app.stDim.Render(m.app.Service.L("/sync para controlar", "/sync to control it")))
	return m.app.clip(strings.Join(parts, m.app.stDim.Render(" · ")), m.w()-1)
}

// renderJob is the summary printed when a batch ends; full keeps every output line for ctrl+o.
func (m *tuiModel) renderJob(title, sel string, j core.Job, elapsed time.Duration, full, canceled bool) string {
	failed := j.Failed()
	st, glyph := m.app.stOK, glyphOK
	switch {
	case canceled:
		st, glyph = m.app.stWarn, glyphWarn
	case failed > 0:
		st, glyph = m.app.stErr, glyphFail
	}
	if j.Version == 2 {
		title += m.app.Service.L(" (resumen)", " (summary)")
	}
	head := m.app.head(st, glyph, title)
	if sel != "" {
		head += m.app.stDim.Render(" · " + sel)
	}
	head += m.app.stDim.Render(" · " + m.app.plural(len(j.Results), m.app.Service.L("placa", "device"), m.app.Service.L("placas", "devices")))
	var body []string
	warned := false
	for _, r := range j.Results {
		warned = warned || r.Warnings > 0
	}
	// Warnings of onError "continue" live in the output, so it is shown whenever there are some.
	if m.app.showOutput(j.Steps) || (warned && j.Version != 2) {
		for _, r := range j.Results {
			body = append(body, m.app.resultLine(r, 9))
			if r.OK {
				for _, l := range m.app.outputLines(r.Output) {
					body = append(body, "    "+l)
				}
			}
		}
	} else {
		var cells []string
		cw, width := 0, 0
		for _, r := range j.Results {
			if r.OK {
				cw = max(cw, lipgloss.Width(r.Alias))
			}
		}
		for _, r := range j.Results {
			if r.OK {
				cell := m.app.stOK.Render("✓") + " " + m.app.pad(r.Alias, cw) + " " + m.app.stDim.Render(m.app.pad(m.app.fmtMS(r.DurationMS), 6)) + m.app.attempts(r)
				cells = append(cells, cell)
				width = max(width, lipgloss.Width(cell))
			}
		}
		for i := range cells {
			cells[i] = m.app.pad(cells[i], width)
		}
		if len(cells) > 0 {
			cols := max(1, (m.w()-8)/(width+2))
			for i := 0; i < len(cells); i += cols {
				body = append(body, strings.Join(cells[i:min(i+cols, len(cells))], "  "))
			}
		}
		for _, r := range j.Results {
			if !r.OK {
				body = append(body, m.app.resultLine(r, 9))
			}
		}
	}
	if !full && len(body) > 16 {
		hidden := len(body) - 14
		body = append(body[:14], m.app.stDim.Render(fmt.Sprintf(m.app.Service.L("… +%d líneas (ctrl+o para ver todo)", "… +%d lines (ctrl+o to see everything)"), hidden)))
	}
	summary := fmt.Sprintf(m.app.Service.L("%d/%d correctas", "%d/%d succeeded"), len(j.Results)-failed, len(j.Results))
	if elapsed > 0 {
		summary += " · " + m.app.fmtDuration(elapsed)
	}
	if j.ID != "" {
		summary += m.app.Service.L(" · lote ", " · batch ") + m.app.shortID(j.ID)
	}
	if canceled {
		summary += m.app.Service.L(" · cancelado", " · canceled")
	}
	if failed > 0 && j.Version != 2 {
		summary += m.app.Service.L(" · /retry reintenta las fallidas", " · /retry retries the failed ones")
	}
	if j.Version == 2 {
		body = append(body, m.app.stDim.Render(m.app.Service.L("Los pasos y salidas completos solo existen durante la sesi\u00f3n original.", "Full steps and output only exist during the original session.")))
	}
	body = append(body, m.app.stDim.Render(summary))
	return m.app.tree(head, body...)
}
func (app *Interface) resultLine(r core.Result, w int) string {
	if r.OK {
		return app.stOK.Render("✓") + " " + app.pad(r.Alias, w) + " " + app.stDim.Render(app.fmtMS(r.DurationMS)) + app.attempts(r)
	}
	msg := strings.TrimSpace(r.Error)
	if i := strings.IndexAny(msg, "\r\n"); i >= 0 {
		msg = msg[:i]
	}
	return app.stErr.Render("✗") + " " + app.pad(r.Alias, w) + " " + app.stErr.Render(msg) + app.attempts(r)
}

// attempts marks a result with its retries (↻) and the failed steps it went past (!).
func (app *Interface) attempts(r core.Result) string {
	out := ""
	if r.Retries > 0 {
		out += " " + app.stDim.Render(fmt.Sprintf("↻%d", r.Retries))
	}
	if r.Warnings > 0 {
		out += " " + app.stWarn.Render(fmt.Sprintf("!%d", r.Warnings))
	}
	return out
}
func (app *Interface) showOutput(steps []core.Spec) bool {
	for _, s := range steps {
		switch s.Action {
		case "shell", "app-list", "screenshot", "push", "apk-install", "pull", "screenrecord", "logcat":
			return true
		}
	}
	return false
}
func (app *Interface) outputLines(out string) []string {
	var lines []string
	for _, l := range strings.Split(strings.TrimRight(out, "\r\n"), "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		if filepath.IsAbs(l) {
			l = app.relPath(l)
		}
		lines = append(lines, l)
	}
	return lines
}
func (m *tuiModel) renderDoctor(ver string, verErr error, d []core.Device, e error) string {
	ok := func(good bool, label, detail string) string {
		mark := m.app.stOK.Render("✓")
		if !good {
			mark = m.app.stErr.Render("✗")
		}
		return mark + " " + m.app.pad(label, 10) + " " + m.app.stDim.Render(detail)
	}
	exists := func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	}
	var body []string
	if verErr != nil {
		body = append(body, ok(false, "adb", verErr.Error()))
	} else {
		body = append(body, ok(true, "adb", strings.TrimSpace(strings.SplitN(ver, "\n", 2)[0])+" · "+m.app.parseADBVersion(ver)))
	}
	body = append(body, ok(exists(m.app.Service.Scrcpy), "scrcpy", m.app.relPath(m.app.Service.Scrcpy)))
	body = append(body, ok(exists(filepath.Join(m.app.Service.ToolDir, "scrcpy-server")), m.app.Service.L("servidor", "server"), m.app.relPath(filepath.Join(m.app.Service.ToolDir, "scrcpy-server"))))
	key := m.app.Service.CxtKeyPath()
	body = append(body, ok(exists(key), m.app.Service.L("clave CXT", "CXT key"), m.app.relPath(key)))
	if e != nil {
		body = append(body, ok(false, m.app.Service.L("placas", "devices"), e.Error()))
		return m.app.tree(m.app.head(m.app.stErr, glyphFail, m.app.Service.L("Diagnóstico", "Diagnosis")), body...)
	}
	count := map[string]int{}
	var unauth []core.Device
	for _, v := range d {
		count[v.State]++
		if v.State == "unauthorized" {
			unauth = append(unauth, v)
		}
	}
	body = append(body, ok(count["device"] > 0, m.app.Service.L("placas", "devices"), fmt.Sprintf(m.app.Service.L("%d conectadas · %d sin autorizar · %d offline · %d desconectadas", "%d connected · %d unauthorized · %d offline · %d disconnected"), count["device"], count["unauthorized"], count["offline"], count["disconnected"])))
	// The only place unavailable devices are named: the lists leave them out.
	var off []string
	for _, v := range d {
		if v.State != "device" {
			name := v.Alias
			if name == "" {
				name = v.Serial
			}
			off = append(off, name+" "+m.app.stateLabel(v.State))
		}
	}
	if len(off) > 0 {
		body = append(body, m.app.stDim.Render(m.app.Service.L("No disponibles: ", "Unavailable: ")+strings.Join(off, " · ")))
	}
	// iPhones being activated are not registered: counted by what they wait for.
	if pending := m.app.Service.PendingIOS(); len(pending) > 0 {
		count, order := map[string]int{}, []string{}
		for _, v := range pending {
			if count[v.Message] == 0 {
				order = append(order, v.Message)
			}
			count[v.Message]++
		}
		var steps []string
		for _, msg := range order {
			steps = append(steps, fmt.Sprintf("%d: %s", count[msg], msg))
		}
		body = append(body, m.app.stWarn.Render(fmt.Sprintf(m.app.Service.L("iPhone por activar (%d) · ", "iPhones to activate (%d) · "), len(pending))+strings.Join(steps, " · ")))
	}
	var alerts []string
	read := 0
	for _, v := range d {
		if _, known := m.health[v.Serial]; known && v.State == "device" {
			read++
			if a := m.healthAlerts(v.Serial); len(a) > 0 {
				alerts = append(alerts, m.app.stWarn.Render("! "+v.Alias+" · "+strings.Join(a, " · ")))
			}
		}
	}
	if read > 0 {
		detail := fmt.Sprintf(m.app.Service.L("%s sin avisos", "%s without alerts"), m.app.plural(read, m.app.Service.L("placa", "device"), m.app.Service.L("placas", "devices")))
		if len(alerts) > 0 {
			detail = fmt.Sprintf(m.app.Service.L("%d de %d con avisos", "%d of %d with alerts"), len(alerts), read)
		}
		body = append(body, ok(len(alerts) == 0, m.app.Service.L("salud", "health"), detail))
		body = append(body, alerts...)
	}
	if len(unauth) > 0 {
		body = append(body, m.app.stWarn.Render(m.app.Service.L("! Sin autorizar: ", "! Unauthorized: ")+m.app.aliasList(unauth, 8)), m.app.stDim.Render(m.app.Service.L("  Cierra esta interfaz y ejecuta tools\\cxt-auth\\authorize.ps1", "  Close this interface and run tools\\cxt-auth\\authorize.ps1")))
	}
	if len(unauth) > 0 || len(alerts) > 0 {
		return m.app.tree(m.app.head(m.app.stWarn, glyphWarn, m.app.Service.L("Diagnóstico", "Diagnosis")), body...)
	}
	return m.app.tree(m.app.head(m.app.stOK, glyphOK, m.app.Service.L("Diagnóstico", "Diagnosis")), body...)
}
func (m *tuiModel) renderGroups() string {
	if len(m.groups) == 0 {
		return m.app.tree(m.app.head(m.app.stAccent, glyphInfo, m.app.Service.L("Grupos", "Groups")), m.app.stDim.Render(m.app.Service.L("No hay grupos. Crea uno: /group create pruebas @DEV-001,DEV-002 o desde /devices", "No groups yet. Create one: /group create tests @DEV-001,DEV-002 or from /devices")))
	}
	var names []string
	nw := 0
	for k := range m.groups {
		names = append(names, k)
		nw = max(nw, lipgloss.Width(k)+1)
	}
	sort.Strings(names)
	var body []string
	for _, k := range names {
		var members []core.Device
		on := 0
		for _, s := range m.groups[k] {
			d, ok := m.aliasOf(s)
			if !ok {
				d = core.Device{Serial: s, Alias: s}
			}
			if d.State == "device" {
				on++
			}
			members = append(members, d)
		}
		count := fmt.Sprintf("%d/%d", on, len(members))
		st := m.app.stOK
		if on < len(members) {
			st = m.app.stWarn
		}
		body = append(body, m.app.clip(m.app.stBold.Render(m.app.pad("@"+k, nw+1))+" "+st.Render(m.app.pad(count, 6))+" "+m.app.stDim.Render(m.app.aliasList(members, 12)), m.w()-6))
	}
	return m.app.tree(m.app.head(m.app.stAccent, glyphInfo, m.app.Service.L("Grupos", "Groups")), body...)
}
func (m *tuiModel) renderHelp() string {
	order := m.app.commandGroups
	uw, hw := 0, 0
	for _, c := range m.app.tuiRegistry {
		uw = max(uw, lipgloss.Width("/"+c.name))
		hw = max(hw, lipgloss.Width(c.help))
	}
	showEx := m.w() >= uw+hw+40
	var body []string
	for i, g := range order {
		if i > 0 {
			body = append(body, "")
		}
		body = append(body, m.app.stBold.Render(m.app.groupTitle(g)))
		for _, c := range m.app.tuiRegistry {
			if c.group == g {
				line := "  " + m.app.stAccent.Render(m.app.pad("/"+c.name, uw+2))
				if showEx {
					line += m.app.pad(c.help, hw+3) + m.app.stDim.Render(c.example)
				} else {
					line += c.help
				}
				body = append(body, m.app.clip(line, m.w()-6))
			}
		}
	}
	body = append(body, "", m.app.stDim.Render(m.app.Service.L("Destino: @grupo, @DEV-001,DEV-002 o @all en la línea, o /target para toda la sesión.", "Target: @group, @DEV-001,DEV-002 or @all on the line, or /target for the whole session.")), m.app.stDim.Render(m.app.Service.L("!orden equivale a /shell orden · /help COMANDO explica uno", "!command is /shell command · /help COMMAND explains one")), m.app.stDim.Render(m.app.Service.L("No hace falta escribir todo: pon el comando y pulsa Enter, y te pregunto lo que falte.", "No need to type everything: enter the command and press Enter, and I ask for whatever is missing.")))
	return m.app.tree(m.app.head(m.app.stAccent, glyphInfo, m.app.Service.L("Comandos", "Commands")), body...)
}
func (m *tuiModel) renderSyncStatus() string {
	st := m.sync
	if st == nil || st.s == nil {
		return m.app.tree(m.app.head(m.app.stDim, glyphInfo, m.app.Service.L("Réplica", "Replica")), m.app.stDim.Render(m.app.Service.L("Sin réplica activa", "No live replica")))
	}
	info := st.s.Info()
	state := m.app.stOK.Render(m.app.Service.L("en vivo", "live"))
	if info.Paused {
		state = m.app.stWarn.Render(m.app.Service.L("en pausa", "paused"))
	}
	body := []string{fmt.Sprintf(m.app.Service.L("Sesión %s · %s · %d eventos · %s", "Session %s · %s · %d events · %s"), info.ID, state, info.Events, m.app.fmtDuration(time.Since(st.started))), m.app.Service.L("Principal ", "Master ") + m.app.stBold.Render(st.master.Alias)}
	for _, s := range info.Targets {
		if s == info.Master {
			continue
		}
		name := s
		if d, ok := m.aliasOf(s); ok {
			name = d.Alias
		}
		if err, bad := info.Errors[s]; bad {
			body = append(body, m.app.stErr.Render("✗ ")+m.app.pad(name, 9)+" "+m.app.stErr.Render(err))
		} else {
			body = append(body, m.app.stOK.Render("✓ ")+name)
		}
	}
	return m.app.tree(m.app.head(m.app.stAccent, glyphInfo, m.app.Service.L("Réplica", "Replica")), body...)
}
func (app *Interface) plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
func (app *Interface) aliasList(d []core.Device, n int) string {
	var names []string
	for i, v := range d {
		if i == n {
			names = append(names, fmt.Sprintf("+%d", len(d)-n))
			break
		}
		if v.Alias != "" {
			names = append(names, v.Alias)
		} else {
			names = append(names, v.Serial)
		}
	}
	return strings.Join(names, ", ")
}

// setDevices keeps the whole inventory, but lists and offers only the available
// devices: the rest (disconnected, unauthorized, locked...) would only get in the way.
func (m *tuiModel) setDevices(d []core.Device) {
	m.inventory = append([]core.Device(nil), d...)
	m.devices = core.Listed(d)
}

// hidden is how many known devices are not available.
func (m *tuiModel) hidden() int {
	return len(m.inventory) - len(m.devices)
}

// aliasOf names a serial from the whole inventory, available or not.
func (m *tuiModel) aliasOf(serial string) (core.Device, bool) {
	for _, d := range m.inventory {
		if d.Serial == serial || (d.Alias != "" && strings.EqualFold(d.Alias, serial)) {
			return d, true
		}
	}
	return core.Device{}, false
}
func (app *Interface) notConnected(d []core.Device) []core.Device {
	var out []core.Device
	for _, v := range d {
		if v.State != "device" {
			out = append(out, v)
		}
	}
	return out
}
func (app *Interface) describeSpec(s core.Spec) string {
	arg := func(i int) string {
		if i < len(s.Args) {
			return s.Args[i]
		}
		return ""
	}
	switch s.Action {
	case "tap":
		return fmt.Sprintf("tap %.2f %.2f", s.X, s.Y)
	case "swipe":
		return fmt.Sprintf("swipe %.2f,%.2f → %.2f,%.2f %dms", s.X, s.Y, s.X2, s.Y2, s.Duration)
	case "apk-install":
		return "apk " + filepath.Base(arg(0))
	case "app-list":
		return "apps"
	case "app-start":
		return "start " + arg(0)
	case "app-stop":
		return "stop " + arg(0)
	case "wait":
		return "wait " + arg(0) + "ms"
	case "text":
		return `text "` + app.clip(arg(0), 30) + `"`
	case "push":
		return "push " + filepath.Base(arg(0)) + " → " + arg(1)
	case "apk-uninstall":
		return "uninstall " + arg(0)
	case "app-clear":
		return "wipe " + arg(0)
	case "perm-grant", "perm-revoke":
		return "perm " + strings.TrimPrefix(s.Action, "perm-") + " " + arg(0) + " " + strings.TrimPrefix(arg(1), "android.permission.")
	case "screenrecord":
		return "record " + arg(0) + "s"
	}
	return app.clip(strings.TrimSpace(s.Action+" "+strings.Join(s.Args, " ")), 50)
}
func (app *Interface) describeSteps(steps []core.Spec) string {
	if len(steps) == 1 {
		return app.describeSpec(steps[0])
	}
	return fmt.Sprintf("%s: %s", app.plural(len(steps), app.Service.L("paso", "step"), app.Service.L("pasos", "steps")), app.stepsList(steps, 3))
}
func (app *Interface) stepsList(steps []core.Spec, n int) string {
	var out []string
	for i, s := range steps {
		if i == n {
			out = append(out, "…")
			break
		}
		out = append(out, app.describeSpec(s))
	}
	return strings.Join(out, ", ")
}
func (app *Interface) shortID(id string) string {
	if i := strings.IndexByte(id, '.'); i > 0 {
		return id[:i]
	}
	return id
}
func (app *Interface) relTime(rfc string) string {
	t, e := time.Parse(time.RFC3339, rfc)
	if e != nil {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return app.Service.L("ahora", "now")
	case d < time.Hour:
		return fmt.Sprintf(app.Service.L("hace %d min", "%d min ago"), int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf(app.Service.L("hace %d h", "%d h ago"), int(d.Hours()))
	}
	return t.Format("02/01 15:04")
}
func (app *Interface) fmtDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %02dm", int(d.Hours()), int(d.Minutes())%60)
}
func (app *Interface) fmtMS(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", float64(ms)/1000)
}
func (app *Interface) relPath(p string) string {
	if r, e := filepath.Rel(app.Service.Root, p); e == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}
