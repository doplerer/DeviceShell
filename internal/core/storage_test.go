package core

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func (app *Service) StorageFixture(t *testing.T) {
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
func (app *Service) PutStorage(t *testing.T, p, text string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(text), 0600); e != nil {
		t.Fatal(e)
	}
}
func (app *Service) testStorageReset(t *testing.T) {
	app.StorageFixture(t)
	kept := []string{app.ConfigPath(), app.RoutinesPath("custom.json"), app.CxtKeyPath(), filepath.Join(app.Root, "docs", "cli.md")}
	for _, p := range kept {
		app.PutStorage(t, p, "preserved")
	}
	for _, p := range []string{app.HistoryPath(), app.CapturesPath("x.png"), app.LogsPath("historial-inicial", "old.txt"), filepath.Join(app.Root, "docs", "historial", "reparacion-usb", "backup.reg")} {
		app.PutStorage(t, p, "ERASE")
	}
	r, e := app.EraseUsage()
	if e != nil || r.Files != 4 {
		t.Fatal(r, e)
	}
	for _, p := range kept {
		b, e := os.ReadFile(p)
		if e != nil || string(b) != "preserved" {
			t.Fatal(p, e)
		}
	}
	if _, e := os.Stat(filepath.Join(app.Root, "docs", "historial")); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	r, e = app.EraseUsage()
	if e != nil || r.Files != 0 {
		t.Fatal(r, e)
	}
}
func (app *Service) testStorageMigrationPrivacy(t *testing.T) {
	app.StorageFixture(t)
	j := Job{ID: "old", Steps: []Spec{{Action: "text", Args: []string{"SECRET"}}, {Action: "shell", Args: []string{"SECRET"}}}, Results: []Result{{Serial: "s", Error: "SECRET", Output: "SECRET"}}}
	if e := app.WriteJSON(app.JobPath(j.ID), j); e != nil {
		t.Fatal(e)
	}
	app.PutStorage(t, app.HistoryPath(), "/text SECRET\n/motion text SECRET\n/shell SECRET\n/unknown SECRET\n/help\n")
	app.PutStorage(t, app.LogsPath("control-x.txt"), "SECRET")
	app.PutStorage(t, app.SessionsPath("stale.json"), "SECRET")
	app.PutStorage(t, app.CapturesPath("keep.png"), "capture")
	app.PutStorage(t, filepath.Join(app.Root, "docs", "historial", "keep.txt"), "archive")
	if e := app.MigrateStorage(); e != nil {
		t.Fatal(e)
	}
	filepath.WalkDir(app.LogsPath(), func(p string, d os.DirEntry, e error) error {
		if e != nil {
			t.Fatal(e)
		}
		if !d.IsDir() {
			b, _ := os.ReadFile(p)
			if strings.Contains(string(b), "SECRET") {
				t.Fatal("Secret persisted", p)
			}
		}
		return nil
	})
	b, _ := os.ReadFile(app.HistoryPath())
	if string(b) != "/help" {
		t.Fatal(string(b))
	}
	var s Job
	if e := app.ReadJSON(app.JobPath("old"), &s); e != nil || s.Version != 2 || s.Results[0].Output != "" {
		t.Fatal(s, e)
	}
	if _, e := os.Stat(app.CapturesPath("keep.png")); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(app.Root, "docs", "historial", "keep.txt")); e != nil {
		t.Fatal(e)
	}
	if e := app.MigrateStorage(); e != nil {
		t.Fatal(e)
	}
}
func (app *Service) testStorageLimits(t *testing.T) {
	app.StorageFixture(t)
	now := time.Now()
	for i := 0; i < 105; i++ {
		p := app.JobPath(fmt.Sprintf("%03d", i))
		app.PutStorage(t, p, "{}")
		stamp := now.Add(-time.Duration(i) * time.Minute)
		os.Chtimes(p, stamp, stamp)
	}
	app.PutStorage(t, app.JobPath("expired"), "{}")
	old := now.Add(-retentionAge - time.Second)
	os.Chtimes(app.JobPath("expired"), old, old)
	if e := app.PruneReports(app.JobsPath(), now); e != nil {
		t.Fatal(e)
	}
	files, _ := filepath.Glob(app.JobsPath("*.json"))
	if len(files) != 100 {
		t.Fatal(len(files))
	}
	var h []string
	for i := 0; i < 150; i++ {
		if i%2 == 0 {
			h = append(h, "/help")
		} else {
			h = append(h, "/devices")
		}
	}
	app.PutStorage(t, app.HistoryPath(), strings.Join(h, "\n"))
	if e := app.PersistHistory("/help"); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(app.HistoryPath())
	if len(strings.Split(string(b), "\n")) != 100 {
		t.Fatal(string(b))
	}
}
func (app *Service) testStorageLockChild(t *testing.T) {
	if os.Getenv("DEVICESHELL_LOCK_TEST") == "" {
		return
	}
	app.Root = os.Getenv("DEVICESHELL_LOCK_TEST")
	if e := app.AcquireUsage(true); e != nil {
		os.Exit(23)
	}
	app.ReleaseUsage()
	os.Exit(0)
}
func (app *Service) testStorageLockAcrossProcesses(t *testing.T) {
	app.StorageFixture(t)
	if e := app.AcquireUsage(false); e != nil {
		t.Fatal(e)
	}
	run := func() error {
		c := exec.Command(os.Args[0], "-test.run=^TestStorageLockChild$")
		c.Env = append(os.Environ(), "DEVICESHELL_LOCK_TEST="+app.Root)
		return c.Run()
	}
	if e := run(); e == nil {
		t.Fatal("exclusive lock bypassed active process")
	}
	app.ReleaseUsage()
	if e := run(); e != nil {
		t.Fatal("lock not released", e)
	}
}
func (app *Service) testStorageDiagnosticBound(t *testing.T) {
	b, _ := app.NewDiagnosticTail()
	b.Write([]byte(strings.Repeat("a", 100000)))
	b.Write([]byte("tail"))
	if len(b.String()) != 65536 || !strings.HasSuffix(b.String(), "tail") {
		t.Fatal("unbounded diagnostic")
	}
}
func (app *Service) testStoragePartialFailure(t *testing.T) {
	app.StorageFixture(t)
	app.PutStorage(t, app.HistoryPath(), "locked")
	f, e := os.Open(app.HistoryPath())
	if e != nil {
		t.Fatal(e)
	}
	r, e := app.EraseUsage()
	f.Close()
	if e == nil {
		t.Skip("platform permits deleting open files")
	}
	if len(r.Failures) == 0 {
		t.Fatal("missing failure details")
	}
	if _, e = app.EraseUsage(); e != nil {
		t.Fatal("could not resume deletion", e)
	}
}
func (app *Service) testStorageResetRedirect(t *testing.T) {
	app.StorageFixture(t)
	outside := t.TempDir()
	app.PutStorage(t, filepath.Join(outside, "keep"), "keep")
	link := app.CapturesPath("link")
	if e := os.Symlink(outside, link); e != nil {
		if runtime.GOOS != "windows" {
			t.Skip("symlink unavailable", e)
		}
		if b, e := exec.Command("cmd.exe", "/c", "mklink", "/J", link, outside).CombinedOutput(); e != nil {
			t.Skip("junction unavailable", e, string(b))
		}
	}
	if _, e := app.EraseUsage(); e == nil {
		t.Fatal("followed link")
	}
	if _, e := os.Stat(filepath.Join(outside, "keep")); e != nil {
		t.Fatal(e)
	}
}
