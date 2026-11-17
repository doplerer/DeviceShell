package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Android windows are drawn by DeviceShell itself, as the iPhone ones, unless config.json
// sets "androidView": "scrcpy" to go back to scrcpy.exe windows.
func (app *Service) NativeAndroidView() bool {
	var config struct{ AndroidView string }
	_ = app.ReadJSON(app.ConfigPath(), &config)
	return !strings.EqualFold(config.AndroidView, "scrcpy")
}

// androidDeviceSession keeps one connector per Android device while it has windows,
// and iosViewLinger more, so a reopened window starts at once.
type androidDeviceSession struct {
	mu       sync.Mutex
	video    *androidVideo
	viewRefs int
	idle     *time.Timer
	area     Rect // the window the next connector is sized for
	fps      int
}

func (app *Service) androidSession(d Device) *androidDeviceSession {
	app.androidMu.Lock()
	defer app.androidMu.Unlock()
	if app.androidSessions == nil {
		app.androidSessions = map[string]*androidDeviceSession{}
	}
	s := app.androidSessions[d.Serial]
	if s == nil {
		s = &androidDeviceSession{}
		app.androidSessions[d.Serial] = s
	}
	return s
}

// start returns the live connector, opening a new one when there is none or it ended.
// Caller holds s.mu.
func (s *androidDeviceSession) start(c context.Context, app *Service, d Device) (*androidVideo, error) {
	if s.video != nil && s.video.alive() {
		return s.video, nil
	}
	s.stop()
	// Only a few servers start at once, so a wall of phones does not push them all together.
	if e := androidStarts.acquire(c); e != nil {
		return nil, e
	}
	v, e := app.openAndroidVideo(c, d.Serial, s.area, s.fps)
	androidStarts.release()
	if e != nil {
		return nil, e
	}
	s.video = v
	return v, nil
}

// stop closes the connector. Caller holds s.mu.
func (s *androidDeviceSession) stop() {
	if s.video != nil {
		s.video.close()
		s.video = nil
	}
}

// closeAndroidSessions closes every Android connector, all at once.
func (app *Service) closeAndroidSessions() {
	app.androidMu.Lock()
	defer app.androidMu.Unlock()
	var wg sync.WaitGroup
	for _, s := range app.androidSessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.idle != nil {
				s.idle.Stop()
				s.idle = nil
			}
			s.stop()
		}()
	}
	wg.Wait()
}

// openAndroidWindow opens a DeviceShell window on an Android device: the picture and the
// finger go through the scrcpy server, without scrcpy.exe. The connector starts while
// the window appears, which says "conectando" until the first frame.
func (app *Service) openAndroidWindow(c context.Context, d Device, r Rect, readOnly bool) (viewHandle, error) {
	fps := viewFPS
	if readOnly {
		fps = watchFPS
	}
	session := app.androidSession(d)
	session.mu.Lock()
	if session.idle != nil {
		session.idle.Stop()
		session.idle = nil
	}
	// A connector left by smaller or slower windows is replaced for this one.
	if session.viewRefs == 0 && (fps > session.fps || androidVideoSize(r) > androidVideoSize(session.area)) {
		session.stop()
	}
	if session.video == nil {
		session.area, session.fps = r, fps
	}
	session.viewRefs++
	session.mu.Unlock()
	ctx, cancel := context.WithCancel(c)
	var once sync.Once
	release := func() {
		once.Do(func() {
			cancel()
			session.mu.Lock()
			defer session.mu.Unlock()
			if session.viewRefs--; session.viewRefs == 0 {
				var idle *time.Timer
				idle = time.AfterFunc(iosViewLinger, func() {
					session.mu.Lock()
					defer session.mu.Unlock()
					if session.idle == idle && session.viewRefs == 0 {
						session.stop()
						session.idle = nil
					}
				})
				session.idle = idle
			}
		})
	}
	link := &androidLink{connector: func(c context.Context) (*androidVideo, error) {
		session.mu.Lock()
		defer session.mu.Unlock()
		return session.start(c, app, d)
	}}
	if !readOnly {
		link.control = &androidInput{app: app, d: d, connector: link.connector}
	}
	go func() {
		if _, e := link.connector(ctx); e != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, "Android", d.Alias+":", e)
		}
	}()
	app.aspectMu.Lock()
	aspect := app.aspects[d.Serial]
	app.aspectMu.Unlock()
	return app.openView(ctx, windowConfig{Title: d.viewTitle(), Area: r, Platform: "android", Aspect: aspect}, link, release)
}

// androidLink gives a window the picture of the device's connector, the one running
// now: a connector that stopped is replaced when the window asks again.
type androidLink struct {
	connector func(context.Context) (*androidVideo, error)
	control   *androidInput // nil: watch only
}

func (l *androidLink) open(c context.Context) (viewSource, error) {
	v, e := l.connector(c)
	if e != nil {
		return viewSource{}, e
	}
	return viewSource{hub: &v.frameHub, status: v, done: v.done, wedged: func() bool { return false }}, nil
}

func (l *androidLink) input() viewInput {
	if l.control == nil {
		return nil
	}
	return l.control
}

// androidInput sends a window's finger, wheel and keys through the scrcpy server.
type androidInput struct {
	app       *Service
	d         Device
	connector func(context.Context) (*androidVideo, error)
}

// video waits a little for a connector that is starting.
func (in *androidInput) video() (*androidVideo, error) {
	c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return in.connector(c)
}

func (in *androidInput) touch(phase string, x, y float64) error {
	video, e := in.video()
	if e != nil {
		return e
	}
	return video.touch(phase, max(0, min(1, x)), max(0, min(1, y)))
}

func (in *androidInput) scroll(x, y float64, steps int) error {
	video, e := in.video()
	if e != nil {
		return e
	}
	return video.scroll(max(0, min(1, x)), max(0, min(1, y)), steps)
}

func (in *androidInput) send(spec Spec) error {
	if spec.Action != "key" && spec.Action != "text" || len(spec.Args) != 1 {
		return errors.New("unsupported input")
	}
	if e := in.app.CheckDeviceSteps(in.d, []Spec{spec}); e != nil {
		return e
	}
	video, e := in.video()
	if e != nil {
		return e
	}
	if spec.Action == "text" {
		return video.text(spec.Args[0])
	}
	name := strings.ToUpper(spec.Args[0])
	code, e := strconv.Atoi(in.app.KeyCodes()[name])
	if e != nil {
		if code, e = strconv.Atoi(name); e != nil {
			return e
		}
	}
	return video.key(code)
}
