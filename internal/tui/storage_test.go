package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doplerer/deviceshell/internal/core"
)

func (app *Interface) testStorageResetConfirmation(t *testing.T) {
	app.storageFixture(t)
	m := app.newTUIModel()
	app.putStorage(t, app.Service.HistoryPath(), "keep")
	m.cmdReset(call{})
	if !m.resetPending {
		t.Fatal("reset skipped confirmation")
	}
	m.resetKey(tea.KeyMsg{Type: tea.KeyEnter})
	if _, e := os.Stat(app.Service.HistoryPath()); e != nil {
		t.Fatal(e)
	}
	m.resetKey(tea.KeyMsg{Type: tea.KeyEsc})
	if m.resetPending {
		t.Fatal("did not cancel")
	}
	m.task = &taskState{}
	m.cmdReset(call{})
	if m.resetPending {
		t.Fatal("active batch accepted")
	}
}
func (app *Interface) testStorageConfirmedReset(t *testing.T) {
	app.storageFixture(t)
	app.putStorage(t, app.Service.HistoryPath(), "erase")
	if e := app.Service.AcquireUsage(false); e != nil {
		t.Fatal(e)
	}
	m := app.newTUIModel()
	m.cmdReset(call{})
	m.resetKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("BORRAR")})
	m.resetKey(tea.KeyMsg{Type: tea.KeyEnter})
	if !m.quitting || len(m.history) > 0 || len(m.sessionJobs) > 0 {
		t.Fatal("reset left session data")
	}
	if _, e := os.Stat(app.Service.HistoryPath()); !os.IsNotExist(e) {
		t.Fatal("history survived", e)
	}
}
func (app *Interface) testStorageSessionDetails(t *testing.T) {
	app.storageFixture(t)
	m := app.newTUIModel()
	j := core.Job{ID: "live", Steps: []core.Spec{{Action: "text", Args: []string{"session-secret"}}}, Results: []core.Result{{OK: false, Output: "session-secret"}}}
	if e := app.Service.SaveJob(j); e != nil {
		t.Fatal(e)
	}
	m.sessionJobs[j.ID] = j
	m.cmdJob(call{args: []string{j.ID}})
	if !strings.Contains(m.lastFull, "session-secret") {
		t.Fatal("lost in-memory detail")
	}
	fresh := app.newTUIModel()
	fresh.cmdJob(call{args: []string{j.ID}})
	if strings.Contains(fresh.lastFull, "session-secret") {
		t.Fatal("persisted private detail")
	}
	if fresh.cmdRetry(call{args: []string{j.ID}}) != nil {
		t.Fatal("retried persisted summary")
	}
}
