package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

type Rect struct{ X, Y, W, H int }
type WindowFrame struct{ Top, Side, Bottom int }

// gridLayout places n identical windows edge to edge from the top-left corner of area, choosing the
// column count that gives the largest windows for screens of the given aspect (height / width).
func (app *Service) GridLayout(n int, area Rect, aspect float64, f WindowFrame) []Rect {
	if n < 1 || area.W < 1 || area.H < 1 {
		return nil
	}
	if aspect <= 0 || math.IsNaN(aspect) {
		aspect = 2.17
	}
	bestW, bestH, bestCols := 0, 0, 1
	for cols := 1; cols <= n; cols++ {
		rows := (n + cols - 1) / cols
		cw := area.W/cols - 2*f.Side
		ch := area.H/rows - f.Top - f.Bottom
		if cw < 1 || ch < 1 {
			continue
		}
		w, h := cw, int(float64(cw)*aspect)
		if h > ch {
			h, w = ch, int(float64(ch)/aspect)
		}
		if w > 0 && h > 0 && w*h > bestW*bestH {
			bestW, bestH, bestCols = w, h, cols
		}
	}
	if bestW == 0 {
		return nil
	}
	out := make([]Rect, n)
	for i := range out {
		col, row := i%bestCols, i/bestCols
		out[i] = Rect{X: area.X + col*(bestW+2*f.Side) + f.Side, Y: area.Y + row*(bestH+f.Top+f.Bottom) + f.Top, W: bestW, H: bestH}
	}
	return out
}

// screenArea is the monitor work area, unless config.json sets "windowArea": [x, y, width, height].
func (app *Service) ScreenArea() Rect {
	var config struct {
		WindowArea []int `json:"windowArea"`
	}
	if app.ReadJSON(app.ConfigPath(), &config) == nil && len(config.WindowArea) == 4 && config.WindowArea[2] > 0 && config.WindowArea[3] > 0 {
		a := config.WindowArea
		return Rect{a[0], a[1], a[2], a[3]}
	}
	return app.WorkArea()
}

// layoutFor computes the window of each device, in order, using the first device's screen proportions.
// Proportions already known are used at once and refreshed in the background for the next layout.
func (app *Service) LayoutFor(c context.Context, devices []Device) []Rect {
	aspect := 0.0
	if len(devices) > 0 {
		serial := devices[0].Serial
		measure := func(c context.Context) float64 {
			dc, cancel := context.WithTimeout(c, 5*time.Second)
			defer cancel()
			w, h, e := app.Dimensions(dc, serial)
			if e != nil || w <= 0 {
				return 0
			}
			a := float64(h) / float64(w)
			app.aspectMu.Lock()
			if app.aspects == nil {
				app.aspects = map[string]float64{}
			}
			app.aspects[serial] = a
			app.aspectMu.Unlock()
			return a
		}
		app.aspectMu.Lock()
		known := app.aspects[serial]
		app.aspectMu.Unlock()
		if aspect = known; aspect > 0 {
			go measure(context.WithoutCancel(c))
		} else {
			aspect = measure(c)
		}
	}
	return app.MultiGridLayout(len(devices), app.ScreenAreas(), aspect, app.WindowDecoration())
}
func (app *Service) WindowArgs(r Rect) []string {
	return []string{fmt.Sprintf("--window-x=%d", r.X), fmt.Sprintf("--window-y=%d", r.Y), fmt.Sprintf("--window-width=%d", r.W), fmt.Sprintf("--window-height=%d", r.H), "--no-window-aspect-ratio-lock"}
}
func (app *Service) FreePort() (int, error) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return 0, e
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

type WinState struct {
	R              Rect
	Alive          bool
	Iconic, Zoomed bool
}

// packStep keeps tiled windows together. home holds each window's outer rectangle at offset 0 and off
// is the current offset of the whole block. Only leader, the window the user is moving (-1 if none),
// can move the block, and only while it keeps its size; it is left alone until released. Every other
// window that is not where it should be gets a fix.
func (app *Service) PackStep(home []Rect, off Rect, cur []WinState, leader int) (Rect, map[int]Rect) {
	if leader >= 0 && leader < len(cur) {
		c, h := cur[leader], home[leader]
		if c.Alive && !c.Iconic && !c.Zoomed && c.R.W == h.W && c.R.H == h.H {
			off = Rect{X: c.R.X - h.X, Y: c.R.Y - h.Y}
		}
	}
	fixes := map[int]Rect{}
	for i, c := range cur {
		if i == leader || !c.Alive || c.Iconic {
			continue
		}
		want := Rect{home[i].X + off.X, home[i].Y + off.Y, home[i].W, home[i].H}
		if c.Zoomed || c.R != want {
			fixes[i] = want
		}
	}
	return off, fixes
}

// viewSize is the longest side the device encodes for a window of r: half again the window, up to
// 1600, so the PC scales the picture down (sharper text and edges) instead of the phone sending it
// at exactly the window size.
func (app *Service) ViewSize(r Rect) int {
	return min(max(r.W, r.H)*3/2, 1600)
}

// viewBitRate is the video bit rate for a window of r at fps: about a quarter of a bit per encoded
// pixel and frame, between 2 and 8 Mbps, so many windows starting together do not choke the USB links.
func (app *Service) ViewBitRate(r Rect, fps int) int {
	long, short := max(r.W, r.H), min(r.W, r.H)
	size := app.ViewSize(r)
	pixels := size * (size * short / max(1, long))
	return min(max(pixels*fps/4, 2_000_000), 8_000_000)
}

// screenArgs builds the scrcpy command line of one tiled window; with readOnly the window only shows the screen.
func (app *Service) ScreenArgs(d Device, r Rect, port int, readOnly bool) []string {
	title := d.viewTitle()
	args := []string{"--serial", d.Serial, "--no-audio", "--no-clipboard-autosync"}
	fps := viewFPS
	if readOnly {
		args = append(args, "--no-control")
		fps = watchFPS
	}
	args = append(args, "--window-title", title, fmt.Sprintf("--max-size=%d", app.ViewSize(r)), fmt.Sprintf("--max-fps=%d", fps), fmt.Sprintf("--video-bit-rate=%d", app.ViewBitRate(r, fps)), viewRenderer, "--force-adb-forward", fmt.Sprintf("--port=%d", port))
	return append(args, app.WindowArgs(r)...)
}

type ScreenWall struct {
	app      *Service
	Devices  []Device
	Layout   []Rect
	Cols     int
	ReadOnly bool
	cancel   context.CancelFunc
	done     chan struct{}
	alive    atomic.Int32
	ports    map[string]int
	pack     *WallPacks
}

func (w *ScreenWall) Done() <-chan struct{} {
	return w.done
}
func (w *ScreenWall) Alive() int {
	return int(w.alive.Load())
}

// Close stops every window and removes the ADB forwards that a killed scrcpy leaves behind.
func (w *ScreenWall) Close() {
	if w.pack != nil {
		w.pack.Stop()
	}
	w.cancel()
	select {
	case <-w.done:
	case <-time.After(5 * time.Second):
	}
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for serial, port := range w.ports {
		w.app.Command(c, "-s", serial, "forward", "--remove", fmt.Sprintf("tcp:%d", port))
	}
}

const (
	viewFPS  = 60 // windows that control their device, as smooth as the iPhone ones
	watchFPS = 30 // watch-only windows
	// OpenGL makes scrcpy build mipmaps, which scale the larger picture down cleanly.
	viewRenderer = "--render-driver=opengl"
)

// openScreenWall opens one window per connected device, all the same size, in a grid that moves as one
// block. Each window controls its own device unless readOnly. Windows that fail or are closed open
// again by themselves until Close.
func (app *Service) OpenScreenWall(parent context.Context, devices []Device, readOnly bool) (*ScreenWall, error) {
	var on []Device
	for _, d := range devices {
		if d.State == "device" {
			on = append(on, d)
		}
	}
	if len(on) == 0 {
		return nil, errors.New(app.L("Ninguna de las placas elegidas está conectada", "None of the chosen devices is connected"))
	}
	layout := app.LayoutFor(parent, on)
	if len(layout) != len(on) {
		return nil, errors.New(app.L("No caben las ventanas en la pantalla; revisa windowArea en config.json", "The windows do not fit on the screen; check windowArea in config.json"))
	}
	c, cancel := context.WithCancel(parent)
	w := &ScreenWall{app: app, Devices: on, Layout: layout, ReadOnly: readOnly, cancel: cancel, done: make(chan struct{}), ports: map[string]int{}}
	for i := 1; i < len(layout) && layout[i].Y == layout[0].Y; i++ {
		w.Cols = i + 1
	}
	w.Cols = max(w.Cols, 1)
	native := app.NativeAndroidView()
	for _, d := range on {
		if d.OS() == "ios" || native {
			continue
		}
		port, e := app.FreePort()
		if e != nil {
			cancel()
			return nil, e
		}
		w.ports[d.Serial] = port
	}
	id := time.Now().Format("20060102-150405.000000000")
	var failed atomic.Bool
	var wg sync.WaitGroup
	// The pack runs before any window exists, so each one joins the block as soon as it appears.
	w.pack = app.StartWallPacks(layout, app.ScreenAreas())
	for i, d := range on {
		start := func() (viewHandle, error) {
			var h viewHandle
			var e error
			if d.OS() == "ios" {
				h, e = app.openIOSWindow(c, d, layout[i], readOnly, nil)
			} else if native {
				h, e = app.openAndroidWindow(c, d, layout[i], readOnly)
			} else {
				cmd := exec.CommandContext(c, app.Scrcpy, app.ScreenArgs(d, layout[i], w.ports[d.Serial], readOnly)...)
				cmd.Env = append(os.Environ(), "ADB="+app.Adb)
				cmd.Dir = app.ToolDir
				h, e = startProcess(cmd)
			}
			if e != nil {
				return nil, e
			}
			w.alive.Add(1)
			return h, nil
		}
		exited := func(h viewHandle, _ error) {
			if h != nil {
				w.alive.Add(-1)
			}
			if c.Err() == nil {
				failed.Store(true)
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			pack, slot := w.pack.For(i)
			app.SuperviseWindow(c, slot, pack, start, exited)
		}()
	}
	go func() {
		wg.Wait()
		w.pack.Stop()
		var serials []string
		for _, d := range on {
			serials = append(serials, d.Serial)
		}
		if e := app.SaveDiagnostic("screens", id, serials, 0, failed.Load()); e != nil {
			fmt.Fprintln(os.Stderr, e)
		}
		close(w.done)
	}()
	return w, nil
}
