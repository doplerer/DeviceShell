package core

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

type Peer struct {
	app             *Service
	serial          string
	server          *scrcpyServer
	conn            net.Conn
	done, broken    chan struct{}
	once, breakOnce sync.Once
	w, h            int
	writeMu         sync.Mutex
	keys            map[uint32][]byte
	touch           []byte
}

func (app *Service) OpenPeer(parent context.Context, serial string) (*Peer, error) {
	p := &Peer{app: app, serial: serial, broken: make(chan struct{})}
	cleanup := func(e error) (*Peer, error) {
		p.Close()
		return nil, e
	}
	var e error
	if p.server, e = app.startScrcpyServer(parent, serial, "video=false audio=false control=true tunnel_forward=true send_device_meta=false clipboard_autosync=false"); e != nil {
		return nil, e
	}
	p.done = p.server.done
	if p.w, p.h, e = app.Dimensions(parent, serial); e != nil {
		return cleanup(e)
	}
	if p.conn, e = p.server.connect(parent); e != nil {
		return cleanup(e)
	}
	go func() {
		io.Copy(io.Discard, p.conn)
		p.fail()
	}()
	return p, nil
}

// fail marks the control connection as unusable; the session then opens a new one.
func (p *Peer) fail() {
	p.breakOnce.Do(func() { close(p.broken) })
}

// Failed reports whether the control connection broke.
func (p *Peer) Failed() bool {
	select {
	case <-p.broken:
		return true
	default:
		return false
	}
}
func (app *Service) ReadHandshake(conn net.Conn) error {
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var b [1]byte
	_, err := io.ReadFull(conn, b[:])
	conn.SetReadDeadline(time.Time{})
	if err == nil && b[0] != 0 {
		return errors.New(app.L("Handshake scrcpy invalido", "Invalid scrcpy handshake"))
	}
	return err
}
func (p *Peer) Close() {
	p.once.Do(func() {
		p.release()
		if p.conn != nil {
			p.conn.Close()
		}
		if p.server != nil {
			p.server.close()
		}
	})
}

// frameLength implements the pinned scrcpy 5.0.1 wire format, not TCP packet boundaries.
func (app *Service) FrameLength(b []byte) (int, error) {
	if len(b) < 1 {
		return 0, nil
	}
	switch b[0] {
	case 0:
		return app.Fixed(b, 14), nil
	case 1, 22:
		if len(b) < 5 {
			return 0, nil
		}
		return app.Variable(b, 5, int(binary.BigEndian.Uint32(b[1:5])))
	case 2:
		return app.Fixed(b, 32), nil
	case 3:
		return app.Fixed(b, 21), nil
	case 4, 8, 10, 18:
		return app.Fixed(b, 2), nil
	case 5, 6, 7, 11, 15, 17, 19, 20:
		return 1, nil
	case 9:
		if len(b) < 14 {
			return 0, nil
		}
		return app.Variable(b, 14, int(binary.BigEndian.Uint32(b[10:14])))
	case 12:
		if len(b) < 8 {
			return 0, nil
		}
		i := 8 + int(b[7])
		if len(b) < i+2 {
			return 0, nil
		}
		return app.Variable(b, i+2, int(binary.BigEndian.Uint16(b[i:i+2])))
	case 13:
		if len(b) < 5 {
			return 0, nil
		}
		return app.Variable(b, 5, int(binary.BigEndian.Uint16(b[3:5])))
	case 14:
		return app.Fixed(b, 3), nil
	case 16:
		if len(b) < 2 {
			return 0, nil
		}
		return app.Variable(b, 2, int(b[1]))
	case 21:
		return app.Fixed(b, 5), nil
	}
	return 0, fmt.Errorf(app.L("Mensaje scrcpy desconocido: %d", "Unknown scrcpy message: %d"), b[0])
}
func (app *Service) Fixed(b []byte, n int) int {
	if len(b) < n {
		return 0
	}
	return n
}
func (app *Service) Variable(b []byte, header, n int) (int, error) {
	if n < 0 || header+n > 262144 {
		return 0, errors.New(app.L("Mensaje excede el limite del protocolo", "Message exceeds the protocol limit"))
	}
	return app.Fixed(b, header+n), nil
}
func (app *Service) ScaleFrame(b []byte, w, h int) []byte {
	v := append([]byte(nil), b...)
	offset := 0
	if b[0] == 2 {
		offset = 10
	} else if b[0] == 3 {
		offset = 1
	} else {
		return v
	}
	sw := int(binary.BigEndian.Uint16(b[offset+8:]))
	sh := int(binary.BigEndian.Uint16(b[offset+10:]))
	if sw < 1 || sh < 1 {
		return nil
	}
	x := int32(binary.BigEndian.Uint32(b[offset:]))
	y := int32(binary.BigEndian.Uint32(b[offset+4:]))
	nx := int64(x) * int64(w) / int64(sw)
	ny := int64(y) * int64(h) / int64(sh)
	if nx < 0 {
		nx = 0
	}
	if ny < 0 {
		ny = 0
	}
	if nx >= int64(w) {
		nx = int64(w - 1)
	}
	if ny >= int64(h) {
		ny = int64(h - 1)
	}
	binary.BigEndian.PutUint32(v[offset:], uint32(nx))
	binary.BigEndian.PutUint32(v[offset+4:], uint32(ny))
	binary.BigEndian.PutUint16(v[offset+8:], uint16(w))
	binary.BigEndian.PutUint16(v[offset+10:], uint16(h))
	return v
}

type LiveSession struct {
	controls                    map[string]*controlQueue
	gesture                     gestureState
	generation                  atomic.Uint64
	app                         *Service
	ID, Token, Endpoint, Master string
	Targets                     []string
	Paused                      atomic.Bool
	Sent                        atomic.Uint64
	mu                          sync.Mutex
	Errors                      map[string]string
	Peers                       []*Peer
	cancel                      context.CancelFunc
	logf                        func(string)
}
type SessionFile struct {
	ID, Token, Endpoint, Master string
	Targets                     []string
}
type SyncInfo struct {
	Errors  map[string]string `json:"errors"`
	Events  uint64            `json:"events"`
	ID      string            `json:"id"`
	Master  string            `json:"master"`
	Paused  bool              `json:"paused"`
	Targets []string          `json:"targets"`
}

func (s *LiveSession) Info() SyncInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	errorsCopy := map[string]string{}
	for k, v := range s.Errors {
		errorsCopy[k] = v
	}
	return SyncInfo{ID: s.ID, Master: s.Master, Targets: s.Targets, Paused: s.Paused.Load(), Events: s.Sent.Load(), Errors: errorsCopy}
}

// Followers counts the devices that replicate the master, connected or still connecting.
func (s *LiveSession) Followers() int {
	n := 0
	for _, t := range s.Targets {
		if t != s.Master {
			n++
		}
	}
	return n
}
func (s *LiveSession) log(msg string) {
	if s.logf != nil {
		s.logf(msg)
		return
	}
	fmt.Fprintln(os.Stderr, msg)
}
func (s *LiveSession) err(serial string, e error) {
	s.mu.Lock()
	s.Errors[serial] = e.Error()
	s.mu.Unlock()
	s.log(serial + ": " + e.Error())
}
func (s *LiveSession) Pause() {
	s.mu.Lock()
	s.Paused.Store(true)
	s.generation.Add(1)
	s.gesture.active = false
	for _, p := range s.Peers {
		p.release()
	}
	s.mu.Unlock()
}
func (s *LiveSession) Resume() {
	s.Paused.Store(false)
}
func (s *LiveSession) Stop() {
	s.cancel()
}

type SyncOptions struct {
	Master                                           Device
	Targets                                          []Device
	Views                                            string
	MasterSize, MasterFPS, FollowerSize, FollowerFPS int
}

// syncRequest controls a session running in another process through its local API.
func (app *Service) SyncRequest(id, action string) (string, error) {
	var f SessionFile
	if e := app.ReadJSON(app.SessionsPath(filepath.Base(id)+".json"), &f); e != nil {
		return "", e
	}
	req, _ := http.NewRequest("POST", f.Endpoint+"/"+action, nil)
	req.Header.Set("Authorization", "Bearer "+f.Token)
	client := http.Client{Timeout: 3 * time.Second}
	r, e := client.Do(req)
	if e != nil {
		return "", errors.New(app.L("La sesion ya no esta activa", "The session is no longer active"))
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(r.Body, 65536))
	if r.StatusCode != 200 {
		return "", fmt.Errorf("Sesion: %s", b)
	}
	return string(b), nil
}

// runSyncSession starts the relay, calls ready once it is live and blocks until the session is stopped.
// Windows and control connections that fail or close are opened again by themselves meanwhile.
func (app *Service) RunSyncSession(parent context.Context, o SyncOptions, logf func(string), ready func(*LiveSession)) error {
	m, targets, views := o.Master, o.Targets, o.Views
	if m.OS() == "ios" {
		return app.runIOSMasterSync(parent, o, logf, ready)
	}
	c, cancel := context.WithCancel(parent)
	defer cancel()
	token := make([]byte, 24)
	rand.Read(token)
	s := &LiveSession{app: app, ID: time.Now().Format("20060102-150405.000000000"), Token: hex.EncodeToString(token), Master: m.Serial, Errors: map[string]string{}, cancel: cancel, logf: logf}
	for _, v := range targets {
		s.Targets = append(s.Targets, v.Serial)
	}
	api, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	defer api.Close()
	s.Endpoint = "http://" + api.Addr().String()
	server := &http.Server{ReadHeaderTimeout: 3 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer "+s.Token {
			http.Error(w, "No autorizado", 403)
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
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s.Info())
	})}
	go server.Serve(api)
	defer server.Close()
	sf := app.SessionsPath(s.ID + ".json")
	if e = app.WriteJSON(sf, SessionFile{s.ID, s.Token, s.Endpoint, s.Master, s.Targets}); e != nil {
		return e
	}
	defer os.Remove(sf)
	var android []Device
	for _, v := range targets {
		if v.Serial == m.Serial {
			continue
		}
		if v.OS() == "ios" {
			s.addControlFollower(c, v)
			continue
		}
		if v.State != "device" {
			s.err(v.Serial, errors.New(app.L("No conectado", "Not connected")))
			continue
		}
		android = append(android, v)
	}
	if len(android) == 0 && len(s.controls) == 0 {
		return errors.New(app.L("No hay seguidores conectados", "No connected followers"))
	}
	// Followers take the master's logical screen while the replica lasts: with the same size and density
	// every button sits on the same pixel, which proportional scaling cannot achieve between screens of
	// different shapes. Runs after the windows and connections are closed.
	app.RecoverDisplays(c)
	defer s.matchDisplays(c, m, android)()
	proxy, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	defer proxy.Close()
	real, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	realPort := real.Addr().(*net.TCPAddr).Port
	real.Close()
	byID := map[string]Device{}
	for _, v := range targets {
		byID[v.Serial] = v
	}
	order := []Device{m}
	if views == "all" {
		order = append(order, android...)
		for _, d := range targets {
			if d.OS() == "ios" && d.State == "device" {
				order = append(order, d)
			}
		}
	}
	followerPorts := map[string]int{}
	for _, d := range order[1:] {
		if d.OS() == "ios" {
			continue
		}
		port, e := app.FreePort()
		if e != nil {
			return e
		}
		followerPorts[d.Serial] = port
	}
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
		cc, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		app.Command(cc, "-s", m.Serial, "forward", "--remove", fmt.Sprintf("tcp:%d", realPort))
		for serial, port := range followerPorts {
			app.Command(cc, "-s", serial, "forward", "--remove", fmt.Sprintf("tcp:%d", port))
		}
	}()
	// Control connections open in parallel, a few at a time, and each one reconnects by itself if it breaks.
	slots := make(chan struct{}, 8)
	for _, v := range android {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.keepPeer(c, v.Serial, slots)
		}()
	}
	geometry := map[string]Rect{}
	layout := app.LayoutFor(c, order)
	for i, r := range layout {
		geometry[order[i].Serial] = r
	}
	// The pack runs before any window exists, so each one joins the block as soon as it appears.
	packs := app.StartWallPacks(layout, app.ScreenAreas())
	defer packs.Stop()
	var expectVideo atomic.Bool
	launch := func(slot int, d Device, title string, fps int, args []string, started func()) {
		args = append([]string{"--serial", d.Serial, "--no-audio", "--no-clipboard-autosync", "--window-title", title, viewRenderer}, args...)
		if r, ok := geometry[d.Serial]; ok {
			args = append(args, fmt.Sprintf("--video-bit-rate=%d", app.ViewBitRate(r, fps)))
			args = append(args, app.WindowArgs(r)...)
		}
		start := func() (viewHandle, error) {
			if started != nil {
				started()
			}
			cmd := exec.CommandContext(c, app.Scrcpy, args...)
			cmd.Env = append(os.Environ(), "ADB="+app.Adb)
			cmd.Dir = app.ToolDir
			return startProcess(cmd)
		}
		exited := func(_ viewHandle, e error) {
			if c.Err() != nil {
				return
			}
			if e == nil {
				e = errors.New(app.L("ventana cerrada", "window closed"))
			}
			s.err(d.Serial, fmt.Errorf(app.L("Vista scrcpy finalizada, se reabre: %w", "scrcpy view ended, reopening: %w"), e))
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			pack, place := packs.For(slot)
			app.SuperviseWindow(c, place, pack, start, exited)
		}()
	}
	launch(0, m, m.viewTitle()+app.L(" · principal", " · master"), o.MasterFPS, []string{"--force-adb-forward", fmt.Sprintf("--port=%d", realPort), "--tunnel-host=127.0.0.1", fmt.Sprintf("--tunnel-port=%d", proxy.Addr().(*net.TCPAddr).Port), fmt.Sprintf("--max-size=%d", o.MasterSize), fmt.Sprintf("--max-fps=%d", o.MasterFPS), "--keyboard=sdk", "--mouse=sdk"}, func() {
		expectVideo.Store(true)
	})
	native := app.NativeAndroidView()
	for slot, d := range order {
		if slot == 0 {
			continue
		}
		if d.OS() == "ios" || native {
			// Watch-only windows drawn by DeviceShell; the followers get input as peers.
			start := func() (viewHandle, error) {
				if d.OS() == "android" {
					return app.openAndroidWindow(c, d, geometry[d.Serial], true)
				}
				return app.openIOSWindow(c, d, geometry[d.Serial], true, nil)
			}
			exited := func(_ viewHandle, e error) {
				if c.Err() == nil && e != nil {
					s.err(d.Serial, e)
				}
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				pack, place := packs.For(slot)
				app.SuperviseWindow(c, place, pack, start, exited)
			}()
			continue
		}
		launch(slot, d, byID[d.Serial].viewTitle(), o.FollowerFPS, []string{"--no-control", fmt.Sprintf("--max-size=%d", o.FollowerSize), fmt.Sprintf("--max-fps=%d", o.FollowerFPS), "--force-adb-forward", fmt.Sprintf("--port=%d", followerPorts[d.Serial])}, nil)
	}
	var connections []net.Conn
	var cm sync.Mutex
	defer func() {
		cm.Lock()
		defer cm.Unlock()
		for _, v := range connections {
			v.Close()
		}
	}()
	go func() {
		<-c.Done()
		proxy.Close()
	}()
	// Every launch of the master opens a video connection (with its handshake byte) and then a control
	// connection. Losing them only ends that launch: the master window opens again and reconnects.
	go func() {
		for {
			client, e := proxy.Accept()
			if e != nil {
				return
			}
			backend, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", realPort), 5*time.Second)
			if e != nil {
				client.Close()
				s.err(m.Serial, e)
				continue
			}
			video := expectVideo.CompareAndSwap(true, false)
			if video {
				if e = app.ReadHandshake(backend); e != nil {
					backend.Close()
					client.Close()
					expectVideo.Store(true)
					continue
				}
				if _, e = client.Write([]byte{0}); e != nil {
					backend.Close()
					client.Close()
					continue
				}
			}
			cm.Lock()
			connections = append(connections, client, backend)
			cm.Unlock()
			go func() {
				defer client.Close()
				defer backend.Close()
				go func() {
					io.Copy(client, backend)
					client.Close()
				}()
				if video {
					io.Copy(backend, client)
					return
				}
				defer s.releaseAll()
				buf := make([]byte, 8192)
				pending := []byte{}
				for {
					n, e := client.Read(buf)
					if n > 0 {
						if _, we := backend.Write(buf[:n]); we != nil {
							return
						}
						pending = append(pending, buf[:n]...)
						for len(pending) > 0 {
							size, pe := app.FrameLength(pending)
							if pe != nil {
								s.err("protocol", pe)
								s.Paused.Store(true)
								return
							}
							if size == 0 {
								break
							}
							frame := pending[:size]
							s.broadcast(frame)
							pending = pending[size:]
						}
					}
					if e != nil {
						return
					}
				}
			}()
		}
	}()
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-c.Done():
				return
			case <-ticker.C:
				s.mu.Lock()
				peers := slices.Clone(s.Peers)
				s.mu.Unlock()
				for _, p := range peers {
					cc, done := context.WithTimeout(c, 2*time.Second)
					w, h, e := app.Dimensions(cc, p.serial)
					done()
					if e == nil {
						s.mu.Lock()
						p.w = w
						p.h = h
						s.mu.Unlock()
					}
				}
			}
		}
	}()
	if ready != nil {
		ready(s)
	}
	<-c.Done()
	if e := app.SaveDiagnostic("sync", s.ID, s.Targets, s.Sent.Load(), len(s.Info().Errors) > 0); e != nil {
		return e
	}
	return nil
}

// keepPeer keeps a control connection to serial open until c ends, opening a new one whenever it breaks.
// slots limits how many connections open at the same time.
func (s *LiveSession) keepPeer(c context.Context, serial string, slots chan struct{}) {
	wait := time.Duration(0)
	for {
		began := time.Now()
		select {
		case slots <- struct{}{}:
		case <-c.Done():
			return
		}
		p, e := s.app.OpenPeer(c, serial)
		<-slots
		if e == nil {
			s.mu.Lock()
			s.Peers = append(s.Peers, p)
			delete(s.Errors, serial)
			s.mu.Unlock()
			select {
			case <-c.Done():
			case <-p.done:
			case <-p.broken:
			}
			s.mu.Lock()
			s.Peers = slices.DeleteFunc(s.Peers, func(q *Peer) bool { return q == p })
			s.mu.Unlock()
			p.Close()
			e = errors.New(s.app.L("Control perdido; reconectando", "Control lost; reconnecting"))
		}
		if c.Err() != nil {
			return
		}
		s.err(serial, e)
		wait = s.app.WindowBackoff(wait, time.Since(began))
		select {
		case <-c.Done():
			return
		case <-time.After(wait):
		}
	}
}

// releaseAll lifts every key and finger still pressed on the followers, as when the master disconnects.
func (s *LiveSession) releaseAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gesture.active = false
	for _, p := range s.Peers {
		p.release()
	}
}
