package core

import (
	"runtime"
	"sort"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32                        = syscall.NewLazyDLL("user32.dll")
	kernel32                      = syscall.NewLazyDLL("kernel32.dll")
	procSystemParametersInfoW     = user32.NewProc("SystemParametersInfoW")
	procGetSystemMetrics          = user32.NewProc("GetSystemMetrics")
	procSetThreadDpiAwarenessCtx  = user32.NewProc("SetThreadDpiAwarenessContext")
	procEnumWindows               = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId  = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible           = user32.NewProc("IsWindowVisible")
	procGetWindowTextLengthW      = user32.NewProc("GetWindowTextLengthW")
	procGetWindowRect             = user32.NewProc("GetWindowRect")
	procSetWindowPos              = user32.NewProc("SetWindowPos")
	procIsWindow                  = user32.NewProc("IsWindow")
	procIsIconic                  = user32.NewProc("IsIconic")
	procIsZoomed                  = user32.NewProc("IsZoomed")
	procShowWindow                = user32.NewProc("ShowWindow")
	procGetGUIThreadInfo          = user32.NewProc("GetGUIThreadInfo")
	procGetForegroundWindow       = user32.NewProc("GetForegroundWindow")
	procSetWinEventHook           = user32.NewProc("SetWinEventHook")
	procUnhookWinEvent            = user32.NewProc("UnhookWinEvent")
	procBeginDeferWindowPos       = user32.NewProc("BeginDeferWindowPos")
	procDeferWindowPos            = user32.NewProc("DeferWindowPos")
	procEndDeferWindowPos         = user32.NewProc("EndDeferWindowPos")
	procPeekMessageW              = user32.NewProc("PeekMessageW")
	procPostThreadMessageW        = user32.NewProc("PostThreadMessageW")
	procMsgWaitForMultiple        = user32.NewProc("MsgWaitForMultipleObjectsEx")
	procGetCurrentThreadId        = kernel32.NewProc("GetCurrentThreadId")
	dpiAwarenessPerMonitorAwareV2 = ^uintptr(3)
)

// WheelLines is the "lines per wheel notch" setting of Windows, 3 by default; a page scroll counts as one.
func (app *Service) WheelLines() int {
	var n uint32
	if r, _, _ := procSystemParametersInfoW.Call(0x0068, 0, uintptr(unsafe.Pointer(&n)), 0); r == 0 || n == 0 || n > 100 {
		return 1
	}
	return int(n)
}

type WindowPack struct {
	app        *Service
	stop, done chan struct{}
	started    chan struct{}
	once       sync.Once
	mu         sync.Mutex
	tid        uintptr // the pack thread, woken with a thread message
	pids       []int
	hwnds      []uintptr
	shown      []bool
}

const (
	wmQuit     = 0x0012
	wmPackWake = 0x8000 + 0x42 // WM_APP + 0x42: Track changed a slot
)

func (p *WindowPack) Stop() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		close(p.stop)
		<-p.started
		procPostThreadMessageW.Call(p.tid, wmQuit, 0, 0)
	})
	<-p.done
}

// Track tells the pack which window is the one of slot: hwnd when it is known (our own
// windows), or else the process pid whose window is searched for (scrcpy). Both zero
// mean the slot has no window right now.
func (p *WindowPack) Track(slot, pid int, hwnd uintptr) {
	if p == nil {
		return
	}
	p.mu.Lock()
	changed := slot >= 0 && slot < len(p.pids) && (p.pids[slot] != pid || p.hwnds[slot] != hwnd)
	if changed {
		p.pids[slot], p.hwnds[slot], p.shown[slot] = pid, hwnd, false
	}
	p.mu.Unlock()
	if changed {
		select {
		case <-p.started:
			procPostThreadMessageW.Call(p.tid, wmPackWake, 0, 0)
		default:
		}
	}
}

// HasWindow reports whether the window of the current process of slot has appeared and joined the pack.
func (p *WindowPack) HasWindow(slot int) bool {
	if p == nil {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return slot >= 0 && slot < len(p.shown) && p.shown[slot]
}

var (
	enumMu      sync.Mutex
	enumFound   map[uint32]uintptr
	enumWindows = syscall.NewCallback(func(h, _ uintptr) uintptr {
		var pid uint32
		procGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
		if _, want := enumFound[pid]; want && enumFound[pid] == 0 {
			if v, _, _ := procIsWindowVisible.Call(h); v != 0 {
				if n, _, _ := procGetWindowTextLengthW.Call(h); n > 0 {
					enumFound[pid] = h
				}
			}
		}
		return 1
	})
)

// findWindows returns the visible, titled top-level window of each process, or 0 if it has none yet.
func (app *Service) FindWindows(pids []int) []uintptr {
	enumMu.Lock()
	defer enumMu.Unlock()
	enumFound = map[uint32]uintptr{}
	for _, p := range pids {
		if p != 0 {
			enumFound[uint32(p)] = 0
		}
	}
	procEnumWindows.Call(enumWindows, 0)
	out := make([]uintptr, len(pids))
	for i, p := range pids {
		if p != 0 {
			out[i] = enumFound[uint32(p)]
		}
	}
	return out
}
func (app *Service) ReadWindow(h uintptr) WinState {
	if v, _, _ := procIsWindow.Call(h); v == 0 {
		return WinState{}
	}
	var r struct{ L, T, R, B int32 }
	procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
	ic, _, _ := procIsIconic.Call(h)
	zo, _, _ := procIsZoomed.Call(h)
	return WinState{R: Rect{int(r.L), int(r.T), int(r.R - r.L), int(r.B - r.T)}, Alive: true, Iconic: ic != 0, Zoomed: zo != 0}
}

type guiThreadInfo struct {
	Size, Flags                                        uint32
	Active, Focus, Capture, MenuOwner, MoveSize, Caret uintptr
	CaretRect                                          [4]int32
}

// inMoveSize reports whether the user is dragging or resizing h with the mouse right now.
func (app *Service) InMoveSize(h uintptr) bool {
	tid, _, _ := procGetWindowThreadProcessId.Call(h, 0)
	if tid == 0 {
		return false
	}
	info := guiThreadInfo{}
	info.Size = uint32(unsafe.Sizeof(info))
	if r, _, _ := procGetGUIThreadInfo.Call(tid, uintptr(unsafe.Pointer(&info))); r == 0 {
		return false
	}
	return info.Flags&0x2 != 0 && info.MoveSize == h
}

// packHooks finds the pack of an event hook; the hook procedure runs on its thread.
var (
	packHooksMu  sync.Mutex
	packHooks    = map[uintptr]*packEvents{}
	packHookProc = syscall.NewCallback(func(hook, event, hwnd, object, child, thread, at uintptr) uintptr {
		packHooksMu.Lock()
		e := packHooks[hook]
		packHooksMu.Unlock()
		if e != nil {
			e.event(uint32(event), hwnd, int32(object), int32(child))
		}
		return 0
	})
)

// packEvents is what the window events tell the pack thread between two steps.
type packEvents struct {
	ours   map[uintptr]bool // the windows of the block, kept by the pack thread
	dirty  bool
	moving uintptr // the window the user drags or resizes now
}

const (
	eventForeground     = 0x0003
	eventMoveSizeStart  = 0x000A
	eventMoveSizeEnd    = 0x000B
	eventMinimizeStart  = 0x0016
	eventMinimizeEnd    = 0x0017
	eventLocationChange = 0x800B
)

func (e *packEvents) event(event uint32, hwnd uintptr, object, child int32) {
	if event == eventForeground {
		e.dirty = true
		return
	}
	if object != 0 || child != 0 || !e.ours[hwnd] { // OBJID_WINDOW, CHILDID_SELF
		return
	}
	switch event {
	case eventMoveSizeStart:
		e.moving = hwnd
	case eventMoveSizeEnd:
		if e.moving == hwnd {
			e.moving = 0
		}
	}
	e.dirty = true
}

// startWindowPack keeps the windows of n slots together as one block: dragging any of them moves all,
// minimizing or restoring one does the same to the rest and bringing one to the front raises the rest
// right behind it. Start it before the windows so each one joins the moment it appears; Track gives
// every slot its window, also again after a relaunch, and a relaunched window returns to its place.
// Window events (moves, minimizing, the foreground) drive it; a 1 s pass catches anything else.
func (app *Service) StartWindowPack(n int) *WindowPack {
	p := &WindowPack{app: app, stop: make(chan struct{}), done: make(chan struct{}), started: make(chan struct{}), pids: make([]int, n), hwnds: make([]uintptr, n), shown: make([]bool, n)}
	go func() {
		defer close(p.done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if procSetThreadDpiAwarenessCtx.Find() == nil {
			procSetThreadDpiAwarenessCtx.Call(dpiAwarenessPerMonitorAwareV2)
		}
		p.tid, _, _ = procGetCurrentThreadId.Call()
		close(p.started)
		events := &packEvents{ours: map[uintptr]bool{}, dirty: true}
		var hooks []uintptr
		for _, r := range [][2]uintptr{{eventForeground, eventForeground}, {eventMoveSizeStart, eventMoveSizeEnd}, {eventMinimizeStart, eventMinimizeEnd}, {eventLocationChange, eventLocationChange}} {
			if h, _, _ := procSetWinEventHook.Call(r[0], r[1], 0, packHookProc, 0, 0, 0); h != 0 { // WINEVENT_OUTOFCONTEXT
				hooks = append(hooks, h)
				packHooksMu.Lock()
				packHooks[h] = events
				packHooksMu.Unlock()
			}
		}
		hooked := len(hooks) == 4
		defer func() {
			for _, h := range hooks {
				procUnhookWinEvent.Call(h)
				packHooksMu.Lock()
				delete(packHooks, h)
				packHooksMu.Unlock()
			}
		}()
		const (
			swpFlags   = 0x0004 | 0x0010 | 0x0200
			swpBehind  = 0x0001 | 0x0002 | 0x0010 | 0x0200
			swMinimize = 6
			swRestore  = 9
		)
		pids := make([]int, n)
		hwnds := make([]uintptr, n)
		owner := make([]int, n)
		ownerHwnd := make([]uintptr, n)
		handles := make([]uintptr, n)
		home := make([]Rect, n)
		placed := make([]bool, n)
		last := make([]Rect, n)
		iconic := false
		off := Rect{}
		front := uintptr(0)
		show := func(i int, on bool) {
			p.mu.Lock()
			if p.pids[i] == pids[i] && p.hwnds[i] == hwnds[i] {
				p.shown[i] = on
			}
			p.mu.Unlock()
		}
		// join makes h the window of slot i once it is visible and not minimized.
		join := func(i int, h uintptr) bool {
			s := app.ReadWindow(h)
			if !s.Alive || s.Iconic {
				return false
			}
			// A new window is born at its grid place, so that is its place in the block for good.
			handles[i], last[i] = h, s.R
			if !placed[i] {
				home[i], placed[i] = s.R, true
			}
			if iconic {
				procShowWindow.Call(h, swMinimize)
			}
			show(i, true)
			return true
		}
		const idle, searching, dragging = time.Second, 100 * time.Millisecond, 16 * time.Millisecond
		step := func() time.Duration {
			p.mu.Lock()
			copy(pids, p.pids)
			copy(hwnds, p.hwnds)
			p.mu.Unlock()
			missing := false
			var search []int
			for i := range handles {
				if owner[i] != pids[i] || ownerHwnd[i] != hwnds[i] {
					owner[i], ownerHwnd[i], handles[i] = pids[i], hwnds[i], 0
				}
				if handles[i] == 0 && hwnds[i] != 0 && !join(i, hwnds[i]) {
					missing = true
				}
				if handles[i] == 0 && hwnds[i] == 0 && pids[i] != 0 {
					missing = true
					search = append(search, i)
				}
			}
			if len(search) > 0 {
				want := make([]int, len(pids))
				for _, i := range search {
					want[i] = pids[i]
				}
				for i, h := range app.FindWindows(want) {
					if h != 0 && handles[i] == 0 && !join(i, h) {
						missing = true
					}
				}
			}
			clear(events.ours)
			var idx []int
			var cur []WinState
			var homes []Rect
			for i, h := range handles {
				if h == 0 {
					continue
				}
				s := app.ReadWindow(h)
				if !s.Alive {
					handles[i] = 0
					show(i, false)
					continue
				}
				events.ours[h] = true
				idx = append(idx, i)
				cur = append(cur, s)
				homes = append(homes, home[i])
			}
			next := idle
			if missing {
				next = searching
			}
			if len(idx) == 0 {
				return next
			}
			for k := range idx {
				if cur[k].Iconic != iconic {
					iconic = cur[k].Iconic
					for j, i := range idx {
						if iconic && !cur[j].Iconic {
							procShowWindow.Call(handles[i], swMinimize)
						} else if !iconic && cur[j].Iconic {
							procShowWindow.Call(handles[i], swRestore)
						}
					}
					return searching
				}
			}
			// Only the window the user is dragging may move the block; any other one out of place is
			// put back, so a window that ignores a move can never drag the block back to it.
			leader := -1
			for k, i := range idx {
				if hooked && handles[i] != events.moving {
					continue
				}
				if app.InMoveSize(handles[i]) {
					leader = k
					break
				}
			}
			fg, _, _ := procGetForegroundWindow.Call()
			if leader < 0 {
				for k, i := range idx {
					want := Rect{home[i].X + off.X, home[i].Y + off.Y, home[i].W, home[i].H}
					if handles[i] == fg && cur[k].R != last[i] && cur[k].R != want {
						leader = k
					}
				}
			}
			var fixes map[int]Rect
			off, fixes = app.PackStep(homes, off, cur, leader)
			if len(fixes) > 0 {
				for k := range fixes {
					if cur[k].Zoomed {
						procShowWindow.Call(handles[idx[k]], swRestore)
					}
				}
				// The whole block moves at once; one by one only if the batch fails.
				batch, _, _ := procBeginDeferWindowPos.Call(uintptr(len(fixes)))
				for k, r := range fixes {
					if batch == 0 {
						break
					}
					batch, _, _ = procDeferWindowPos.Call(batch, handles[idx[k]], 0, uintptr(r.X), uintptr(r.Y), uintptr(r.W), uintptr(r.H), swpFlags)
				}
				ok := uintptr(0)
				if batch != 0 {
					ok, _, _ = procEndDeferWindowPos.Call(batch)
				}
				if ok == 0 {
					for k, r := range fixes {
						procSetWindowPos.Call(handles[idx[k]], 0, uintptr(r.X), uintptr(r.Y), uintptr(r.W), uintptr(r.H), swpFlags)
					}
				}
			}
			for k, i := range idx {
				last[i] = cur[k].R
			}
			if fg != front {
				front = fg
				for _, i := range idx {
					if handles[i] != fg {
						continue
					}
					for k, j := range idx {
						if j != i && !cur[k].Iconic {
							procSetWindowPos.Call(handles[j], fg, 0, 0, 0, 0, swpBehind)
						}
					}
					break
				}
			}
			if leader >= 0 {
				// While dragging the pack follows closely, also between events.
				return dragging
			}
			if !hooked {
				// Without window events it polls as it used to.
				return min(next, 50*time.Millisecond)
			}
			return next
		}
		var message nativeMsg
		next := time.Now()
		lastStep := time.Time{}
		for {
			wait := max(0, time.Until(next))
			procMsgWaitForMultiple.Call(0, 0, uintptr(wait.Milliseconds()), 0x04FF, 0x0004) // QS_ALLINPUT, MWMO_INPUTAVAILABLE
			for {
				got, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0, 1) // PM_REMOVE
				if got == 0 {
					break
				}
				switch message.Message {
				case wmQuit:
					return
				case wmPackWake:
					events.dirty = true
				}
			}
			select {
			case <-p.stop:
				return
			default:
			}
			now := time.Now()
			if events.dirty && now.Sub(lastStep) < 10*time.Millisecond {
				// Events come at the rate of the mouse: one step every 10 ms is enough.
				next = lastStep.Add(10 * time.Millisecond)
				continue
			}
			if events.dirty || !now.Before(next) {
				events.dirty = false
				lastStep = now
				next = now.Add(step())
			}
		}
	}()
	return p
}

var (
	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	monitorsMu              sync.Mutex
	monitorsFound           []monitorArea
	monitorsCallback        = syscall.NewCallback(func(monitor, dc, rect, data uintptr) uintptr {
		var info struct {
			Size       uint32
			Area, Work struct{ L, T, R, B int32 }
			Flags      uint32
		}
		info.Size = uint32(unsafe.Sizeof(info))
		if r, _, _ := procGetMonitorInfoW.Call(monitor, uintptr(unsafe.Pointer(&info))); r != 0 && info.Work.R > info.Work.L && info.Work.B > info.Work.T {
			w := info.Work
			monitorsFound = append(monitorsFound, monitorArea{Rect{int(w.L), int(w.T), int(w.R - w.L), int(w.B - w.T)}, info.Flags&1 != 0})
		}
		return 1
	})
)

type monitorArea struct {
	r       Rect
	primary bool
}

// MonitorAreas is the work area of every monitor in physical pixels: the main one
// first, then from left to right and top to bottom.
func (app *Service) MonitorAreas() []Rect {
	monitorsMu.Lock()
	defer monitorsMu.Unlock()
	monitorsFound = nil
	app.PhysicalPixels(func() {
		procEnumDisplayMonitors.Call(0, 0, monitorsCallback, 0)
	})
	found := monitorsFound
	sort.SliceStable(found, func(i, j int) bool {
		a, b := found[i], found[j]
		if a.primary != b.primary {
			return a.primary
		}
		if a.r.X != b.r.X {
			return a.r.X < b.r.X
		}
		return a.r.Y < b.r.Y
	})
	out := make([]Rect, len(found))
	for i, m := range found {
		out[i] = m.r
	}
	return out
}

// physicalPixels runs f with per-monitor DPI awareness, the same pixel space scrcpy (SDL3) positions windows in.
func (app *Service) PhysicalPixels(f func()) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if procSetThreadDpiAwarenessCtx.Find() == nil {
		old, _, _ := procSetThreadDpiAwarenessCtx.Call(dpiAwarenessPerMonitorAwareV2)
		if old != 0 {
			defer procSetThreadDpiAwarenessCtx.Call(old)
		}
	}
	f()
}
func (app *Service) WorkArea() Rect {
	var r struct{ L, T, R, B int32 }
	ok := uintptr(0)
	app.PhysicalPixels(func() {
		ok, _, _ = procSystemParametersInfoW.Call(0x0030, 0, uintptr(unsafe.Pointer(&r)), 0)
	})
	if ok == 0 || r.R <= r.L || r.B <= r.T {
		return Rect{0, 0, 1920, 1040}
	}
	return Rect{int(r.L), int(r.T), int(r.R - r.L), int(r.B - r.T)}
}

// windowDecoration measures the title bar from system metrics; Windows 10/11 draw 1 px visible side borders.
func (app *Service) WindowDecoration() WindowFrame {
	metric := func(i uintptr) int {
		v, _, _ := procGetSystemMetrics.Call(i)
		return int(int32(v))
	}
	top := 0
	app.PhysicalPixels(func() {
		top = metric(4) + metric(33) + metric(92)
	})
	if top <= 0 {
		top = 31
	}
	return WindowFrame{Top: top, Side: 1, Bottom: 1}
}
