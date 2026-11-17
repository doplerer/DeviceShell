package core

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/doplerer/deviceshell/internal/filelock"
)

const retentionLimit = 100
const retentionAge = 7 * 24 * time.Hour

func (app *Service) AcquireUsage(exclusive bool) error {
	f, err := os.OpenFile(filepath.Join(app.Root, ".deviceshell.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	if err = filelock.Lock(f, exclusive); err != nil {
		f.Close()
		return errors.New(app.L("DeviceShell ocupado: cierra otras instancias y el mantenimiento.", "DeviceShell busy: close other instances and maintenance."))
	}
	app.usageLock = f
	return nil
}
func (app *Service) ReleaseUsage() {
	if app.usageLock != nil {
		filelock.Unlock(app.usageLock)
		app.usageLock.Close()
		app.usageLock = nil
	}
}
func (app *Service) WithStore(fn func() error) error {
	app.storeMu.Lock()
	defer app.storeMu.Unlock()
	f, e := os.OpenFile(filepath.Join(app.Root, ".deviceshell-store.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if e = filelock.Lock(f, true); e == nil {
			break
		}
		if time.Now().After(deadline) {
			return e
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer filelock.Unlock(f)
	return fn()
}

// Reconstruct summaries from an allowlist, never by redacting arbitrary output.
func (app *Service) SummarizeJob(j Job) Job {
	s := Job{Version: 2, ID: j.ID, Started: j.Started}
	for _, step := range j.Steps {
		name := step.Action
		if _, known := app.ActionArity()[name]; !known {
			name = "unknown"
		}
		s.Steps = append(s.Steps, Spec{Action: name, RoutinePlatform: step.RoutinePlatform})
	}
	for _, r := range j.Results {
		s.Results = append(s.Results, Result{Platform: r.Platform, ErrorCode: r.ErrorCode, Serial: r.Serial, Alias: r.Alias, OK: r.OK, DurationMS: r.DurationMS, Retries: r.Retries, Warnings: r.Warnings})
	}
	return s
}
func (app *Service) SaveJob(j Job) error {
	return app.WithStore(func() error {
		if e := app.WriteJSON(app.JobPath(j.ID), app.SummarizeJob(j)); e != nil {
			return e
		}
		return app.PruneReports(app.JobsPath(), time.Now())
	})
}
func (app *Service) SafeHistory(line string) bool {
	switch strings.TrimSpace(line) {
	case "/settings", "/devices", "/doctor", "/apps", "/jobs", "/help", "/sync", "/copy", "/clear", "/exit":
		return true
	}
	return false
}
func (app *Service) LoadHistory() []string {
	f, e := os.Open(app.HistoryPath())
	if e != nil {
		return nil
	}
	defer f.Close()
	var h []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" {
			h = append(h, l)
		}
	}
	if len(h) > 100 {
		h = h[len(h)-100:]
	}
	return h
}

func (app *Service) PersistHistory(line string) error {
	return app.WithStore(func() error {
		var lines []string
		b, e := os.ReadFile(app.HistoryPath())
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		for _, l := range strings.Split(string(b), "\n") {
			l = strings.TrimSpace(l)
			if app.SafeHistory(l) {
				lines = append(lines, l)
			}
		}
		if app.SafeHistory(line) && (len(lines) == 0 || lines[len(lines)-1] != line) {
			lines = append(lines, line)
		}
		if len(lines) > retentionLimit {
			lines = lines[len(lines)-retentionLimit:]
		}
		return app.AtomicText(app.HistoryPath(), strings.Join(lines, "\n"))
	})
}
func (app *Service) AtomicText(path, value string) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.WriteString(value)
	}
	closeErr := f.Close()
	if e != nil {
		return e
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func (app *Service) PruneReports(dir string, now time.Time) error {
	entries, e := os.ReadDir(dir)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	type report struct {
		path string
		mod  time.Time
	}
	var files []report
	for _, v := range entries {
		if v.IsDir() || !strings.HasSuffix(v.Name(), ".json") {
			continue
		}
		info, e := v.Info()
		if e != nil {
			return e
		}
		files = append(files, report{filepath.Join(dir, v.Name()), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].mod.Equal(files[j].mod) {
			return files[i].path > files[j].path
		}
		return files[i].mod.After(files[j].mod)
	})
	for i, f := range files {
		if i >= retentionLimit || now.Sub(f.mod) > retentionAge {
			if e := os.Remove(f.path); e != nil {
				return e
			}
		}
	}
	return nil
}

// Called under the exclusive usage lock before any ordinary app activity.
func (app *Service) MigrateStorage() error {
	if e := app.ValidateResetPaths(); e != nil {
		return e
	}
	marker := app.DataPath(".storage-v2")
	if _, e := os.Stat(marker); os.IsNotExist(e) {
		files, e := filepath.Glob(app.JobsPath("*.json"))
		if e != nil {
			return e
		}
		for _, p := range files {
			info, e := os.Stat(p)
			if e != nil {
				return e
			}
			var j Job
			if e = app.ReadJSON(p, &j); e != nil {
				if e = os.Remove(p); e != nil {
					return e
				}
				continue
			}
			if j.ID == "" {
				j.ID = strings.TrimSuffix(filepath.Base(p), ".json")
			}
			if e = app.WriteJSON(p, app.SummarizeJob(j)); e != nil {
				return e
			}
			if e = os.Chtimes(p, info.ModTime(), info.ModTime()); e != nil {
				return e
			}
		}
		orphans, _ := filepath.Glob(app.JobsPath(".tmp-*"))
		for _, p := range orphans {
			if e = os.Remove(p); e != nil {
				return e
			}
		}
		if e = filepath.WalkDir(app.LogsPath(), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if p == app.JobsPath() || p == app.SessionsPath() {
					return filepath.SkipDir
				}
				return nil
			}
			return os.Remove(p)
		}); e != nil && !os.IsNotExist(e) {
			return e
		}
		if e = app.PersistHistory(""); e != nil {
			return e
		}
		if e = app.AtomicText(marker, "2\n"); e != nil {
			return e
		}
	}
	entries, e := os.ReadDir(app.SessionsPath())
	if e != nil {
		return e
	}
	for _, v := range entries {
		if e = os.RemoveAll(app.SessionsPath(v.Name())); e != nil {
			return e
		}
	}
	for _, dir := range []string{app.JobsPath(), app.LogsPath("sync"), app.LogsPath("screens"), app.LogsPath("mantenimiento")} {
		if e = app.PruneReports(dir, time.Now()); e != nil {
			return e
		}
	}
	return nil
}
func (app *Service) ResetPaths() []string {
	return []string{app.DataPath(), app.OutputPath(), filepath.Join(app.Root, "docs", "historial")}
}
func (app *Service) ValidateResetPaths() error {
	abs, e := filepath.Abs(app.Root)
	if e != nil {
		return e
	}
	info, e := os.Lstat(abs)
	if e != nil {
		return e
	}
	if app.IsRedirect(info) {
		return errors.New("Root is a link")
	}
	for _, p := range append(app.ResetPaths(), filepath.Join(app.Root, "docs")) {
		e = filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			// WalkDir already checked this directory itself; an unreadable one
			// cannot be followed and must not block ordinary use.
			if errors.Is(err, fs.ErrPermission) && d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			if err != nil {
				return err
			}
			info, e := os.Lstat(path)
			if e != nil {
				return e
			}
			if app.IsRedirect(info) {
				return fmt.Errorf("Unsafe redirected path: %s", path)
			}
			return nil
		})
		if e != nil {
			return e
		}
	}
	return nil
}

type ResetReport struct {
	Files    int      `json:"files"`
	Bytes    int64    `json:"bytes"`
	Failures []string `json:"failures,omitempty"`
}

func (app *Service) ResetPreview() (ResetReport, error) {
	r := ResetReport{}
	if e := app.ValidateResetPaths(); e != nil {
		return r, e
	}
	for _, p := range app.ResetPaths() {
		e := filepath.WalkDir(p, func(path string, d fs.DirEntry, e error) error {
			if os.IsNotExist(e) {
				return nil
			}
			if errors.Is(e, fs.ErrPermission) && d != nil && d.IsDir() {
				r.Failures = append(r.Failures, path+": "+app.L("sin permiso de lectura", "not readable"))
				return filepath.SkipDir
			}
			if e != nil {
				return e
			}
			if !d.IsDir() {
				v, e := d.Info()
				if e != nil {
					return e
				}
				r.Files++
				r.Bytes += v.Size()
			}
			return nil
		})
		if e != nil {
			return r, e
		}
	}
	return r, nil
}
func (app *Service) EraseUsage() (ResetReport, error) {
	r, e := app.ResetPreview()
	if e != nil {
		return r, e
	}
	for _, p := range app.ResetPaths() {
		if e = os.RemoveAll(p); e != nil {
			r.Failures = append(r.Failures, e.Error())
		}
	}
	if e = app.EnsureLayout(); e != nil {
		r.Failures = append(r.Failures, e.Error())
	}
	if len(r.Failures) > 0 {
		return r, errors.New(strings.Join(r.Failures, "; "))
	}
	return r, nil
}
func (app *Service) ResetDescription(r ResetReport) string {
	return fmt.Sprintf(app.L("Borrar data/, output/ y docs/historial/: %d archivos, %d bytes. Se conservan ajustes, rutinas y claves CXT.", "Delete data/, output/ and docs/historial/: %d files, %d bytes. Settings, routines and CXT keys are kept."), r.Files, r.Bytes)
}
