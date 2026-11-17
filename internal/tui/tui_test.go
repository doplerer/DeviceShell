package tui

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/doplerer/deviceshell/internal/core"
)

func (app *Interface) testTokenize(t *testing.T) {
	cases := map[string][]string{`/apk "C:\ruta con espacios\app.apk" @pruebas`: {"/apk", `C:\ruta con espacios\app.apk`, "@pruebas"}, `/motion text l'eau   claire`: {"/motion", "text", "l'eau", "claire"}, `/shell 'getprop ro.product.model'`: {"/shell", "getprop ro.product.model"}, `/push C:\a\b.txt /sdcard/Download/`: {"/push", `C:\a\b.txt`, "/sdcard/Download/"}, `"sin cerrar`: {"sin cerrar"}}
	for in, want := range cases {
		var got []string
		for _, tk := range app.tokenize(in) {
			got = append(got, tk.text)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q: %q", in, got)
		}
	}
}
func (app *Interface) testModel(t *testing.T) *tuiModel {
	t.Helper()
	app.Service.Root = t.TempDir()
	app.Service.Ctx = context.Background()
	if e := app.Service.EnsureLayout(); e != nil {
		t.Fatal(e)
	}
	devices := []core.Device{{Serial: "S1", Alias: "DEV-001", Model: "SM_S901U1", State: "device"}, {Serial: "S2", Alias: "DEV-002", Model: "SM_S901U1", State: "device"}, {Serial: "S3", Alias: "DEV-003", State: "offline"}}
	app.Service.WriteJSON(app.Service.InventoryPath(), devices)
	app.Service.WriteJSON(app.Service.GroupsPath(), map[string][]string{"pruebas": {"S1", "S2"}})
	os.WriteFile(app.Service.RoutinesPath("preparacion.json"), []byte(`{"steps":[{"action":"key","args":["HOME"]},{"action":"text","args":["${mensaje}"]}]}`), 0600)
	app.tuiDiscover = func() ([]core.Device, error) {
		return devices, nil
	}
	app.tuiLive = app.tuiDiscover
	app.tuiHealth = func([]core.Device, int) map[string]core.Health {
		return map[string]core.Health{}
	}
	app.tuiRunJob = func(c context.Context, target []core.Device, steps []core.Spec, o core.BatchOptions) (core.Job, error) {
		j := core.Job{ID: "20261029-120000.000000001", Steps: steps}
		for i, d := range target {
			o.OnStart(i)
			r := core.Result{Serial: d.Serial, Alias: d.Alias, OK: d.State == "device", DurationMS: 120}
			if !r.OK {
				r.Error = "Dispositivo no autorizado/conectado: " + d.State
			}
			o.OnResult(i, r)
			j.Results = append(j.Results, r)
		}
		return j, nil
	}
	m := app.newTUIModel()
	app.upd(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	app.printed(m)
	return m
}
func (app *Interface) upd(m *tuiModel, msg tea.Msg) tea.Cmd {
	_, c := m.Update(msg)
	return c
}

// run executes a command and feeds the messages that matter back into the model, skipping slow timers.
func (app *Interface) run(m *tuiModel, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	ch := make(chan tea.Msg, 1)
	go func() {
		ch <- cmd()
	}()
	var msg tea.Msg
	select {
	case msg = <-ch:
	case <-time.After(300 * time.Millisecond):
		return
	}
	if v := reflect.ValueOf(msg); v.Kind() == reflect.Slice && v.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
		for i := 0; i < v.Len(); i++ {
			app.run(m, v.Index(i).Interface().(tea.Cmd))
		}
		return
	}
	switch msg.(type) {
	case asyncMsg, taskDoneMsg, devicesMsg, healthMsg:
		app.run(m, app.upd(m, msg))
	}
}
func (app *Interface) typeLine(m *tuiModel, s string) {
	m.setInput(s)
	app.run(m, app.upd(m, tea.KeyMsg{Type: tea.KeyEnter}))
}

var readMark = map[*tuiModel]int{}

// printed returns what the model added to its transcript since the last call.
func (app *Interface) printed(m *tuiModel) string {
	s := strings.Join(m.transcript[readMark[m]:], "\n")
	readMark[m] = len(m.transcript)
	return s
}
func (app *Interface) testParseTargetsAndRaw(t *testing.T) {
	m := app.testModel(t)
	c, e := m.parse("/motion tap 0.5 0.5 @pruebas")
	if e != nil || c.sel.Group != "pruebas" || len(c.args) != 2 {
		t.Fatal(c, e)
	}
	c, e = m.parse("/motion key HOME @DEV-001 @DEV-002")
	if e != nil || c.sel.Devices != "DEV-001,DEV-002" {
		t.Fatal(c, e)
	}
	c, e = m.parse("!echo @home y @all")
	if e != nil || c.cmd.name != "shell" || c.raw != "echo @home y" || !c.sel.All {
		t.Fatal(c.raw, c.sel, e)
	}
	c, e = m.parse(`/motion text "Hola, mañana 😊" @DEV-001`)
	if e != nil || c.raw != "Hola, mañana 😊" || c.sel.Devices != "DEV-001" {
		t.Fatal(c.raw, e)
	}
	c, e = m.parse("/sync start --master DEV-001 --views=master @pruebas")
	if e != nil || c.flags["master"] != "DEV-001" || c.flags["views"] != "master" {
		t.Fatal(c.flags, e)
	}
	if _, e = m.parse("/tapp 1 1"); e == nil || !strings.Contains(e.Error(), "/motion tap") {
		t.Fatal("Missing suggestion", e)
	}
	if _, e = m.parse("/motion tap 0.5"); e == nil {
		t.Fatal("Missing argument accepted")
	}
	if _, e = m.parse("/help @all"); e == nil {
		t.Fatal("Destination accepted by /help")
	}
}
func (app *Interface) testSuggestions(t *testing.T) {
	m := app.testModel(t)
	m.setInput("/scr")
	if len(m.sugs) == 0 || m.sugs[0].value != "/screenshot" {
		t.Fatal(m.sugs)
	}
	m.applySuggestion(m.sugs[0])
	if m.input.Value() != "/screenshot " {
		t.Fatalf("%q", m.input.Value())
	}
	m.setInput("/screenshot @pru")
	if len(m.sugs) != 1 || m.sugs[0].value != "@pruebas" {
		t.Fatal(m.sugs)
	}
	m.setInput("/motion key HOME @DEV-001,")
	for _, s := range m.sugs {
		if s.value == "@DEV-001,DEV-001" || !strings.HasPrefix(s.value, "@DEV-001,") {
			t.Fatal(s)
		}
	}
	m.setInput("/routine ")
	if len(m.sugs) != 1 || m.sugs[0].value != "preparacion" {
		t.Fatal(m.sugs)
	}
	m.setInput("/routine preparacion ")
	if len(m.sugs) != 1 || m.sugs[0].value != "mensaje=" {
		t.Fatal(m.sugs)
	}
}
func (app *Interface) testRunBatch(t *testing.T) {
	m := app.testModel(t)
	app.typeLine(m, "/motion tap 0.5 0.5 @all")
	out := app.printed(m)
	if m.task != nil || !strings.Contains(out, "tap 0.50 0.50") || !strings.Contains(out, "2/2 correctas") {
		t.Fatal(out)
	}
	// An unavailable device is left out, not run to fail.
	app.typeLine(m, "/motion key HOME @DEV-001,DEV-003")
	out = app.printed(m)
	if m.task != nil || !strings.Contains(out, "No disponibles, se omiten: DEV-003") || !strings.Contains(out, "1/1 correctas") {
		t.Fatal(out)
	}
	app.typeLine(m, "/motion key HOME @DEV-003")
	out = app.printed(m)
	if m.task != nil || !strings.Contains(out, "Ninguna de las placas elegidas está disponible: DEV-003") {
		t.Fatal(out)
	}
}
func (app *Interface) testTarget(t *testing.T) {
	m := app.testModel(t)
	app.typeLine(m, "/screenshot")
	if _, dest := m.formStep(); m.form == nil || dest == nil || m.task != nil {
		t.Fatal("Implicit destination")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.form != nil {
		t.Fatal("Esc did not cancel the assistant")
	}
	m.target = core.Selection{Group: "pruebas"}
	m.layout()
	if m.target.Group != "pruebas" || !strings.Contains(m.footer(), "@pruebas") {
		t.Fatal(m.target)
	}
	app.typeLine(m, "/routine preparacion mensaje=hola")
	if out := app.printed(m); m.task != nil || !strings.Contains(out, "@pruebas") {
		t.Fatal(out)
	}
	app.typeLine(m, "/group create nuevo @DEV-001")
	if g, _ := app.Service.Groups(); len(g["nuevo"]) != 1 {
		t.Fatal(g, app.printed(m))
	}
	// Renaming keeps the devices and moves the current target with the group.
	m.target = core.Selection{Group: "nuevo"}
	app.typeLine(m, "/group rename nuevo caja-2")
	if g, _ := app.Service.Groups(); len(g["caja-2"]) != 1 || g["nuevo"] != nil || m.target.Group != "caja-2" || !strings.Contains(app.printed(m), "@nuevo → @caja-2") {
		t.Fatal(g, m.target, app.printed(m))
	}
	app.typeLine(m, "/group rename caja-2 pruebas")
	if g, _ := app.Service.Groups(); len(g["caja-2"]) != 1 || !strings.Contains(app.printed(m), "Ya hay un grupo") {
		t.Fatal("renamed over an existing group", g)
	}
}
func (app *Interface) testRemovedSimulationRejected(t *testing.T) {
	m := app.testModel(t)
	app.tuiDiscover = func() ([]core.Device, error) {
		t.Fatal("Removed flag triggered discovery")
		return nil, nil
	}
	for _, line := range []string{"/mode dry-run", "/mode dry", "/mode simulacion", "/mode simulación", "/screenshot --dry-run @all", "/group create nuevo --dry-run=true @DEV-001"} {
		app.typeLine(m, line)
		if m.task != nil || m.busy != "" || m.form != nil {
			t.Fatalf("Removed option accepted: %s", line)
		}
		if !strings.Contains(app.printed(m), "›") {
			t.Fatalf("Missing error for %s", line)
		}
	}
	c, err := m.parse("/shell echo --dry-run @all")
	if err != nil || c.raw != "echo --dry-run" {
		t.Fatal("Literal shell arguments changed", c, err)
	}
	m.setInput("/mode ")
	for _, s := range m.sugs {
		if strings.Contains(s.value, "dry") {
			t.Fatal("Removed mode in menu", s)
		}
	}
}
func (app *Interface) testCancelAndExit(t *testing.T) {
	m := app.testModel(t)
	block := make(chan struct{})
	app.tuiRunJob = func(c context.Context, target []core.Device, steps []core.Spec, o core.BatchOptions) (core.Job, error) {
		<-c.Done()
		close(block)
		return core.Job{Steps: steps, Results: []core.Result{{Alias: "DEV-001", Error: "Cancelado"}}}, nil
	}
	cmd := m.startTask("wait", "@DEV-001", m.devices[:1], []core.Spec{{Action: "wait", Args: []string{"5000"}}})
	done := make(chan tea.Msg, 1)
	go func() {
		done <- cmd()
	}()
	app.upd(m, tea.KeyMsg{Type: tea.KeyEsc})
	<-block
	app.upd(m, <-done)
	if m.task != nil || !strings.Contains(app.printed(m), "cancelado") {
		t.Fatal("Cancel not reported")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if c := app.upd(m, tea.KeyMsg{Type: tea.KeyCtrlC}); c == nil || !m.quitting {
		t.Fatal("Double Ctrl+C did not quit")
	}
}
func (app *Interface) frame(m *tuiModel) []string {
	m.layout()
	return strings.Split(m.View(), "\n")
}
func (app *Interface) testBottomBarPinned(t *testing.T) {
	m := app.testModel(t)
	for _, w := range []int{40, 80, 160} {
		app.upd(m, tea.WindowSizeMsg{Width: w, Height: 30})
		app.typeLine(m, "/help")
		app.typeLine(m, "/group")
		check := func(state string, input bool) {
			f := app.frame(m)
			if len(f) != 30 {
				t.Fatalf("%d cols, %s: %d rows", w, state, len(f))
			}
			for _, l := range f {
				if lipgloss.Width(l) > w {
					t.Fatalf("%d cols, %s: line wider than terminal: %q", w, state, l)
				}
			}
			if last := f[len(f)-1]; !strings.Contains(last, app.Service.L("dispositivo", "device")) {
				t.Fatalf("%d cols, %s: footer not on the last row: %q", w, state, last)
			}
			if prompt, below := ansi.Strip(f[len(f)-3]), ansi.Strip(f[len(f)-2]); input && (!strings.HasPrefix(prompt, " > ") || strings.ContainsAny(prompt, "┌└") || strings.TrimSpace(below) != "") {
				t.Fatalf("%d cols, %s: input line moved, boxed or glued to the footer:\n%s", w, state, strings.Join(f[len(f)-5:], "\n"))
			}
			if above := ansi.Strip(f[len(f)-4]); (state == "sugerencias" || state == "atajos") && strings.TrimSpace(above) != "" {
				t.Fatalf("%d cols, %s: list glued to the input: %q", w, state, above)
			}
		}
		check("vacío", true)
		m.setInput("/s")
		check("sugerencias", true)
		m.setInput("?")
		check("atajos", true)
		m.setInput(strings.Repeat("texto largo ", 30))
		check("línea larga", true)
		m.setInput("")
		m.task = &taskState{title: strings.Repeat("rutina muy larga ", 10), sel: "@all", started: time.Now(), targets: m.devices, steps: make([]core.Spec, 2), state: []int{2, 1, 0}, step: []int{1, 1, 0}, cancel: func() {
		}}
		check("progreso", true)
		m.task = nil
		m.panel = &devicesPanel{app: app, marked: map[string]bool{"S1": true}}
		check("panel", false)
		m.panel = nil
	}
}
func (app *Interface) testTranscriptScroll(t *testing.T) {
	m := app.testModel(t)
	app.typeLine(m, "/help")
	m.layout()
	if !m.vp.AtBottom() {
		t.Fatal("Transcript does not follow new output")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyPgUp})
	if m.vp.AtBottom() {
		t.Fatal("PgUp did not scroll")
	}
	m.print("nuevo")
	m.layout()
	if m.vp.AtBottom() || !m.newBelow || !strings.Contains(m.footer(), "nuevos") {
		t.Fatal("Scrolled transcript jumped or did not flag new output")
	}
	app.typeLine(m, "/group")
	if !m.vp.AtBottom() {
		t.Fatal("Submitting did not return to the end")
	}
	app.upd(m, tea.KeyMsg{Type: tea.KeyCtrlL})
	if len(m.transcript) != 0 || !strings.Contains(m.View(), "by doplerer") {
		t.Fatal("Ctrl+L did not restore the welcome screen", m.transcript)
	}
	app.typeLine(m, "/clear")
	if len(m.transcript) != 0 || !strings.Contains(m.View(), "by doplerer") {
		t.Fatal("/clear did not restore the welcome screen", m.transcript)
	}
}
func (app *Interface) testViewsRender(t *testing.T) {
	m := app.testModel(t)
	if testing.Verbose() {
		app.upd(m, tea.WindowSizeMsg{Width: 100, Height: 40})
		app.printed(m)
		t.Log("\n" + m.View())
		app.typeLine(m, "/help")
		m.setInput("/s")
		t.Log("\n" + app.printed(m) + "\n" + strings.Join(app.frame(m), "\n"))
		m.panel = &devicesPanel{app: app, marked: map[string]bool{"S1": true}}
		t.Log("\n" + strings.Join(app.frame(m), "\n"))
		m.panel = nil
		m.setInput("")
		app.typeLine(m, "/routine preparacion mensaje=hola @all")
		t.Log("\n" + strings.Join(app.frame(m), "\n"))
		app.run(m, app.upd(m, tea.KeyMsg{Type: tea.KeyEnter}))
		app.typeLine(m, "/shell getprop @pruebas")
		m.task = &taskState{title: "routine preparacion (2 pasos)", sel: "@all", started: time.Now(), targets: m.devices, steps: make([]core.Spec, 2), state: []int{2, 1, 0}, step: []int{1, 1, 0}, cancel: func() {
		}}
		m.setInput("/scr")
		t.Log("\n" + app.printed(m) + "\n" + strings.Join(app.frame(m), "\n"))
	}
}
func (app *Interface) testLogoShine(t *testing.T) {
	m := app.testModel(t)
	click := func(x, y int) tea.Cmd {
		return app.upd(m, tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress})
	}
	app.upd(m, tea.WindowSizeMsg{Width: 40, Height: 30})
	app.frame(m)
	if m.logoArea != (screenArea{}) || click(20, 10) != nil || m.logoShine != -1 {
		t.Fatal("compact welcome must not animate")
	}
	app.upd(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	app.frame(m)
	a := m.logoArea
	if a.w != 49 || a.h != 12 || !strings.Contains(ansi.Strip(app.frame(m)[a.y]), "██████╗  ███████╗ ██╗   ██") {
		t.Fatalf("logo area does not match the logo: %+v", a)
	}
	if click(a.x-1, a.y) != nil || click(a.x, a.y+a.h) != nil || m.logoShine != -1 {
		t.Fatal("click outside the logo started the shine")
	}
	rest := ansi.Strip(m.welcomeView())
	if click(a.x+a.w/2, a.y+a.h/2) == nil || m.logoShine != logoShineStart {
		t.Fatal("click on the logo did not start the shine")
	}
	if app.upd(m, logoFrameMsg{m.logoRun - 1}) != nil || m.logoShine != logoShineStart {
		t.Fatal("frame of an older shine advanced the new one")
	}
	for frames := 0; m.logoShine != -1; frames++ {
		if frames > 50 {
			t.Fatal("shine never ended")
		}
		if ansi.Strip(m.welcomeView()) != rest {
			t.Fatal("shine moved the layout")
		}
		app.upd(m, logoFrameMsg{m.logoRun})
	}
	if app.upd(m, logoFrameMsg{m.logoRun}) != nil || m.logoShine != -1 {
		t.Fatal("finished shine kept running")
	}
	if len(m.transcript) != 0 {
		t.Fatal("shine entered transcript")
	}
}
func (app *Interface) testWelcomeLayout(t *testing.T) {
	for _, language := range []string{"es", "en"} {
		t.Run(language, func(t *testing.T) {
			app.useLanguage(t, language)
			m := app.testModel(t)
			for _, size := range []struct {
				w, h    int
				large   bool
				visible bool
				tips    bool
			}{{40, 30, false, true, true}, {80, 30, true, true, true}, {160, 40, true, true, true}, {80, 12, false, true, false}, {40, 10, false, true, false}} {
				app.upd(m, tea.WindowSizeMsg{Width: size.w, Height: size.h})
				f := app.frame(m)
				if len(f) != size.h {
					t.Fatalf("%dx%d: got %d rows", size.w, size.h, len(f))
				}
				for _, line := range f {
					if lipgloss.Width(line) > size.w {
						t.Fatalf("%dx%d: line exceeds terminal width: %q", size.w, size.h, line)
					}
				}
				content := ansi.Strip(strings.Join(f[:m.vp.Height], "\n"))
				if strings.Contains(content, "by doplerer") != size.visible || strings.Contains(content, "██████╗  ███████╗ ██╗   ██") != size.large {
					t.Fatalf("%dx%d: unexpected welcome variant:\n%s", size.w, size.h, content)
				}
				rows := strings.Split(content, "\n")
				title := app.Service.L("Para empezar", "To get started")
				if strings.Contains(content, title) != size.tips {
					t.Fatalf("%dx%d: unexpected starter visibility", size.w, size.h)
				}
				start := 0
				if size.tips {
					if !strings.HasPrefix(rows[1], "  "+title) {
						t.Fatal("Starter title not at top left", content)
					}
					for i, command := range []string{"/devices", "/view", "/sync", "/help"} {
						if !strings.HasPrefix(rows[i+2], "  "+command) {
							t.Fatal("Missing starter command", command, content)
						}
					}
					start = 7
				}
				if size.visible {
					first, last := -1, -1
					for i, row := range rows {
						if i < start {
							continue
						}
						if strings.TrimSpace(row) != "" {
							if first < 0 {
								first = i
							}
							last = i
						}
						if strings.Contains(row, "by doplerer") {
							left := lipgloss.Width(row[:strings.Index(row, "by doplerer")])
							right := m.vp.Width - 1 - left - len("by doplerer")
							if left-right < -1 || left-right > 1 {
								t.Fatalf("signature not horizontally centered: %q", row)
							}
							if ruled := lipgloss.Width(strings.TrimSpace(row)); size.large && ruled != lipgloss.Width(strings.TrimPrefix(welcomeLogo, "\n")) {
								t.Fatalf("signature rule is %d wide, not as wide as the logo: %q", ruled, row)
							}
						}
						if strings.Contains(row, app.Service.L("dispositivo", "device")) && !strings.Contains(row, "│") {
							t.Fatalf("metrics not separated: %q", row)
						}
					}
					gap := first - start - (len(rows) - 1 - last)
					if gap < -1 || gap > 1 {
						t.Fatalf("welcome not vertically centered:\n%s", content)
					}
					for _, want := range []string{app.Service.L("2 dispositivos", "2 devices"), app.Service.L("1 grupo", "1 group"), app.Service.L("1 rutina", "1 routine")} {
						if !strings.Contains(content, want) {
							t.Fatalf("missing %q:\n%s", want, content)
						}
					}
				}
				if len(m.transcript) != 0 {
					t.Fatal("welcome entered transcript")
				}
			}
			app.upd(m, tea.WindowSizeMsg{Width: 100, Height: 30})
			for _, tc := range []struct {
				states []string
				want   string
				unauth bool
			}{{[]string{"device", "device", "unauthorized", "offline", "disconnected"}, app.Service.L("2 dispositivos", "2 devices"), true}, {nil, app.Service.L("0 dispositivos", "0 devices"), false}, {[]string{"device", "offline"}, app.Service.L("1 dispositivo", "1 device"), false}, {[]string{"device", "device", "device", "offline"}, app.Service.L("3 dispositivos", "3 devices"), false}} {
				var devices []core.Device
				for _, state := range tc.states {
					devices = append(devices, core.Device{State: state})
				}
				app.upd(m, devicesMsg{d: devices})
				for _, view := range []string{m.welcomeView(), m.footer()} {
					if !strings.Contains(ansi.Strip(view), tc.want) {
						t.Fatalf("missing live counter %q: %s", tc.want, view)
					}
				}
				if tc.unauth && (!strings.Contains(m.footer(), app.Service.L("1 sin autorizar", "1 unauthorized")) || !strings.Contains(ansi.Strip(m.welcomeView()), app.Service.L("1 sin autorizar", "1 unauthorized"))) {
					t.Fatal("unauthorized warning missing")
				}
			}
		})
	}
}
func (app *Interface) testWelcomeTransitions(t *testing.T) {
	m := app.testModel(t)
	if !strings.Contains(m.View(), "by doplerer") || len(m.transcript) != 0 {
		t.Fatal("missing initial welcome")
	}
	if !strings.Contains(m.View(), "Para empezar") || !strings.Contains(m.View(), "/sync") {
		t.Fatal("missing initial essential commands")
	}
	m.setInput("/s")
	if view := strings.Join(app.frame(m), "\n"); !strings.Contains(view, "by doplerer") || !strings.Contains(view, "Para empezar") {
		t.Fatal("typing hid welcome")
	}
	app.typeLine(m, "/help")
	if strings.Contains(m.View(), "by doplerer") || strings.Contains(strings.Join(m.transcript, "\n"), "by doplerer") || strings.Contains(m.View(), "Elegir dispositivos") {
		t.Fatal("welcome leaked into results")
	}
	app.typeLine(m, "/clear")
	m.panel = &devicesPanel{app: app, marked: map[string]bool{}}
	if view := strings.Join(app.frame(m), "\n"); strings.Contains(view, "by doplerer") || strings.Contains(view, "Para empezar") {
		t.Fatal("welcome shown behind panel")
	}
	m.panel = nil
	m.busy = "working"
	if view := strings.Join(app.frame(m), "\n"); strings.Contains(view, "by doplerer") || strings.Contains(view, "Para empezar") {
		t.Fatal("welcome shown during activity")
	}
	m.busy = ""
	m.layout()
	if !strings.Contains(m.View(), "by doplerer") || !strings.Contains(m.View(), "Para empezar") {
		t.Fatal("empty screen did not restore welcome")
	}
	app.typeLine(m, "/copy")
	if !strings.Contains(app.printed(m), "Todavía no hay resultados que copiar") {
		t.Fatal("welcome treated as a copyable result")
	}
	app.setTestLanguage(t, m, "en")
	if strings.Contains(strings.Join(m.transcript, "\n"), "by doplerer") {
		t.Fatal("language change printed welcome")
	}
	t.Cleanup(func() {
		app.Service.Lang = "es"
		app.RebuildTexts()
	})
	app.typeLine(m, "/clear")
	if view := m.View(); !strings.Contains(view, "devices") || !strings.Contains(view, "1 group") || !strings.Contains(view, "To get started") || strings.Contains(view, "Para empezar") || strings.Contains(view, "placas") {
		t.Fatal("welcome did not use current language")
	}
	os.WriteFile(app.Service.RoutinesPath("otra.json"), []byte("{}"), 0600)
	m.clearTranscript()
	m.layout()
	if !strings.Contains(m.View(), "2 routines") {
		t.Fatal("routine count was not refreshed")
	}
}
func (app *Interface) testHelpAndFullCommandMenu(t *testing.T) {
	m := app.testModel(t)
	app.typeLine(m, "/help motion tap")
	if out := app.printed(m); !strings.Contains(out, "Ejemplo") || !strings.Contains(out, "0 a 1") {
		t.Fatal(out)
	}
	app.upd(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	m.setInput("/")
	listed := map[string]bool{}
	for _, s := range m.sugs {
		listed[s.value] = true
	}
	for _, c := range app.tuiRegistry {
		if !listed["/"+c.name] {
			t.Fatalf("/ does not list /%s", c.name)
		}
	}
	for range 12 {
		app.upd(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if v := strings.Join(app.frame(m), "\n"); !strings.Contains(v, "más") {
		t.Fatal("Long menu without scroll hint")
	}
}
func (app *Interface) testWheelMovesSuggestions(t *testing.T) {
	m := app.testModel(t)
	app.upd(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	wheel := func(b tea.MouseButton) {
		app.upd(m, tea.MouseMsg{Button: b, Action: tea.MouseActionPress})
	}
	// Windows Terminal sends one event per scroll line, three per notch by default.
	m.wheelStep = 3
	m.setInput("/")
	for range 3 {
		wheel(tea.MouseButtonWheelDown)
	}
	if m.sugIdx != 1 || !m.sugMoved {
		t.Fatalf("one notch left the menu at %d", m.sugIdx)
	}
	for range 6 {
		wheel(tea.MouseButtonWheelDown)
	}
	if m.sugIdx != 3 {
		t.Fatalf("two fast notches left the menu at %d", m.sugIdx)
	}
	wheel(tea.MouseButtonWheelDown)
	m.wheelAt = time.Time{}
	wheel(tea.MouseButtonWheelDown)
	if m.sugIdx != 5 {
		t.Fatalf("a notch after a pause left the menu at %d", m.sugIdx)
	}
	wheel(tea.MouseButtonWheelUp)
	if m.sugIdx != 4 {
		t.Fatalf("wheel up left the menu at %d", m.sugIdx)
	}
	m.history = []string{"/devices"}
	m.hidx = len(m.history)
	m.setInput("")
	wheel(tea.MouseButtonWheelUp)
	if v := m.input.Value(); v != "" {
		t.Fatalf("wheel without a menu browsed the history: %q", v)
	}
}
func (app *Interface) testTerminalPasteStaysOnOneLine(t *testing.T) {
	m := app.testModel(t)
	app.upd(m, tea.WindowSizeMsg{Width: 100, Height: 20})
	app.printed(m)
	// The Windows console delivers a paste made by the terminal (Warp's middle click) as keystrokes.
	for _, r := range "3 eventos\n3 eventos" {
		if r == '\n' {
			app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
		} else {
			app.upd(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
	}
	if out := app.printed(m); out != "" {
		t.Fatalf("the paste ran a line: %s", out)
	}
	if v := m.input.Value(); v != "3 eventos 3 eventos" {
		t.Fatalf("pasted text was not kept on one line: %q", v)
	}
	m.charAt = time.Now().Add(-time.Second)
	app.upd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if out := app.printed(m); !strings.Contains(out, "desconocido") || m.input.Value() != "" {
		t.Fatalf("a typed Enter did not run the line: %q", out)
	}
}
func (app *Interface) testProgressBar(t *testing.T) {
	for _, c := range [][4]int{{0, 0, 16, 30}, {7, 1, 16, 30}, {16, 0, 16, 10}, {0, 16, 16, 10}, {1, 1, 3, 1}} {
		if w := lipgloss.Width(app.progressBar(c[0], c[1], c[2], c[3])); w != c[3] {
			t.Fatal(c, w)
		}
	}
}
func (app *Interface) testWrapIndented(t *testing.T) {
	long := app.tree(app.head(app.stOK, glyphOK, "título"), strings.Repeat("palabra ", 40))
	for _, l := range strings.Split(app.wrapIndented(long, 60), "\n")[1:] {
		if lipgloss.Width(l) > 60 || !strings.HasPrefix(ansi.Strip(l), "  "+glyphRule) {
			t.Fatalf("%q", l)
		}
	}
}
func (app *Interface) key(s string) tea.KeyMsg {
	types := map[string]tea.KeyType{"left": tea.KeyLeft, "right": tea.KeyRight, "shift+left": tea.KeyShiftLeft, "shift+right": tea.KeyShiftRight, "ctrl+left": tea.KeyCtrlLeft, "ctrl+right": tea.KeyCtrlRight, "ctrl+shift+left": tea.KeyCtrlShiftLeft, "shift+home": tea.KeyShiftHome, "backspace": tea.KeyBackspace, "ctrl+a": tea.KeyCtrlA, "ctrl+c": tea.KeyCtrlC, "ctrl+x": tea.KeyCtrlX, "ctrl+v": tea.KeyCtrlV, "esc": tea.KeyEsc}
	if t, ok := types[s]; ok {
		return tea.KeyMsg{Type: t}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}
func (app *Interface) testLineEditorSelectionAndClipboard(t *testing.T) {
	var clip string
	app.clipboardWrite = func(s string) error {
		clip = s
		return nil
	}
	app.clipboardRead = func() (string, error) {
		return "pegado\r\nen dos líneas", nil
	}
	m := app.testModel(t)
	m.setInput("/motion tap 0.5 0.5 @pruebas")
	app.upd(m, app.key("ctrl+shift+left"))
	if got := m.input.SelectedText(); got != "pruebas" {
		t.Fatalf("%q", got)
	}
	m.setInput("hola mundo")
	app.upd(m, app.key("shift+left"))
	app.upd(m, app.key("shift+left"))
	if m.input.SelectedText() != "do" {
		t.Fatal(m.input.SelectedText())
	}
	if app.upd(m, app.key("ctrl+c")); clip != "do" || !m.quitArmed.IsZero() {
		t.Fatal("Ctrl+C with selection did not copy", clip)
	}
	app.upd(m, app.key("X"))
	if m.input.Value() != "hola munX" {
		t.Fatal(m.input.Value())
	}
	app.upd(m, app.key("ctrl+left"))
	app.upd(m, app.key("ctrl+left"))
	if m.input.Position() != 0 {
		t.Fatal(m.input.Position())
	}
	app.upd(m, app.key("ctrl+a"))
	app.upd(m, app.key("ctrl+x"))
	if clip != "hola munX" || m.input.Value() != "" {
		t.Fatal(clip, m.input.Value())
	}
	app.upd(m, app.key("ctrl+v"))
	if m.input.Value() != "pegado en dos líneas" {
		t.Fatalf("%q", m.input.Value())
	}
	app.upd(m, app.key("shift+home"))
	app.upd(m, app.key("backspace"))
	if m.input.Value() != "" {
		t.Fatal(m.input.Value())
	}
	app.upd(m, app.key("ctrl+c"))
	if m.quitArmed.IsZero() {
		t.Fatal("Ctrl+C without selection should arm exit")
	}
	app.typeLine(m, "/help motion tap")
	app.typeLine(m, "/copy")
	if !strings.Contains(clip, "0 a 1") || strings.Contains(clip, "\x1b") {
		t.Fatalf("%q", clip)
	}
}
func (app *Interface) testLineEditorView(t *testing.T) {
	e := app.newLineEditor()
	e.Width = 10
	e.SetValue(strings.Repeat("abc😊", 10))
	e.CursorEnd()
	if w := lipgloss.Width(e.View()); w > 10 {
		t.Fatal(w)
	}
	e.SetCursor(0)
	if v := ansi.Strip(e.View()); !strings.HasPrefix(v, "abc") {
		t.Fatalf("%q", v)
	}
}
func (app *Interface) testLineEditorFirstCharAfterLongLine(t *testing.T) {
	e := app.newLineEditor()
	e.Width = 10
	e.SetValue(strings.Repeat("x", 40))
	e.CursorEnd()
	e.View()
	e.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	e.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if v := ansi.Strip(e.View()); !strings.HasPrefix(v, "a") {
		t.Fatalf("%q", v)
	}
	m := app.testModel(t)
	m.setInput(strings.Repeat("palabra ", 30))
	m.View()
	m.layout()
	m.setInput("")
	app.upd(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	if !strings.Contains(ansi.Strip(strings.Join(app.frame(m), "\n")), "> /") {
		t.Fatal("First character hidden")
	}
}
func (app *Interface) testConnectedDevicesFirst(t *testing.T) {
	m := app.testModel(t)
	app.upd(m, devicesMsg{d: []core.Device{{Serial: "S1", Alias: "DEV-001", State: "disconnected"}, {Serial: "S2", Alias: "DEV-002", State: "device"}, {Serial: "S3", Alias: "DEV-003", State: "offline"}, {Serial: "S4", Alias: "DEV-004", State: "device"}}})
	var order []string
	for _, d := range m.devices {
		order = append(order, d.Alias)
	}
	if !reflect.DeepEqual(order, []string{"DEV-002", "DEV-004"}) {
		t.Fatal("only the available devices are listed", order)
	}
	for _, h := range []int{40, 16} {
		app.upd(m, tea.WindowSizeMsg{Width: 100, Height: h})
		m.panel = &devicesPanel{app: app, marked: map[string]bool{}}
		f := app.frame(m)
		view := ansi.Strip(strings.Join(f, "\n"))
		if len(f) != h {
			t.Fatalf("height %d: got %d rows", h, len(f))
		}
		if h == 40 && (!strings.Contains(view, "2 no disponibles") || strings.Contains(view, "DEV-001") || strings.Contains(view, "DEV-003")) {
			t.Fatal("unavailable devices must stay out, counted in one line\n" + view)
		}
	}
	profile := lipgloss.ColorProfile()
	t.Cleanup(func() {
		lipgloss.SetColorProfile(profile)
	})
	lipgloss.SetColorProfile(termenv.TrueColor)
	app.upd(m, tea.WindowSizeMsg{Width: 100, Height: 40})
	for _, row := range app.frame(m) {
		plain := ansi.Strip(row)
		if strings.Contains(plain, "DEV-002") && !strings.Contains(row, "38;2;63;185;80") {
			t.Fatal("connected state not green: " + row)
		}
	}
	m.panel = nil
	sugs := m.mentionSuggestions("@DEV")
	if len(sugs) != 2 || sugs[0].value != "@DEV-002" || sugs[1].value != "@DEV-004" {
		t.Fatal("only the available devices are offered", sugs)
	}
	if d, ok := m.aliasOf("S1"); !ok || d.Alias != "DEV-001" {
		t.Fatal("groups still name unavailable members", d, ok)
	}
}
func (app *Interface) testClearShowsWelcomeWithScreensOpen(t *testing.T) {
	m := app.testModel(t)
	done := make(chan struct{})
	m.wall = &screenWall{Devices: m.devices[:2], Layout: []core.Rect{{X: 1, Y: 31, W: 200, H: 434}}, cancel: func() {
	}, done: done, ports: map[string]int{}}
	m.wall.alive.Store(2)
	m.sync = &syncState{s: &core.LiveSession{Errors: map[string]string{}}, master: m.devices[0]}
	app.typeLine(m, "/help")
	app.typeLine(m, "/clear")
	view := strings.Join(app.frame(m), "\n")
	if !strings.Contains(view, "by doplerer") || !strings.Contains(view, "Pantallas") {
		t.Fatal("welcome hidden while screens are open\n" + view)
	}
}
