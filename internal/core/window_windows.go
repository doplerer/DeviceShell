package core

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/atotto/clipboard"

	"github.com/doplerer/deviceshell/internal/procstat"
)

// A DeviceShell window is a Win32 window of this process with its own thread (a goroutine
// locked to it, idle in GetMessage) and a render goroutine. The render goroutine takes
// each new picture from the device's frame hub and scales it straight into the back
// one of two DIB sections, swaps them and invalidates; WM_PAINT only copies the front
// one to the screen. A minimized window neither scales nor paints.

type nativeRect struct{ Left, Top, Right, Bottom int32 }
type nativePoint struct{ X, Y int32 }
type nativeMsg struct {
	Window         uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	X, Y           int32
	Private        uint32
}
type nativeClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClsExtra, WndExtra                 int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Name                         *uint16
	SmallIcon                          uintptr
}
type nativePaint struct {
	DC              uintptr
	Erase           int32
	Area            nativeRect
	Restore, Update int32
	Reserved        [32]byte
}
type dibHeader struct {
	Size                   uint32
	Width, Height          int32
	Planes, BitCount       uint16
	Compression, SizeImage uint32
	XPels, YPels           int32
	Used, Important        uint32
}

var (
	gdi32                 = syscall.NewLazyDLL("gdi32.dll")
	procBeginPaint        = user32.NewProc("BeginPaint")
	procEndPaint          = user32.NewProc("EndPaint")
	procFillRect          = user32.NewProc("FillRect")
	procGetClientRect     = user32.NewProc("GetClientRect")
	procInvalidateRect    = user32.NewProc("InvalidateRect")
	procSetWindowTextW    = user32.NewProc("SetWindowTextW")
	procPostMessageW      = user32.NewProc("PostMessageW")
	procPostQuitMessage   = user32.NewProc("PostQuitMessage")
	procDefWindowProcW    = user32.NewProc("DefWindowProcW")
	procGetMessageW       = user32.NewProc("GetMessageW")
	procTranslateMessage  = user32.NewProc("TranslateMessage")
	procDispatchMessageW  = user32.NewProc("DispatchMessageW")
	procSetCapture        = user32.NewProc("SetCapture")
	procReleaseCapture    = user32.NewProc("ReleaseCapture")
	procScreenToClient    = user32.NewProc("ScreenToClient")
	procClientToScreen    = user32.NewProc("ClientToScreen")
	procGetKeyState       = user32.NewProc("GetKeyState")
	procRegisterClassExW  = user32.NewProc("RegisterClassExW")
	procCreateWindowExW   = user32.NewProc("CreateWindowExW")
	procLoadCursorW       = user32.NewProc("LoadCursorW")
	procGetModuleHandleW  = kernel32.NewProc("GetModuleHandleW")
	procCreateCompatDC    = gdi32.NewProc("CreateCompatibleDC")
	procSelectObject      = gdi32.NewProc("SelectObject")
	procDeleteObject      = gdi32.NewProc("DeleteObject")
	procDeleteDC          = gdi32.NewProc("DeleteDC")
	procGetStockObject    = gdi32.NewProc("GetStockObject")
	procSetStretchBltMode = gdi32.NewProc("SetStretchBltMode")
	procStretchBlt        = gdi32.NewProc("StretchBlt")
	procBitBlt            = gdi32.NewProc("BitBlt")
	procCreateDIBSection  = gdi32.NewProc("CreateDIBSection")
	procGetDC             = user32.NewProc("GetDC")

	// One window class and one window procedure for every window: Go can only make a
	// limited number of callbacks, so they cannot be one per window.
	windowsMu       sync.RWMutex
	windowsByHandle = map[uintptr]*nativeWindow{}
	windowClassOnce sync.Once
	windowClassErr  error
	windowClassName = syscall.StringToUTF16Ptr("DEVICESHELL_VIEW")
	windowProc      = syscall.NewCallback(func(h uintptr, msg uint32, wp, lp uintptr) uintptr {
		windowsMu.RLock()
		w := windowsByHandle[h]
		windowsMu.RUnlock()
		if w == nil {
			r, _, _ := procDefWindowProcW.Call(h, uintptr(msg), wp, lp)
			return r
		}
		return w.message(msg, wp, lp)
	})
)

const (
	wmClose   = 0x0010
	srcCopy   = 0x00CC0020
	sizeIcons = 1 // WM_SIZE: SIZE_MINIMIZED
)

func registerWindowClass() error {
	windowClassOnce.Do(func() {
		instance, _, _ := procGetModuleHandleW.Call(0)
		arrow, _, _ := procLoadCursorW.Call(0, 32512) // IDC_ARROW
		// CS_OWNDC: each window keeps its DC, which the render goroutine draws on.
		class := nativeClass{Size: uint32(unsafe.Sizeof(nativeClass{})), Style: 0x0020, Proc: windowProc, Instance: instance, Cursor: arrow, Name: windowClassName}
		if ok, _, e := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&class))); ok == 0 {
			windowClassErr = fmt.Errorf("RegisterClassExW: %v", e)
		}
	})
	return windowClassErr
}

// dibBuffer is a top-down 32-bit DIB section whose pixels the render goroutine writes.
type dibBuffer struct {
	bitmap uintptr
	bits   []byte
	w, h   int
}

func newDIB(w, h int) (*dibBuffer, error) {
	header := dibHeader{Size: 40, Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	var bits unsafe.Pointer
	bitmap, _, e := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&header)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bitmap == 0 || bits == nil {
		return nil, fmt.Errorf("CreateDIBSection %dx%d: %v", w, h, e)
	}
	return &dibBuffer{bitmap: bitmap, bits: unsafe.Slice((*byte)(bits), w*h*4), w: w, h: h}, nil
}

func (d *dibBuffer) free() {
	if d != nil && d.bitmap != 0 {
		procDeleteObject.Call(d.bitmap)
		d.bitmap, d.bits = 0, nil
	}
}

// touchQueue keeps finger samples in order but lets a newer move replace a move
// that has not been sent yet, and adds wheel notches to a scroll not sent yet, so a
// slow link never builds up lag.
type touchQueue struct {
	mu     sync.Mutex
	events []touchEvent
	signal chan struct{}
}

func (q *touchQueue) push(e touchEvent) {
	q.mu.Lock()
	n := len(q.events)
	switch {
	case e.Phase == "move" && n > 0 && q.events[n-1].Phase == "move":
		q.events[n-1] = e
	case e.Phase == "scroll" && n > 0 && q.events[n-1].Phase == "scroll":
		q.events[n-1].Steps += e.Steps
	default:
		q.events = append(q.events, e)
	}
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

func (q *touchQueue) pop() (touchEvent, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.events) == 0 {
		return touchEvent{}, false
	}
	e := q.events[0]
	q.events = q.events[1:]
	return e, true
}

// noVideoStatus is the title status for a source without picture (flag bit 0).
func noVideoStatus(flags uint32) string {
	switch {
	case flags&videoSourceFailed != 0:
		return "no video from the phone (details in output/logs/android-video-*.log)"
	case flags&videoQTMissing != 0:
		return "no video: USB driver missing, reconnect the iPhone"
	case flags&videoQTFailed != 0:
		return "USB video error (details in output/logs/ios-video-*.log)"
	case flags&videoWedged != 0:
		return "touch blocked: restart the iPhone"
	default:
		return "waiting for video"
	}
}

type nativeWindow struct {
	cfg     windowConfig
	link    viewLink
	in      viewInput // nil: watch only
	android bool
	hwnd    uintptr
	ctx     context.Context
	cancel  context.CancelFunc
	release func()
	// loopDone closes when the message loop ends, done when everything is released.
	loopDone, done, renderDone chan struct{}

	// The picture: render writes back, paint shows front; both under mu.
	mu          sync.Mutex
	front, back *dibBuffer
	iw, ih      int     // the device picture size (its aspect), for letterbox and touches
	seq, shown  uint64  // pictures swapped in and painted
	dc          uintptr // the window's own DC (CS_OWNDC)
	memDC       uintptr // holds the front DIB selected
	memDefault  uintptr // memDC's own bitmap, put back before deleting it
	barsFor     [4]int  // client and picture size the bars were last filled for

	clientW, clientH atomic.Int32
	minimized        atomic.Bool
	wake             chan struct{} // WM_SIZE: the render goroutine looks again

	// Input, on the window thread.
	down          bool
	lastX, lastY  float64
	highSurrogate uint16
	events        chan Spec
	touches       *touchQueue
	wheelMu       sync.Mutex
	wheelSteps    int
	wheelX        float64
	wheelY        float64
	wheelTimer    *time.Timer

	titleMu    sync.Mutex
	shownTitle string
}

// openNativeWindow opens the window on its own thread and returns once it exists.
// release runs after the window closed (also when c ends).
func openNativeWindow(c context.Context, cfg windowConfig, link viewLink, release func()) (viewHandle, error) {
	w := &nativeWindow{cfg: cfg, link: link, in: link.input(), android: cfg.Platform == "android", release: release,
		loopDone: make(chan struct{}), done: make(chan struct{}), renderDone: make(chan struct{}),
		wake: make(chan struct{}, 1), events: make(chan Spec, 32), touches: &touchQueue{signal: make(chan struct{}, 1)}}
	w.ctx, w.cancel = context.WithCancel(c)
	// Device aspect until the first frame arrives (portrait).
	w.iw, w.ih = 1320, 2868
	if w.android {
		w.iw, w.ih = 1080, 2340
	}
	if cfg.Aspect > 0 {
		w.iw, w.ih = 1000, int(1000*cfg.Aspect)
	}
	ready := make(chan error, 1)
	go w.run(ready)
	if e := <-ready; e != nil {
		w.cancel()
		return nil, e
	}
	return w, nil
}

func (w *nativeWindow) Wait() error     { <-w.done; return nil }
func (w *nativeWindow) Window() uintptr { return w.hwnd }
func (w *nativeWindow) PID() int        { return 0 }
func (w *nativeWindow) Kill() {
	select {
	case <-w.loopDone:
	default:
		procPostMessageW.Call(w.hwnd, wmClose, 0, 0)
	}
}

// run is the window thread: it never unlocks, so the thread ends with the window.
func (w *nativeWindow) run(ready chan<- error) {
	runtime.LockOSThread()
	// The layout is in physical pixels, the space scrcpy (SDL3) places its windows in.
	if procSetThreadDpiAwarenessCtx.Find() == nil {
		procSetThreadDpiAwarenessCtx.Call(dpiAwarenessPerMonitorAwareV2)
	}
	if e := registerWindowClass(); e != nil {
		ready <- e
		return
	}
	instance, _, _ := procGetModuleHandleW.Call(0)
	title := w.titleWith("connecting")
	area := w.cfg.Area
	hwnd, _, e := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(windowClassName)), uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(title))), 0x00CF0000,
		uintptr(area.X), uintptr(area.Y), uintptr(max(100, area.W)), uintptr(max(100, area.H)), 0, 0, instance, 0)
	if hwnd == 0 {
		ready <- fmt.Errorf("CreateWindowExW: %v", e)
		return
	}
	w.hwnd, w.shownTitle = hwnd, title
	w.dc, _, _ = procGetDC.Call(hwnd)
	w.memDC, _, _ = procCreateCompatDC.Call(w.dc)
	windowsMu.Lock()
	windowsByHandle[hwnd] = w
	windowsMu.Unlock()
	// Area is the client area, as scrcpy's --window-* options: grow the window by its
	// title bar and borders (the invisible ones too) before showing it.
	var outer, inner nativeRect
	var origin nativePoint
	procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&outer)))
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&inner)))
	procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&origin)))
	left, top := origin.X-outer.Left, origin.Y-outer.Top
	extraW, extraH := (outer.Right-outer.Left)-inner.Right, (outer.Bottom-outer.Top)-inner.Bottom
	// SWP_NOZORDER|SWP_NOACTIVATE, then SW_SHOWNOACTIVATE: a wall of windows opening does
	// not take the keyboard from the console window after window.
	procSetWindowPos.Call(hwnd, 0, uintptr(int32(area.X)-left), uintptr(int32(area.Y)-top), uintptr(int32(max(100, area.W))+extraW), uintptr(int32(max(100, area.H))+extraH), 0x0004|0x0010)
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&inner)))
	w.clientW.Store(inner.Right)
	w.clientH.Store(inner.Bottom)
	procShowWindow.Call(hwnd, 4)
	ready <- nil

	go w.render()
	if w.in != nil {
		go w.sendInput()
		go w.sendTouches()
	}
	go func() {
		select {
		case <-w.ctx.Done():
			procPostMessageW.Call(hwnd, wmClose, 0, 0)
		case <-w.loopDone:
		}
	}()
	var message nativeMsg
	for {
		result, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0)
		if int32(result) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&message)))
	}
	close(w.loopDone)
	w.cancel()
	<-w.renderDone
	windowsMu.Lock()
	delete(windowsByHandle, hwnd)
	windowsMu.Unlock()
	w.mu.Lock()
	if w.memDefault != 0 {
		procSelectObject.Call(w.memDC, w.memDefault)
	}
	w.front.free()
	w.back.free()
	w.front, w.back = nil, nil
	procDeleteDC.Call(w.memDC)
	w.mu.Unlock()
	if w.wheelTimer != nil {
		w.wheelTimer.Stop()
	}
	w.release()
	close(w.done)
}

// titleWith is the window title for a status, kept short so it is not cut off:
// "DEV-019 · iOS Wi-Fi · 43 fps".
func (w *nativeWindow) titleWith(status string) string {
	return w.cfg.Title + " · " + status
}

func (w *nativeWindow) setTitle(status string) {
	w.titleMu.Lock()
	defer w.titleMu.Unlock()
	if t := w.titleWith(status); t != w.shownTitle {
		w.shownTitle = t
		procSetWindowTextW.Call(w.hwnd, uintptr(unsafe.Pointer(syscall.StringToUTF16Ptr(t))))
	}
}

func (w *nativeWindow) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// render plays the device's picture, opening it again whenever its connector stops.
// A device that cannot be opened (locked, unplugged, busy) keeps its window, which
// says why and tries again after a growing pause: closing and reopening the window
// would only make it blink.
func (w *nativeWindow) render() {
	defer close(w.renderDone)
	failures := 0
	for w.ctx.Err() == nil {
		sc, stop := context.WithCancel(w.ctx)
		src, e := w.link.open(sc)
		if e != nil {
			stop()
			if w.ctx.Err() != nil {
				return
			}
			pause := openRetryPause(failures)
			failures++
			why := []rune(e.Error())
			if len(why) > 80 {
				why = append(why[:77], '…')
			}
			w.setTitle(fmt.Sprintf("disconnected: %s · retrying in %.0f s", string(why), max(1, pause.Seconds())))
			select {
			case <-w.ctx.Done():
				return
			case <-time.After(pause):
			}
			continue
		}
		failures = 0
		if src.done != nil {
			go func() {
				select {
				case <-src.done:
					stop()
				case <-sc.Done():
				}
			}()
		}
		w.play(sc, src)
		stop()
		if w.ctx.Err() == nil {
			w.setTitle("reconnecting")
		}
	}
}

// play shows the pictures of src until sc ends.
func (w *nativeWindow) play(sc context.Context, src viewSource) {
	wedged := src.wedged
	if wedged == nil {
		wedged = func() bool { return false }
	}
	watching := false
	watch := func(on bool) {
		if on != watching {
			watching = on
			src.hub.watch(on)
		}
	}
	defer watch(false)
	var last uint64
	var scaler yuvScaler
	frames, rateStart := 0, time.Time{}
	// Every 5 s, also without pictures, the device's log says how this window does,
	// so a stall shows where it is.
	var (
		statsAt                  = time.Now()
		statsDecoded, statsUSB   = src.hub.decoded.Load(), src.status.received()
		shown                    int
		rendering                int64
		fw, fh, sourceW, sourceH int
	)
	src.hub.say("window %dx%d", w.clientW.Load(), w.clientH.Load())
	stats := func() {
		elapsed := time.Since(statsAt).Seconds()
		if elapsed < 5 {
			return
		}
		decoded, usb := src.hub.decoded.Load(), src.status.received()
		src.hub.say("video %dx%d (%s %dx%d): decoded=%.1f fps shown=%.1f fps render=%.2f ms usb=%.1f Mbit/s",
			fw, fh, src.hub.source, sourceW, sourceH, float64(decoded-statsDecoded)/elapsed, float64(shown)/elapsed,
			float64(rendering)/1e6/float64(max(1, shown)), float64(usb-statsUSB)*8/1e6/elapsed)
		statsAt, statsDecoded, statsUSB, shown, rendering = time.Now(), decoded, usb, 0, 0
	}
	for sc.Err() == nil {
		stats()
		if w.minimized.Load() {
			watch(false)
			select {
			case <-w.wake:
			case <-sc.Done():
			}
			continue
		}
		watch(true)
		img, seq := src.hub.next(sc, last, time.Second)
		if img == nil {
			if sc.Err() == nil && !src.status.live() {
				flags := src.status.flags() | videoNoPicture
				if wedged() {
					flags |= videoWedged
				}
				w.setTitle(noVideoStatus(flags))
				rateStart = time.Time{}
			}
			continue
		}
		last = seq
		started := procstat.Now()
		fw, fh = fitScaled(img.w, img.h, max(16, int(w.clientW.Load())), max(16, int(w.clientH.Load())))
		back, e := w.backBuffer(fw, fh)
		if e != nil {
			src.hub.release(img)
			w.setTitle("out of memory for the picture")
			continue
		}
		scaler.scale(img, back.bits, fw, fh)
		iw, ih := img.w, img.h
		sourceW, sourceH = iw, ih
		src.hub.release(img)
		perf.scaleNs.Add(procstat.Now() - started)
		w.mu.Lock()
		w.front, w.back = back, w.front
		// The front DIB stays selected in memDC; the one it replaces is free again.
		if old, _, _ := procSelectObject.Call(w.memDC, back.bitmap); w.memDefault == 0 {
			w.memDefault = old
		}
		w.iw, w.ih = iw, ih
		w.seq++
		w.mu.Unlock()
		w.present()
		shown++
		rendering += procstat.Now() - started
		// The title shows the frame rate actually shown, once per second.
		if rateStart.IsZero() {
			frames, rateStart = 0, time.Now()
			continue
		}
		frames++
		if elapsed := time.Since(rateStart); elapsed >= time.Second {
			status := fmt.Sprintf("%.0f fps", float64(frames)/elapsed.Seconds())
			if wedged() {
				status += " · touch blocked: restart the iPhone"
			}
			w.setTitle(status)
			frames, rateStart = 0, time.Now()
		}
	}
}

// backBuffer returns the back DIB at fw x fh, making a new one when the size changed.
func (w *nativeWindow) backBuffer(fw, fh int) (*dibBuffer, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.back != nil && w.back.w == fw && w.back.h == fh {
		return w.back, nil
	}
	w.back.free()
	w.back = nil
	b, e := newDIB(fw, fh)
	if e != nil {
		return nil, e
	}
	w.back = b
	return b, nil
}

// openRetryPause is the wait after the failed opens so far: 300 ms, then 1, 2, 4, 8
// and 15 s.
func openRetryPause(failures int) time.Duration {
	if failures == 0 {
		return 300 * time.Millisecond
	}
	return min(time.Second<<(failures-1), 15*time.Second)
}

// letterbox is where a picture of aspect iw x ih goes in a cw x ch client area.
func letterbox(cw, ch, iw, ih int) (int, int, int, int) {
	if iw < 1 || ih < 1 {
		return 0, 0, cw, ch
	}
	if cw*ih > ch*iw {
		nw := ch * iw / ih
		return (cw - nw) / 2, 0, nw, ch
	}
	nh := cw * ih / iw
	return 0, (ch - nh) / 2, cw, nh
}

// area is the letterboxed image inside the client area.
func (w *nativeWindow) area() (int, int, int, int) {
	w.mu.Lock()
	iw, ih := w.iw, w.ih
	w.mu.Unlock()
	return letterbox(int(w.clientW.Load()), int(w.clientH.Load()), iw, ih)
}

// point maps client pixels to 0..1, clamped to the image edge; inside reports
// whether the pointer was on the image itself.
func (w *nativeWindow) point(px, py int) (float64, float64, bool) {
	ox, oy, aw, ah := w.area()
	if aw < 2 || ah < 2 {
		return 0, 0, false
	}
	inside := px >= ox && py >= oy && px < ox+aw && py < oy+ah
	x := max(0, min(1, float64(px-ox)/float64(aw-1)))
	y := max(0, min(1, float64(py-oy)/float64(ah-1)))
	return x, y, inside
}

// draw puts the front picture on dc and, with bars, fills the black bars around it.
// The picture is fitted to the window: only the bars are filled, so nothing flickers.
// Until a picture of a new window size arrives the last one is stretched. Caller holds w.mu.
func (w *nativeWindow) draw(dc uintptr, bars bool) {
	cw, ch := int(w.clientW.Load()), int(w.clientH.Load())
	black, _, _ := procGetStockObject.Call(4)
	fill := func(r nativeRect) {
		if r.Right > r.Left && r.Bottom > r.Top {
			procFillRect.Call(dc, uintptr(unsafe.Pointer(&r)), black)
		}
	}
	front := w.front
	if front == nil {
		if bars {
			fill(nativeRect{0, 0, int32(cw), int32(ch)})
		}
		return
	}
	x, y, iw, ih := letterbox(cw, ch, front.w, front.h)
	if abs(iw-front.w) <= 2 && abs(ih-front.h) <= 2 {
		iw, ih = front.w, front.h
		x, y = (cw-iw)/2, (ch-ih)/2
	}
	if bars {
		fill(nativeRect{0, 0, int32(cw), int32(y)})
		fill(nativeRect{0, int32(y + ih), int32(cw), int32(ch)})
		fill(nativeRect{0, int32(y), int32(x), int32(y + ih)})
		fill(nativeRect{int32(x + iw), int32(y), int32(cw), int32(y + ih)})
		w.barsFor = [4]int{cw, ch, front.w, front.h}
	}
	if iw == front.w && ih == front.h {
		procBitBlt.Call(dc, uintptr(x), uintptr(y), uintptr(iw), uintptr(ih), w.memDC, 0, 0, srcCopy)
	} else {
		procSetStretchBltMode.Call(dc, 3) // COLORONCOLOR: fast, the frame is transient
		procStretchBlt.Call(dc, uintptr(x), uintptr(y), uintptr(iw), uintptr(ih), w.memDC, 0, 0, uintptr(front.w), uintptr(front.h), srcCopy)
	}
	w.shown = w.seq
}

// paint answers WM_PAINT (the window was uncovered or resized): everything again.
func (w *nativeWindow) paint() {
	var p nativePaint
	procBeginPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
	w.mu.Lock()
	w.draw(w.dc, true)
	w.mu.Unlock()
	procEndPaint.Call(w.hwnd, uintptr(unsafe.Pointer(&p)))
}

// present shows a new front picture right away, from the render goroutine, on the
// window's own DC: no WM_PAINT round trip, whose window manager calls are what limits
// a wall of hundreds of windows at 60 fps.
func (w *nativeWindow) present() {
	started := procstat.Now()
	w.mu.Lock()
	bars := w.front == nil || w.barsFor != [4]int{int(w.clientW.Load()), int(w.clientH.Load()), w.front.w, w.front.h}
	w.draw(w.dc, bars)
	w.mu.Unlock()
	perf.painted.Add(1)
	perf.paintNs.Add(procstat.Now() - started)
}

// sendInput passes keys and text to the device, in order.
func (w *nativeWindow) sendInput() {
	for {
		select {
		case <-w.ctx.Done():
			return
		case spec := <-w.events:
			if e := w.in.send(spec); e != nil {
				w.setTitle("input rejected")
			}
		}
	}
}

// sendTouches passes finger samples in order, moves coalesced and paced at ~80 Hz.
func (w *nativeWindow) sendTouches() {
	var lastMove time.Time
	for {
		select {
		case <-w.ctx.Done():
			return
		case <-w.touches.signal:
		}
		for {
			e, ok := w.touches.pop()
			if !ok {
				break
			}
			if e.Phase == "move" {
				if wait := 12*time.Millisecond - time.Since(lastMove); wait > 0 {
					time.Sleep(wait)
				}
				lastMove = time.Now()
			}
			var err error
			if e.Phase == "scroll" {
				err = w.in.scroll(e.X, e.Y, e.Steps)
			} else {
				err = w.in.touch(e.Phase, e.X, e.Y)
			}
			if err != nil {
				w.setTitle("input rejected")
			}
		}
	}
}

func (w *nativeWindow) send(s Spec) {
	if w.in == nil {
		return
	}
	select {
	case w.events <- s:
	default:
		w.setTitle("cola de entrada llena")
	}
}

func (w *nativeWindow) touch(phase string, x, y float64) {
	if w.in != nil {
		w.touches.push(touchEvent{Phase: phase, X: x, Y: y})
	}
}

// flushWheel turns a wheel burst into one short finger drag at the pointer (iPhone).
func (w *nativeWindow) flushWheel() {
	w.wheelMu.Lock()
	steps, x, y := w.wheelSteps, w.wheelX, w.wheelY
	w.wheelSteps = 0
	w.wheelMu.Unlock()
	if steps == 0 || w.down {
		return
	}
	distance := max(-.4, min(.4, .12*float64(steps)))
	y2 := max(0, min(1, y+distance))
	w.touch("down", x, y)
	for i := 1; i <= 6; i++ {
		w.touch("move", x, y+(y2-y)*float64(i)/6)
		time.Sleep(20 * time.Millisecond)
	}
	w.touch("up", x, y2)
}

func clientPoint(lp uintptr) (int, int) {
	return int(int16(lp & 65535)), int(int16((lp >> 16) & 65535))
}

// message is the window procedure, on the window thread.
func (w *nativeWindow) message(msg uint32, wp, lp uintptr) uintptr {
	h := w.hwnd
	switch msg {
	case 0x0002: // WM_DESTROY
		procPostQuitMessage.Call(0)
		return 0
	case 0x0005: // WM_SIZE
		w.minimized.Store(wp == sizeIcons)
		if wp != sizeIcons {
			w.clientW.Store(int32(lp & 65535))
			w.clientH.Store(int32((lp >> 16) & 65535))
		}
		w.signal()
		procInvalidateRect.Call(h, 0, 0)
		return 0
	case 0x0014: // WM_ERASEBKGND: painting covers everything
		return 1
	case 0x000F: // WM_PAINT
		w.paint()
		return 0
	case 0x0201: // WM_LBUTTONDOWN
		px, py := clientPoint(lp)
		if x, y, inside := w.point(px, py); inside {
			w.down, w.lastX, w.lastY = true, x, y
			procSetCapture.Call(h)
			w.touch("down", x, y)
		}
		return 0
	case 0x0200: // WM_MOUSEMOVE
		if w.down {
			px, py := clientPoint(lp)
			x, y, _ := w.point(px, py)
			if x != w.lastX || y != w.lastY {
				w.lastX, w.lastY = x, y
				w.touch("move", x, y)
			}
		}
		return 0
	case 0x0202: // WM_LBUTTONUP
		if w.down {
			px, py := clientPoint(lp)
			x, y, _ := w.point(px, py)
			w.down = false
			w.touch("up", x, y)
			procReleaseCapture.Call()
		}
		return 0
	case 0x0215, 0x0008: // WM_CAPTURECHANGED, WM_KILLFOCUS: lift the finger
		if w.down {
			w.down = false
			w.touch("up", w.lastX, w.lastY)
		}
		return 0
	case 0x020A: // WM_MOUSEWHEEL (screen coordinates)
		pt := nativePoint{int32(int16(lp & 65535)), int32(int16((lp >> 16) & 65535))}
		procScreenToClient.Call(h, uintptr(unsafe.Pointer(&pt)))
		x, y, inside := w.point(int(pt.X), int(pt.Y))
		if !inside {
			x, y = .5, .5
		}
		step := 1
		if int16(wp>>16) < 0 {
			step = -1
		}
		if w.in == nil {
			return 0
		}
		if w.android {
			// Android scrolls for real, notch by notch, where the pointer is.
			w.touches.push(touchEvent{Phase: "scroll", X: x, Y: y, Steps: step})
			return 0
		}
		w.wheelMu.Lock()
		w.wheelSteps += step
		w.wheelX, w.wheelY = x, y
		if w.wheelTimer == nil {
			w.wheelTimer = time.AfterFunc(80*time.Millisecond, w.flushWheel)
		} else {
			w.wheelTimer.Reset(80 * time.Millisecond)
		}
		w.wheelMu.Unlock()
		return 0
	case 0x0204: // WM_RBUTTONDOWN: BACK on Android, as in scrcpy
		if w.android {
			w.send(Spec{Action: "key", Args: []string{"BACK"}})
		}
		return 0
	case 0x0100: // WM_KEYDOWN
		ctrl, _, _ := procGetKeyState.Call(0x11)
		if w.android && wp == 0x1B { // Esc
			w.send(Spec{Action: "key", Args: []string{"BACK"}})
			return 0
		}
		if int16(ctrl) < 0 && wp == 'V' {
			if w.in != nil {
				if text, e := clipboard.ReadAll(); e == nil {
					w.send(Spec{Action: "text", Args: []string{text}})
				}
			}
			return 0
		}
		if int16(ctrl) < 0 && wp == 'H' {
			w.send(Spec{Action: "key", Args: []string{"HOME"}})
			return 0
		}
		if wp == 13 {
			w.send(Spec{Action: "key", Args: []string{"ENTER"}})
			return 0
		}
		if wp == 8 {
			w.send(Spec{Action: "key", Args: []string{"DEL"}})
			return 0
		}
	case 0x0102: // WM_CHAR
		if wp >= 0xD800 && wp <= 0xDBFF {
			w.highSurrogate = uint16(wp)
			return 0
		}
		character := rune(wp)
		if wp >= 0xDC00 && wp <= 0xDFFF {
			if w.highSurrogate == 0 {
				return 0
			}
			character = utf16.DecodeRune(rune(w.highSurrogate), rune(wp))
		}
		w.highSurrogate = 0
		if character >= 32 {
			w.send(Spec{Action: "text", Args: []string{string(character)}})
		}
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(h, uintptr(msg), wp, lp)
	return r
}
