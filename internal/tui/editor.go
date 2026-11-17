package tui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type lineEditor struct {
	app              *Interface
	value            []rune
	pos              int
	anchor           int
	offset           int
	Width            int
	Prompt           string
	PromptStyle      lipgloss.Style
	Placeholder      string
	PlaceholderStyle lipgloss.Style
}

func (app *Interface) newLineEditor() lineEditor {
	return lineEditor{app: app, anchor: -1, Width: 40}
}
func (e *lineEditor) Value() string {
	return string(e.value)
}
func (e *lineEditor) Position() int {
	return e.pos
}
func (e *lineEditor) SetValue(s string) {
	e.value = []rune(e.app.sanitizeLine(s))
	e.pos = min(e.pos, len(e.value))
	e.offset = 0
	e.anchor = -1
}
func (e *lineEditor) SetCursor(i int) {
	e.pos = max(0, min(i, len(e.value)))
	e.anchor = -1
}
func (e *lineEditor) CursorEnd() {
	e.SetCursor(len(e.value))
}
func (e *lineEditor) HasSelection() bool {
	return e.anchor >= 0 && e.anchor != e.pos
}
func (e *lineEditor) selection() (int, int) {
	return min(e.anchor, e.pos), max(e.anchor, e.pos)
}
func (e *lineEditor) SelectedText() string {
	if !e.HasSelection() {
		return ""
	}
	a, b := e.selection()
	return string(e.value[a:b])
}
func (e *lineEditor) ClearSelection() {
	e.anchor = -1
}
func (e *lineEditor) SelectAll() {
	e.anchor, e.pos = 0, len(e.value)
}
func (e *lineEditor) deleteSelection() {
	a, b := e.selection()
	e.value = append(e.value[:a:a], e.value[b:]...)
	e.pos, e.anchor = a, -1
}
func (e *lineEditor) insert(s string) {
	if e.HasSelection() {
		e.deleteSelection()
	}
	e.anchor = -1
	r := []rune(e.app.sanitizeLine(s))
	if len(e.value)+len(r) > 8192 {
		r = r[:max(0, 8192-len(e.value))]
	}
	e.value = append(e.value[:e.pos], append(r, e.value[e.pos:]...)...)
	e.pos += len(r)
}

// sanitizeLine keeps pasted text on one line, so a multi-line paste never submits a half command.
func (app *Interface) sanitizeLine(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(s)
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
func (app *Interface) isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-'
}
func (e *lineEditor) wordLeft() int {
	i := e.pos
	for i > 0 && !e.app.isWordRune(e.value[i-1]) {
		i--
	}
	for i > 0 && e.app.isWordRune(e.value[i-1]) {
		i--
	}
	return i
}
func (e *lineEditor) wordRight() int {
	i := e.pos
	for i < len(e.value) && !e.app.isWordRune(e.value[i]) {
		i++
	}
	for i < len(e.value) && e.app.isWordRune(e.value[i]) {
		i++
	}
	return i
}

// move places the cursor; with extend it grows the selection from where it started.
func (e *lineEditor) move(to int, extend bool) {
	if extend {
		if e.anchor < 0 {
			e.anchor = e.pos
		}
	} else {
		e.anchor = -1
	}
	e.pos = max(0, min(to, len(e.value)))
}
func (e *lineEditor) Update(msg tea.Msg) tea.Cmd {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch k.String() {
	case "left":
		if e.HasSelection() {
			a, _ := e.selection()
			e.move(a, false)
		} else {
			e.move(e.pos-1, false)
		}
	case "right":
		if e.HasSelection() {
			_, b := e.selection()
			e.move(b, false)
		} else {
			e.move(e.pos+1, false)
		}
	case "shift+left":
		e.move(e.pos-1, true)
	case "shift+right":
		e.move(e.pos+1, true)
	case "ctrl+left", "alt+left", "alt+b":
		e.move(e.wordLeft(), false)
	case "ctrl+right", "alt+right", "alt+f":
		e.move(e.wordRight(), false)
	case "ctrl+shift+left":
		e.move(e.wordLeft(), true)
	case "ctrl+shift+right":
		e.move(e.wordRight(), true)
	case "home", "ctrl+home":
		e.move(0, false)
	case "end", "ctrl+end", "ctrl+e":
		e.move(len(e.value), false)
	case "shift+home", "ctrl+shift+home":
		e.move(0, true)
	case "shift+end", "ctrl+shift+end":
		e.move(len(e.value), true)
	case "ctrl+a":
		e.SelectAll()
	case "backspace", "ctrl+h":
		if e.HasSelection() {
			e.deleteSelection()
		} else if e.pos > 0 {
			e.anchor = e.pos - 1
			e.deleteSelection()
		}
	case "delete", "ctrl+d":
		if e.HasSelection() {
			e.deleteSelection()
		} else if e.pos < len(e.value) {
			e.anchor = e.pos + 1
			e.deleteSelection()
		}
	case "ctrl+w", "alt+backspace":
		if !e.HasSelection() {
			e.anchor = e.wordLeft()
		}
		e.deleteSelection()
	case "alt+delete", "alt+d":
		if !e.HasSelection() {
			e.anchor = e.wordRight()
		}
		e.deleteSelection()
	case "ctrl+u":
		e.anchor = 0
		e.deleteSelection()
	case "ctrl+k":
		e.anchor = len(e.value)
		e.deleteSelection()
	case "ctrl+x":
		if e.HasSelection() {
			e.app.clipboardWrite(e.SelectedText())
			e.deleteSelection()
		}
	case "ctrl+v":
		if s, err := e.app.clipboardRead(); err == nil {
			e.insert(s)
		}
	default:
		if k.Type == tea.KeyRunes || k.Type == tea.KeySpace {
			e.insert(string(k.Runes))
		}
	}
	return nil
}

// View draws prompt and text; the visible window follows the cursor and the selection is highlighted.
func (e *lineEditor) View() string {
	prompt := e.PromptStyle.Render(e.Prompt)
	width := max(1, e.Width)
	if len(e.value) == 0 {
		ph := []rune(e.Placeholder)
		if len(ph) == 0 {
			return prompt + e.app.stCursor.Render(" ")
		}
		return prompt + e.app.stCursor.Render(string(ph[0])) + e.PlaceholderStyle.Render(e.app.clip(string(ph[1:]), width-1))
	}
	cell := func(r rune) int {
		return max(1, lipgloss.Width(string(r)))
	}
	span := func(from int) int {
		used := 1
		for _, r := range e.value[from:e.pos] {
			used += cell(r)
		}
		return used
	}
	e.offset = min(e.offset, e.pos)
	for e.offset < e.pos && span(e.offset) > width {
		e.offset++
	}
	for e.offset > 0 && span(e.offset-1) <= width {
		e.offset--
	}
	a, b := -1, -1
	if e.HasSelection() {
		a, b = e.selection()
	}
	var out strings.Builder
	used := 0
	for i := e.offset; i < len(e.value) && used+cell(e.value[i]) <= width; i++ {
		s := string(e.value[i])
		switch {
		case i == e.pos:
			s = e.app.stCursor.Render(s)
		case i >= a && i < b:
			s = e.app.stSelection.Render(s)
		}
		out.WriteString(s)
		used += cell(e.value[i])
	}
	if e.pos == len(e.value) && used < width {
		out.WriteString(e.app.stCursor.Render(" "))
	}
	return prompt + out.String()
}
