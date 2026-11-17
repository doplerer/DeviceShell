package core

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if e := os.MkdirAll(filepath.Dir(p), 0o755); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte(body), 0o644); e != nil {
		t.Fatal(e)
	}
}

func readTestFile(t *testing.T, p string) string {
	t.Helper()
	b, e := os.ReadFile(p)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}

func TestMigrateLayoutRenamesLegacyInstallation(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	for rel, body := range map[string]string{
		"datos/inventario.json":               "[]",
		"datos/grupos.json":                   "{}",
		"datos/historial-tui.txt":             "/devices",
		"rutinas/preparacion.json":            "[]",
		"salidas/registros/jobs/a.json":       "{}",
		"salidas/registros/sesiones/s.json":   "{}",
		"salidas/capturas/a.png":              "png",
		"salidas/descargas/BOT/f.txt":         "f",
		"salidas/logcat/l.txt":                "l",
		"herramientas/instalacion.json":       `{"Asset":"scrcpy.zip"}`,
		"herramientas/cxt-auth/adbkey":        "key",
		"herramientas/cxt-auth/autorizar.ps1": "ps",
	} {
		writeTestFile(t, app.Root, rel, body)
	}
	if e := app.MigrateLayout(); e != nil {
		t.Fatal(e)
	}
	for p, want := range map[string]string{
		app.InventoryPath():                  "[]",
		app.GroupsPath():                     "{}",
		app.HistoryPath():                    "/devices",
		app.RoutinesPath("preparacion.json"): "[]",
		app.JobsPath("a.json"):               "{}",
		app.SessionsPath("s.json"):           "{}",
		app.CapturesPath("a.png"):            "png",
		app.DownloadsPath("BOT", "f.txt"):    "f",
		app.LogcatPath("l.txt"):              "l",
		app.InstallManifestPath():            `{"Asset":"scrcpy.zip"}`,
		app.CxtKeyPath():                     "key",
		app.CxtAuthScriptPath():              "ps",
	} {
		if got := readTestFile(t, p); got != want {
			t.Fatalf("%s = %q, want %q", p, got, want)
		}
	}
	for _, old := range []string{"datos", "rutinas", "salidas", "herramientas"} {
		if _, e := os.Stat(filepath.Join(app.Root, old)); !os.IsNotExist(e) {
			t.Fatalf("%s still exists", old)
		}
	}
	// A second run finds nothing to do.
	if e := app.MigrateLayout(); e != nil {
		t.Fatal(e)
	}
}

func TestMigrateLayoutKeepsBothWhenNewExists(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	writeTestFile(t, app.Root, "datos/inventario.json", "old")
	writeTestFile(t, app.Root, "data/inventory.json", "new")
	e := app.MigrateLayout()
	if !IsLayoutConflict(e) {
		t.Fatalf("want a conflict, got %v", e)
	}
	if readTestFile(t, filepath.Join(app.Root, "datos", "inventario.json")) != "old" || readTestFile(t, app.InventoryPath()) != "new" {
		t.Fatal("conflicting paths must be left untouched")
	}
}

func TestMigrateLayoutRenamesBotfarmAliasesAndLocks(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	writeTestFile(t, app.Root, ".botfarm.lock", "")
	writeTestFile(t, app.Root, ".botfarm-store.lock", "")
	writeTestFile(t, app.Root, "data/inventory.json", `[
		{"Serial": "A", "Alias": "BOT-001"},
		{"Serial": "B", "Alias": "Box-7"},
		{"Serial": "C", "Alias": "BOT-012"},
		{"Serial": "D", "Alias": "BOT-003"},
		{"Serial": "E", "Alias": "DEV-003"}
	]`)
	if e := app.MigrateLayout(); e != nil {
		t.Fatal(e)
	}
	for _, name := range legacyLocks {
		if _, e := os.Stat(filepath.Join(app.Root, name)); !os.IsNotExist(e) {
			t.Fatalf("%s kept: %v", name, e)
		}
	}
	devices, e := app.LoadInventory(app.InventoryPath())
	if e != nil {
		t.Fatal(e)
	}
	want := map[string]string{"A": "DEV-001", "B": "Box-7", "C": "DEV-012", "D": "BOT-003", "E": "DEV-003"}
	for _, d := range devices {
		if d.Alias != want[d.Serial] {
			t.Fatalf("%s: alias %q, want %q", d.Serial, d.Alias, want[d.Serial])
		}
	}
	// New devices continue after the highest migrated number.
	merged, e := app.MergeInventory(devices, []Device{{Serial: "F", State: "device"}}, "now")
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range merged {
		if d.Serial == "F" && d.Alias != "DEV-013" {
			t.Fatalf("new device got %q, want DEV-013", d.Alias)
		}
	}
	// A second run changes nothing.
	before := readTestFile(t, app.InventoryPath())
	if e := app.MigrateLayout(); e != nil {
		t.Fatal(e)
	}
	if after := readTestFile(t, app.InventoryPath()); after != before {
		t.Fatal("migration is not idempotent")
	}
}

func TestGetenvFallsBackToBotfarmNames(t *testing.T) {
	t.Setenv("DEVICESHELL_ROOT", "")
	t.Setenv("BOTFARM_ROOT", `C:\old`)
	if got := Getenv("ROOT"); got != `C:\old` {
		t.Fatalf("fallback: %q", got)
	}
	t.Setenv("DEVICESHELL_ROOT", `C:\new`)
	if got := Getenv("ROOT"); got != `C:\new` {
		t.Fatalf("current name: %q", got)
	}
}

func TestMigrateLayoutRenamesSpanishConfigKeys(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	writeTestFile(t, app.Root, "config.json", `{"language": "es", "areaVentanas": [0, 0, 800, 600], "monitores": "todos"}`)
	if e := app.MigrateLayout(); e != nil {
		t.Fatal(e)
	}
	c, raw, e := app.ReadSettings()
	if e != nil {
		t.Fatal(e)
	}
	if len(c.WindowArea) != 4 || c.WindowArea[2] != 800 || string(raw["monitors"]) != `"all"` {
		t.Fatalf("keys not migrated: %v %s", c.WindowArea, raw["monitors"])
	}
	if _, old := raw["areaVentanas"]; old {
		t.Fatal("old key kept")
	}
}
