package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/doplerer/deviceshell/internal/ios/coredevice"
	"github.com/doplerer/deviceshell/internal/ios/remotepairing"
)

// iPhones over Wi-Fi, with no app on the phone. An iPhone connected over USB is paired
// once for Wi-Fi (RemotePairing; a new pairing asks "Trust" on the phone). From then
// on the phone advertises _remotepairing._tcp on the LAN: the scanner finds it with
// mDNS, checks over its tunnel that it is one of ours (only a phone that trusts this
// PC opens it) and which UDID it is, and discovery lists it as connected over Wi-Fi
// while it has no cable. Its picture is the DisplayService H.264 stream instead of
// QuickTime; touches, keys, text and apps work as over USB.

// iosWiFiInfo is what the registry (tools/ios/config.json "wifi") keeps for
// each phone that answered over Wi-Fi or was paired for it.
type iosWiFiInfo struct {
	Host   string `json:"host,omitempty"`
	Port   int    `json:"port,omitempty"`
	Model  string `json:"model,omitempty"`
	IOS    string `json:"ios,omitempty"`
	Paired string `json:"paired,omitempty"` // when DeviceShell paired it over USB
	Seen   string `json:"seen,omitempty"`   // last time it answered over Wi-Fi
}

// iosNeedsUSB marks a phone found over Wi-Fi whose developer image is not mounted
// (it restarted): it cannot be controlled until it is connected by cable once.
const iosNeedsUSB = "needs-usb"

const (
	wifiScanEvery   = 10 * time.Second
	wifiBrowseWait  = 1500 * time.Millisecond
	wifiForget      = 35 * time.Second // missing from browses for this long: gone
	wifiCheckEvery  = time.Minute      // a present phone's tunnel is checked again
	wifiForeignWait = 10 * time.Minute // a phone that does not trust this PC
)

// wifiPhone is a phone present on the LAN now.
type wifiPhone struct {
	udid, host   string
	port         int
	model, ios   string
	seen         time.Time
	checked      time.Time
	needsUSB     bool
	checkFailure string
}

// wifiProbe is what opening a phone's tunnel tells.
type wifiProbe struct {
	udid, model, ios string
	developer        bool // the developer image's services are published
}

type iosWiFi struct {
	mu        sync.Mutex
	started   bool
	present   map[string]*wifiPhone // by UDID
	foreign   map[string]time.Time  // host -> next try
	pairTried map[string]string     // UDID -> result of this run's pairing
	// Replaced in tests.
	reachable func(host string, port int) bool
	browse    func(c context.Context, hints []string) []remotepairing.Advert
	probe     func(c context.Context, host string, port int) (wifiProbe, error)
	sweep     func() []string
	// scanMu runs one round at a time (the loop, and the one after a pairing).
	scanMu sync.Mutex
}

// wifiSweepLimit is the largest local network whose every address is asked directly
// when a paired phone is missing (a /22).
const wifiSweepLimit = 1024

func (app *Service) remotePairingDir() string {
	return app.IOSToolsPath("remotepairing")
}

func (app *Service) pairRecords() (remotepairing.PairRecordManager, error) {
	dir := app.remotePairingDir()
	if e := os.MkdirAll(dir, 0o700); e != nil {
		return remotepairing.PairRecordManager{}, e
	}
	return remotepairing.NewPairRecordManager(dir)
}

func (app *Service) wifiLog(format string, args ...any) {
	app.wifi.mu.Lock()
	defer app.wifi.mu.Unlock()
	f, e := os.OpenFile(app.LogsPath("ios-wifi.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, args...))
}

// wifiPhones returns the phones present on the LAN; start begins scanning the first
// time (discovery does, in the real program only: WiFi set by main).
func (app *Service) wifiPhones(start bool) []wifiPhone {
	app.wifi.mu.Lock()
	if start && app.WiFi && !app.wifi.started && app.RunIOS == nil && app.StartIOSConnector == nil {
		// The first round runs now, so a one-shot command (devices, doctor) sees them.
		app.wifi.started = true
		app.wifi.mu.Unlock()
		app.wifiScan(app.wifiContext())
		go app.wifiScanLoop()
		app.wifi.mu.Lock()
	}
	out := make([]wifiPhone, 0, len(app.wifi.present))
	for _, p := range app.wifi.present {
		out = append(out, *p)
	}
	app.wifi.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].udid < out[j].udid })
	return out
}

func (app *Service) wifiPhone(udid string) (wifiPhone, bool) {
	app.wifi.mu.Lock()
	defer app.wifi.mu.Unlock()
	if p := app.wifi.present[udid]; p != nil {
		return *p, true
	}
	return wifiPhone{}, false
}

func (app *Service) wifiContext() context.Context {
	if app.Ctx != nil {
		return app.Ctx
	}
	return context.Background()
}

func (app *Service) wifiScanLoop() {
	ctx := app.wifiContext()
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wifiScanEvery):
		}
		app.wifiScan(ctx)
	}
}

// wifiScan is one round: browse the LAN, check new addresses (and known ones every
// wifiCheckEvery) over their tunnel, forget phones that stopped answering. While a
// paired phone is missing, every address of the local network is asked directly:
// a phone just paired has no known address yet, and many networks (PC by cable,
// phone on Wi-Fi) do not carry the multicast query.
func (app *Service) wifiScan(c context.Context) {
	app.wifi.scanMu.Lock()
	defer app.wifi.scanMu.Unlock()
	reg := app.readIOSOptions().WiFi
	var hints []string
	byHost := map[string]string{}
	for udid, info := range reg {
		if info.Host != "" {
			hints = append(hints, info.Host)
			byHost[info.Host] = udid
		}
	}
	missing := false
	app.wifi.mu.Lock()
	for udid := range reg {
		missing = missing || app.wifi.present[udid] == nil
	}
	app.wifi.mu.Unlock()
	if missing {
		sweep := app.wifi.sweep
		if sweep == nil {
			sweep = func() []string { return remotepairing.LocalHosts(wifiSweepLimit) }
		}
		hints = append(hints, sweep()...)
	}
	browse, probe := app.wifi.browse, app.wifi.probe
	if browse == nil {
		browse = func(c context.Context, hints []string) []remotepairing.Advert {
			return remotepairing.Browse(c, hints, wifiBrowseWait)
		}
	}
	if probe == nil {
		probe = app.probeWiFi
	}
	now := time.Now()
	ads := browse(c, hints)
	// A known phone that missed the mDNS query (saving power on Wi-Fi) still answers
	// on its RemotePairing port.
	answered := map[string]bool{}
	for _, a := range ads {
		answered[a.Host] = true
	}
	var knock sync.WaitGroup
	var knockMu sync.Mutex
	for _, info := range reg {
		if info.Host == "" || info.Port == 0 || answered[info.Host] {
			continue
		}
		knock.Add(1)
		go func(host string, port int) {
			defer knock.Done()
			if app.wifiReachable(host, port) {
				knockMu.Lock()
				ads = append(ads, remotepairing.Advert{Host: host, Port: port})
				knockMu.Unlock()
			}
		}(info.Host, info.Port)
	}
	knock.Wait()
	app.wifi.mu.Lock()
	if app.wifi.present == nil {
		app.wifi.present, app.wifi.foreign = map[string]*wifiPhone{}, map[string]time.Time{}
	}
	for _, p := range app.wifi.present {
		byHost[p.host] = p.udid
	}
	type job struct {
		host string
		port int
	}
	var jobs []job
	for _, a := range ads {
		if until, ok := app.wifi.foreign[a.Host]; ok && now.Before(until) {
			continue
		}
		udid := byHost[a.Host]
		p := app.wifi.present[udid]
		// A phone with a window open is not checked again: its tunnel is in use.
		if p != nil && p.host == a.Host && (now.Sub(p.checked) < wifiCheckEvery || app.iosInUse("ios:"+udid)) {
			p.seen, p.port = now, a.Port
			continue
		}
		jobs = append(jobs, job{a.Host, a.Port})
	}
	app.wifi.mu.Unlock()
	changed := map[string]iosWiFiInfo{}
	for _, j := range jobs {
		pc, cancel := context.WithTimeout(c, 20*time.Second)
		r, e := probe(pc, j.host, j.port)
		cancel()
		app.wifi.mu.Lock()
		switch {
		case e != nil && errors.Is(e, errWiFiNotOurs):
			app.wifi.foreign[j.host] = now.Add(wifiForeignWait)
		case e != nil:
			// Unreachable for now: a known phone keeps its place until it expires.
			if udid := byHost[j.host]; udid != "" && app.wifi.present[udid] != nil {
				app.wifi.present[udid].checkFailure = e.Error()
			}
		default:
			delete(app.wifi.foreign, j.host)
			// An address now used by another phone belongs to that one.
			for udid, p := range app.wifi.present {
				if p.host == j.host && udid != r.udid {
					delete(app.wifi.present, udid)
				}
			}
			app.wifi.present[r.udid] = &wifiPhone{udid: r.udid, host: j.host, port: j.port, model: r.model, ios: r.ios, seen: now, checked: now, needsUSB: !r.developer}
			old := reg[r.udid]
			info := old
			info.Host, info.Port, info.Model, info.IOS = j.host, j.port, r.model, r.ios
			info.Seen = now.Format(time.RFC3339)
			changed[r.udid] = info
		}
		app.wifi.mu.Unlock()
		if errors.Is(e, errWiFiNotOurs) {
			app.wifiLog("check %s:%d: does not trust this PC; not tried again for %v", j.host, j.port, wifiForeignWait)
		} else if e != nil {
			app.wifiLog("check %s:%d: %v", j.host, j.port, e)
		} else {
			app.wifiLog("check %s:%d: %s iOS %s %s, developer image %v", j.host, j.port, r.udid, r.ios, r.model, r.developer)
		}
	}
	app.wifi.mu.Lock()
	for udid, p := range app.wifi.present {
		if now.Sub(p.seen) > wifiForget {
			delete(app.wifi.present, udid)
		}
	}
	app.wifi.mu.Unlock()
	if len(changed) > 0 {
		_ = app.updateIOSOptions(func(cfg *iosOptions) {
			if cfg.WiFi == nil {
				cfg.WiFi = map[string]iosWiFiInfo{}
			}
			for udid, info := range changed {
				cfg.WiFi[udid] = info
			}
		})
	}
}

var errWiFiNotOurs = errors.New("the iPhone does not trust this PC")

// probeWiFi opens the phone's tunnel to learn which phone it is and whether its
// developer image is mounted, then closes it.
func (app *Service) probeWiFi(c context.Context, host string, port int) (wifiProbe, error) {
	tun, e := app.openWiFiTunnel(c, host, port, "")
	if e != nil {
		return wifiProbe{}, e
	}
	defer tun.Close()
	r := wifiProbe{udid: tun.UDID, ios: tun.Version}
	r.model, _ = tun.Properties["ProductType"].(string)
	has := func(name string) bool { return tun.Services[iosServiceNames[name]].Port != 0 }
	r.developer = has("capture") || has("apps")
	return r, nil
}

// openWiFiTunnel opens the tunnel of the phone at host:port; with udid set, it must be
// that phone.
func (app *Service) openWiFiTunnel(c context.Context, host string, port int, udid string) (*coredevice.Tunnel, error) {
	pm, e := app.pairRecords()
	if e != nil {
		return nil, e
	}
	type result struct {
		tun *coredevice.Tunnel
		e   error
	}
	done := make(chan result, 1)
	go func() {
		conn, _, e := remotepairing.Dial(host, port, pm)
		if e != nil {
			if errors.Is(e, remotepairing.ErrNotPaired) {
				e = errWiFiNotOurs
			}
			done <- result{nil, e}
			return
		}
		tun, e := coredevice.OpenConn(udid, "", conn)
		done <- result{tun, e}
	}()
	select {
	case <-c.Done():
		go func() {
			if r := <-done; r.tun != nil {
				r.tun.Close()
			}
		}()
		return nil, c.Err()
	case r := <-done:
		if r.e != nil {
			return nil, r.e
		}
		if udid != "" && r.tun.UDID != udid {
			r.tun.Close()
			return nil, fmt.Errorf("en %s ahora hay otro iPhone", host)
		}
		return r.tun, nil
	}
}

// connectIOSWiFi opens the Go engine over Wi-Fi; its DisplayService stream is the
// picture (video, when set).
func (app *Service) connectIOSWiFi(c context.Context, d Device, video *iosVideo) (*iosNative, error) {
	udid := d.NativeID()
	p, ok := app.wifiPhone(udid)
	if !ok {
		return nil, errors.New(app.L("el iPhone ya no aparece en la red Wi-Fi", "the iPhone is no longer on the Wi-Fi network"))
	}
	n := app.newIOSNative(udid)
	n.showVideo = true
	if video != nil {
		n.onDisplay = video.attach
	}
	started := time.Now()
	tun, e := app.openWiFiTunnel(c, p.host, p.port, udid)
	if e != nil {
		n.logf("Wi-Fi connect to %s:%d failed: %v", p.host, p.port, e)
		n.closeLog()
		return nil, fmt.Errorf("Wi-Fi: %w", e)
	}
	n.logf("Wi-Fi tunnel to %s:%d up in %v: iOS %s, %d services", p.host, p.port, time.Since(started), tun.Version, len(tun.Services))
	if e = n.begin(tun, true); e != nil {
		n.logf("connect failed: %v", e)
		n.closeLog()
		if errors.Is(e, errIOSDeveloperImage) {
			app.wifi.mu.Lock()
			if p := app.wifi.present[udid]; p != nil {
				p.needsUSB = true
			}
			app.wifi.mu.Unlock()
			return nil, errors.New(app.L("conecta el iPhone por cable una vez: tras reiniciarse necesita la imagen de desarrollo", "connect the iPhone by cable once: after a restart it needs the developer disk image"))
		}
		return nil, e
	}
	return n, nil
}

// pairForWiFi pairs an iPhone connected over USB for Wi-Fi, once: in the background,
// with its own tunnel, since a new pairing waits for "Trust" on the phone. A phone
// already in the registry (paired, or seen over Wi-Fi) is left alone, and a refused
// pairing is not asked again until DeviceShell restarts.
// wifiPairDone marks a successful pairing in wifi.pairTried; WiFiStatus lists only
// the pairings still running or failed.
const wifiPairDone = "paired"

func (app *Service) pairForWiFi(d Device) {
	if app.RunIOS != nil || app.StartIOSConnector != nil || d.OS() != "ios" || d.State != "device" || d.Transport == "wifi" {
		return
	}
	udid := d.NativeID()
	if _, ok := app.readIOSOptions().WiFi[udid]; ok {
		return
	}
	app.wifi.mu.Lock()
	if app.wifi.pairTried == nil {
		app.wifi.pairTried = map[string]string{}
	}
	if _, tried := app.wifi.pairTried[udid]; tried {
		app.wifi.mu.Unlock()
		return
	}
	app.wifi.pairTried[udid] = app.L("emparejando (acepta «Confiar» en el iPhone si lo pregunta)", "pairing (accept «Trust» on the iPhone if it asks)")
	app.wifi.mu.Unlock()
	go func() {
		c, cancel := context.WithTimeout(app.wifiContext(), 2*time.Minute)
		defer cancel()
		e := func() error {
			pm, e := app.pairRecords()
			if e != nil {
				return e
			}
			tun, e := coredevice.Open(udid)
			if e != nil {
				return e
			}
			defer tun.Close()
			_, e = remotepairing.Pair(c, tun, pm)
			return e
		}()
		app.wifiLog("pair %s (%s): %v", d.Alias, udid, e)
		result := wifiPairDone
		if e != nil {
			result = app.L("no emparejado: ", "not paired: ") + e.Error() + app.L(" (se reintenta al reiniciar DeviceShell)", " (tried again when DeviceShell restarts)")
		} else {
			_ = app.updateIOSOptions(func(cfg *iosOptions) {
				if cfg.WiFi == nil {
					cfg.WiFi = map[string]iosWiFiInfo{}
				}
				info := cfg.WiFi[udid]
				info.Model, info.IOS, info.Paired = d.Model, d.OSVersion, time.Now().Format(time.RFC3339)
				cfg.WiFi[udid] = info
			})
			if app.wifiPaired() {
				go app.wifiScan(app.wifiContext())
			}
		}
		app.wifi.mu.Lock()
		app.wifi.pairTried[udid] = result
		app.wifi.mu.Unlock()
	}()
}

// wifiPaired forgets the addresses found not to trust this PC: the phone just paired
// was probably one of them (it was checked over Wi-Fi while it still had to answer
// «Confiar»), and it must be found at once, not after wifiForeignWait.
// It reports whether the scanner runs, so the caller can start a round now.
func (app *Service) wifiPaired() bool {
	app.wifi.mu.Lock()
	defer app.wifi.mu.Unlock()
	clear(app.wifi.foreign)
	return app.wifi.started
}

// wifiDevices lists the phones present over Wi-Fi that discovery did not see over
// USB.
func (app *Service) wifiDevices(usb map[string]bool) []Device {
	var out []Device
	for _, p := range app.wifiPhones(true) {
		if usb[p.udid] {
			continue
		}
		d := Device{Serial: "ios:" + p.udid, Platform: "ios", State: "device", Model: p.model, OSVersion: p.ios, Transport: "wifi"}
		switch {
		case !iosSupportedVersion(p.ios):
			d.State = iosOutdated
		case p.needsUSB:
			d.State = iosNeedsUSB
		case app.iosIsLocked(d.Serial):
			d.State = iosLocked
		}
		out = append(out, d)
	}
	return out
}

// WiFiStatus describes the Wi-Fi iPhones for doctor: present ones with their address,
// and registered ones not found on the network.
func (app *Service) WiFiStatus(inventory []Device) []string {
	alias := map[string]string{}
	cable := map[string]bool{}
	for _, d := range inventory {
		alias[d.NativeID()] = d.Alias
		cable[d.NativeID()] = d.OS() == "ios" && d.Transport == "usb" && d.State != "disconnected"
	}
	name := func(udid string) string {
		if a := alias[udid]; a != "" {
			return a
		}
		return udid
	}
	var lines []string
	present := map[string]bool{}
	for _, p := range app.wifiPhones(false) {
		present[p.udid] = true
		line := name(p.udid) + ": Wi-Fi " + p.host
		if cable[p.udid] {
			line = name(p.udid) + app.L(": por cable · también disponible por Wi-Fi (", ": on cable · also available over Wi-Fi (") + p.host + ")"
		} else if p.needsUSB {
			line += app.L(" · necesita cable una vez (se reinició)", " · needs the cable once (it restarted)")
		}
		lines = append(lines, line)
	}
	for udid, info := range app.readIOSOptions().WiFi {
		if !present[udid] {
			lines = append(lines, name(udid)+app.L(": emparejado para Wi-Fi, no está en la red ahora (mismo Wi-Fi que el PC, sin aislamiento de clientes)", ": paired for Wi-Fi, not on the network now (same Wi-Fi as the PC, no client isolation)"))
		}
		_ = info
	}
	app.wifi.mu.Lock()
	for udid, result := range app.wifi.pairTried {
		if result != wifiPairDone {
			lines = append(lines, name(udid)+": Wi-Fi "+result)
		}
	}
	app.wifi.mu.Unlock()
	sort.Strings(lines)
	return lines
}

// iosInUse reports whether a window shows the iPhone now.
func (app *Service) iosInUse(serial string) bool {
	app.iosMu.Lock()
	s := app.iosSessions[serial]
	app.iosMu.Unlock()
	return s != nil && s.views.Load() > 0
}

// wifiReachable reports whether host accepts a connection on port (its RemotePairing
// listener), closing it at once.
func (app *Service) wifiReachable(host string, port int) bool {
	if f := app.wifi.reachable; f != nil {
		return f(host, port)
	}
	conn, e := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 800*time.Millisecond)
	if e != nil {
		return false
	}
	conn.Close()
	return true
}
