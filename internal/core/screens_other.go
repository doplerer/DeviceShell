//go:build !windows

package core

func (app *Service) WorkArea() Rect {
	return Rect{0, 0, 1920, 1040}
}
func (app *Service) MonitorAreas() []Rect {
	return []Rect{app.WorkArea()}
}
func (app *Service) WheelLines() int {
	return 1
}
func (app *Service) WindowDecoration() WindowFrame {
	return WindowFrame{Top: 31, Side: 1, Bottom: 1}
}

type WindowPack struct{ app *Service }

func (app *Service) StartWindowPack(int) *WindowPack {
	return &WindowPack{app: app}
}
func (self *WindowPack) Stop() {
}
func (self *WindowPack) Track(int, int, uintptr) {
}
func (self *WindowPack) HasWindow(int) bool {
	return true
}
