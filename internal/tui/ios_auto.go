package tui

import (
	"slices"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/doplerer/deviceshell/internal/core"
)

type iosAutoDone struct{}

// iosAutoCmd prepares in the background the available iPhones each refresh finds;
// the service skips the ones already handled for this connection and never touches
// unavailable ones. It prints nothing: a device that becomes ready simply shows up
// in the list, and /doctor has the details.
func (m *tuiModel) iosAutoCmd(devices []core.Device) tea.Cmd {
	if m.iosAutoBusy || m.task != nil || !slices.ContainsFunc(devices, func(d core.Device) bool { return d.OS() == "ios" && d.State != "disconnected" }) {
		return nil
	}
	m.iosAutoBusy = true
	prepare := m.app.tuiPrepareIOS
	return func() tea.Msg {
		prepare(devices, nil)
		return iosAutoDone{}
	}
}
