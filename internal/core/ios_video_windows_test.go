package core

import (
	"context"
	"testing"
	"time"
)

func TestIOSVideoSaysWhyThereIsNoPicture(t *testing.T) {
	v := &iosVideo{missing: true}
	v.init("quicktime", nil)
	v.failure.Store("falta el controlador")
	if v.live() || v.flags() != videoQTMissing {
		t.Fatal("a missing driver must be reported", v.flags())
	}
	if got := noVideoStatus(v.flags() | videoNoPicture); got != "no video: USB driver missing, reconnect the iPhone" {
		t.Fatal(got)
	}
}

// The hub hands the newest picture to every window and reuses the buffers it released.
func TestFrameHubNewestFrameAndSpares(t *testing.T) {
	var h frameHub
	h.init("test", nil)
	img := solidImage(1320, 2868, 235, 128, 128, false)
	view := yuvView{w: img.w, h: img.h, cw: img.cw, sx: 1, sy: 1, y: img.y, u: img.u, v: img.v, ys: img.w, cs: img.cw}
	c := context.Background()
	if got, _ := h.next(c, 0, 10*time.Millisecond); got != nil {
		t.Fatal("no picture yet")
	}
	h.publish(view)
	a, seq := h.next(c, 0, time.Second)
	if a == nil || a.w != 1320 || a.h != 2868 {
		t.Fatal("first picture", a)
	}
	h.publish(view)
	h.publish(view)
	b, seq2 := h.next(c, seq, time.Second)
	if b == nil || seq2 != seq+2 {
		t.Fatal("a slow window must skip to the newest picture", seq, seq2)
	}
	h.release(a)
	h.release(b)
	h.mu.Lock()
	spare := len(h.spare)
	h.mu.Unlock()
	if spare != 2 {
		t.Fatalf("released pictures must return to the spare list, have %d", spare)
	}
}
