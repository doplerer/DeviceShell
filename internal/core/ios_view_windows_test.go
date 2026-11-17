package core

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

func TestNoVideoStatus(t *testing.T) {
	for flags, want := range map[uint32]string{
		1:         "waiting for video",
		1 | 2:     "touch blocked: restart the iPhone",
		1 | 4:     "USB video error (details in output/logs/ios-video-*.log)",
		1 | 4 | 2: "USB video error (details in output/logs/ios-video-*.log)",
		1 | 8 | 4: "no video: USB driver missing, reconnect the iPhone",
		1 | 16:    "no video from the phone (details in output/logs/android-video-*.log)",
	} {
		if got := noVideoStatus(flags); got != want {
			t.Errorf("flags %d: %q, want %q", flags, got, want)
		}
	}
}

// recordingInput keeps what a window sends.
type recordingInput struct {
	mu     sync.Mutex
	phases []string
	specs  []Spec
}

func (r *recordingInput) touch(phase string, x, y float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if x < 0 || x > 1 || y < 0 || y > 1 {
		return fmt.Errorf("touch outside 0..1: %v %v", x, y)
	}
	r.phases = append(r.phases, phase)
	return nil
}
func (r *recordingInput) scroll(float64, float64, int) error { return nil }
func (r *recordingInput) send(s Spec) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs = append(r.specs, s)
	return nil
}

// testLink feeds a window from a hub; opens counts how often the window asked.
type testLink struct {
	src   *benchSource
	in    viewInput
	opens atomic.Int32
}

func (l *testLink) open(context.Context) (viewSource, error) {
	l.opens.Add(1)
	return viewSource{hub: &l.src.frameHub, status: l.src}, nil
}
func (l *testLink) input() viewInput { return l.in }

func windowTitle(hwnd uintptr) string {
	title := make([]uint16, 256)
	user32.NewProc("GetWindowTextW").Call(hwnd, uintptr(unsafe.Pointer(&title[0])), 256)
	return syscall.UTF16ToString(title)
}

// The window lives in this process: it shows the hub's pictures fitted to its client
// area, passes the mouse and keys to its input and, watch only, has no input at all.
func TestNativeWindowInProcess(t *testing.T) {
	app := &Service{}
	for _, readOnly := range []bool{false, true} {
		src := &benchSource{}
		src.init("test", nil)
		rec := &recordingInput{}
		link := &testLink{src: src}
		if !readOnly {
			link.in = rec
		}
		released := make(chan struct{})
		c, cancel := context.WithCancel(context.Background())
		h, e := app.openView(c, windowConfig{Title: "DeviceShell window test", Area: Rect{20, 20, 240, 440}, Platform: "android"}, link, func() { close(released) })
		if e != nil {
			cancel()
			t.Fatal(e)
		}
		hwnd := h.Window()
		if hwnd == 0 || h.PID() != 0 {
			t.Fatal("an in-process window must give its handle", hwnd, h.PID())
		}
		// Area is the client area, like scrcpy's --window-* options.
		var area nativeRect
		var origin nativePoint
		app.PhysicalPixels(func() {
			procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&area)))
			procClientToScreen.Call(hwnd, uintptr(unsafe.Pointer(&origin)))
		})
		if got := (Rect{int(origin.X), int(origin.Y), int(area.Right), int(area.Bottom)}); got != (Rect{20, 20, 240, 440}) {
			t.Error("client area", got)
		}
		// Pure red, then pure blue pictures (YUV BT.709, video range).
		red, blue := solidImage(216, 468, 63, 102, 240, false), solidImage(216, 468, 32, 240, 118, false)
		view := func(img *yuvImage) yuvView {
			return yuvView{w: img.w, h: img.h, cw: img.cw, sx: 1, sy: 1, y: img.y, u: img.u, v: img.v, ys: img.w, cs: img.cw}
		}
		getDC, releaseDC, getPixel := user32.NewProc("GetDC"), user32.NewProc("ReleaseDC"), gdi32.NewProc("GetPixel")
		colours := map[uintptr]bool{}
		for i := 0; i < 40; i++ {
			if i%2 == 0 {
				src.publish(view(red))
			} else {
				src.publish(view(blue))
			}
			time.Sleep(30 * time.Millisecond)
			app.PhysicalPixels(func() {
				dc, _, _ := getDC.Call(hwnd)
				c, _, _ := getPixel.Call(dc, 120, 220)
				releaseDC.Call(hwnd, dc)
				colours[c&0xF0F0F0] = true
			})
		}
		if !colours[0x0000F0] || !colours[0xF00000] {
			t.Error("picture did not follow the frames", colours)
		}
		// Only name and fps: no hints, watch-only or not.
		if title := windowTitle(hwnd); !strings.HasPrefix(title, "DeviceShell window test · ") || !strings.HasSuffix(title, " fps") {
			t.Error("title", title)
		}
		procPostMessageW.Call(hwnd, 0x0102, 'a', 0)
		procPostMessageW.Call(hwnd, 0x0102, 0xD83D, 0)
		procPostMessageW.Call(hwnd, 0x0102, 0xDE0A, 0)
		// A drag that ends outside the window must still lift the finger.
		cx, cy := uintptr(area.Right/2), uintptr(area.Bottom/2)
		procPostMessageW.Call(hwnd, 0x0201, 1, cy<<16|cx)
		procPostMessageW.Call(hwnd, 0x0200, 1, (cy+30)<<16|cx)
		procPostMessageW.Call(hwnd, 0x0202, 0, uintptr(uint16(0xFFF0))<<16|cx)
		time.Sleep(300 * time.Millisecond)
		// Closing the window by hand ends it and releases the device.
		procPostMessageW.Call(hwnd, wmClose, 0, 0)
		finished := make(chan struct{})
		go func() { h.Wait(); close(finished) }()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("window did not close")
		}
		select {
		case <-released:
		default:
			t.Error("closing must release the device")
		}
		cancel()
		rec.mu.Lock()
		phases, specs := fmt.Sprint(rec.phases), len(rec.specs)
		rec.mu.Unlock()
		if !readOnly && (phases != "[down move up]" || specs != 2) {
			t.Error("input", phases, specs)
		}
		if readOnly && (phases != "[]" || specs != 0) {
			t.Error("a watch-only window sent input", phases, specs)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Closing the window's context closes it; the supervisor reopens a window closed by
// hand and stops with its context.
func TestSuperviseNativeWindow(t *testing.T) {
	app := &Service{}
	src := &benchSource{}
	src.init("test", nil)
	pack := app.StartWindowPack(1)
	defer pack.Stop()
	c, cancel := context.WithCancel(context.Background())
	defer cancel()
	var opened atomic.Int32
	var mu sync.Mutex
	var handles []viewHandle
	done := make(chan struct{})
	go func() {
		app.SuperviseWindow(c, 0, pack, func() (viewHandle, error) {
			opened.Add(1)
			h, e := app.openView(c, windowConfig{Title: "DeviceShell supervisor test", Area: Rect{40, 40, 200, 400}, Platform: "ios"}, &testLink{src: src}, func() {})
			if e == nil {
				mu.Lock()
				handles = append(handles, h)
				mu.Unlock()
			}
			return h, e
		}, nil)
		close(done)
	}()
	for end := time.Now().Add(5 * time.Second); !pack.HasWindow(0) && time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
	}
	if !pack.HasWindow(0) {
		t.Fatal("the window did not join the pack")
	}
	mu.Lock()
	first := handles[0]
	mu.Unlock()
	first.Kill()
	for end := time.Now().Add(5 * time.Second); opened.Load() < 2 && time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
	}
	if opened.Load() < 2 {
		t.Fatal("a closed window must open again")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervisor did not stop")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, h := range handles {
		finished := make(chan struct{})
		go func() { h.Wait(); close(finished) }()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatal("a window outlived its context")
		}
	}
	if n := perf.windows.Load(); n != 0 {
		t.Error("windows still counted open:", n)
	}
}
