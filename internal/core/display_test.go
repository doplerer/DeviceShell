package core

import (
	"slices"
	"strings"
	"testing"
)

func (app *Service) testParseDisplay(t *testing.T) {
	d, e := app.ParseDisplay("Physical size: 1080x2340\r\n", "Physical density: 480\r\nOverride density: 420\r\n")
	if e != nil || d != (DisplayState{PhysW: 1080, PhysH: 2340, W: 1080, H: 2340, PhysDensity: 480, Density: 420, DensityOverride: true}) {
		t.Fatal(d, e)
	}
	d, e = app.ParseDisplay("Physical size: 1080x2340\nOverride size: 1080x1920\n", "Physical density: 480\n")
	if e != nil || d.W != 1080 || d.H != 1920 || !d.SizeOverride || d.Density != 480 || d.DensityOverride {
		t.Fatal(d, e)
	}
	if _, e = app.ParseDisplay("error: no devices", "Physical density: 480"); e == nil {
		t.Fatal("garbage accepted")
	}
}
func (app *Service) testMatchDisplay(t *testing.T) {
	tall := DisplayState{PhysW: 1080, PhysH: 2340, W: 1080, H: 2340, PhysDensity: 480, Density: 420, DensityOverride: true}
	short := DisplayState{PhysW: 1080, PhysH: 1920, W: 1080, H: 1920, PhysDensity: 480, Density: 420, DensityOverride: true}
	to := app.MatchDisplay(tall, short)
	cmds := app.DisplayCommands(tall, to)
	if len(cmds) != 1 || !slices.Equal(cmds[0], []string{"wm", "size", "1080x1920"}) {
		t.Fatal(cmds)
	}
	// Going back keeps Samsung's density override and only resets the size.
	if back := app.DisplayCommands(to, tall); len(back) != 1 || !slices.Equal(back[0], []string{"wm", "size", "reset"}) {
		t.Fatal(back)
	}
	if same := app.DisplayCommands(short, app.MatchDisplay(short, short)); len(same) != 0 {
		t.Fatal(same)
	}
	dense := short
	dense.Density = 480
	to = app.MatchDisplay(short, dense)
	if cmds := app.DisplayCommands(short, to); len(cmds) != 1 || !slices.Equal(cmds[0], []string{"wm", "density", "480"}) {
		t.Fatal(cmds)
	}
	plain := DisplayState{PhysW: 720, PhysH: 1280, W: 720, H: 1280, PhysDensity: 320, Density: 320}
	if back := app.DisplayCommands(app.MatchDisplay(plain, tall), plain); len(back) != 2 || back[1][2] != "reset" {
		t.Fatal(back)
	}
}
func (app *Service) testResolutionSpec(t *testing.T) {
	for args, want := range map[string][]string{"": {"show", "keep"}, "reset": {"reset", "keep"}, "1080X1920": {"1080x1920", "keep"}, "1080x1920 420": {"1080x1920", "420"}, "show reset": {"show", "reset"}} {
		s, e := app.ResolutionSpec(strings.Fields(args))
		if e != nil || s.Action != "resolution" || !slices.Equal(s.Args, want) {
			t.Fatal(args, s, e)
		}
	}
	for _, args := range []string{"1080", "abc", "1080x1920 5", "1080x1920 abc", "1080x1920 420 9"} {
		if _, e := app.ResolutionSpec(strings.Fields(args)); e == nil {
			t.Fatal("accepted", args)
		}
	}
}
