package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
)

// iosViewLinger is how long the connector outlives the last iPhone window.
const iosViewLinger = time.Minute

type ControlEvent struct {
	Spec       Spec
	Generation uint64
}

// openIOSWindow opens the window at once, saying "conectando", and starts the
// connector behind it, as Android does: starting QuickTime and the tunnel takes seconds.
// The connector stays in this process; a watch-only window gets no input at all.
func (app *Service) openIOSWindow(c context.Context, d Device, r Rect, readOnly bool, onInput func(Spec)) (viewHandle, error) {
	session := app.iosSession(d)
	session.mu.Lock()
	if session.controller && !readOnly {
		session.mu.Unlock()
		return nil, errors.New("iPhone already has a controller")
	}
	session.viewRefs++
	session.views.Add(1)
	if session.idle != nil {
		session.idle.Stop()
		session.idle = nil
	}
	if !readOnly {
		session.controller = true
	}
	session.mu.Unlock()
	ctx, cancel := context.WithCancel(c)
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			cancel()
			session.mu.Lock()
			session.viewRefs--
			session.views.Add(-1)
			if !readOnly {
				session.controller = false
			}
			if session.viewRefs == 0 {
				// Keep the connector, and its live video, for a reopened window:
				// starting device streams too often wedges the iPhone's video
				// service until it is restarted.
				var idle *time.Timer
				idle = time.AfterFunc(iosViewLinger, func() {
					session.mu.Lock()
					defer session.mu.Unlock()
					if session.idle == idle && session.viewRefs == 0 {
						session.close(app)
						session.idle = nil
					}
				})
				session.idle = idle
			}
			session.mu.Unlock()
		})
	}
	// Video and finger samples only take the session lock to find the connector,
	// so neither waits behind an action in progress.
	connector := func(c context.Context) (iosConnector, *iosVideo, error) {
		session.mu.Lock()
		defer session.mu.Unlock()
		if e := session.start(c, app, d); e != nil {
			return nil, nil, e
		}
		return session.conn, session.video, nil
	}
	go func() {
		if _, _, e := connector(ctx); e != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "iPhone", d.Alias+":", e)
		}
	}()
	link := &iosLink{connector: connector}
	if !readOnly {
		in := &iosInput{app: app, d: d, connector: connector, onInput: onInput, queue: make(chan Spec, 32)}
		link.control = in
		if onInput == nil {
			go in.runQueue(ctx, session)
		}
	}
	return app.openView(ctx, windowConfig{Title: d.viewTitle(), Area: r, Platform: "ios"}, link, cleanup)
}

// iosLink gives a window the picture of the iPhone's connector, the one running now.
type iosLink struct {
	connector func(context.Context) (iosConnector, *iosVideo, error)
	control   *iosInput // nil: watch only
}

func (l *iosLink) open(c context.Context) (viewSource, error) {
	conn, video, e := l.connector(c)
	if e != nil {
		return viewSource{}, e
	}
	if video == nil {
		return viewSource{}, errors.New("no video")
	}
	return viewSource{hub: &video.frameHub, status: video, done: video.closed, wedged: keepWedged(c, conn.wedged)}, nil
}

func (l *iosLink) input() viewInput {
	if l.control == nil {
		return nil
	}
	return l.control
}

// iosInput moves the finger live on the iPhone and runs keys and text as actions,
// or hands them to onInput (the replica of a synchronised session).
type iosInput struct {
	app       *Service
	d         Device
	connector func(context.Context) (iosConnector, *iosVideo, error)
	onInput   func(Spec)
	queue     chan Spec
	gestureMu sync.Mutex
	gesture   gestureState
}

// runQueue runs the window's keys and text, one at a time, until c ends.
func (in *iosInput) runQueue(c context.Context, session *iosDeviceSession) {
	for {
		select {
		case <-c.Done():
			return
		case spec := <-in.queue:
			cc, stop := context.WithTimeout(c, 30*time.Second)
			session.mu.Lock()
			_, err := session.run(cc, in.app, in.d, spec)
			if err != nil {
				fmt.Fprintln(os.Stderr, "iPhone input:", err)
			}
			session.mu.Unlock()
			stop()
		}
	}
}

func (in *iosInput) touch(phase string, x, y float64) error {
	x, y = max(0, min(1, x)), max(0, min(1, y))
	c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, e := in.connector(c)
	if e == nil {
		e = conn.liveTouch(c, phase, x, y)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, "iPhone touch", in.d.Alias+":", e)
		return e
	}
	// The finger already moved on this phone; replicas get the whole gesture.
	in.gestureMu.Lock()
	var spec *Spec
	switch phase {
	case "down":
		in.gesture = gestureState{x: x, y: y, started: time.Now(), active: true}
	case "up":
		if in.gesture.active {
			spec = in.gesture.finish(x, y)
		}
		in.gesture.active = false
	}
	in.gestureMu.Unlock()
	if spec != nil && in.onInput != nil {
		in.onInput(*spec)
	}
	return nil
}

// scroll is not used: the window turns the wheel into a finger drag on the iPhone.
func (in *iosInput) scroll(float64, float64, int) error { return nil }

func (in *iosInput) send(spec Spec) error {
	switch spec.Action {
	case "tap", "swipe", "text", "key":
	default:
		return errors.New("unsupported input")
	}
	if e := in.app.CheckDeviceSteps(in.d, []Spec{spec}); e != nil {
		return e
	}
	if in.onInput != nil {
		in.onInput(spec)
		return nil
	}
	select {
	case in.queue <- spec:
		return nil
	default:
		return errors.New("input queue full")
	}
}
