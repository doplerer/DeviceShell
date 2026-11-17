package core

import (
	"os"
	"testing"
)

func overlaps(a, b Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}

func inside(r, a Rect) bool {
	return r.X >= a.X && r.Y >= a.Y && r.X+r.W <= a.X+a.W && r.Y+r.H <= a.Y+a.H
}

func TestMultiGridLayoutFillsMonitorsInOrder(t *testing.T) {
	app := New()
	f := WindowFrame{Top: 31, Side: 1, Bottom: 1}
	areas := []Rect{{0, 0, 2560, 1400}, {2560, 0, 1920, 1040}}
	if got, want := app.MultiGridLayout(7, areas[:1], 2.17, f), app.GridLayout(7, areas[0], 2.17, f); len(got) != 7 || got[3] != want[3] {
		t.Fatal("one monitor must be GridLayout", got, want)
	}
	single := app.GridLayout(60, areas[0], 2.17, f)
	for _, n := range []int{1, 20, 60, 61, 150, 300} {
		got := app.MultiGridLayout(n, areas, 2.17, f)
		if len(got) != n {
			t.Fatalf("n=%d: %d windows", n, len(got))
		}
		seenSecond := false
		for i, r := range got {
			if r.W != got[0].W || r.H != got[0].H {
				t.Fatalf("n=%d: window %d is %v, not like %v", n, i, r, got[0])
			}
			outer := Rect{r.X - f.Side, r.Y - f.Top, r.W + 2*f.Side, r.H + f.Top + f.Bottom}
			switch {
			case inside(outer, areas[0]):
				if seenSecond {
					t.Fatalf("n=%d: the first monitor must fill first", n)
				}
			case inside(outer, areas[1]):
				seenSecond = true
			default:
				t.Fatalf("n=%d: window %d outside the monitors: %v", n, i, r)
			}
			for j := range i {
				if overlaps(r, got[j]) {
					t.Fatalf("n=%d: windows %d and %d overlap", n, i, j)
				}
			}
		}
		// With room for more, two monitors never make windows smaller than one.
		if n == 60 && got[0].W < single[0].W {
			t.Fatalf("60 windows: %d wide on two monitors, %d on one", got[0].W, single[0].W)
		}
	}
	if app.MultiGridLayout(5, nil, 2.17, f) != nil {
		t.Fatal("no monitors, no layout")
	}
}

func TestWallPacksOnePerMonitor(t *testing.T) {
	app := New()
	areas := []Rect{{0, 0, 1000, 1000}, {1000, 0, 1000, 1000}}
	layout := []Rect{{10, 10, 50, 100}, {1010, 10, 50, 100}, {70, 10, 50, 100}, {1070, 10, 50, 100}, {130, 10, 50, 100}}
	w := app.StartWallPacks(layout, areas)
	defer w.Stop()
	if len(w.packs) != 2 {
		t.Fatal("packs", len(w.packs))
	}
	for i, want := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}, {0, 2}} {
		if w.pack[i] != want[0] || w.slot[i] != want[1] {
			t.Fatalf("window %d: pack %d slot %d, want %v", i, w.pack[i], w.slot[i], want)
		}
	}
	if p, slot := w.For(4); p != w.packs[0] || slot != 2 {
		t.Fatal("For", slot)
	}
}

func TestScreenAreasFromConfig(t *testing.T) {
	app := New()
	app.Root = t.TempDir()
	write := func(s string) {
		if e := os.WriteFile(app.ConfigPath(), []byte(s), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(`{}`)
	if a := app.ScreenAreas(); len(a) != 1 || a[0] != app.WorkArea() {
		t.Fatal("default: the main monitor", a)
	}
	write(`{"monitors": "all"}`)
	if a := app.ScreenAreas(); len(a) != len(app.MonitorAreas()) || len(a) == 0 {
		t.Fatal("all: every monitor", a)
	}
	write(`{"monitors": "all", "windowArea": [10, 20, 800, 600]}`)
	if a := app.ScreenAreas(); len(a) != 1 || a[0] != (Rect{10, 20, 800, 600}) {
		t.Fatal("windowArea comes first", a)
	}
}
