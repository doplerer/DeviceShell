package core

import (
	"encoding/binary"
	"testing"
	"time"
)

func TestControlGestureQueuePauseAndOverflow(t *testing.T) {
	q := &controlQueue{events: make(chan ControlEvent, 32)}
	s := &LiveSession{app: New(), controls: map[string]*controlQueue{"ios:X": q}, Errors: map[string]string{}}
	frame := make([]byte, 32)
	frame[0] = 2
	binary.BigEndian.PutUint16(frame[18:], 101)
	binary.BigEndian.PutUint16(frame[20:], 201)
	binary.BigEndian.PutUint32(frame[10:], 50)
	binary.BigEndian.PutUint32(frame[14:], 100)
	s.decodeControl(frame)
	frame[1] = 1
	s.decodeControl(frame)
	ev := <-q.events
	if ev.Spec.Action != "tap" || ev.Spec.X != .5 || ev.Spec.Y != .5 {
		t.Fatal(ev)
	}
	s.Pause()
	s.enqueueControl(Spec{Action: "tap"})
	if len(q.events) != 0 {
		t.Fatal("paused enqueue")
	}
	s.Resume()
	s.enqueueControl(Spec{Action: "key", Args: []string{"HOME"}})
	if ev = <-q.events; ev.Generation != 1 {
		t.Fatal("generation not advanced")
	}
	for i := 0; i < 33; i++ {
		s.enqueueControl(Spec{Action: "tap"})
	}
	if !q.stopped || s.Info().Errors["ios:X"] == "" {
		t.Fatal("overflow not isolated")
	}
}
func TestPausedGestureDoesNotLeak(t *testing.T) {
	q := &controlQueue{events: make(chan ControlEvent, 32)}
	s := &LiveSession{app: New(), controls: map[string]*controlQueue{"x": q}, Errors: map[string]string{}}
	s.gesture = gestureState{active: true, started: time.Now()}
	s.Pause()
	s.Resume()
	if s.gesture.active {
		t.Fatal("gesture survived pause")
	}
}
