package core

import (
	"testing"
)

func TestTouchQueueKeepsOrderAndCoalescesMoves(t *testing.T) {
	q := &touchQueue{signal: make(chan struct{}, 1)}
	for _, e := range []touchEvent{{"down", 0, 0, 0}, {"move", .1, 0, 0}, {"move", .2, 0, 0}, {"move", .3, 0, 0}, {"up", .3, 0, 0}, {"down", .5, .5, 0}, {"scroll", .5, .5, 1}, {"scroll", .5, .5, 1}, {"scroll", .5, .5, -3}} {
		q.push(e)
	}
	var got []touchEvent
	for e, ok := q.pop(); ok; e, ok = q.pop() {
		got = append(got, e)
	}
	// Wheel notches add up while unsent: 1 + 1 - 3.
	want := []touchEvent{{"down", 0, 0, 0}, {"move", .3, 0, 0}, {"up", .3, 0, 0}, {"down", .5, .5, 0}, {"scroll", .5, .5, -1}}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal(got)
		}
	}
}

func TestLetterbox(t *testing.T) {
	if x, y, w, h := letterbox(240, 440, 1080, 2340); x != 18 || y != 0 || w != 203 || h != 440 {
		t.Fatal(x, y, w, h)
	}
	if x, y, w, h := letterbox(400, 400, 100, 50); x != 0 || y != 100 || w != 400 || h != 200 {
		t.Fatal(x, y, w, h)
	}
}

func TestOpenRetryPauseGrows(t *testing.T) {
	want := []string{"300ms", "1s", "2s", "4s", "8s", "15s", "15s"}
	for i, w := range want {
		if got := openRetryPause(i).String(); got != w {
			t.Fatalf("after %d failures: %s, want %s", i, got, w)
		}
	}
}
