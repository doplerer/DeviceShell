package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"

	"github.com/doplerer/deviceshell/internal/core"
)

// wheelIdle is the pause after which the wheel starts counting a new notch, so a leftover partial count never shifts it.
const wheelIdle = 100 * time.Millisecond

// pasteBurst: an Enter or Tab this close to a typed character is part of a paste made by the terminal,
// which the Windows console delivers as keystrokes, and must not run each line.
const pasteBurst = 15 * time.Millisecond

type tuiModel struct {
	app                     *Interface
	width, height           int
	input                   lineEditor
	spin                    spinner.Model
	send                    func(tea.Msg)
	vp                      viewport.Model
	transcript              []string
	bottom                  string
	dirty                   bool
	follow                  bool
	newBelow                bool
	devices                 []core.Device // the available ones, the only ones listed or offered
	inventory               []core.Device // every known device, for names and diagnosis
	groups                  map[string][]string
	packages                map[string]bool
	jobs                    []core.Job
	routineCount            int
	target                  core.Selection
	parallel                int
	timeout                 time.Duration
	sessionJobs             map[string]core.Job
	sessionOrder            []string
	resetPending            bool
	resetText               string
	history                 []string
	hidx                    int
	draft                   string
	recommendation          string
	dismissedRecommendation string
	sugs                    []suggestion
	sugIdx                  int
	sugStart                int
	sugHidden               bool
	sugOffset               int
	sugMoved                bool
	sugFree                 bool
	wheelAt                 time.Time
	wheelUp                 bool
	wheelCount              int
	wheelStep               int // wheel events per notch
	charAt                  time.Time
	form                    *formState
	hint                    string
	panel                   tuiPanel
	task                    *taskState
	sync                    *syncState
	wall                    *screenWall
	busy                    string
	busySince               time.Time
	lastFull                string
	lastShort               string
	lastFailed              []string
	flash                   string
	flashAt                 time.Time
	lastJob                 string
	ticks                   int
	refreshing              bool
	quitArmed               time.Time
	exiting                 bool
	quitting                bool
	health                  map[string]core.Health
	healthBusy              bool
	iosAutoBusy             bool
	alertTemp               int
	alertBattery            int
	logoShine               int // position of the welcome logo shine, -1 at rest
	logoRun                 int // bumps on every click so frames of an older shine are dropped
	logoArea                screenArea
}
type taskState struct {
	title, sel string
	started    time.Time
	cancel     context.CancelFunc
	canceled   bool
	targets    []core.Device
	steps      []core.Spec
	state      []int
	step       []int
	retry      []int // attempt of the current step, 0 on the first one
}
type syncState struct {
	s       *core.LiveSession
	info    core.SyncInfo
	master  core.Device
	peers   int
	views   string
	cancel  context.CancelFunc
	started time.Time
}
type tickMsg struct{}
type logoFrameMsg struct{ run int }
type devicesMsg struct {
	d   []core.Device
	err error
}
type asyncMsg struct{ apply func(*tuiModel) tea.Cmd }
type progressMsg struct {
	t              *taskState
	i, step, retry int
	r              *core.Result
}
type healthMsg map[string]core.Health
type taskDoneMsg struct {
	t   *taskState
	job core.Job
	err error
}
type syncReadyMsg struct {
	st *syncState
	s  *core.LiveSession
}
type syncLogMsg string
type syncEndMsg struct {
	st  *syncState
	err error
}
type quitTimeoutMsg struct{}
type wallEndMsg struct{ w *screenWall }

func (app *Interface) InteractiveTerminal() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}
func (app *Interface) Run() error {
	if !app.InteractiveTerminal() {
		return errors.New(app.Service.L("La interfaz interactiva necesita una terminal", "The interactive interface needs a terminal"))
	}
	m := app.newTUIModel()
	c, _, err := app.Service.ReadSettings()
	if err != nil {
		return err
	}
	m.applySettings(c)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	m.send = p.Send
	// Technical messages written to the error output (by DeviceShell's background work or
	// by a library) would land in the middle of the screen: while the interface runs
	// they go to a log instead.
	restore := app.redirectErrors()
	_, e := p.Run()
	restore()
	if m.wall != nil {
		m.wall.Close()
	}
	if len(m.transcript) > 0 {
		fmt.Println(strings.Join(m.transcript, "\n"))
	}
	return e
}
func (app *Interface) newTUIModel() *tuiModel {
	in := app.newLineEditor()
	in.Prompt = defaultPrompt
	in.PromptStyle = app.stSel
	in.Placeholder = app.placeholderText()
	in.PlaceholderStyle = app.stDim
	sp := spinner.New()
	sp.Spinner = app.spinnerFrames
	sp.Style = app.stAccent
	vp := viewport.New(0, 0)
	vp.MouseWheelDelta = 3
	m := &tuiModel{app: app, input: in, spin: sp, vp: vp, send: func(tea.Msg) {
	}, groups: map[string][]string{}, packages: map[string]bool{}, sessionJobs: map[string]core.Job{}, parallel: app.Service.Parallel, timeout: app.Service.Timeout, wheelStep: 1, logoShine: -1}
	// Windows Terminal and Warp send one wheel event per scroll line; the classic console sends one per notch.
	if os.Getenv("WT_SESSION") != "" || os.Getenv("TERM_PROGRAM") == "WarpTerminal" {
		m.wheelStep = app.Service.WheelLines()
	}
	if m.parallel < 1 {
		m.parallel = 4
	}
	if m.timeout <= 0 {
		m.timeout = 180 * time.Second
	}
	if d, e := app.Service.LoadInventory(app.Service.InventoryPath()); e == nil {
		m.setDevices(d)
	}
	if c, _, err := app.Service.ReadSettings(); err == nil {
		m.applySettings(c)
	}
	m.reloadGroups()
	m.history = app.Service.LoadHistory()
	m.hidx = len(m.history)
	return m
}
func (app *Interface) parseADBVersion(out string) string {
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "Version" {
			return f[1]
		}
	}
	if f := strings.Fields(out); len(f) > 0 {
		return f[len(f)-1]
	}
	return ""
}
func (m *tuiModel) w() int {
	if m.width < 20 {
		return 100
	}
	return m.width
}
func (m *tuiModel) h() int {
	if m.height < 10 {
		return 30
	}
	return m.height
}
func (app *Interface) placeholderText() string {
	return app.Service.L("Escribe un comando (/help) o elige placas con /devices", "Type a command (/help) or pick devices with /devices")
}
func (m *tuiModel) print(s string) {
	m.transcript = append(m.transcript, s)
	if len(m.transcript) > 1000 {
		m.transcript = m.transcript[len(m.transcript)-1000:]
	}
	m.dirty = true
}

// copyText puts plain text on the clipboard and confirms it briefly in the status bar.
func (m *tuiModel) copyText(s, done string) {
	if e := m.app.clipboardWrite(s); e != nil {
		m.printErr(m.app.Service.L("No se pudo copiar al portapapeles: ", "Could not copy to the clipboard: ") + e.Error())
		return
	}
	m.flash, m.flashAt = done, time.Now()
}

// clearTranscript returns to the welcome screen without adding it to the results.
func (m *tuiModel) clearTranscript() tea.Cmd {
	m.transcript = nil
	m.dirty, m.follow, m.newBelow = true, true, false
	m.reloadGroups()
	return nil
}
func (m *tuiModel) printInfo(s string) {
	m.print("  " + m.app.stAccent.Render(glyphInfo) + " " + m.app.stDim.Render(s))
}
func (m *tuiModel) printErr(e any) {
	m.print("  " + m.app.stErr.Render(glyphFail+" "+fmt.Sprint(e)))
}
func (m *tuiModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.app.tickCmd(), m.refreshCmd(), func() tea.Msg {
		// Screens left changed by a replica that ended abruptly go back to how they were.
		m.app.Service.RecoverDisplays(m.app.Service.Ctx)
		return nil
	})
}
func (app *Interface) tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}
func (app *Interface) logoFrameCmd(run int) tea.Cmd {
	return tea.Tick(30*time.Millisecond, func(time.Time) tea.Msg {
		return logoFrameMsg{run}
	})
}
func (m *tuiModel) refreshCmd() tea.Cmd {
	if m.refreshing {
		return nil
	}
	m.refreshing = true
	return func() tea.Msg {
		d, e := m.app.tuiLive()
		return devicesMsg{d, e}
	}
}

// healthCmd reads battery and temperature of the connected devices in the background.
func (m *tuiModel) healthCmd() tea.Cmd {
	if m.healthBusy {
		return nil
	}
	m.healthBusy = true
	devices, parallel := m.devices, m.parallel
	return func() tea.Msg {
		return healthMsg(m.app.tuiHealth(devices, parallel))
	}
}

// healthAlerts are the alerts of one device with the limits of /settings; alertCount counts the connected ones with any.
func (m *tuiModel) healthAlerts(serial string) []string {
	h, ok := m.health[serial]
	if !ok {
		return nil
	}
	return m.app.Service.HealthAlerts(h, m.alertTemp, m.alertBattery)
}
func (m *tuiModel) alertCount() int {
	n := 0
	for _, d := range m.devices {
		if d.State == "device" && len(m.healthAlerts(d.Serial)) > 0 {
			n++
		}
	}
	return n
}
func (m *tuiModel) reloadGroups() {
	if g, e := m.app.Service.Groups(); e == nil {
		m.groups = g
	}
	if routines, e := filepath.Glob(m.app.Service.RoutinesPath("*.json")); e == nil {
		m.routineCount = len(routines)
	}
}

// wrapIndented wraps each line to width; continuation rows keep the result rule and indent so blocks stay readable.
func (app *Interface) wrapIndented(s string, width int) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if lipgloss.Width(l) <= width {
			continue
		}
		cont := "    "
		if strings.HasPrefix(ansi.Strip(l), "  "+glyphRule) {
			cont = "  " + app.stFaint.Render(glyphRule) + "   "
		}
		rows := strings.Split(ansi.Wrap(l, width, " "), "\n")
		rest := strings.TrimLeft(strings.Join(rows[1:], " "), " ")
		more := strings.Split(ansi.Wrap(rest, max(10, width-lipgloss.Width(cont)), " "), "\n")
		for j := range more {
			more[j] = cont + strings.TrimLeft(more[j], " ")
		}
		lines[i] = rows[0] + "\n" + strings.Join(more, "\n")
	}
	return strings.Join(lines, "\n")
}
func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m.refreshRecommendation()
	cmd := m.update(msg)
	m.layout()
	return m, cmd
}

// layout gives the bottom area (status, input, footer) the rows it needs and the transcript the rest.
// The transcript stays glued to its end unless the user scrolled up to read.
func (m *tuiModel) layout() {
	m.refreshRecommendation()
	if m.width == 0 {
		return
	}
	stick := m.vp.AtBottom() || m.follow
	if m.dirty || m.vp.Width != m.w() {
		m.vp.Width = m.w()
		var b strings.Builder
		for i, s := range m.transcript {
			if i > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(m.app.wrapIndented(s, m.w()-1))
		}
		m.vp.SetContent(b.String())
		if m.dirty && !stick {
			m.newBelow = true
		}
		m.dirty, m.follow = false, false
	}
	if stick {
		m.newBelow = false
	}
	m.bottom = m.bottomView()
	m.vp.Height = max(1, m.h()-lipgloss.Height(m.bottom))
	if stick {
		m.vp.GotoBottom()
	}
	if m.vp.AtBottom() {
		m.newBelow = false
	}
}
func (m *tuiModel) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.fitInput()
		if m.form == nil {
			m.input.Placeholder = m.app.clip(m.app.placeholderText(), m.input.Width)
		}
		return nil
	case tea.KeyMsg:
		return m.key(msg)
	case tea.MouseMsg:
		// A click on the welcome logo sweeps a shine across it; clicking again restarts it.
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && m.showWelcome() && m.logoArea.contains(msg.X, msg.Y) {
			m.logoRun++
			m.logoShine = logoShineStart
			return m.app.logoFrameCmd(m.logoRun)
		}
		// With a list open the wheel moves its selection like the arrows; otherwise it scrolls the results.
		if up := msg.Button == tea.MouseButtonWheelUp; msg.Action == tea.MouseActionPress && (up || msg.Button == tea.MouseButtonWheelDown) && m.wheelSelects() {
			if up != m.wheelUp || time.Since(m.wheelAt) > wheelIdle {
				m.wheelCount = 0
			}
			m.wheelAt, m.wheelUp = time.Now(), up
			m.wheelCount++
			if (m.wheelCount-1)%max(1, m.wheelStep) != 0 {
				return nil
			}
			if up {
				return m.key(tea.KeyMsg{Type: tea.KeyUp})
			}
			return m.key(tea.KeyMsg{Type: tea.KeyDown})
		}
		var c tea.Cmd
		m.vp, c = m.vp.Update(msg)
		return c
	case spinner.TickMsg:
		var c tea.Cmd
		m.spin, c = m.spin.Update(msg)
		return c
	case tickMsg:
		if m.resetPending {
			return m.app.tickCmd()
		}
		m.ticks++
		if m.sync != nil && m.sync.s != nil {
			m.sync.info = m.sync.s.Info()
		}
		cmds := []tea.Cmd{m.app.tickCmd()}
		if m.ticks%3 == 0 && m.task == nil {
			cmds = append(cmds, m.refreshCmd())
		}
		if m.ticks%10 == 0 {
			m.reloadGroups()
		}
		// Battery and temperature are read soon after starting and then every minute, never during a batch.
		if (m.ticks == 5 || m.ticks%60 == 0) && m.task == nil {
			cmds = append(cmds, m.healthCmd())
		}
		return tea.Batch(cmds...)
	case logoFrameMsg:
		if msg.run != m.logoRun || m.logoShine == -1 {
			return nil
		}
		m.logoShine += logoShineStep
		// The band is done once its halo has left the last column of the bottom row.
		if m.logoShine > m.logoArea.w+2*m.logoArea.h+5 {
			m.logoShine = -1
			return nil
		}
		return m.app.logoFrameCmd(m.logoRun)
	case devicesMsg:
		m.refreshing = false
		if msg.err == nil && !m.resetPending {
			m.setDevices(msg.d)
			return m.iosAutoCmd(msg.d)
		}
		return nil
	case healthMsg:
		m.healthBusy = false
		m.health = msg
		return nil
	case iosAutoDone:
		m.iosAutoBusy = false
		return nil
	case asyncMsg:
		m.busy = ""
		return msg.apply(m)
	case progressMsg:
		t := m.task
		if t == nil || msg.t != t {
			return nil
		}
		if len(t.retry) < len(t.state) {
			t.retry = make([]int, len(t.state))
		}
		switch {
		case msg.r != nil:
			t.state[msg.i] = 2
			if !msg.r.OK {
				t.state[msg.i] = 3
			}
		case msg.retry > 0:
			t.retry[msg.i] = msg.retry
		default:
			t.state[msg.i] = 1
			t.step[msg.i] = msg.step
			t.retry[msg.i] = 0
		}
		return nil
	case taskDoneMsg:
		return m.taskDone(msg)
	case syncReadyMsg:
		if msg.st != m.sync {
			return nil
		}
		msg.st.s = msg.s
		msg.st.info = msg.s.Info()
		msg.st.peers = msg.s.Followers()
		m.print("\n" + m.app.tree(m.app.head(m.app.stOK, glyphOK, m.app.Service.L("Réplica en vivo", "Live replica"))+m.app.stDim.Render(m.app.Service.L(" · sesión ", " · session ")+msg.s.ID), fmt.Sprintf(m.app.Service.L("Principal %s → %d seguidoras", "Master %s → %d followers"), m.app.stBold.Render(msg.st.master.Alias), msg.st.peers), m.app.stDim.Render(m.app.Service.L("Interactúa con la ventana de la principal · /sync para pausar o detener", "Work in the master window · /sync to pause or stop"))))
		return nil
	case syncLogMsg:
		m.print("  " + m.app.stWarn.Render("!") + " " + m.app.stDim.Render(string(msg)))
		return nil
	case syncEndMsg:
		if msg.st == m.sync {
			m.sync = nil
		}
		if msg.err != nil {
			m.print("\n" + m.app.tree(m.app.head(m.app.stErr, glyphFail, m.app.Service.L("Réplica no disponible", "Replica unavailable")), m.app.stErr.Render(msg.err.Error())))
		} else {
			info := msg.st.info
			m.print("\n" + m.app.tree(m.app.head(m.app.stDim, glyphInfo, m.app.Service.L("Réplica terminada", "Replica ended")), fmt.Sprintf(m.app.Service.L("%d eventos replicados · %s", "%d events replicated · %s"), info.Events, m.app.fmtDuration(time.Since(msg.st.started))), m.app.stDim.Render(m.app.Service.L("Informe: ", "Report: ")+m.app.relPath(m.app.Service.LogsPath("sync", info.ID+".json")))))
		}
		return m.maybeExit()
	case wallEndMsg:
		if msg.w == m.wall {
			m.wall = nil
			m.printInfo(m.app.Service.L("Pantallas cerradas", "Screens closed"))
		}
		return nil
	case quitTimeoutMsg:
		m.quitting = true
		return tea.Quit
	}
	return nil
}
func (m *tuiModel) key(k tea.KeyMsg) tea.Cmd {
	s := k.String()
	if (s == "enter" || s == "tab") && time.Since(m.charAt) < pasteBurst {
		if m.panel != nil || m.resetPending {
			return nil
		}
		k, s = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}, " "
	}
	switch {
	case k.Type == tea.KeyRunes || k.Type == tea.KeySpace:
		if m.panel == nil && !m.resetPending {
			m.charAt = time.Now()
		}
	case s != "enter" && s != "tab":
		m.charAt = time.Time{}
	}
	if p, ok := m.panel.(*settingsPanel); ok {
		return p.key(m, k)
	}
	if m.resetPending {
		return m.resetKey(k)
	}
	if s == "ctrl+c" && m.panel == nil && m.input.HasSelection() {
		m.copyText(m.input.SelectedText(), m.app.Service.L("Selección copiada", "Selection copied"))
		return nil
	}
	if s == "ctrl+c" {
		switch {
		case m.panel != nil:
			old := m.panel
			m.panel = nil
			if p, ok := old.(*devicesPanel); ok && p.onCancel != nil {
				p.onCancel()
			}
		case m.input.Value() != "":
			m.setInput("")
		case time.Since(m.quitArmed) < 2*time.Second:
			return m.quit()
		default:
			m.quitArmed = time.Now()
		}
		return nil
	}
	m.quitArmed = time.Time{}
	if m.exiting {
		return nil
	}
	if m.panel != nil {
		return m.panel.key(m, k)
	}
	if m.form != nil && m.formArrow(s) {
		return nil
	}
	switch s {
	case "esc":
		switch {
		case m.input.HasSelection():
			m.input.ClearSelection()
		case m.form != nil:
			m.cancelForm()
		case m.recommendationVisible():
			m.dismissedRecommendation = m.recommendation
		case m.sugVisible():
			m.sugHidden = true
		case m.task != nil:
			if !m.task.canceled {
				m.task.canceled = true
				m.task.cancel()
			}
		default:
			m.setInput("")
		}
		return nil
	case "ctrl+o":
		if m.lastFull == "" {
			m.printInfo(m.app.Service.L("Todavía no hay salida que ampliar", "There is no output to expand yet"))
		} else {
			m.print("\n" + m.lastFull)
		}
		return nil
	case "ctrl+l":
		return m.clearTranscript()
	case "pgup":
		m.vp.PageUp()
		return nil
	case "pgdown":
		m.vp.PageDown()
		return nil
	case "ctrl+home":
		m.vp.GotoTop()
		return nil
	case "ctrl+end":
		m.vp.GotoBottom()
		return nil
	case "home", "end":
		if m.input.Value() == "" {
			if s == "home" {
				m.vp.GotoTop()
			} else {
				m.vp.GotoBottom()
			}
			return nil
		}
	case "tab":
		if m.recommendationVisible() {
			m.setInput(m.recommendation)
		} else if m.sugVisible() {
			m.applySuggestion(m.sugs[m.sugIdx])
		}
		return nil
	case "right":
		if m.recommendationVisible() {
			m.setInput(m.recommendation)
			return nil
		}
	case "up":
		if m.sugVisible() {
			m.sugMoved = true
			m.sugIdx = (m.sugIdx + len(m.sugs) - 1) % len(m.sugs)
		} else if m.form == nil {
			m.historyMove(-1)
		}
		return nil
	case "down":
		if m.sugVisible() {
			m.sugMoved = true
			m.sugIdx = (m.sugIdx + 1) % len(m.sugs)
		} else if m.form == nil {
			m.historyMove(1)
		}
		return nil
	case "backspace":
		if m.form != nil && m.input.Value() == "" {
			return m.formBack()
		}
	case "enter":
		if m.form != nil {
			return m.formEnter()
		}
		if m.takeSuggestion() {
			sg := m.sugs[m.sugIdx]
			if sg.pick {
				m.applySuggestion(sg)
				return nil
			}
			if m.currentToken() != sg.value {
				m.applySuggestion(sg)
				if sg.noSpace {
					return nil
				}
			}
		}
		line := strings.TrimSpace(m.input.Value())
		if line == "" || line == "?" {
			m.setInput("")
			return nil
		}
		return m.submitOrGuide(line)
	}
	before, pos := m.input.Value(), m.input.Position()
	c := m.input.Update(k)
	if m.input.Value() != before {
		m.sugHidden = false
		m.sugIdx = 0
		m.sugMoved = false
	}
	if m.input.Value() != before || m.input.Position() != pos {
		m.updateSuggestions()
	}
	return c
}
func (m *tuiModel) setInput(v string) {
	m.input.SetValue(v)
	m.input.CursorEnd()
	m.charAt = time.Time{}
	m.sugIdx = 0
	m.sugHidden = false
	m.sugMoved = false
	m.updateSuggestions()
}

// takeSuggestion says whether Enter should use the highlighted suggestion rather than what was typed.
func (m *tuiModel) takeSuggestion() bool {
	if !m.sugVisible() {
		return false
	}
	empty := strings.TrimSpace(m.input.Value()) == ""
	if m.form != nil && empty && !m.sugMoved {
		if p, _ := m.formStep(); p != nil && p.optional && p.def == "" {
			return false
		}
	}
	if !m.sugFree || m.sugMoved {
		return true
	}
	return m.form != nil && empty
}

// wheelSelects says whether up and down currently move a list (suggestions, panel or prompt).
func (m *tuiModel) wheelSelects() bool {
	switch {
	case m.resetPending || m.exiting:
		return false
	case m.panel != nil:
		p, ok := m.panel.(*settingsPanel)
		return !ok || !p.editing
	}
	return m.sugVisible() && m.input.Value() != "?"
}

// async runs slow work (ADB, inventory) off the event loop; apply runs back on it with the result.
func (m *tuiModel) async(label string, f func() func(*tuiModel) tea.Cmd) tea.Cmd {
	if m.busy != "" {
		m.printErr(m.app.Service.L("Espera a que termine: ", "Wait until this finishes: ") + m.busy)
		return nil
	}
	m.busy = label
	m.busySince = time.Now()
	return func() tea.Msg {
		return asyncMsg{f()}
	}
}
func (m *tuiModel) submit(line string) tea.Cmd {
	m.dismissedRecommendation = ""
	m.addHistory(line)
	m.follow = true
	m.print("\n" + m.app.stSel.Render(glyphInfo) + " " + m.app.stBold.Render(line))
	c, e := m.parse(line)
	if e != nil {
		m.printErr(e)
		return nil
	}
	return c.cmd.run(m, c)
}
func (m *tuiModel) quit() tea.Cmd {
	if m.task == nil && m.sync == nil {
		m.quitting = true
		return tea.Quit
	}
	m.exiting = true
	if m.task != nil && !m.task.canceled {
		m.task.canceled = true
		m.task.cancel()
	}
	if m.sync != nil {
		m.sync.cancel()
	}
	return tea.Tick(10*time.Second, func(time.Time) tea.Msg {
		return quitTimeoutMsg{}
	})
}
func (m *tuiModel) maybeExit() tea.Cmd {
	if m.exiting && m.task == nil && m.sync == nil {
		m.quitting = true
		return tea.Quit
	}
	return nil
}

// dispatchBatch starts the batch right away on the available targets; the others
// (disconnected, locked...) are left out with one warning so they do not get in the way.
func (m *tuiModel) dispatchBatch(title, sel string, targets []core.Device, steps []core.Spec) tea.Cmd {
	if off := m.app.notConnected(targets); len(off) > 0 {
		targets = core.Listed(targets)
		if len(targets) == 0 {
			m.printErr(m.app.Service.L("Ninguna de las placas elegidas está disponible: ", "None of the chosen devices is available: ") + m.app.aliasList(off, 6) + m.app.Service.L(" · /doctor para ver por qué", " · /doctor shows why"))
			return nil
		}
		m.print("  " + m.app.stWarn.Render(m.app.Service.L("! No disponibles, se omiten: ", "! Unavailable, skipped: ")+m.app.aliasList(off, 6)))
	}
	return m.startTask(title, sel, targets, steps)
}
func (m *tuiModel) startTask(title, sel string, targets []core.Device, steps []core.Spec) tea.Cmd {
	if m.task != nil {
		m.printErr(m.app.Service.L("Hay un lote en curso; espera a que termine o pulsa esc para cancelarlo", "A batch is running; wait for it or press esc to cancel it"))
		return nil
	}
	c, cancel := context.WithCancel(m.app.Service.Ctx)
	t := &taskState{title: title, sel: sel, started: time.Now(), cancel: cancel, targets: targets, steps: steps, state: make([]int, len(targets)), step: make([]int, len(targets)), retry: make([]int, len(targets))}
	m.task = t
	send := m.send
	o := core.BatchOptions{Parallel: m.parallel, Timeout: m.timeout, OnStart: func(i int) {
		send(progressMsg{t: t, i: i})
	}, OnStep: func(i, n int) {
		send(progressMsg{t: t, i: i, step: n})
	}, OnRetry: func(i, n, attempt int) {
		send(progressMsg{t: t, i: i, step: n, retry: attempt})
	}, OnResult: func(i int, r core.Result) {
		send(progressMsg{t: t, i: i, r: &r})
	}}
	return func() tea.Msg {
		j, e := m.app.tuiRunJob(c, targets, steps, o)
		cancel()
		return taskDoneMsg{t, j, e}
	}
}
func (m *tuiModel) taskDone(msg taskDoneMsg) tea.Cmd {
	if msg.t == m.task {
		m.task = nil
	}
	t := msg.t
	elapsed := time.Since(t.started)
	m.lastShort = "\n" + m.renderJob(t.title, t.sel, msg.job, elapsed, false, t.canceled)
	m.print(m.lastShort)
	m.lastFull = m.renderJob(t.title, t.sel, msg.job, elapsed, true, t.canceled)
	if msg.err != nil {
		m.printErr(m.app.Service.L("No se pudo guardar el informe: ", "Could not save the report: ") + msg.err.Error())
	}
	m.lastJob = msg.job.ID
	if m.sessionJobs == nil {
		m.sessionJobs = map[string]core.Job{}
	}
	m.sessionJobs[msg.job.ID] = msg.job
	m.sessionOrder = append(m.sessionOrder, msg.job.ID)
	if len(m.sessionOrder) > 100 {
		delete(m.sessionJobs, m.sessionOrder[0])
		m.sessionOrder = m.sessionOrder[1:]
	}
	m.jobs = nil
	m.lastFailed = nil
	for _, r := range msg.job.Results {
		if !r.OK {
			m.lastFailed = append(m.lastFailed, r.Alias)
		}
	}
	for _, r := range msg.job.Results {
		for _, l := range strings.Split(r.Output, "\n") {
			if p, ok := strings.CutPrefix(strings.TrimSpace(l), "package:"); ok && p != "" {
				m.packages[p] = true
			}
		}
	}
	return tea.Batch(m.refreshCmd(), m.maybeExit())
}
func (m *tuiModel) startSync(master core.Device, targets []core.Device, views string) tea.Cmd {
	c, cancel := context.WithCancel(m.app.Service.Ctx)
	st := &syncState{master: master, views: views, cancel: cancel, started: time.Now()}
	m.sync = st
	send := m.send
	o := core.SyncOptions{Master: master, Targets: targets, Views: views, MasterSize: m.app.orDefault(m.app.Service.MasterSize, 1024), MasterFPS: m.app.orDefault(m.app.Service.MasterFPS, 60), FollowerSize: m.app.orDefault(m.app.Service.FollowerSize, 480), FollowerFPS: m.app.orDefault(m.app.Service.FollowerFPS, 30)}
	return func() tea.Msg {
		e := m.app.tuiRunSync(c, o, func(s string) {
			go send(syncLogMsg(s))
		}, func(s *core.LiveSession) {
			send(syncReadyMsg{st, s})
		})
		cancel()
		return syncEndMsg{st, e}
	}
}
func (app *Interface) orDefault(v, d int) int {
	if v > 0 {
		return v
	}
	return d
}
func (m *tuiModel) addHistory(line string) {
	m.hidx = len(m.history)
	if strings.ContainsAny(line, "\r\n") || (len(m.history) > 0 && m.history[len(m.history)-1] == line) {
		return
	}
	m.history = append(m.history, line)
	m.hidx = len(m.history)
	if len(m.history) > 100 {
		m.history = m.history[len(m.history)-100:]
		m.hidx = len(m.history)
	}
	if e := m.app.Service.PersistHistory(line); e != nil {
		m.printErr(e)
	}
}
func (m *tuiModel) historyMove(d int) {
	n := m.hidx + d
	if n < 0 || n > len(m.history) {
		return
	}
	if m.hidx == len(m.history) {
		m.draft = m.input.Value()
	}
	m.hidx = n
	v := m.draft
	if n < len(m.history) {
		v = m.history[n]
	}
	m.input.SetValue(v)
	m.input.CursorEnd()
	m.sugs = nil
	m.sugHidden = true
}
