package core

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"time"
)

// Every DeviceShell window runs inside this process (window_windows.go): it takes its
// pictures straight from the device's frame hub and gives its mouse and keyboard to
// the connector, with no loopback server, token or child process in between.

// touchEvent is one finger sample in 0..1 screen coordinates; a "scroll" is wheel
// notches at that point (Android windows only).
type touchEvent struct {
	Phase string
	X, Y  float64
	Steps int
}

// windowConfig is what a window is opened with.
type windowConfig struct {
	Title    string
	Area     Rect    // client area, as scrcpy's --window-* options
	Platform string  // "ios" or "android": hints, keys and wheel
	Aspect   float64 // screen height / width until the first frame, 0 if unknown
}

// viewSource is one run of a device's picture: it lasts until done closes (the
// connector stopped; the window then opens the next one).
type viewSource struct {
	hub    *frameHub
	status frameSource
	done   <-chan struct{} // nil: never
	wedged func() bool     // the iPhone touch stream stopped answering
}

// viewInput is how a window controls its device. A watch-only window has none, so it
// cannot send anything whatever happens inside it.
type viewInput interface {
	touch(phase string, x, y float64) error
	scroll(x, y float64, steps int) error
	send(Spec) error // "key" or "text" (and "tap"/"swipe" on the iPhone)
}

// viewLink is what a window needs from its device.
type viewLink interface {
	// open waits for the connector (it may be starting) and returns its picture.
	open(c context.Context) (viewSource, error)
	// input is nil for a watch-only window.
	input() viewInput
}

// viewHandle is a window under supervision: one drawn by this process or a scrcpy
// process.
type viewHandle interface {
	Wait() error     // until the window closes or the process exits
	Kill()           // closes the window or kills the process
	Window() uintptr // the window, when known from the start (0 for processes)
	PID() int        // the process to look for windows of (0 for our own windows)
}

// processHandle supervises a window process (scrcpy.exe).
type processHandle struct {
	cmd  *exec.Cmd
	once sync.Once
	err  error
	done chan struct{}
}

func newProcessHandle(cmd *exec.Cmd) *processHandle {
	h := &processHandle{cmd: cmd, done: make(chan struct{})}
	go func() {
		h.err = cmd.Wait()
		close(h.done)
	}()
	return h
}

func (h *processHandle) Wait() error     { <-h.done; return h.err }
func (h *processHandle) Kill()           { h.cmd.Process.Kill() }
func (h *processHandle) Window() uintptr { return 0 }
func (h *processHandle) PID() int        { return h.cmd.Process.Pid }

// startProcess starts cmd and supervises it.
func startProcess(cmd *exec.Cmd) (viewHandle, error) {
	if e := cmd.Start(); e != nil {
		return nil, e
	}
	return newProcessHandle(cmd), nil
}

// errReadOnly is what a watch-only path answers, should anything ask it for input.
var errReadOnly = errors.New("solo vista")

// openView opens a window on link. release runs once the window has closed, or right
// away if it cannot open; c closing closes the window.
func (app *Service) openView(c context.Context, cfg windowConfig, link viewLink, release func()) (viewHandle, error) {
	counted := app.windowOpened()
	done := func() {
		counted()
		release()
	}
	h, e := openNativeWindow(c, cfg, link, done)
	if e != nil {
		done()
		return nil, e
	}
	return h, nil
}

// keepWedged polls wedged every 2 s until c ends; the window shows it.
func keepWedged(c context.Context, wedged func(context.Context) bool) func() bool {
	var mu sync.Mutex
	state := false
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-c.Done():
				return
			case <-ticker.C:
				w := wedged(c)
				mu.Lock()
				state = w
				mu.Unlock()
			}
		}
	}()
	return func() bool {
		mu.Lock()
		defer mu.Unlock()
		return state
	}
}
