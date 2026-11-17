package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DisplayState is the logical screen of an Android device: the physical values and the effective ones,
// which are the overrides set with wm size / wm density when there are any.
type DisplayState struct {
	PhysW, PhysH, W, H, PhysDensity, Density int
	SizeOverride, DensityOverride            bool
}

func (d DisplayState) String() string {
	return fmt.Sprintf("%d×%d · %d dpi", d.W, d.H, d.Density)
}

var (
	physicalSize     = regexp.MustCompile(`Physical size:\s*(\d+)x(\d+)`)
	overrideSize     = regexp.MustCompile(`Override size:\s*(\d+)x(\d+)`)
	physicalDensity  = regexp.MustCompile(`Physical density:\s*(\d+)`)
	overrideDensity  = regexp.MustCompile(`Override density:\s*(\d+)`)
	resolutionFormat = regexp.MustCompile(`^(\d{3,4})x(\d{3,4})$`)
)

// parseDisplay reads the output of wm size and wm density.
func (app *Service) ParseDisplay(sizeOut, densityOut string) (DisplayState, error) {
	atoi := func(s string) int {
		v, _ := strconv.Atoi(s)
		return v
	}
	var d DisplayState
	m := physicalSize.FindStringSubmatch(sizeOut)
	p := physicalDensity.FindStringSubmatch(densityOut)
	if m == nil || p == nil {
		return d, errors.New(app.L("No se puede leer la resolucion", "Cannot read the screen resolution"))
	}
	d.PhysW, d.PhysH, d.PhysDensity = atoi(m[1]), atoi(m[2]), atoi(p[1])
	d.W, d.H, d.Density = d.PhysW, d.PhysH, d.PhysDensity
	if o := overrideSize.FindStringSubmatch(sizeOut); o != nil {
		d.W, d.H, d.SizeOverride = atoi(o[1]), atoi(o[2]), true
	}
	if o := overrideDensity.FindStringSubmatch(densityOut); o != nil {
		d.Density, d.DensityOverride = atoi(o[1]), true
	}
	return d, nil
}
func (app *Service) ReadDisplay(c context.Context, serial string) (DisplayState, error) {
	size, e := app.Remote(c, serial, "wm", "size")
	if e != nil {
		return DisplayState{}, e
	}
	density, e := app.Remote(c, serial, "wm", "density")
	if e != nil {
		return DisplayState{}, e
	}
	return app.ParseDisplay(size, density)
}

// displayCommands are the wm commands that turn the screen from into to. A value that to does not
// override is reset to the physical one.
func (app *Service) DisplayCommands(from, to DisplayState) [][]string {
	var out [][]string
	if from.SizeOverride != to.SizeOverride || from.W != to.W || from.H != to.H {
		if to.SizeOverride {
			out = append(out, []string{"wm", "size", fmt.Sprintf("%dx%d", to.W, to.H)})
		} else {
			out = append(out, []string{"wm", "size", "reset"})
		}
	}
	if from.DensityOverride != to.DensityOverride || from.Density != to.Density {
		if to.DensityOverride {
			out = append(out, []string{"wm", "density", strconv.Itoa(to.Density)})
		} else {
			out = append(out, []string{"wm", "density", "reset"})
		}
	}
	return out
}

// applyDisplay changes the screen of serial from its current state into to.
func (app *Service) ApplyDisplay(c context.Context, serial string, from, to DisplayState) error {
	for _, cmd := range app.DisplayCommands(from, to) {
		if _, e := app.Remote(c, serial, cmd...); e != nil {
			return e
		}
	}
	return nil
}

// matchDisplay is the state that gives a device the same logical screen as master.
func (app *Service) MatchDisplay(own, master DisplayState) DisplayState {
	to := own
	if own.W != master.W || own.H != master.H {
		to.W, to.H, to.SizeOverride = master.W, master.H, true
	}
	if own.Density != master.Density {
		to.Density, to.DensityOverride = master.Density, true
	}
	return to
}

// resolutionSpec turns the arguments of the resolution command into its step: none shows the screen,
// "reset" goes back to the physical size and WIDTHxHEIGHT [DPI] fixes it. Density is only touched when
// given: Samsung phones keep their own density override, which a reset would undo.
func (app *Service) ResolutionSpec(args []string) (Spec, error) {
	s := Spec{Action: "resolution", Args: []string{"show", "keep"}}
	if len(args) > 2 {
		return s, errors.New(app.L("Uso: resolution [ANCHOxALTO|reset] [DPI]", "Usage: resolution [WIDTHxHEIGHT|reset] [DPI]"))
	}
	for i, a := range args {
		s.Args[i] = strings.ToLower(a)
	}
	return s, app.ValidateSpec(s)
}

// restoreDisplay puts serial back to prev, whatever its screen is now.
func (app *Service) restoreDisplay(c context.Context, serial string, prev DisplayState) error {
	c, cancel := context.WithTimeout(c, 5*time.Second)
	defer cancel()
	now, e := app.ReadDisplay(c, serial)
	if e != nil {
		return e
	}
	return app.ApplyDisplay(c, serial, now, prev)
}
func (app *Service) displayJournal(id string) string {
	return app.SessionsPath(id + ".display.json")
}

// matchDisplays gives every follower the logical screen of the master, so that each button sits on the
// same pixel on every device. The previous state of each changed device is written to a journal before
// anything changes; the returned function puts the devices back and removes the journal.
func (s *LiveSession) matchDisplays(c context.Context, master Device, followers []Device) func() {
	app := s.app
	mc, cancel := context.WithTimeout(c, 5*time.Second)
	want, e := app.ReadDisplay(mc, master.Serial)
	cancel()
	if e != nil {
		s.err(master.Serial, e)
		return func() {}
	}
	type change struct {
		d        Device
		from, to DisplayState
	}
	var mu sync.Mutex
	var changes []change
	var wg sync.WaitGroup
	slots := make(chan struct{}, 16)
	for _, d := range followers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			dc, cancel := context.WithTimeout(c, 5*time.Second)
			defer cancel()
			own, e := app.ReadDisplay(dc, d.Serial)
			if e != nil {
				s.err(d.Serial, e)
				return
			}
			if to := app.MatchDisplay(own, want); len(app.DisplayCommands(own, to)) > 0 {
				mu.Lock()
				changes = append(changes, change{d, own, to})
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(changes) == 0 {
		return func() {}
	}
	journal := map[string]DisplayState{}
	for _, ch := range changes {
		journal[ch.d.Serial] = ch.from
	}
	path := app.displayJournal(s.ID)
	if e := app.WriteJSON(path, journal); e != nil {
		s.err(master.Serial, e)
		return func() {}
	}
	for _, ch := range changes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			dc, cancel := context.WithTimeout(c, 5*time.Second)
			defer cancel()
			if e := app.ApplyDisplay(dc, ch.d.Serial, ch.from, ch.to); e != nil {
				s.err(ch.d.Serial, e)
				return
			}
			s.log(fmt.Sprintf(app.L("%s: %s → %s mientras dure la réplica", "%s: %s → %s while the replica lasts"), ch.d.Alias, ch.from, ch.to))
		}()
	}
	wg.Wait()
	return func() {
		left := map[string]DisplayState{}
		for _, ch := range changes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if e := app.restoreDisplay(context.Background(), ch.d.Serial, ch.from); e != nil {
					mu.Lock()
					left[ch.d.Serial] = ch.from
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if len(left) == 0 {
			os.Remove(path)
		} else {
			app.WriteJSON(path, left)
		}
	}
}

// recoverDisplays puts back the screens changed by replicas that ended without restoring them, such
// as when the program was closed abruptly. Devices still offline stay in their journal for next time.
func (app *Service) RecoverDisplays(c context.Context) {
	files, _ := filepath.Glob(app.SessionsPath("*.display.json"))
	for _, path := range files {
		id := strings.TrimSuffix(filepath.Base(path), ".display.json")
		if _, e := app.SyncRequest(id, "status"); e == nil {
			continue
		}
		journal := map[string]DisplayState{}
		if app.ReadJSON(path, &journal) != nil {
			continue
		}
		left := map[string]DisplayState{}
		for serial, prev := range journal {
			if app.restoreDisplay(c, serial, prev) != nil {
				left[serial] = prev
			}
		}
		if len(left) == 0 {
			os.Remove(path)
			os.Remove(app.SessionsPath(id + ".json"))
		} else {
			app.WriteJSON(path, left)
		}
	}
}
