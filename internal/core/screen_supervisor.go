package core

import (
	"context"
	"errors"
	"time"
)

// windowStartTimeout is how long a launched window may take to appear before it is killed and relaunched.
var windowStartTimeout = 20 * time.Second

// windowBackoff is the pause before the next relaunch, given the previous pause and how long the last
// launch lived: 1 s, then doubling up to 15 s, and back to 1 s after a launch that ran for 30 s.
func (app *Service) WindowBackoff(prev, ranFor time.Duration) time.Duration {
	if prev <= 0 || ranFor >= 30*time.Second {
		return time.Second
	}
	return min(2*prev, 15*time.Second)
}

// superviseWindow keeps the window of slot open until c ends. Whenever it cannot open, closes for any
// reason (also when closed by hand) or, being a process, shows no window in time, it is opened again
// after a growing pause. exited runs after every launch, before the pause and also when c ends; its
// handle is nil when the window could not open.
func (app *Service) SuperviseWindow(c context.Context, slot int, pack *WindowPack, start func() (viewHandle, error), exited func(viewHandle, error)) {
	wait := time.Duration(0)
	for c.Err() == nil {
		began := time.Now()
		h, e := start()
		if e == nil {
			pack.Track(slot, h.PID(), h.Window())
			e = app.waitWindow(c, slot, pack, h)
			pack.Track(slot, 0, 0)
		}
		if exited != nil {
			exited(h, e)
		}
		wait = app.WindowBackoff(wait, time.Since(began))
		select {
		case <-c.Done():
			return
		case <-time.After(wait):
		}
	}
}

// waitWindow waits for h to close, killing it if its window does not join the pack in time.
func (app *Service) waitWindow(c context.Context, slot int, pack *WindowPack, h viewHandle) error {
	exit := make(chan error, 1)
	go func() { exit <- h.Wait() }()
	deadline := time.NewTimer(windowStartTimeout)
	defer deadline.Stop()
	poll := time.NewTicker(250 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case e := <-exit:
			return e
		case <-poll.C:
			if pack.HasWindow(slot) {
				poll.Stop()
				deadline.Stop()
			}
		case <-deadline.C:
			if !pack.HasWindow(slot) {
				h.Kill()
				<-exit
				return errors.New(app.L("La ventana no apareció a tiempo", "The window did not appear in time"))
			}
		case <-c.Done():
			h.Kill()
			return <-exit
		}
	}
}
