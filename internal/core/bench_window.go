package core

import (
	"context"
	"sync"
)

// benchLayout places the bench windows as the screen wall would, on every monitor if
// asked.
func (app *Service) benchLayout(n int, allMonitors bool) ([]Rect, []Rect) {
	areas := app.ScreenAreas()
	if allMonitors {
		areas = app.MonitorAreas()
	}
	return app.MultiGridLayout(n, areas, 2340.0/1080, app.WindowDecoration()), areas
}

// benchLink is a fake connector's link: its picture and, unless watch only, input
// that goes nowhere.
type benchLink struct {
	src      *benchSource
	readOnly bool
}

func (l *benchLink) open(context.Context) (viewSource, error) {
	return viewSource{hub: &l.src.frameHub, status: l.src}, nil
}

func (l *benchLink) input() viewInput {
	if l.readOnly {
		return nil
	}
	return benchInput{}
}

type benchInput struct{}

func (benchInput) touch(string, float64, float64) error { return nil }
func (benchInput) scroll(float64, float64, int) error   { return nil }
func (benchInput) send(Spec) error                      { return nil }

// benchWindow opens one window on src, as openAndroidWindow does.
func (app *Service) benchWindow(c context.Context, wg *sync.WaitGroup, slot int, pack *WindowPack, children *benchChildren, r Rect, title, platform string, readOnly bool, src *benchSource) error {
	h, e := app.openView(c, windowConfig{Title: title, Area: r, Platform: platform}, &benchLink{src: src, readOnly: readOnly}, func() {})
	if e != nil {
		return e
	}
	pack.Track(slot, h.PID(), h.Window())
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.Wait()
	}()
	return nil
}
