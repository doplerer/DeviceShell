package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/doplerer/deviceshell/internal/core"
)

const defaultPrompt = "> "

type formState struct {
	cmd     *tuiCommand
	values  []string
	preset  map[string]string
	flags   map[string]string
	sel     core.Selection
	skipped bool
	err     string
}

// destSatisfied reports whether a command already knows its devices.
func (m *tuiModel) destSatisfied(name string, args []string, sel core.Selection) bool {
	return !sel.Empty() || !m.target.Empty() || (name == "sync" && len(args) > 2)
}

// incomplete reports whether a parsed line lacks required arguments or its destination.
func (m *tuiModel) incomplete(c call) bool {
	args := c.args
	if c.cmd.raw {
		args = nil
		if c.raw != "" {
			args = []string{c.raw}
		}
	}
	params, dest := m.app.commandParams(m, c.cmd.name, args)
	for i, p := range params {
		if i >= len(args) && !p.optional {
			return true
		}
	}
	return dest != nil && !m.destSatisfied(c.cmd.name, args, c.sel)
}

// submitOrGuide runs a complete line, or opens the assistant with what was typed so far.
func (m *tuiModel) submitOrGuide(line string) tea.Cmd {
	c, e := m.parse(line)
	m.setInput("")
	if c.cmd != nil && (e == nil || m.app.isUsageError(e)) && m.incomplete(c) {
		return m.startForm(c)
	}
	return m.submit(line)
}
func (m *tuiModel) startForm(c call) tea.Cmd {
	f := &formState{cmd: c.cmd, flags: c.flags, sel: c.sel, preset: map[string]string{}}
	switch {
	case c.cmd.raw:
		if c.raw != "" {
			f.values = []string{c.raw}
		}
	case c.cmd.name == "routine" && len(c.args) > 0:
		f.values = c.args[:1]
		for _, kv := range c.args[1:] {
			if k, _, ok := strings.Cut(kv, "="); ok {
				f.preset[k] = kv
			}
		}
	default:
		f.values = append([]string(nil), c.args...)
	}
	m.form = f
	return m.formAdvance()
}

// formStep returns the argument being asked, or the destination step, or neither when the form is complete.
func (m *tuiModel) formStep() (*param, *destStep) {
	f := m.form
	params, dest := m.app.commandParams(m, f.cmd.name, f.values)
	if !f.skipped && len(f.values) < len(params) {
		return &params[len(f.values)], nil
	}
	if dest != nil && !m.destSatisfied(f.cmd.name, f.values, f.sel) {
		return nil, dest
	}
	return nil, nil
}

// formAdvance moves to the next question, filling presets, and runs the command when nothing is missing.
func (m *tuiModel) formAdvance() tea.Cmd {
	f := m.form
	for {
		p, dest := m.formStep()
		if p != nil && p.kind == pRoutineVar && f.preset[p.key] != "" {
			f.values = append(f.values, f.preset[p.key])
			continue
		}
		switch {
		case p != nil:
			m.setPrompt(f.cmd.name + " " + glyphInfo + " " + p.name + " " + glyphInfo + " ")
			m.input.Placeholder = m.app.clip(p.help, m.input.Width)
			if p.def != "" {
				m.input.Placeholder = m.app.clip(m.app.Service.L("Enter: ", "Enter: ")+p.def+" · "+p.help, m.input.Width)
			}
		case dest != nil:
			m.setPrompt(f.cmd.name + " " + glyphInfo + " " + dest.name + " " + glyphInfo + " ")
			m.input.Placeholder = m.app.clip(m.app.Service.L("elige en la lista o escribe @grupo, DEV-001,DEV-002…", "pick from the list or type @group, DEV-001,DEV-002…"), m.input.Width)
		default:
			return m.formFinish()
		}
		m.setInput("")
		return nil
	}
}

// formFinish composes the full command, so the short form is visible in the history, and runs it.
func (m *tuiModel) formFinish() tea.Cmd {
	f := m.form
	parts := []string{"/" + f.cmd.name}
	for _, v := range f.values {
		if f.cmd.raw {
			parts = append(parts, v)
		} else {
			parts = append(parts, m.app.quoteArg(v))
		}
	}
	if !f.sel.Empty() {
		parts = append(parts, f.sel.String())
	}
	for k, v := range f.flags {
		if v == "" {
			parts = append(parts, "--"+k)
		} else {
			parts = append(parts, "--"+k+"="+m.app.quoteArg(v))
		}
	}
	m.endForm()
	return m.submit(strings.Join(parts, " "))
}
func (m *tuiModel) endForm() {
	m.form = nil
	m.setPrompt(defaultPrompt)
	m.input.Placeholder = m.app.clip(m.app.placeholderText(), m.input.Width)
	m.setInput("")
}
func (m *tuiModel) formEnter() tea.Cmd {
	f := m.form
	text := m.input.Value()
	if m.takeSuggestion() {
		s := m.sugs[m.sugIdx]
		switch {
		case s.pick:
			return m.openPicker(func(sel core.Selection) tea.Cmd {
				f.sel = sel
				return m.formAdvance()
			})
		case s.noSpace:
			m.applySuggestion(s)
			return nil
		}
		text = s.value
	}
	p, dest := m.formStep()
	switch {
	case p != nil:
		v, e := m.checkParam(*p, text)
		if e != nil {
			f.err = e.Error()
			return nil
		}
		if f.cmd.name == "motion" {
			f.cmd = m.app.lookupCommand("motion " + v)
			f.err = ""
			return m.formAdvance()
		}
		switch {
		case v == "" && p.optional:
			f.skipped = true
		case p.kind == pRoutineVar:
			f.values = append(f.values, p.key+"="+v)
		default:
			f.values = append(f.values, v)
		}
	case dest != nil:
		sel, e := m.destFromText(text)
		if e != nil {
			f.err = e.Error()
			return nil
		}
		f.sel = sel
	}
	f.err = ""
	return m.formAdvance()
}

// formArrow changes a number or duration being asked with the arrows; it reports whether it did.
// An empty box starts from the default or the first suggestion.
func (m *tuiModel) formArrow(s string) bool {
	step, ok := m.app.arrowStep(s)
	p, _ := m.formStep()
	if !ok || p == nil || (p.kind != pNumber && p.kind != pDuration) {
		return false
	}
	v := strings.TrimSpace(m.input.Value())
	if v == "" {
		v = p.def
		if v == "" {
			for _, c := range m.paramSuggestions(*p, m.form.values, "", true) {
				v = c.value
				break
			}
		}
	}
	if p.kind == pDuration {
		v = m.app.stepDuration(v, m.timeout, step)
	} else {
		v = m.app.stepInt(v, p.min, step, p.min, p.max)
	}
	m.setInput(v)
	m.sugHidden = true
	m.form.err = ""
	return true
}

// formBack reopens the previous answer for editing.
func (m *tuiModel) formBack() tea.Cmd {
	f := m.form
	f.err = ""
	switch {
	case f.skipped:
		f.skipped = false
	case len(f.values) == 0 && strings.HasPrefix(f.cmd.name, "motion "):
		f.cmd = m.app.lookupCommand("motion")
	case len(f.values) > 0:
		last := f.values[len(f.values)-1]
		f.values = f.values[:len(f.values)-1]
		if p, _ := m.formStep(); p != nil && p.kind == pRoutineVar {
			_, last, _ = strings.Cut(last, "=")
			delete(f.preset, p.key)
		}
		m.formAdvance()
		m.setInput(last)
		return nil
	}
	return m.formAdvance()
}
func (m *tuiModel) cancelForm() {
	m.endForm()
	m.printInfo(m.app.Service.L("Asistente cancelado", "Assistant canceled"))
}

// destFromText accepts @all, @grupo, a device or a comma list, with or without @.
func (m *tuiModel) destFromText(text string) (core.Selection, error) {
	x := strings.TrimPrefix(strings.TrimSpace(text), "@")
	if x == "" {
		return core.Selection{}, fmt.Errorf("%s", m.app.Service.L("Elige a qué placas: todas, un grupo o algunas", "Choose which devices: all, a group or some"))
	}
	if !m.resolvable(x) {
		return core.Selection{}, fmt.Errorf(m.app.Service.L("No conozco %s: elige en la lista o escribe un grupo o alias", "Unknown %s: pick from the list or type a group or alias"), x)
	}
	return m.selection([]string{x})
}

// openPicker shows the device list in pick mode; Enter hands the marked devices to done.
func (m *tuiModel) openPicker(done func(core.Selection) tea.Cmd) tea.Cmd {
	m.panel = &devicesPanel{app: m.app, marked: map[string]bool{}, onPick: done}
	return nil
}
func (m *tuiModel) setPrompt(p string) {
	m.input.Prompt = p
	m.fitInput()
}
func (m *tuiModel) fitInput() {
	m.input.Width = max(10, m.w()-4-lipgloss.Width(m.input.Prompt))
}

// formView is the card above the input: answered steps, the current one with its explanation, and the rest.
func (m *tuiModel) formView() string {
	f := m.form
	params, dest := m.app.commandParams(m, f.cmd.name, f.values)
	cur, curDest := m.formStep()
	nw := 8
	for _, p := range params {
		nw = max(nw, lipgloss.Width(p.name))
	}
	if dest != nil {
		nw = max(nw, lipgloss.Width(dest.name))
	}
	nw += 2
	rule := m.app.stFaint.Render(glyphRule)
	keys := m.app.Service.L("   enter sigue · retroceso vuelve · esc cancela", "   enter next · backspace back · esc cancel")
	if cur != nil && (cur.kind == pNumber || cur.kind == pDuration) {
		keys = m.app.Service.L("   ↑↓ ±1 · ←→ ±10 · enter sigue · retroceso vuelve · esc cancela", "   ↑↓ ±1 · ←→ ±10 · enter next · backspace back · esc cancel")
	}
	lines := []string{m.app.stSel.Render("/"+f.cmd.name) + m.app.stDim.Render(" · "+f.cmd.help+keys)}
	for i, p := range params {
		switch {
		case i < len(f.values):
			v := f.values[i]
			if p.kind == pRoutineVar {
				_, v, _ = strings.Cut(v, "=")
			}
			lines = append(lines, "  "+rule+" "+m.app.stOK.Render(glyphOK)+" "+m.app.stDim.Render(m.app.pad(p.name, nw))+v)
		case cur != nil && i == len(f.values):
			lines = append(lines, "  "+rule+" "+m.app.stSel.Render(glyphPointer+" "+m.app.pad(p.name, nw))+m.app.stAccent.Render(p.help))
		case f.skipped && p.optional:
			lines = append(lines, "  "+rule+" "+m.app.stDim.Render("– "+m.app.pad(p.name, nw)+m.app.Service.L("por defecto", "default")))
		default:
			lines = append(lines, "  "+rule+" "+m.app.stDim.Render("· "+m.app.pad(p.name, nw)+p.help))
		}
	}
	if dest != nil {
		switch {
		case curDest != nil:
			lines = append(lines, "  "+rule+" "+m.app.stSel.Render(glyphPointer+" "+m.app.pad(dest.name, nw))+m.app.stAccent.Render(dest.help))
		case !f.sel.Empty():
			lines = append(lines, "  "+rule+" "+m.app.stOK.Render(glyphOK)+" "+m.app.stDim.Render(m.app.pad(dest.name, nw))+f.sel.String())
		default:
			lines = append(lines, "  "+rule+" "+m.app.stDim.Render("· "+m.app.pad(dest.name, nw)+dest.help))
		}
	}
	if f.err != "" {
		lines = append(lines, "  "+rule+" "+m.app.stErr.Render(glyphFail+" "+f.err))
	}
	return strings.Join(lines, "\n")
}

// commandHint is the line above the input while typing: the command's shape with the current argument
// highlighted and what it means, so nobody has to remember the syntax.
func (m *tuiModel) commandHint(c *tuiCommand, prior []string, mention bool, typingMention bool) string {
	params, dest := m.app.commandParams(m, c.name, prior)
	n := len(prior)
	shape := []string{m.app.stSel.Render("/" + c.name)}
	help := ""
	for i, p := range params {
		label := p.name
		if p.optional {
			label = "[" + label + "]"
		}
		if i == n && !typingMention {
			shape = append(shape, m.app.stSel.Render("‹"+label+"›"))
			help = p.name + ": " + p.help
		} else if i < n {
			shape = append(shape, m.app.stOK.Render(label))
		} else {
			shape = append(shape, m.app.stDim.Render(label))
		}
	}
	if dest != nil && c.name != "target" {
		label := strings.ToLower(dest.name)
		switch {
		case mention:
			shape = append(shape, m.app.stOK.Render(label))
		case n >= len(params) || typingMention:
			shape = append(shape, m.app.stSel.Render("‹"+label+"›"))
			help = dest.name + ": " + dest.help + m.app.Service.L(" (elige abajo, sin escribir @)", " (pick below, no @ needed)")
		default:
			shape = append(shape, m.app.stDim.Render(label))
		}
	}
	if help == "" {
		help = c.help
	}
	return "  " + strings.Join(shape, " ") + m.app.stDim.Render("   "+help)
}
