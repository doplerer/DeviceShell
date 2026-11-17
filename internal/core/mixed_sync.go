package core

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
)

type gestureState struct {
	x, y    float64
	started time.Time
	active  bool
}

// finish turns a press released at (x, y) into the tap or swipe that replays it.
func (g gestureState) finish(x, y float64) *Spec {
	duration := max(1, int(time.Since(g.started).Milliseconds()))
	spec := Spec{Action: "swipe", X: g.x, Y: g.y, X2: x, Y2: y, Duration: duration}
	if (g.x-x)*(g.x-x)+(g.y-y)*(g.y-y) < .0001 && duration < 400 {
		spec.Action = "tap"
	}
	return &spec
}

type controlQueue struct {
	events  chan ControlEvent
	stopped bool
}

func (app *Service) executeControl(c context.Context, d Device, spec Spec) (string, error) {
	if e := app.CheckDeviceSteps(d, []Spec{spec}); e != nil {
		return "", e
	}
	if d.OS() != "ios" {
		return app.Execute(c, d, spec)
	}
	session := app.iosSession(d)
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.run(c, app, d, spec)
}
func (s *LiveSession) addControlFollower(c context.Context, d Device) {
	if s.controls == nil {
		s.controls = map[string]*controlQueue{}
	}
	q := &controlQueue{events: make(chan ControlEvent, 32)}
	s.controls[d.Serial] = q
	go func() {
		for {
			select {
			case <-c.Done():
				return
			case ev := <-q.events:
				if s.Paused.Load() || ev.Generation != s.generation.Load() {
					continue
				}
				cc, stop := context.WithTimeout(c, 30*time.Second)
				_, e := s.app.executeControl(cc, d, ev.Spec)
				stop()
				if e != nil {
					s.err(d.Serial, e)
				}
			}
		}
	}()
}

// Caller holds s.mu. Slow followers cannot block the master or each other.
func (s *LiveSession) enqueueControl(spec Spec) {
	if s.Paused.Load() {
		return
	}
	for serial, q := range s.controls {
		if q.stopped {
			continue
		}
		select {
		case q.events <- ControlEvent{Spec: spec, Generation: s.generation.Load()}:
		default:
			q.stopped = true
			s.Errors[serial] = "replica stopped: queue full; restart session"
		}
	}
}
func (s *LiveSession) decodeControl(frame []byte) {
	switch frame[0] {
	case 2:
		if len(frame) < 32 {
			return
		}
		w, h := float64(binary.BigEndian.Uint16(frame[18:20])), float64(binary.BigEndian.Uint16(frame[20:22]))
		if w < 2 || h < 2 {
			return
		}
		x, y := float64(binary.BigEndian.Uint32(frame[10:14]))/(w-1), float64(binary.BigEndian.Uint32(frame[14:18]))/(h-1)
		x = max(0, min(1, x))
		y = max(0, min(1, y))
		switch frame[1] {
		case 0:
			s.gesture = gestureState{x: x, y: y, started: time.Now(), active: true}
		case 1:
			if s.gesture.active {
				s.gesture.active = false
				s.enqueueControl(*s.gesture.finish(x, y))
			}
		case 3:
			s.gesture.active = false
		}
	case 0:
		if len(frame) < 14 || frame[1] != 1 {
			return
		}
		key := binary.BigEndian.Uint32(frame[2:6])
		name := map[uint32]string{3: "HOME", 66: "ENTER", 67: "DEL", 4: "BACK", 187: "RECENTS"}[key]
		if name != "" {
			s.enqueueControl(Spec{Action: "key", Args: []string{name}})
		}
	case 1:
		if len(frame) >= 5 {
			s.enqueueControl(Spec{Action: "text", Args: []string{string(frame[5:])}})
		}
	case 9:
		if len(frame) >= 14 && frame[9] != 0 {
			s.enqueueControl(Spec{Action: "text", Args: []string{string(frame[14:])}})
		}
	case 3:
		if len(frame) < 21 {
			return
		}
		delta := int16(binary.BigEndian.Uint16(frame[15:17]))
		y := .25
		if delta > 0 {
			y = .75
		}
		s.enqueueControl(Spec{Action: "swipe", X: .5, Y: .5, X2: .5, Y2: y, Duration: 250})
	}
}

func (app *Service) runIOSMasterSync(parent context.Context, o SyncOptions, logf func(string), ready func(*LiveSession)) error {
	c, cancel := context.WithCancel(parent)
	defer cancel()
	b := make([]byte, 32)
	rand.Read(b)
	s := &LiveSession{app: app, ID: time.Now().Format("20060102-150405.000000000"), Token: hex.EncodeToString(b), Master: o.Master.Serial, Errors: map[string]string{}, cancel: cancel, logf: logf}
	for _, d := range o.Targets {
		if d.Serial == o.Master.Serial {
			continue
		}
		s.Targets = append(s.Targets, d.Serial)
		if d.State != "device" {
			s.Errors[d.Serial] = "not connected"
			continue
		}
		s.addControlFollower(c, d)
	}
	if len(s.controls) == 0 {
		return fmt.Errorf("no connected followers")
	}
	api, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	s.Endpoint = "http://" + api.Addr().String()
	server := &http.Server{ReadHeaderTimeout: 3 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+s.Token {
			http.Error(w, "unauthorized", 403)
			return
		}
		switch r.URL.Path {
		case "/pause":
			s.Pause()
		case "/resume":
			s.Resume()
		case "/stop":
			s.Stop()
		case "/status":
		default:
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(s.Info())
	})}
	go server.Serve(api)
	defer server.Close()
	path := app.SessionsPath(s.ID + ".json")
	if e = app.WriteJSON(path, SessionFile{s.ID, s.Token, s.Endpoint, s.Master, s.Targets}); e != nil {
		return e
	}
	defer os.Remove(path)
	order := []Device{o.Master}
	if o.Views == "all" {
		for _, d := range o.Targets {
			if d.Serial != o.Master.Serial && d.State == "device" {
				order = append(order, d)
			}
		}
	}
	layout := app.LayoutFor(c, order)
	if len(layout) != len(order) {
		return fmt.Errorf("screen layout unavailable")
	}
	// The pack runs before any window exists, so each one joins the block as soon as it appears.
	packs := app.StartWallPacks(layout, app.ScreenAreas())
	defer packs.Stop()
	var wg sync.WaitGroup
	ports := map[string]int{}
	defer func() {
		cancel()
		wg.Wait()
		cc, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		for serial, port := range ports {
			app.Command(cc, "-s", serial, "forward", "--remove", fmt.Sprintf("tcp:%d", port))
		}
	}()
	for i, d := range order {
		var start func() (viewHandle, error)
		if d.OS() == "android" && app.NativeAndroidView() {
			start = func() (viewHandle, error) {
				return app.openAndroidWindow(c, d, layout[i], true)
			}
		} else if d.OS() == "android" {
			port, e := app.FreePort()
			if e != nil {
				s.err(d.Serial, e)
				continue
			}
			ports[d.Serial] = port
			start = func() (viewHandle, error) {
				cmd := exec.CommandContext(c, app.Scrcpy, app.ScreenArgs(d, layout[i], port, true)...)
				cmd.Env = append(os.Environ(), "ADB="+app.Adb)
				cmd.Dir = app.ToolDir
				return startProcess(cmd)
			}
		} else {
			master := i == 0
			var input func(Spec)
			if master {
				masterQueue := make(chan ControlEvent, 32)
				input = func(spec Spec) {
					generation := s.generation.Load()
					if s.Paused.Load() {
						generation = ^uint64(0)
					}
					select {
					case masterQueue <- ControlEvent{Spec: spec, Generation: generation}:
					default:
						s.err(o.Master.Serial, fmt.Errorf("master input queue full"))
					}
				}
				go func() {
					for {
						select {
						case <-c.Done():
							return
						case event := <-masterQueue:
							// The window already applied taps and swipes live on the
							// master; only keys and text still run there.
							if event.Spec.Action != "tap" && event.Spec.Action != "swipe" {
								cc, stop := context.WithTimeout(c, 30*time.Second)
								_, e := app.executeControl(cc, o.Master, event.Spec)
								stop()
								if e != nil {
									s.err(o.Master.Serial, e)
									continue
								}
							}
							s.mu.Lock()
							if event.Generation == s.generation.Load() {
								s.enqueueControl(event.Spec)
							}
							s.Sent.Add(1)
							s.mu.Unlock()
						}
					}
				}()
			}
			start = func() (viewHandle, error) {
				return app.openIOSWindow(c, d, layout[i], !master, input)
			}
		}
		report := func(_ viewHandle, e error) {
			if c.Err() == nil && e != nil {
				s.err(d.Serial, e)
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			pack, slot := packs.For(i)
			app.SuperviseWindow(c, slot, pack, start, report)
		}()
	}
	if ready != nil {
		ready(s)
	}
	<-c.Done()
	return app.SaveDiagnostic("sync", s.ID, s.Targets, s.Sent.Load(), len(s.Info().Errors) > 0)
}
