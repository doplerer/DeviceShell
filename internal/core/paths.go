package core

import (
	"os"
	"path/filepath"
)

// Layout of an installation under Service.Root. Every folder and file name the
// program uses lives here; layout_migration.go maps the names of older versions.
//
//	config.json
//	data/      inventory.json, groups.json, tui-history.txt, locks
//	routines/  *.json
//	output/    logs/{jobs,sessions}, screenshots/, downloads/, logcat/
//	tools/     install.json, scrcpy, cxt-auth/, ios/
const (
	configFile    = "config.json"
	dataDir       = "data"
	routinesDir   = "routines"
	outputDir     = "output"
	toolsDir      = "tools"
	logsDir       = "logs"
	jobsDir       = "jobs"
	sessionsDir   = "sessions"
	screenshotDir = "screenshots"
	downloadsDir  = "downloads"
	logcatDir     = "logcat"
)

func (app *Service) path(base []string, parts []string) string {
	return filepath.Join(append(append([]string{app.Root}, base...), parts...)...)
}

func (app *Service) ConfigPath() string              { return filepath.Join(app.Root, configFile) }
func (app *Service) DataPath(parts ...string) string { return app.path([]string{dataDir}, parts) }
func (app *Service) RoutinesPath(parts ...string) string {
	return app.path([]string{routinesDir}, parts)
}
func (app *Service) OutputPath(parts ...string) string { return app.path([]string{outputDir}, parts) }
func (app *Service) ToolsPath(parts ...string) string  { return app.path([]string{toolsDir}, parts) }
func (app *Service) LogsPath(parts ...string) string {
	return app.path([]string{outputDir, logsDir}, parts)
}
func (app *Service) JobsPath(parts ...string) string {
	return app.path([]string{outputDir, logsDir, jobsDir}, parts)
}
func (app *Service) SessionsPath(parts ...string) string {
	return app.path([]string{outputDir, logsDir, sessionsDir}, parts)
}
func (app *Service) CapturesPath(parts ...string) string {
	return app.path([]string{outputDir, screenshotDir}, parts)
}
func (app *Service) DownloadsPath(parts ...string) string {
	return app.path([]string{outputDir, downloadsDir}, parts)
}
func (app *Service) LogcatPath(parts ...string) string {
	return app.path([]string{outputDir, logcatDir}, parts)
}

func (app *Service) InventoryPath() string       { return app.DataPath("inventory.json") }
func (app *Service) GroupsPath() string          { return app.DataPath("groups.json") }
func (app *Service) HistoryPath() string         { return app.DataPath("tui-history.txt") }
func (app *Service) InstallManifestPath() string { return app.ToolsPath("install.json") }
func (app *Service) CxtKeyPath() string          { return app.ToolsPath("cxt-auth", "adbkey") }
func (app *Service) CxtAuthScriptPath() string   { return app.ToolsPath("cxt-auth", "authorize.ps1") }
func (app *Service) IOSToolsPath(parts ...string) string {
	return app.ToolsPath(append([]string{"ios"}, parts...)...)
}

func (app *Service) EnsureLayout() error {
	for _, p := range []string{app.DataPath(), app.RoutinesPath(), app.JobsPath(), app.SessionsPath(), app.CapturesPath(), app.DownloadsPath(), app.LogcatPath()} {
		if e := os.MkdirAll(p, 0755); e != nil {
			return e
		}
	}
	return nil
}

// Getenv reads DEVICESHELL_<name>, falling back to the BOTFARM_<name> spelling of
// installations from before the rename.
func Getenv(name string) string {
	if v := os.Getenv("DEVICESHELL_" + name); v != "" {
		return v
	}
	return os.Getenv("BOTFARM_" + name)
}
