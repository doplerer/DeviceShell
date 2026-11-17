package tui

import (
	"context"
	"sync/atomic"

	"github.com/doplerer/deviceshell/internal/core"
)

type screenWall struct {
	actual   *core.ScreenWall
	Devices  []core.Device
	Layout   []core.Rect
	Cols     int
	ReadOnly bool
	cancel   context.CancelFunc
	done     chan struct{}
	alive    atomic.Int32
	ports    map[string]int
}

func (w *screenWall) Done() <-chan struct{} {
	if w.actual != nil {
		return w.actual.Done()
	}
	return w.done
}
func (w *screenWall) Alive() int {
	if w.actual != nil {
		return w.actual.Alive()
	}
	return int(w.alive.Load())
}
func (w *screenWall) Close() {
	if w.actual != nil {
		w.actual.Close()
		return
	}
	if w.cancel != nil {
		w.cancel()
	}
}
