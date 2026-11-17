package tui

import (
	"time"

	"github.com/doplerer/deviceshell/internal/core"
)

func (m *tuiModel) applySettings(c core.AppSettings) {
	m.parallel = c.Parallel
	m.timeout, _ = time.ParseDuration(c.Timeout)
	m.alertTemp, m.alertBattery = c.AlertTemp, c.AlertBattery
}
