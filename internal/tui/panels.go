package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/doplerer/deviceshell/internal/core"
)

type tuiPanel interface {
	key(m *tuiModel, k tea.KeyMsg) tea.Cmd
	view(m *tuiModel) string
}

func (m *tuiModel) panelBox(title, body, hint string) string {
	box := lipgloss.NewStyle().Border(m.app.frameBorder).BorderForeground(m.app.colAccent).Padding(0, 1).Width(m.w() - 3)
	out := box.Render(m.app.stBold.Render(title) + "\n" + body)
	for _, h := range strings.Split(hint, "\n") {
		out += "\n" + m.app.clip("  "+m.app.stDim.Render(h), m.w()-1)
	}
	return out
}

// scrollWindow keeps the cursor visible inside a list of n rows showing at most size.
func (app *Interface) scrollWindow(cursor, offset, n, size int) int {
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+size {
		offset = cursor - size + 1
	}
	return max(0, min(offset, n-size))
}

type devicesPanel struct {
	app            *Interface
	cursor, offset int
	marked         map[string]bool
	onCancel       func()
	onPick         func(core.Selection) tea.Cmd
}

func (m *tuiModel) sortedGroups() []string {
	var names []string
	for g := range m.groups {
		names = append(names, g)
	}
	sort.Strings(names)
	return names
}
func (p *devicesPanel) selected(m *tuiModel) []core.Device {
	var out []core.Device
	for _, d := range m.devices {
		if p.marked[d.Serial] {
			out = append(out, d)
		}
	}
	if len(out) == 0 && p.cursor < len(m.devices) {
		out = append(out, m.devices[p.cursor])
	}
	return out
}
func (app *Interface) selectionOf(d []core.Device) core.Selection {
	var ids []string
	for _, v := range d {
		if v.Alias != "" {
			ids = append(ids, v.Alias)
		} else {
			ids = append(ids, v.Serial)
		}
	}
	return core.Selection{Devices: strings.Join(ids, ",")}
}
func (p *devicesPanel) key(m *tuiModel, k tea.KeyMsg) tea.Cmd {
	n := len(m.devices)
	if p.onPick != nil {
		s := k.String()
		if i, e := strconv.Atoi(s); e == nil && i >= 1 && i <= len(m.sortedGroups()) {
			m.panel = nil
			return p.onPick(core.Selection{Group: m.sortedGroups()[i-1]})
		}
		switch s {
		case "g", "r", "s", "v", "p":
			return nil
		}
	}
	switch k.String() {
	case "esc", "q":
		m.panel = nil
		if p.onCancel != nil {
			p.onCancel()
		}
	case "up", "k":
		if n > 0 {
			p.cursor = (p.cursor + n - 1) % n
		}
	case "down", "j":
		if n > 0 {
			p.cursor = (p.cursor + 1) % n
		}
	case "pgup":
		p.cursor = max(0, p.cursor-10)
	case "pgdown":
		p.cursor = min(n-1, p.cursor+10)
	case " ", "x":
		if p.cursor < n {
			s := m.devices[p.cursor].Serial
			p.marked[s] = !p.marked[s]
		}
	case "a":
		all := true
		for _, d := range m.devices {
			if d.State == "device" && !p.marked[d.Serial] {
				all = false
			}
		}
		for _, d := range m.devices {
			if d.State == "device" {
				p.marked[d.Serial] = !all
			}
		}
	case "enter", "t":
		sel := p.app.selectionOf(p.selected(m))
		m.panel = nil
		if sel.Empty() {
			return nil
		}
		if p.onPick != nil {
			return p.onPick(sel)
		}
		m.target = sel
		m.print(p.app.tree(p.app.head(p.app.stOK, glyphOK, p.app.Service.L("Destino fijado", "Target set"))+p.app.stDim.Render(" · "+sel.String()), p.app.stDim.Render(p.app.Service.L("Las acciones sin @destino irán a estas placas", "Actions without @target will go to these devices"))))
	case "g":
		sel := p.app.selectionOf(p.selected(m))
		m.panel = nil
		m.input.SetValue("/group create  " + sel.String())
		m.input.SetCursor(len([]rune("/group create ")))
		m.sugs = nil
	case "r":
		if p.cursor < n {
			d := m.devices[p.cursor]
			m.panel = nil
			m.setInput("/rename " + d.Alias + " ")
			m.sugs = nil
		}
	case "s":
		sel := p.app.selectionOf(p.selected(m))
		m.panel = nil
		return m.submit("/screenshot " + sel.String())
	case "v":
		sel := p.app.selectionOf(p.selected(m))
		m.panel = nil
		return m.submit("/view " + sel.String())
	case "p":
		if p.cursor >= n {
			return nil
		}
		master := m.devices[p.cursor]
		line := "/sync start " + master.Alias
		for _, d := range p.selected(m) {
			if d.Serial != master.Serial {
				line += " " + d.Alias
			}
		}
		m.panel = nil
		return m.submit(line)
	}
	return nil
}

// healthCell shows battery and temperature in fixed columns; a value past its limit in /settings is marked with !.
func (p *devicesPanel) healthCell(m *tuiModel, v core.Device) string {
	h, ok := m.health[v.Serial]
	if !ok || v.State != "device" {
		return p.app.pad("", 12)
	}
	bat, temp, batSt, tempSt := "", "", p.app.stDim, p.app.stDim
	if h.Battery >= 0 {
		bat = fmt.Sprintf("%d%%", h.Battery)
		if h.Charging {
			bat += "⚡"
		}
		if h.LowBattery(m.alertBattery) {
			bat, batSt = "!"+bat, p.app.stWarn
		}
	}
	if h.TempC > 0 {
		temp = fmt.Sprintf("%.0f°", h.TempC)
		if h.TooHot(m.alertTemp) {
			temp, tempSt = "!"+temp, p.app.stWarn
		}
	}
	return batSt.Render(p.app.pad(bat, 7)) + " " + tempSt.Render(p.app.pad(temp, 4))
}
func (p *devicesPanel) view(m *tuiModel) string {
	d := m.devices
	// The unavailable devices are not listed; one line says how many there are.
	extra := 0
	if m.hidden() > 0 {
		extra = 1
	}
	pending := m.app.Service.PendingIOS()
	if len(pending) > 0 {
		extra++
	}
	size := max(3, min(len(d), m.h()-13-extra))
	p.offset = p.app.scrollWindow(p.cursor, p.offset, len(d), size)
	inGroups := map[string][]string{}
	var names []string
	for g := range m.groups {
		names = append(names, g)
	}
	sort.Strings(names)
	for _, g := range names {
		for _, s := range m.groups[g] {
			inGroups[s] = append(inGroups[s], "@"+g)
		}
	}
	aw, mw := 7, 6
	for _, v := range d {
		aw = max(aw, lipgloss.Width(v.Alias))
		mw = max(mw, lipgloss.Width(v.Model))
	}
	mw = min(mw, 16)
	connected, marked := 0, 0
	for _, v := range d {
		if v.State == "device" {
			connected++
		}
		if p.marked[v.Serial] {
			marked++
		}
	}
	var rows []string
	if p.offset > 0 {
		rows = append(rows, p.app.stDim.Render(fmt.Sprintf(p.app.Service.L("    ↑ %d más", "    ↑ %d more"), p.offset)))
	}
	for i := p.offset; i < min(len(d), p.offset+size); i++ {
		v := d[i]
		ptr, box, alias := "  ", p.app.stFaint.Render("[ ]"), p.app.pad(v.Alias, aw)
		serial, model := p.app.stDim.Render(p.app.pad(v.Serial, 17)), p.app.pad(v.Model+" ["+v.OS()+"]", mw+10)
		if p.marked[v.Serial] {
			box = p.app.stSel.Render("[x]")
		}
		if i == p.cursor {
			ptr, alias = p.app.stAccent.Render(glyphPointer+" "), p.app.stActive.Render(p.app.pad(v.Alias, aw))
		}
		line := ptr + box + " " + alias + "  " + serial + " " + model + "  " + p.app.pad(p.app.deviceDot(v), 16) + " " + p.healthCell(m, v) + " " + p.app.stDim.Render(strings.Join(inGroups[v.Serial], " "))
		rows = append(rows, p.app.clip(line, m.w()-7))
	}
	if rest := len(d) - p.offset - size; rest > 0 {
		rows = append(rows, p.app.stDim.Render(fmt.Sprintf(p.app.Service.L("    ↓ %d más", "    ↓ %d more"), rest)))
	}
	if n := m.hidden(); n > 0 {
		rows = append(rows, p.app.stFaint.Render(fmt.Sprintf(p.app.Service.L("    %d no disponibles · /doctor para ver por qué", "    %d unavailable · /doctor shows why"), n)))
	}
	// iPhones being activated are not in the inventory yet: only counted, with what to do.
	if len(pending) > 0 {
		rows = append(rows, p.app.clip(p.app.stWarn.Render(fmt.Sprintf(p.app.Service.L("    %d iPhone por activar · %s", "    %d iPhone(s) to activate · %s"), len(pending), pending[0].Message)), m.w()-7))
	}
	title := fmt.Sprintf(p.app.Service.L("Dispositivos · %d conectadas", "Devices · %d connected"), connected)
	if marked > 0 {
		title += p.app.stAccent.Render(fmt.Sprintf(p.app.Service.L(" · %d marcadas", " · %d marked"), marked))
	}
	if p.onPick != nil {
		var shortcuts []string
		for i, g := range m.sortedGroups() {
			if i == 9 {
				break
			}
			shortcuts = append(shortcuts, fmt.Sprintf("%s @%s (%d)", p.app.stSel.Render(strconv.Itoa(i+1)), g, len(m.groups[g])))
		}
		hint := p.app.Service.L("↑↓ mover · espacio marcar · a todas las conectadas · enter usar las marcadas (o la del cursor) · esc volver", "↑↓ move · space mark · a every connected · enter use the marked ones (or the one under the cursor) · esc back")
		if len(shortcuts) > 0 {
			hint = p.app.Service.L("grupos: ", "groups: ") + strings.Join(shortcuts, " · ") + "\n" + hint
		}
		return m.panelBox(p.app.Service.L("Elige las placas · ", "Choose devices · ")+title, strings.Join(rows, "\n"), hint)
	}
	return m.panelBox(title, strings.Join(rows, "\n"), p.app.Service.L("↑↓ mover · espacio marcar · a todas · enter fijar destino · g crear grupo · r renombrar · esc cerrar\ns capturas · v ver pantallas · p replicar (la del cursor es la principal; las marcadas, seguidoras)", "↑↓ move · space mark · a all · enter set as target · g create group · r rename · esc close\ns screenshots · v view screens · p replicate (the cursor one is the master; the marked ones follow)"))
}

type jobsPanel struct {
	app            *Interface
	cursor, offset int
}

func (p *jobsPanel) key(m *tuiModel, k tea.KeyMsg) tea.Cmd {
	n := len(m.jobs)
	switch k.String() {
	case "esc", "q":
		m.panel = nil
	case "up", "k":
		if n > 0 {
			p.cursor = (p.cursor + n - 1) % n
		}
	case "down", "j":
		if n > 0 {
			p.cursor = (p.cursor + 1) % n
		}
	case "enter":
		if p.cursor < n {
			m.panel = nil
			return m.submit("/jobs " + m.jobs[p.cursor].ID)
		}
	case "r":
		if p.cursor < n {
			m.panel = nil
			return m.submit("/retry " + m.jobs[p.cursor].ID)
		}
	}
	return nil
}
func (p *jobsPanel) view(m *tuiModel) string {
	size := max(3, min(len(m.jobs), m.h()-12))
	p.offset = p.app.scrollWindow(p.cursor, p.offset, len(m.jobs), size)
	var rows []string
	for i := p.offset; i < min(len(m.jobs), p.offset+size); i++ {
		j := m.jobs[i]
		ptr, id := "  ", p.app.shortID(j.ID)
		if i == p.cursor {
			ptr, id = p.app.stAccent.Render(glyphPointer+" "), p.app.stActive.Render(id)
		}
		ok := len(j.Results) - j.Failed()
		status := p.app.stOK.Render(p.app.pad(fmt.Sprintf("✓ %d/%d", ok, len(j.Results)), 9))
		if j.Failed() > 0 {
			status = p.app.stErr.Render(p.app.pad(fmt.Sprintf("✗ %d/%d", ok, len(j.Results)), 9))
		}
		rows = append(rows, p.app.clip(ptr+id+"  "+status+" "+p.app.stDim.Render(p.app.pad(p.app.relTime(j.Started), 12))+" "+p.app.describeSteps(j.Steps), m.w()-7))
	}
	return m.panelBox(fmt.Sprintf(p.app.Service.L("Lotes recientes · %d", "Recent batches · %d"), len(m.jobs)), strings.Join(rows, "\n"), p.app.Service.L("↑↓ mover · enter ver detalle · r reintentar fallidas · esc cerrar", "↑↓ move · enter details · r retry failed · esc close"))
}

type syncPanel struct{ app *Interface }

func (p *syncPanel) key(m *tuiModel, k tea.KeyMsg) tea.Cmd {
	if m.sync == nil {
		m.panel = nil
		return nil
	}
	s := m.sync.s
	switch k.String() {
	case "esc", "q", "enter":
		m.panel = nil
	case "p", " ":
		if s != nil {
			if s.Paused.Load() {
				s.Resume()
			} else {
				s.Pause()
			}
			m.sync.info = s.Info()
		}
	case "s", "x":
		m.panel = nil
		if s != nil {
			s.Stop()
		} else {
			m.sync.cancel()
		}
		m.printInfo(p.app.Service.L("Deteniendo réplica…", "Stopping the replica…"))
	}
	return nil
}
func (p *syncPanel) view(m *tuiModel) string {
	if m.sync == nil {
		return m.panelBox(p.app.Service.L("Réplica", "Replica"), p.app.stDim.Render(p.app.Service.L("La réplica ha terminado", "The replica has ended")), p.app.Service.L("esc cerrar", "esc close"))
	}
	body := m.renderSyncStatus()
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	}
	action := p.app.Service.L("p pausar", "p pause")
	if m.sync.info.Paused {
		action = p.app.Service.L("p reanudar", "p resume")
	}
	return m.panelBox(p.app.Service.L("Réplica en vivo", "Live replica"), body, action+p.app.Service.L(" · s detener · esc cerrar", " · s stop · esc close"))
}
