package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doplerer/deviceshell/internal/core"
)

type settingsPanel struct {
	app                    *Interface
	cursor, offset, option int
	choosing, editing      bool
	editor                 lineEditor
	area                   []int
	field                  int
	config                 core.AppSettings
	err, notice            string
}

func (app *Interface) retiredSetting(name string) bool {
	switch strings.ToLower(strings.TrimPrefix(name, "/")) {
	case "language", "idioma", "lang", "mode", "modo", "parallel", "paralelo", "timeout", "target", "destino":
		return true
	}
	return false
}
func (m *tuiModel) cmdSettings(call) tea.Cmd {
	c, _, err := m.app.Service.ReadSettings()
	if err != nil {
		m.printErr(err)
		return nil
	}
	m.reloadGroups()
	m.panel = &settingsPanel{app: m.app, config: c}
	return nil
}
func (p *settingsPanel) labels() []string {
	return []string{p.app.Service.L("Idioma", "Language"), p.app.Service.L("Concurrencia", "Concurrency"), p.app.Service.L("Tiempo máximo", "Timeout"), p.app.Service.L("Destino por defecto", "Default target"), p.app.Service.L("Área de ventanas", "Window area"), p.app.Service.L("Clave CXT", "CXT key"), p.app.Service.L("Temperatura máx.", "Max temperature"), p.app.Service.L("Batería mín.", "Min battery"), p.app.Service.L("Vídeo iPhone por cable", "iPhone video on cable")}
}
func (p *settingsPanel) descriptions() []string {
	return []string{p.app.Service.L("Español o inglés · guardado", "Spanish or English · saved"), p.app.Service.L("Entre 1 y 64 dispositivos por lote · guardado", "1 to 64 devices per batch · saved"), p.app.Service.L("Por paso y dispositivo: 90s, 3m… · guardado", "Per step and device: 90s, 3m… · saved"), p.app.Service.L("Ninguno, todos, grupo o selección · solo sesión", "None, all, group or selection · session only"), p.app.Service.L("Se aplica a futuras aperturas", "Applies to future windows"), p.app.Service.L("Los cambios se aplican al reiniciar DeviceShell", "Changes apply after restarting DeviceShell"), p.app.Service.L("Aviso por encima de estos °C · 0 sin aviso · guardado", "Alert above these °C · 0 no alert · saved"), p.app.Service.L("Aviso por debajo de este % · 0 sin aviso · guardado", "Alert below this % · 0 no alert · saved"), p.app.Service.L("Por Wi‑Fi siempre DisplayService · se aplica al abrir la pantalla · guardado", "Over Wi‑Fi always DisplayService · applies when a screen opens · saved")}
}
func (p *settingsPanel) values(m *tuiModel) []string {
	target := m.target.String()
	if target == "" {
		target = p.app.Service.L("Ninguno", "None")
	}
	area := p.app.Service.L("Automática", "Automatic")
	if len(p.config.WindowArea) == 4 {
		a := p.config.WindowArea
		area = fmt.Sprintf("%d, %d · %d×%d", a[0], a[1], a[2], a[3])
	}
	key := p.app.Service.L("Desactivada", "Disabled")
	if p.config.UseCxtKey {
		key = p.app.Service.L("Activada", "Enabled")
	}
	if p.config.UseCxtKey != p.app.Service.StartupCxt {
		key += p.app.Service.L(" · reiniciar", " · restart")
	}
	limit := func(n int, unit string) string {
		if n == 0 {
			return p.app.Service.L("Sin aviso", "No alert")
		}
		return strconv.Itoa(n) + " " + unit
	}
	video := p.videoChoices()[0].help
	if p.config.IPhoneVideo == core.IPhoneVideoQuickTime {
		video = p.videoChoices()[1].help
	}
	return []string{p.app.Service.LanguageName(p.app.Service.Lang), strconv.Itoa(m.parallel), m.timeout.String(), target, area, key, limit(m.alertTemp, "°C"), limit(m.alertBattery, "%"), video}
}

// settingsRows is the number of rows of the panel.
const settingsRows = 9

func (p *settingsPanel) videoChoices() []choice {
	return []choice{{core.IPhoneVideoDisplayService, p.app.Service.L("DisplayService (recomendado)", "DisplayService (recommended)")}, {core.IPhoneVideoQuickTime, p.app.Service.L("QuickTime (máx. fluidez)", "QuickTime (smoothest)")}}
}
func (p *settingsPanel) choices(m *tuiModel) []choice {
	switch p.cursor {
	case 0:
		return []choice{{"es", "español"}, {"en", "English"}}
	case 3:
		out := []choice{{"none", p.app.Service.L("Ninguno", "None")}, {"all", p.app.Service.L("Todos los conectados", "Every connected device")}, {"pick", p.app.Service.L("Elegir dispositivos…", "Choose devices…")}}
		for _, g := range m.sortedGroups() {
			out = append(out, choice{"group:" + g, "@" + g})
		}
		return out
	case 4:
		return []choice{{"auto", p.app.Service.L("Automática", "Automatic")}, {"custom", p.app.Service.L("Personalizada", "Custom")}}
	case 5:
		return []choice{{"off", p.app.Service.L("Desactivada", "Disabled")}, {"on", p.app.Service.L("Activada", "Enabled")}}
	case 8:
		return p.videoChoices()
	}
	return nil
}
func (p *settingsPanel) beginEdit(value string) {
	p.choosing = false
	p.editing = true
	p.editor = p.app.newLineEditor()
	p.editor.Prompt = "› "
	p.editor.PromptStyle = p.app.stSel
	p.editor.SetValue(value)
	p.editor.CursorEnd()
	p.editor.SelectAll()
}
func (p *settingsPanel) save(m *tuiModel, key string, value any) {
	c, err := p.app.Service.SaveSetting(key, value)
	if err != nil {
		p.err = err.Error()
		return
	}
	p.config = c
	m.applySettings(c)
	if key == "language" {
		p.app.Service.Lang = c.Language
		p.app.RebuildTexts()
		m.fitInput()
		m.input.Placeholder = p.app.clip(p.app.placeholderText(), m.input.Width)
	}
	p.editing, p.choosing = false, false
	p.err = ""
	p.notice = p.app.Service.L("Guardado", "Saved")
}

// numericField says whether the value being edited is a number or a duration, which the arrows change.
func (p *settingsPanel) numericField() bool {
	switch p.cursor {
	case 1, 2, 4, 6, 7:
		return true
	}
	return false
}

// stepped is the edited value moved by step, within the limits Enter checks.
func (p *settingsPanel) stepped(m *tuiModel, step int) string {
	v := p.editor.Value()
	switch p.cursor {
	case 1:
		return p.app.stepInt(v, m.parallel, step, 1, 64)
	case 2:
		return p.app.stepDuration(v, m.timeout, step)
	case 4:
		lo := 0
		if p.field >= 2 {
			lo = 1
		}
		return p.app.stepInt(v, p.area[p.field], step, lo, 0)
	case 6:
		return p.app.stepInt(v, m.alertTemp, step, 0, 90)
	}
	return p.app.stepInt(v, m.alertBattery, step, 0, 100)
}
func (p *settingsPanel) key(m *tuiModel, k tea.KeyMsg) tea.Cmd {
	s := k.String()
	if s == "esc" || (s == "ctrl+c" && !(p.editing && p.editor.HasSelection())) {
		if p.editing || p.choosing {
			p.editing, p.choosing = false, false
			p.err = ""
		} else {
			m.panel = nil
		}
		return nil
	}
	if p.editing {
		if s == "ctrl+c" {
			m.copyText(p.editor.SelectedText(), p.app.Service.L("Selección copiada", "Selection copied"))
			return nil
		}
		if step, ok := p.app.arrowStep(s); ok && p.numericField() {
			p.editor.SetValue(p.stepped(m, step))
			p.editor.CursorEnd()
			p.err = ""
			return nil
		}
		if s != "enter" {
			return p.editor.Update(k)
		}
		value := strings.TrimSpace(p.editor.Value())
		if p.cursor == 2 {
			d, e := time.ParseDuration(value)
			if e != nil || d <= 0 {
				p.err = p.app.Service.L("Duración positiva: 90s, 3m…", "Positive duration: 90s, 3m…")
				return nil
			}
			p.save(m, "timeout", value)
			return nil
		}
		n, e := strconv.Atoi(value)
		if e != nil {
			p.err = p.app.Service.L("Introduce un entero", "Enter an integer")
			return nil
		}
		switch p.cursor {
		case 1:
			if n < 1 || n > 64 {
				p.err = p.app.Service.L("Entre 1 y 64", "Between 1 and 64")
				return nil
			}
			p.save(m, "parallel", n)
		case 6, 7:
			key, top := "alertTemp", 90
			if p.cursor == 7 {
				key, top = "alertBattery", 100
			}
			if n < 0 || n > top {
				p.err = fmt.Sprintf(p.app.Service.L("Entre 0 (sin aviso) y %d", "Between 0 (no alert) and %d"), top)
				return nil
			}
			p.save(m, key, n)
		case 4:
			if p.field >= 2 && n <= 0 {
				p.err = p.app.Service.L("La dimensión debe ser positiva", "Dimension must be positive")
				return nil
			}
			p.area[p.field] = n
			if p.field < 3 {
				p.field++
				p.err = ""
				p.beginEdit(strconv.Itoa(p.area[p.field]))
			} else {
				p.save(m, "windowArea", p.area)
			}
		}
		return nil
	}
	if p.choosing {
		choices := p.choices(m)
		switch s {
		case "up":
			p.option = (p.option + len(choices) - 1) % len(choices)
		case "down":
			p.option = (p.option + 1) % len(choices)
		case "enter":
			v := choices[p.option].value
			switch p.cursor {
			case 0:
				p.save(m, "language", v)
			case 3:
				switch v {
				case "none":
					m.target = core.Selection{}
				case "all":
					m.target = core.Selection{All: true}
				case "pick":
					p.choosing = false
					m.panel = &devicesPanel{app: p.app, marked: map[string]bool{}, onCancel: func() {
						m.panel = p
					}, onPick: func(s core.Selection) tea.Cmd {
						m.target = s
						m.panel = p
						return nil
					}}
					return nil
				default:
					m.target = core.Selection{Group: strings.TrimPrefix(v, "group:")}
				}
				p.choosing = false
				p.notice = p.app.Service.L("Aplicado · solo sesión", "Applied · session only")
			case 4:
				if v == "auto" {
					p.save(m, "windowArea", nil)
				} else {
					p.area = []int{0, 0, 1920, 1080}
					if len(p.config.WindowArea) == 4 {
						copy(p.area, p.config.WindowArea)
					} else {
						a := p.app.Service.WorkArea()
						p.area = []int{a.X, a.Y, a.W, a.H}
					}
					p.field = 0
					p.beginEdit(strconv.Itoa(p.area[0]))
				}
			case 5:
				p.save(m, "useCxtKey", v == "on")
			case 8:
				p.save(m, "iphoneVideo", v)
			}
		}
		return nil
	}
	switch s {
	case "up", "k":
		p.cursor = (p.cursor + settingsRows - 1) % settingsRows
		p.notice = ""
		p.err = ""
	case "down", "j":
		p.cursor = (p.cursor + 1) % settingsRows
		p.notice = ""
		p.err = ""
	case "enter":
		p.err = ""
		p.notice = ""
		p.option = 0
		switch p.cursor {
		case 1:
			p.beginEdit(strconv.Itoa(m.parallel))
		case 2:
			p.beginEdit(m.timeout.String())
		case 6:
			p.beginEdit(strconv.Itoa(m.alertTemp))
		case 7:
			p.beginEdit(strconv.Itoa(m.alertBattery))
		default:
			p.choosing = true
			if p.cursor == 0 && p.app.Service.Lang == "en" {
				p.option = 1
			}
			if p.cursor == 3 {
				if m.target.All {
					p.option = 1
				} else if m.target.Devices != "" {
					p.option = 2
				} else if m.target.Group != "" {
					for i, c := range p.choices(m) {
						if c.value == "group:"+m.target.Group {
							p.option = i
							break
						}
					}
				}
			}
			if p.cursor == 5 && p.config.UseCxtKey {
				p.option = 1
			}
			if p.cursor == 4 && len(p.config.WindowArea) == 4 {
				p.option = 1
			}
			if p.cursor == 8 && p.config.IPhoneVideo == core.IPhoneVideoQuickTime {
				p.option = 1
			}
		}
	}
	return nil
}
func (p *settingsPanel) view(m *tuiModel) string {
	labels := p.labels()
	title := p.app.Service.L("Ajustes", "Settings")
	var rows []string
	selected := p.cursor
	if p.editing {
		p.editor.Width = max(1, m.w()-10)
		title += " · " + labels[p.cursor]
		if p.cursor == 4 {
			title += " · " + []string{"X", "Y", p.app.Service.L("Ancho", "Width"), p.app.Service.L("Alto", "Height")}[p.field]
		}
		rows = []string{p.editor.View()}
		selected = 0
	} else if p.choosing {
		title += " · " + labels[p.cursor]
		for _, c := range p.choices(m) {
			rows = append(rows, c.help)
		}
		selected = p.option
	} else {
		vals := p.values(m)
		sections := map[int]string{0: p.app.Service.L("General", "General"), 1: p.app.Service.L("Ejecución", "Execution"), 4: p.app.Service.L("Pantallas", "Screens"), 5: p.app.Service.L("Conexión", "Connection"), 6: p.app.Service.L("Alertas", "Alerts"), 8: "iPhone"}
		for i, label := range labels {
			if section, ok := sections[i]; ok {
				rows = append(rows, p.app.stDim.Render(section))
			}
			if i == p.cursor {
				selected = len(rows)
			}
			rows = append(rows, p.app.pad(label, min(22, max(8, m.w()-14)))+" "+vals[i])
		}
	}
	size := max(1, m.h()-10)
	p.offset = p.app.scrollWindow(selected, p.offset, len(rows), size)
	end := min(len(rows), p.offset+size)
	for i := p.offset; i < end; i++ {
		rows[i] = p.app.clip("  "+rows[i], m.w()-7)
		if i == selected && !p.editing {
			rows[i] = p.app.stActive.Render(rows[i])
		}
	}
	body := strings.Join(rows[p.offset:end], "\n")
	detail := p.descriptions()[p.cursor]
	if p.err != "" {
		detail = "! " + p.err
	} else if p.notice != "" {
		detail = p.notice + " · " + detail
	}
	body += "\n" + p.app.stDim.Render(p.app.clip(detail, m.w()-7))
	hint := p.app.Service.L("↑↓ mover · enter confirmar · esc volver", "↑↓ move · enter confirm · esc back")
	if p.editing && p.numericField() {
		hint = p.app.Service.L("↑↓ ±1 · ←→ ±10 · enter guardar · esc volver", "↑↓ ±1 · ←→ ±10 · enter save · esc back")
		if p.cursor == 2 {
			hint = p.app.Service.L("↑↓ ±1s · ←→ ±10s · enter guardar · esc volver", "↑↓ ±1s · ←→ ±10s · enter save · esc back")
		}
	}
	return m.panelBox(title, body, hint)
}
