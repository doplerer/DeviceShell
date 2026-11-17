package core

import (
	"context"
	"time"
)

// startLimiter lets at most n starts run at once: a wall of phones opening together
// would otherwise push N servers, or switch N iPhones' USB mode, all at the same time.
type startLimiter chan struct{}

func newStartLimiter(n int) startLimiter { return make(startLimiter, n) }

// acquire waits for a free slot or for c to end.
func (l startLimiter) acquire(c context.Context) error {
	select {
	case l <- struct{}{}:
		return nil
	case <-c.Done():
		return c.Err()
	}
}

func (l startLimiter) release() { <-l }

var (
	// qtSwitches: QuickTime mode changes re-enumerate the iPhone on its USB controller;
	// a few at a time keep the controllers from re-enumerating in a chain.
	qtSwitches = newStartLimiter(4)
	// androidStarts: scrcpy servers being pushed and started.
	androidStarts = newStartLimiter(16)
)

// watchStall calls stalled, once, when counter has not moved for limit while active
// holds. It checks on every tick (the tick carries the time) and ends when done closes
// or after stalled; now (the start) and ticks come from the caller so tests can use a
// fake clock.
func watchStall(done <-chan struct{}, ticks <-chan time.Time, now func() time.Time, counter func() int64, active func() bool, limit time.Duration, stalled func()) {
	last, since := counter(), now()
	var t time.Time
	for {
		select {
		case <-done:
			return
		case t = <-ticks:
		}
		if v := counter(); v != last || !active() {
			last, since = v, t
			continue
		}
		if t.Sub(since) >= limit {
			stalled()
			return
		}
	}
}

// watchStallEvery runs watchStall with the real clock, checking every second.
func watchStallEvery(done <-chan struct{}, counter func() int64, active func() bool, limit time.Duration, stalled func()) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	watchStall(done, ticker.C, time.Now, counter, active, limit, stalled)
}
