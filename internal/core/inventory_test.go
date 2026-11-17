package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func (app *Service) testInventoryExpansionAndIdentity(t *testing.T) {
	oldRoot := app.Root
	app.Root = t.TempDir()
	defer func() {
		app.Root = oldRoot
	}()
	if e := app.EnsureLayout(); e != nil {
		t.Fatal(e)
	}
	p := app.InventoryPath()
	var old []Device
	for _, n := range []int{0, 1, 16, 20, 24} {
		live := []Device{}
		for i := n; i > 0; i-- {
			live = append(live, Device{Serial: fmt.Sprintf("S%03d", i), State: "device", Model: "S22"})
		}
		merged, e := app.MergeInventory(old, live, "now")
		if e != nil || len(merged) != n {
			t.Fatal(n, e)
		}
		for _, v := range old {
			found := false
			for _, x := range merged {
				if v.Serial == x.Serial && v.Alias == x.Alias {
					found = true
				}
			}
			if !found {
				t.Fatal("Identity changed")
			}
		}
		if e = app.WriteJSON(p, merged); e != nil {
			t.Fatal(e)
		}
		old, e = app.LoadInventory(p)
		if e != nil || len(old) != n {
			t.Fatal("Reload", n, e)
		}
	}
	disconnected, e := app.MergeInventory(old, nil, "later")
	if e != nil || len(disconnected) != 24 {
		t.Fatal(e)
	}
	for _, d := range disconnected {
		if d.State != "disconnected" {
			t.Fatal(d)
		}
	}
	mixed, e := app.MergeInventory(disconnected, []Device{{Serial: "S001", State: "unauthorized"}, {Serial: "S002", State: "offline"}}, "later")
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range mixed {
		if d.Serial == "S001" && d.State != "unauthorized" {
			t.Fatal(d)
		}
		if d.Serial == "S002" && d.State != "offline" {
			t.Fatal(d)
		}
	}
}
func (app *Service) testInvalidInventoryPreserved(t *testing.T) {
	for _, raw := range []string{"{broken", "null"} {
		p := filepath.Join(t.TempDir(), "inventory.json")
		os.WriteFile(p, []byte(raw), 0600)
		if _, e := app.LoadInventory(p); e == nil {
			t.Fatal("Invalid inventory accepted", raw)
		}
		b, _ := os.ReadFile(p)
		if string(b) != raw {
			t.Fatal("Original changed")
		}
	}
	for _, v := range [][]Device{{{Serial: ""}}, {{Serial: "a"}, {Serial: "a"}}} {
		if _, e := app.MergeInventory(v, nil, "now"); e == nil {
			t.Fatal("Invalid records accepted")
		}
	}
}
func (app *Service) testOutputLayout(t *testing.T) {
	oldRoot := app.Root
	app.Root = t.TempDir()
	defer func() {
		app.Root = oldRoot
	}()
	if e := app.EnsureLayout(); e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{app.JobsPath(), app.SessionsPath(), app.CapturesPath()} {
		if _, e := os.Stat(p); e != nil {
			t.Fatal(e)
		}
	}
	if app.CapturesPath("a.png") != filepath.Join(app.Root, "output", "screenshots", "a.png") {
		t.Fatal("Unexpected output path")
	}
}
