package tui

import tea "github.com/charmbracelet/bubbletea"

func (m *tuiModel) cmdReset(c call) tea.Cmd {
	if len(c.flags) > 0 {
		m.printErr("/reset does not accept flags")
		return nil
	}
	if m.task != nil || m.sync != nil || m.wall != nil || m.busy != "" || m.refreshing {
		m.printErr(m.app.Service.L("Cierra lotes, pantallas y réplicas y espera a que termine la consulta.", "Close batches, screens and replicas and wait for the current query."))
		return nil
	}
	r, e := m.app.Service.ResetPreview()
	if e != nil {
		m.printErr(e)
		return nil
	}
	m.resetPending = true
	m.resetText = ""
	m.input.SetValue("")
	m.print(m.app.Service.ResetDescription(r) + "\n" + m.app.Service.L("Escribe BORRAR y pulsa Enter; Esc cancela.", "Type BORRAR and press Enter; Esc cancels."))
	return nil
}
func (m *tuiModel) resetKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc", "ctrl+c":
		m.resetPending = false
		m.resetText = ""
		m.input.SetValue("")
		m.printInfo(m.app.Service.L("Cancelado", "Canceled"))
	case "backspace":
		if len(m.resetText) > 0 {
			m.resetText = m.resetText[:len(m.resetText)-1]
		}
	case "enter":
		if m.resetText != "BORRAR" {
			m.printErr(m.app.Service.L("Escribe BORRAR exactamente.", "Type BORRAR exactly."))
			return nil
		}
		m.app.Service.ReleaseUsage()
		e := m.app.Service.AcquireUsage(true)
		m.transcript = nil
		m.history = nil
		m.sessionJobs = nil
		m.lastFull = ""
		m.lastShort = ""
		if e == nil {
			_, e = m.app.Service.EraseUsage()
		}
		if e != nil {
			m.printErr(e)
		} else {
			m.printInfo(m.app.Service.L("Datos de uso eliminados. Ajustes y rutinas conservados.", "Usage data deleted. Settings and routines kept."))
		}
		m.quitting = true
		return tea.Quit
	default:
		if k.Type == tea.KeyRunes {
			m.resetText += string(k.Runes)
			if len(m.resetText) > 32 {
				m.resetText = m.resetText[:32]
			}
		}
	}
	m.input.SetValue(m.resetText)
	return nil
}
