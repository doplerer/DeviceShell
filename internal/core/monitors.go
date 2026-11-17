package core

import (
	"strings"
)

// ScreenAreas are the rectangles the windows are laid out in: "windowArea" of
// config.json when set, every monitor's work area with "monitors": "all", or else
// the main monitor's.
func (app *Service) ScreenAreas() []Rect {
	var config struct {
		WindowArea []int  `json:"windowArea"`
		Monitors   string `json:"monitors"`
	}
	_ = app.ReadJSON(app.ConfigPath(), &config)
	if a := config.WindowArea; len(a) == 4 && a[2] > 0 && a[3] > 0 {
		return []Rect{{a[0], a[1], a[2], a[3]}}
	}
	if strings.EqualFold(config.Monitors, "all") {
		if areas := app.MonitorAreas(); len(areas) > 0 {
			return areas
		}
	}
	return []Rect{app.WorkArea()}
}

// MultiGridLayout places n identical windows over several areas, filling each one in
// order before the next: the largest windows for which all n fit. With one area it is
// GridLayout.
func (app *Service) MultiGridLayout(n int, areas []Rect, aspect float64, f WindowFrame) []Rect {
	if len(areas) == 1 {
		return app.GridLayout(n, areas[0], aspect, f)
	}
	if n < 1 || len(areas) == 0 {
		return nil
	}
	if aspect <= 0 || aspect != aspect {
		aspect = 2.17
	}
	cell := func(w int) (int, int, int) {
		h := int(float64(w) * aspect)
		return h, w + 2*f.Side, h + f.Top + f.Bottom
	}
	capacity := func(w int) int {
		_, cw, ch := cell(w)
		total := 0
		for _, a := range areas {
			if a.W >= cw && a.H >= ch {
				total += (a.W / cw) * (a.H / ch)
			}
		}
		return total
	}
	widest := 0
	for _, a := range areas {
		widest = max(widest, a.W)
	}
	// Capacity only shrinks as windows grow: the largest width that still fits n.
	lo, hi := 0, widest
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if capacity(mid) >= n {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo < 1 {
		return nil
	}
	h, cw, ch := cell(lo)
	out := make([]Rect, 0, n)
	for _, a := range areas {
		if a.W < cw || a.H < ch {
			continue
		}
		cols, rows := a.W/cw, a.H/ch
		for i := 0; i < cols*rows && len(out) < n; i++ {
			col, row := i%cols, i/cols
			out = append(out, Rect{X: a.X + col*cw + f.Side, Y: a.Y + row*ch + f.Top, W: lo, H: h})
		}
	}
	return out
}

// WallPacks keeps the windows of each monitor together as one block of their own.
type WallPacks struct {
	packs []*WindowPack
	pack  []int // the block of each window
	slot  []int // its place in that block
}

// StartWallPacks starts one window pack per area of areas that the layout uses.
func (app *Service) StartWallPacks(layout []Rect, areas []Rect) *WallPacks {
	w := &WallPacks{pack: make([]int, len(layout)), slot: make([]int, len(layout))}
	groups := map[int]int{} // area -> pack
	var sizes []int
	for i, r := range layout {
		area := 0
		cx, cy := r.X+r.W/2, r.Y+r.H/2
		for k, a := range areas {
			if cx >= a.X && cx < a.X+a.W && cy >= a.Y && cy < a.Y+a.H {
				area = k
				break
			}
		}
		g, ok := groups[area]
		if !ok {
			g = len(sizes)
			groups[area] = g
			sizes = append(sizes, 0)
		}
		w.pack[i], w.slot[i] = g, sizes[g]
		sizes[g]++
	}
	for _, n := range sizes {
		w.packs = append(w.packs, app.StartWindowPack(n))
	}
	return w
}

// For returns the pack and slot of window i.
func (w *WallPacks) For(i int) (*WindowPack, int) {
	if w == nil || i < 0 || i >= len(w.pack) {
		return nil, -1
	}
	return w.packs[w.pack[i]], w.slot[i]
}

// HasWindow reports whether window i has joined its block.
func (w *WallPacks) HasWindow(i int) bool {
	p, slot := w.For(i)
	return p.HasWindow(slot)
}

func (w *WallPacks) Stop() {
	if w == nil {
		return
	}
	for _, p := range w.packs {
		p.Stop()
	}
}
