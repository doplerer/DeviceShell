package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// legacyLayout maps the Spanish names of installations made before the English
// layout (paths.go) to the current ones, relative to Service.Root. Parents come
// before their children, so each entry is resolved inside the already renamed folder.
var legacyLayout = [][2]string{
	{"datos", dataDir},
	{"data/inventario.json", "data/inventory.json"},
	{"data/grupos.json", "data/groups.json"},
	{"data/historial-tui.txt", "data/tui-history.txt"},
	{"rutinas", routinesDir},
	{"salidas", outputDir},
	{"output/registros", "output/logs"},
	{"output/logs/sesiones", "output/logs/sessions"},
	{"output/capturas", "output/screenshots"},
	{"output/descargas", "output/downloads"},
	{"herramientas", toolsDir},
	{"tools/instalacion.json", "tools/install.json"},
	{"tools/cxt-auth/autorizar.ps1", "tools/cxt-auth/authorize.ps1"},
	{"tools/ios/setup/_controlador-quicktime.json", "tools/ios/setup/_quicktime-driver.json"},
}

// MigrateLayout renames the folders and files of an older installation to the
// current layout. It only renames when the old name exists and the new one does
// not, so it is safe to run on every start. When both exist it leaves them alone
// and reports the conflict, because merging them could lose data.
func (app *Service) MigrateLayout() error {
	if e := app.removeLegacyLocks(); e != nil {
		return e
	}
	var conflicts []string
	for _, p := range legacyLayout {
		from, to := filepath.Join(app.Root, filepath.FromSlash(p[0])), filepath.Join(app.Root, filepath.FromSlash(p[1]))
		if _, e := os.Lstat(from); e != nil {
			continue
		}
		if _, e := os.Lstat(to); e == nil {
			conflicts = append(conflicts, p[0])
			continue
		}
		if e := os.Rename(from, to); e != nil {
			return fmt.Errorf("%s: %w", app.L("no se puede actualizar la estructura de carpetas; cierra las demás instancias de DeviceShell", "cannot update the folder layout; close other DeviceShell instances"), e)
		}
	}
	if e := app.migrateAliases(); e != nil {
		return e
	}
	if e := app.migrateConfigKeys(); e != nil {
		return e
	}
	if len(conflicts) > 0 {
		return &LayoutConflictError{Paths: conflicts}
	}
	return nil
}

// legacyLocks are the lock files of versions released as BOTFARM. Windows refuses
// to delete a lock file another process holds open, so a removal that fails means
// an older instance is still running on this installation.
var legacyLocks = []string{".botfarm.lock", ".botfarm-store.lock"}

func (app *Service) removeLegacyLocks() error {
	for _, name := range legacyLocks {
		p := filepath.Join(app.Root, name)
		if _, e := os.Lstat(p); e != nil {
			continue
		}
		if e := os.Remove(p); e != nil {
			return fmt.Errorf("%s: %w", app.L("cierra las demás instancias de DeviceShell o BOTFARM", "close other DeviceShell or BOTFARM instances"), e)
		}
	}
	return nil
}

// legacyAlias matches the aliases older versions generated (BOT-001, BOT-002…).
var legacyAlias = regexp.MustCompile(`^BOT-(\d+)$`)

// migrateAliases renames generated aliases from BOT-NNN to DEV-NNN, keeping the
// number. Aliases the user chose are kept, and so is a BOT-NNN whose DEV-NNN is
// already taken. Groups store serials, so they need no change.
func (app *Service) migrateAliases() error {
	p := app.InventoryPath()
	if _, e := os.Stat(p); e != nil {
		return nil
	}
	devices, e := app.LoadInventory(p)
	if e != nil {
		return nil // MergeInventory reports an invalid inventory with its own message.
	}
	taken := map[string]bool{}
	for _, d := range devices {
		taken[d.Alias] = true
	}
	changed := false
	for i, d := range devices {
		m := legacyAlias.FindStringSubmatch(d.Alias)
		if m == nil || taken[aliasPrefix+m[1]] {
			continue
		}
		devices[i].Alias = aliasPrefix + m[1]
		taken[devices[i].Alias] = true
		changed = true
	}
	if !changed {
		return nil
	}
	return app.WriteJSON(p, devices)
}

// legacyConfigKeys maps the Spanish keys of older config.json files to the current ones.
var legacyConfigKeys = [][2]string{{"areaVentanas", "windowArea"}, {"monitores", "monitors"}}

// migrateConfigKeys renames the Spanish keys of config.json, unless the English key is
// already there, and turns "monitores": "todos" into "monitors": "all".
func (app *Service) migrateConfigKeys() error {
	raw := map[string]json.RawMessage{}
	if e := app.ReadJSON(app.ConfigPath(), &raw); e != nil || raw == nil {
		return nil // ReadSettings reports a missing or invalid config.json.
	}
	changed := false
	for _, k := range legacyConfigKeys {
		v, old := raw[k[0]]
		if !old {
			continue
		}
		delete(raw, k[0])
		changed = true
		if _, current := raw[k[1]]; current {
			continue
		}
		if k[1] == "monitors" && strings.EqualFold(strings.Trim(string(v), `"`), "todos") {
			v = json.RawMessage(`"all"`)
		}
		raw[k[1]] = v
	}
	if !changed {
		return nil
	}
	return app.WriteJSON(app.ConfigPath(), raw)
}

// LayoutConflictError lists old paths that were kept because their new name
// already exists. The installation still works with the new paths.
type LayoutConflictError struct{ Paths []string }

func (e *LayoutConflictError) Error() string {
	return fmt.Sprintf("old layout paths kept because the new ones exist: %v", e.Paths)
}

// IsLayoutConflict tells a warning from MigrateLayout apart from a failure.
func IsLayoutConflict(e error) bool {
	var c *LayoutConflictError
	return errors.As(e, &c)
}
