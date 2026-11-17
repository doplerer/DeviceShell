package tui

import (
	"os"
	"path/filepath"
	"testing"
)

func (app *Interface) storageFixture(t *testing.T) {
	t.Helper()
	old := app.Root
	app.Root = t.TempDir()
	t.Cleanup(func() {
		app.ReleaseUsage()
		app.Root = old
	})
	if e := app.EnsureLayout(); e != nil {
		t.Fatal(e)
	}
}
func (app *Interface) putStorage(t *testing.T, p, text string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
}
