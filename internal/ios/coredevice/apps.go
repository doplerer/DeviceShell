package coredevice

import (
	"fmt"
	"path"
	"strings"
	"sync"

	"github.com/danielpaulus/go-ios/ios/appservice"
	"github.com/danielpaulus/go-ios/ios/installationproxy"
	"github.com/danielpaulus/go-ios/ios/instruments"
	"github.com/danielpaulus/go-ios/ios/pasteboard"
)

// Launch opens an app without closing it first if it already runs.
func (t *Tunnel) Launch(bundleID string, args []string) error {
	apps, e := appservice.New(t.Device)
	if e != nil {
		return e
	}
	defer apps.Close()
	list := make([]any, len(args))
	for i, a := range args {
		list[i] = a
	}
	_, e = apps.LaunchApp(bundleID, list, map[string]any{}, map[string]any{}, false)
	return e
}

// executable is the ".app/<binary>" ending of bundleID's process path; the process
// list only carries paths.
func (t *Tunnel) executable(bundleID string) (string, error) {
	proxy, e := installationproxy.New(t.Device)
	if e != nil {
		return "", e
	}
	defer proxy.Close()
	apps, e := proxy.BrowseAllApps()
	if e != nil {
		return "", e
	}
	for _, a := range apps {
		if a.CFBundleIdentifier() == bundleID {
			return "/" + path.Base(a.Path()) + "/" + a.CFBundleExecutable(), nil
		}
	}
	return "", fmt.Errorf("app %s is not installed", bundleID)
}

// Kill ends bundleID's process and returns its PID, or 0 if it was not running.
func (t *Tunnel) Kill(bundleID string) (int, error) {
	suffix, e := t.executable(bundleID)
	if e != nil {
		return 0, e
	}
	apps, e := appservice.New(t.Device)
	if e != nil {
		return 0, e
	}
	defer apps.Close()
	procs, e := apps.ListProcesses()
	if e != nil {
		return 0, e
	}
	for _, p := range procs {
		if strings.HasSuffix(p.Path, suffix) {
			return p.Pid, apps.KillProcess(p.Pid)
		}
	}
	return 0, nil
}

// Paste puts text on the general pasteboard; Cmd+V then types it in any layout.
func (t *Tunnel) Paste(text string) error {
	p, e := pasteboard.New(t.Device)
	if e != nil {
		return e
	}
	defer p.Close()
	return p.SetText(text)
}

// Screenshots keeps one DVT connection: about 2.5 times faster than a new one per shot.
type Screenshots struct {
	t   *Tunnel
	mu  sync.Mutex
	dvt *instruments.ScreenshotService
}

func (t *Tunnel) Screenshots() *Screenshots { return &Screenshots{t: t} }

// Take returns a PNG of the screen, reconnecting once if the connection dropped.
func (s *Screenshots) Take() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var e error
	for attempt := range 2 {
		if s.dvt == nil {
			if s.dvt, e = instruments.NewScreenshotService(s.t.Device); e != nil {
				return nil, e
			}
		}
		var png []byte
		if png, e = s.dvt.TakeScreenshot(); e == nil {
			return png, nil
		}
		s.dvt.Close()
		s.dvt = nil
		_ = attempt
	}
	return nil, e
}

func (s *Screenshots) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dvt != nil {
		s.dvt.Close()
		s.dvt = nil
	}
}
