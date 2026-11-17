package tui

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/doplerer/deviceshell/internal/core"
)

func (app *Interface) testViewAndSyncCommands(t *testing.T) {
	m := app.testModel(t)
	var opened []core.Device
	app.tuiOpenWall = func(_ context.Context, d []core.Device, readOnly bool) (*screenWall, error) {
		opened = d
		w := &screenWall{Devices: d, Layout: []core.Rect{{X: 1, Y: 31, W: 200, H: 434}}, Cols: 2, ReadOnly: readOnly, cancel: func() {
		}, done: make(chan struct{})}
		w.alive.Store(int32(len(d)))
		return w, nil
	}
	app.typeLine(m, "/view @pruebas")
	if out := app.printed(m); len(opened) != 2 || m.wall == nil || !strings.Contains(out, "Pantallas abiertas") {
		t.Fatal(out)
	}
	if !strings.Contains(strings.Join(app.frame(m), "\n"), "/view close") {
		t.Fatal("Missing open screens status")
	}
	app.typeLine(m, "/view @all")
	if !strings.Contains(app.printed(m), "Ya hay") {
		t.Fatal("Second wall accepted")
	}
	close(m.wall.done)
	app.upd(m, wallEndMsg{m.wall})
	if m.wall != nil {
		t.Fatal("Closed wall still tracked")
	}
	var synced core.SyncOptions
	app.tuiRunSync = func(_ context.Context, o core.SyncOptions, _ func(string), _ func(*core.LiveSession)) error {
		synced = o
		return nil
	}
	app.typeLine(m, "/sync start DEV-001 DEV-002")
	if m.sync == nil || synced.Master.Alias != "DEV-001" || len(synced.Targets) != 2 {
		t.Fatal(app.printed(m))
	}
	app.upd(m, syncEndMsg{m.sync, nil})
	app.printed(m)
	app.typeLine(m, "/sync start DEV-003 DEV-001")
	if !strings.Contains(app.printed(m), "no está conectada") {
		t.Fatal("Offline master accepted")
	}
	app.typeLine(m, "/sync start DEV-001")
	if _, dest := m.formStep(); m.form == nil || dest == nil || dest.name != "Seguidoras" {
		t.Fatal("Missing followers step")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	app.typeLine(m, "/view @all")
	if m.wall == nil {
		t.Fatal("Screens did not open", app.printed(m))
	}
	m.wall.Close()
}
func (app *Interface) testViewReadOnlyFlag(t *testing.T) {
	m := app.testModel(t)
	c, e := m.parse("/view --read-only @pruebas")
	if _, ok := c.flags["read-only"]; e != nil || !ok || c.sel.Group != "pruebas" {
		t.Fatal(c.flags, c.sel, e)
	}
	var ro bool
	app.tuiOpenWall = func(_ context.Context, d []core.Device, readOnly bool) (*screenWall, error) {
		ro = readOnly
		return &screenWall{Devices: d, Layout: []core.Rect{{X: 1, Y: 31, W: 200, H: 434}}, Cols: 1, ReadOnly: readOnly, cancel: func() {
		}, done: make(chan struct{})}, nil
	}
	app.typeLine(m, "/view @all --read-only")
	if out := app.printed(m); !ro || !strings.Contains(out, "Sólo vista") {
		t.Fatal(out)
	}
}
