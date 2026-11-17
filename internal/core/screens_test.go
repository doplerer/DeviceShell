package core

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func (app *Service) testGridLayout(t *testing.T) {
	frame := WindowFrame{Top: 31, Side: 1, Bottom: 1}
	for _, area := range []Rect{{0, 0, 1920, 1032}, {0, 0, 1366, 728}, {100, 50, 1200, 900}} {
		for _, n := range []int{1, 2, 5, 16, 20} {
			got := app.GridLayout(n, area, 2.17, frame)
			if len(got) != n {
				t.Fatalf("%v n=%d: %d windows", area, n, len(got))
			}
			for i, r := range got {
				if r.W != got[0].W || r.H != got[0].H {
					t.Fatalf("%v n=%d: window %d differs %v vs %v", area, n, i, r, got[0])
				}
				if r.X-frame.Side < area.X || r.Y-frame.Top < area.Y || r.X+r.W+frame.Side > area.X+area.W || r.Y+r.H+frame.Bottom > area.Y+area.H {
					t.Fatalf("%v n=%d: window %d outside: %v", area, n, i, r)
				}
				for j := 0; j < i; j++ {
					o := got[j]
					if r.X-frame.Side < o.X+o.W+frame.Side && o.X-frame.Side < r.X+r.W+frame.Side && r.Y-frame.Top < o.Y+o.H+frame.Bottom && o.Y-frame.Top < r.Y+r.H+frame.Bottom {
						t.Fatalf("%v n=%d: windows %d and %d overlap", area, n, i, j)
					}
				}
			}
			for cols := 1; cols <= n; cols++ {
				rows := (n + cols - 1) / cols
				cw, ch := area.W/cols-2, area.H/rows-32
				w, h := cw, int(float64(cw)*2.17)
				if h > ch {
					h, w = ch, int(float64(ch)/2.17)
				}
				if w*h > got[0].W*got[0].H {
					t.Fatalf("%v n=%d: %d columns would be larger", area, n, cols)
				}
			}
		}
	}
	g := app.GridLayout(16, Rect{0, 0, 1920, 1032}, 2.17, frame)
	if g[1].X != g[0].X+g[0].W+2 || g[0].X != 1 || g[0].Y != 31 {
		t.Fatal(g[:2])
	}
	if app.GridLayout(0, Rect{0, 0, 100, 100}, 2, frame) != nil || app.GridLayout(3, Rect{0, 0, 2, 2}, 2, frame) != nil {
		t.Fatal("Impossible layouts accepted")
	}
}
func (app *Service) testPackStep(t *testing.T) {
	home := []Rect{{0, 0, 240, 455}, {240, 0, 240, 455}, {480, 0, 240, 455}}
	at := func(off Rect) []WinState {
		var s []WinState
		for _, h := range home {
			s = append(s, WinState{R: Rect{h.X + off.X, h.Y + off.Y, h.W, h.H}, Alive: true})
		}
		return s
	}
	if off, fixes := app.PackStep(home, Rect{}, at(Rect{}), -1); off != (Rect{}) || len(fixes) != 0 {
		t.Fatal(off, fixes)
	}
	cur := at(Rect{})
	cur[1].R.X, cur[1].R.Y = 340, 50
	off, fixes := app.PackStep(home, Rect{}, cur, 1)
	if off != (Rect{X: 100, Y: 50}) || len(fixes) != 2 || fixes[0] != (Rect{100, 50, 240, 455}) || fixes[2] != (Rect{580, 50, 240, 455}) {
		t.Fatal(off, fixes)
	}
	if off2, fixes := app.PackStep(home, off, at(off), -1); off2 != off || len(fixes) != 0 {
		t.Fatal(off2, fixes)
	}
	// A window out of place that nobody is dragging is put back; it never moves the block.
	cur = at(off)
	cur[0].R.X, cur[0].R.Y = 0, 0
	if off2, fixes := app.PackStep(home, off, cur, -1); off2 != off || len(fixes) != 1 || fixes[0] != (Rect{100, 50, 240, 455}) {
		t.Fatal(off2, fixes)
	}
	// The dragged window wins even if a window before it is stuck elsewhere.
	cur[2].R.X, cur[2].R.Y = 700, 80
	if off2, fixes := app.PackStep(home, off, cur, 2); off2 != (Rect{X: 220, Y: 80}) || len(fixes) != 2 || fixes[0] != (Rect{220, 80, 240, 455}) || fixes[1] != (Rect{460, 80, 240, 455}) {
		t.Fatal(off2, fixes)
	}
	// While being resized the leader moves nothing and is left alone until released.
	cur = at(off)
	cur[2].R.W, cur[2].R.X = 300, 520
	if off2, fixes := app.PackStep(home, off, cur, 2); off2 != off || len(fixes) != 0 {
		t.Fatal(off2, fixes)
	}
	if off2, fixes := app.PackStep(home, off, cur, -1); off2 != off || len(fixes) != 1 || fixes[2] != (Rect{580, 50, 240, 455}) {
		t.Fatal(off2, fixes)
	}
	cur = at(off)
	cur[0] = WinState{R: Rect{0, 0, 1920, 1032}, Alive: true, Zoomed: true}
	cur[1].Alive = false
	cur[2].Iconic, cur[2].R = true, Rect{-32000, -32000, 160, 28}
	if off2, fixes := app.PackStep(home, off, cur, -1); off2 != off || len(fixes) != 1 || fixes[0] != (Rect{100, 50, 240, 455}) {
		t.Fatal(off2, fixes)
	}
}
func (app *Service) testViewBitRate(t *testing.T) {
	if b := app.ViewBitRate(Rect{0, 0, 120, 260}, 15); b != 2_000_000 {
		t.Fatal(b)
	}
	if b := app.ViewBitRate(Rect{0, 0, 1080, 2340}, 30); b != 8_000_000 {
		t.Fatal(b)
	}
	// 1560x720 encoded (half again the window) at a quarter bit per pixel.
	if b := app.ViewBitRate(Rect{0, 0, 480, 1040}, 10); b != 2_808_000 {
		t.Fatal(b)
	}
	if s := app.ViewSize(Rect{0, 0, 1080, 2340}); s != 1600 {
		t.Fatal(s)
	}
}
func (app *Service) testWindowBackoff(t *testing.T) {
	var got []time.Duration
	wait := time.Duration(0)
	for range 6 {
		wait = app.WindowBackoff(wait, time.Second)
		got = append(got, wait)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 15 * time.Second}
	if !slices.Equal(got, want) {
		t.Fatal(got)
	}
	if w := app.WindowBackoff(15*time.Second, time.Minute); w != time.Second {
		t.Fatal(w)
	}
}
func (app *Service) testSuperviseWindow(t *testing.T) {
	c, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var errs []error
	start := func() (viewHandle, error) {
		return startProcess(exec.CommandContext(c, os.Args[0], "-test.run=^$"))
	}
	exited := func(h viewHandle, e error) {
		mu.Lock()
		defer mu.Unlock()
		if h == nil {
			t.Error("process did not start", e)
		}
		errs = append(errs, e)
		if len(errs) == 2 {
			cancel()
		}
	}
	finished := make(chan struct{})
	go func() {
		app.SuperviseWindow(c, 0, nil, start, exited)
		close(finished)
	}()
	select {
	case <-finished:
	case <-time.After(20 * time.Second):
		t.Fatal("window not relaunched or supervisor not stopped")
	}
	if len(errs) != 2 {
		t.Fatal(errs)
	}
	if runtime.GOOS != "windows" {
		return
	}
	// A process that never shows a window is killed and reported.
	old := windowStartTimeout
	windowStartTimeout = 500 * time.Millisecond
	defer func() { windowStartTimeout = old }()
	c, cancel = context.WithCancel(context.Background())
	defer cancel()
	pack := app.StartWindowPack(1)
	defer pack.Stop()
	var hung error
	app.SuperviseWindow(c, 0, pack, func() (viewHandle, error) {
		cmd := exec.CommandContext(c, os.Args[0], "-test.run=^TestHelperSleep$")
		cmd.Env = append(os.Environ(), "DEVICESHELL_TEST_SLEEP=1")
		return startProcess(cmd)
	}, func(_ viewHandle, e error) {
		hung = e
		cancel()
	})
	if hung == nil || !strings.Contains(hung.Error(), "a tiempo") && !strings.Contains(hung.Error(), "in time") {
		t.Fatal(hung)
	}
}
func (app *Service) testScreenArgs(t *testing.T) {
	d := Device{Serial: "S1", Alias: "DEV-001"}
	r := Rect{1, 31, 238, 423}
	control := strings.Join(app.ScreenArgs(d, r, 5000, false), " ")
	if strings.Contains(control, "--no-control") || !strings.Contains(control, "--window-x=1") || !strings.Contains(control, "--port=5000") || !strings.Contains(control, "--no-clipboard-autosync") || !strings.Contains(control, "--video-bit-rate=3385560") || !strings.Contains(control, "--max-size=634") || !strings.Contains(control, "--max-fps=60") || !strings.Contains(control, "--render-driver=opengl") || !strings.Contains(control, "--window-title DEV-001 · Android USB ") {
		t.Fatal(control)
	}
	if ro := strings.Join(app.ScreenArgs(d, r, 5000, true), " "); !strings.Contains(ro, "--no-control") || !strings.Contains(ro, "--window-title DEV-001 · Android USB ") || !strings.Contains(ro, "--max-fps=30") {
		t.Fatal(ro)
	}
}
