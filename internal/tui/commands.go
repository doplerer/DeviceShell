package tui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/doplerer/deviceshell/internal/core"
)

type token struct {
	text       string
	quoted     bool
	start, end int
}

// tokenize splits like a shell without escapes, so Windows paths keep their backslashes.
// A quote only opens at the start of a token: "l'eau" stays a single literal word.
func (app *Interface) tokenize(s string) []token {
	var out []token
	var b strings.Builder
	start, quote, quoted := -1, rune(0), false
	for i, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
		case (r == '"' || r == '\'') && start < 0:
			start, quote, quoted = i, r, true
		case unicode.IsSpace(r):
			if start >= 0 {
				out = append(out, token{b.String(), quoted, start, i})
				b.Reset()
				start, quoted = -1, false
			}
		default:
			if start < 0 {
				start = i
			}
			b.WriteRune(r)
		}
	}
	if start >= 0 {
		out = append(out, token{b.String(), quoted, start, len(s)})
	}
	return out
}

type suggestion struct {
	value, label, detail string
	example              string
	keywords             string
	bold, indent         bool
	noSpace              bool
	pick                 bool
}
type tuiCommand struct {
	name, usage, help, group string
	example, notes           string
	children                 []*tuiCommand
	aliases                  []string
	target                   bool
	sel                      bool
	raw                      bool
	min, max                 int
	run                      func(m *tuiModel, c call) tea.Cmd
}
type call struct {
	cmd   *tuiCommand
	line  string
	args  []string
	raw   string
	flags map[string]string
	sel   core.Selection
}

func (app *Interface) groupTitle(key string) string {
	switch key {
	case "help":
		return app.Service.L("Ayuda", "Help")
	case "inventory":
		return app.Service.L("Inventario", "Inventory")
	case "actions":
		return app.Service.L("Acciones", "Actions")
	case "system":
		return app.Service.L("Sistema", "System")
	case "routines":
		return app.Service.L("Rutinas y lotes", "Routines and batches")
	case "screens":
		return app.Service.L("Pantallas y réplica", "Screens and replica")
	}
	return app.Service.L("Sesión", "Session")
}
func (app *Interface) lookupCommand(name string) *tuiCommand {
	name = strings.ToLower(strings.TrimPrefix(name, "/"))
	for _, c := range app.allTUICommands(app.tuiRegistry) {
		if c.name == name || slices.Contains(c.aliases, name) {
			return c
		}
	}
	return nil
}

// allTUICommands includes children for lookup and semantic search, but not the root menu.
func (app *Interface) allTUICommands(commands []*tuiCommand) []*tuiCommand {
	out := append([]*tuiCommand(nil), commands...)
	for _, c := range commands {
		out = append(out, app.allTUICommands(c.children)...)
	}
	return out
}

// commandTokens combines a recognized command path, preserving source byte offsets for raw text.
func (app *Interface) commandTokens(toks []token) []token {
	if len(toks) < 2 {
		return toks
	}
	if c := app.lookupCommand(toks[0].text); c != nil && len(c.children) > 0 {
		if child := app.lookupCommand(c.name + " " + strings.ToLower(toks[1].text)); child != nil {
			head := token{text: child.name, start: toks[0].start, end: toks[1].end}
			return append([]token{head}, toks[2:]...)
		}
	}
	return toks
}
func (app *Interface) buildTUICommands() []*tuiCommand {
	act := func(name, usage, help string, min, max int, build func(call) ([]core.Spec, error)) *tuiCommand {
		return &tuiCommand{name: name, usage: usage, help: help, group: "actions", target: true, sel: true, min: min, max: max, run: func(m *tuiModel, c call) tea.Cmd {
			steps, e := build(c)
			if e != nil {
				m.printErr(e)
				return nil
			}
			title := app.describeSteps(steps)
			if c.cmd.name == "routine" {
				title = fmt.Sprintf("routine %s (%s)", c.args[0], app.plural(len(steps), app.Service.L("paso", "step"), app.Service.L("pasos", "steps")))
			}
			return m.runOnTargets(c, title, steps)
		}}
	}
	one := func(action string) func(call) ([]core.Spec, error) {
		return func(c call) ([]core.Spec, error) {
			return []core.Spec{{Action: action, Args: c.args}}, nil
		}
	}
	coord := func(action string) func(call) ([]core.Spec, error) {
		return func(c call) ([]core.Spec, error) {
			s, e := app.Service.CoordinateSpec(action, c.args)
			return []core.Spec{s}, e
		}
	}
	nonEmpty := func(action, what string) func(call) ([]core.Spec, error) {
		return func(c call) ([]core.Spec, error) {
			if c.raw == "" {
				return nil, errors.New(app.Service.L("Falta ", "Missing ") + what)
			}
			return []core.Spec{{Action: action, Args: []string{c.raw}}}, nil
		}
	}
	apk := act("apk", "/apk FILE.apk", app.Service.L("Instalar un APK", "Install an APK"), 1, 1, func(c call) ([]core.Spec, error) {
		return []core.Spec{{Action: "apk-install", Args: c.args}}, nil
	})
	push := act("push", "/push LOCAL ANDROID_PATH", app.Service.L("Enviar un archivo", "Send a file"), 2, 2, one("push"))
	text := act("text", "/text TEXT", app.Service.L("Pegar texto UTF-8 (también emojis)", "Paste UTF-8 text (emojis too)"), 1, 1, nonEmpty("text", app.Service.L("el texto", "the text")))
	text.raw = true
	shell := act("shell", "/shell COMMAND   ·   !COMMAND", app.Service.L("Ejecutar una orden explícita en Android", "Run an explicit Android command"), 1, 1, nonEmpty("shell", app.Service.L("la orden", "the command")))
	shell.raw = true
	key := act("key", "/key HOME|BACK|ENTER|POWER|VOLUME_UP|…|CODE", app.Service.L("Pulsar una tecla", "Press a key"), 1, 1, one("key"))
	routine := act("routine", "/routine NAME [key=value…]", app.Service.L("Ejecutar una rutina de routines/", "Run a routine from routines/"), 1, -1, func(c call) ([]core.Spec, error) {
		return app.Service.LoadRoutine(c.args)
	})
	routine.group = "routines"
	// System commands change the phones themselves; the destructive ones carry ⚠ in their help.
	system := func(c *tuiCommand) *tuiCommand {
		c.group = "system"
		return c
	}
	orDefault := func(action, def string) func(call) ([]core.Spec, error) {
		return func(c call) ([]core.Spec, error) {
			args := c.args
			if len(args) == 0 {
				args = []string{def}
			}
			return []core.Spec{{Action: action, Args: args}}, nil
		}
	}
	aka := func(c *tuiCommand, aliases ...string) *tuiCommand {
		c.aliases = aliases
		return c
	}
	more := []*tuiCommand{
		aka(act("uninstall", "/uninstall PACKAGE", app.Service.L("⚠ Desinstalar una aplicación", "⚠ Uninstall an app"), 1, 1, one("apk-uninstall")), "desinstalar"),
		aka(act("wipe", "/wipe PACKAGE", app.Service.L("⚠ Borrar los datos de una aplicación", "⚠ Clear the data of an app"), 1, 1, one("app-clear")), "borrar-app", "clear-data"),
		aka(act("pull", "/pull ANDROID_PATH", app.Service.L("Traer un archivo del móvil a output/downloads", "Bring a file from the phone to output/downloads"), 1, 1, one("pull")), "traer", "descargar"),
		aka(system(act("reboot", "/reboot [system|recovery|bootloader]", app.Service.L("⚠ Reiniciar las placas", "⚠ Reboot the devices"), 0, 1, orDefault("reboot", "system"))), "reiniciar"),
		system(act("wifi", "/wifi on|off", app.Service.L("Encender o apagar el Wi-Fi", "Turn Wi-Fi on or off"), 1, 1, one("wifi"))),
		aka(system(act("rotation", "/rotation auto|0|90|180|270", app.Service.L("Girar con el sensor o fijar la orientación", "Rotate with the sensor or lock the orientation"), 1, 1, one("rotation"))), "rotar"),
		aka(system(act("resolution", "/resolution [WIDTHxHEIGHT|reset] [DPI]", app.Service.L("Ver o fijar la resolución de pantalla", "Show or set the screen resolution"), 0, 2, func(c call) ([]core.Spec, error) {
			s, e := app.Service.ResolutionSpec(c.args)
			return []core.Spec{s}, e
		})), "resolucion", "resolución"),
		aka(system(act("android-setting", "/android-setting system|secure|global KEY VALUE", app.Service.L("Cambiar un ajuste de Android", "Change an Android setting"), 3, 3, one("setting"))), "ajuste", "ajuste-android"),
		aka(system(act("perm", "/perm grant|revoke PACKAGE PERMISSION", app.Service.L("Conceder o retirar un permiso a una app", "Grant or revoke an app permission"), 3, 3, func(c call) ([]core.Spec, error) {
			sub := strings.ToLower(c.args[0])
			if sub != "grant" && sub != "revoke" {
				return nil, errors.New(app.Service.L("Usa /perm grant o /perm revoke", "Use /perm grant or /perm revoke"))
			}
			return []core.Spec{{Action: "perm-" + sub, Args: c.args[1:]}}, nil
		})), "permiso", "permisos"),
		aka(system(act("logcat", "/logcat [LINES]", app.Service.L("Guardar el registro del sistema en output/logcat", "Save the system log to output/logcat"), 0, 1, orDefault("logcat", "500"))), "registro"),
		aka(system(act("record", "/record SECONDS", app.Service.L("Grabar un vídeo de la pantalla", "Record a video of the screen"), 1, 1, one("screenrecord"))), "grabar"),
	}
	cmds := []*tuiCommand{{name: "reset", aliases: []string{"borrar-datos"}, usage: "/reset", help: app.Service.L("Borrar todos los datos de uso; conservar ajustes y rutinas", "Delete all usage data; keep settings and routines"), group: "session", run: (*tuiModel).cmdReset}, {name: "settings", usage: "/settings", help: app.Service.L("Ajustes de la app", "Application settings"), group: "session", max: 0, run: (*tuiModel).cmdSettings}, {name: "devices", aliases: []string{"dispositivos", "placas"}, usage: "/devices", help: app.Service.L("Selector de placas: marcar, fijar destino, agrupar, renombrar", "Device picker: mark, set target, group, rename"), group: "inventory", run: (*tuiModel).cmdDevices}, {name: "doctor", usage: "/doctor", help: app.Service.L("Comprobar ADB, scrcpy, clave CXT y conexiones", "Check ADB, scrcpy, the CXT key and connections"), group: "inventory", run: (*tuiModel).cmdDoctor}, {name: "rename", usage: "/rename ALIAS_OR_SERIAL NEW_ALIAS", help: app.Service.L("Cambiar el alias de una placa", "Change a device alias"), group: "inventory", min: 2, max: 2, run: (*tuiModel).cmdRename}, {name: "group", aliases: []string{"groups", "grupo", "grupos"}, usage: "/group [list | create NAME @target | set NAME @target | rename NAME NEW_NAME | delete NAME]", help: app.Service.L("Grupos persistentes de placas", "Saved groups of devices"), group: "inventory", sel: true, max: 3, run: (*tuiModel).cmdGroup}, apk, act("apps", "/apps", app.Service.L("Listar aplicaciones instaladas", "List installed apps"), 0, 0, one("app-list")), act("start", "/start PACKAGE", app.Service.L("Abrir una aplicación", "Open an app"), 1, 1, one("app-start")), act("stop", "/stop PACKAGE", app.Service.L("Detener una aplicación", "Stop an app"), 1, 1, one("app-stop")), act("url", "/url URL", app.Service.L("Abrir una URL", "Open a URL"), 1, 1, one("url")), push, shell, {name: "motion", usage: "/motion tap|swipe|key|text", help: app.Service.L("Interactuar con el móvil: gestos, teclas y texto", "Interact with the phone: gestures, keys and text"), group: "actions", sel: true, min: 1, max: 1, children: []*tuiCommand{act("tap", "/tap X Y", app.Service.L("Toque en coordenadas normalizadas 0..1", "Tap at normalized coordinates 0..1"), 2, 2, coord("tap")), act("swipe", "/swipe X Y X2 Y2 [ms]", app.Service.L("Deslizamiento normalizado", "Normalized swipe"), 4, 5, coord("swipe")), key, text}}, act("screenshot", "/screenshot", app.Service.L("Guardar capturas en output/screenshots", "Save screenshots to output/screenshots"), 0, 0, func(call) ([]core.Spec, error) {
		return []core.Spec{{Action: "screenshot", Args: []string{}}}, nil
	}), act("wait", "/wait MILLISECONDS", app.Service.L("Esperar", "Wait"), 1, 1, one("wait")), routine, {name: "jobs", aliases: []string{"job", "lotes"}, usage: "/jobs [ID]", help: app.Service.L("Lotes recientes o detalle de un lote", "Recent batches or details of a batch"), group: "routines", max: 1, run: (*tuiModel).cmdJobs}, {name: "retry", usage: "/retry [ID]", help: app.Service.L("Reintentar las placas fallidas (por defecto, el último lote)", "Retry the failed devices (by default, of the last batch)"), group: "routines", max: 1, run: (*tuiModel).cmdRetry}, {name: "view", aliases: []string{"ver", "pantallas", "screens"}, usage: "/view @target [--read-only] | close", help: app.Service.L("Pantallas en cuadrícula: maneja cada móvil desde su ventana", "Screens in a grid: control each device from its window"), group: "screens", sel: true, max: 1, run: (*tuiModel).cmdView}, {name: "sync", aliases: []string{"replica"}, usage: "/sync start MASTER FOLLOWERS… | @target [--views master] · pause · resume · stop · status [ID]", help: app.Service.L("Réplica: controlas una placa y las demás la imitan", "Replica: you control one device and the others copy it"), group: "screens", sel: true, max: -1, run: (*tuiModel).cmdSync}, {name: "copy", aliases: []string{"copiar"}, usage: "/copy", help: app.Service.L("Copiar el último resultado al portapapeles", "Copy the last result to the clipboard"), group: "session", run: (*tuiModel).cmdCopy}, {name: "clear", aliases: []string{"cls", "limpiar"}, usage: "/clear", help: app.Service.L("Limpiar el historial de la pantalla", "Clear the results on screen"), group: "session", run: func(m *tuiModel, _ call) tea.Cmd {
		return m.clearTranscript()
	}}, {name: "help", aliases: []string{"ayuda", "?"}, usage: "/help [COMMAND [ACTION]]", help: app.Service.L("Todos los comandos con un ejemplo", "Every command with an example"), group: "help", max: 2, run: (*tuiModel).cmdHelp}, {name: "exit", aliases: []string{"quit", "salir", "q"}, usage: "/exit", help: app.Service.L("Salir (cierra lotes y réplicas)", "Quit (closes batches and replicas)"), group: "session", run: func(m *tuiModel, _ call) tea.Cmd {
		return m.quit()
	}}}
	cmds = append(cmds, &tuiCommand{name: "app", usage: "/app install FILE | uninstall ID", help: app.Service.L("Instalar/desinstalar aplicaciones Android o iPhone", "Install/uninstall Android or iPhone applications"), group: "actions", children: []*tuiCommand{act("install", "/install FILE", app.Service.L("Instalar APK o IPA firmado", "Install an APK or signed IPA"), 1, 1, one("app-install")), act("uninstall", "/uninstall ID", app.Service.L("Desinstalar aplicación", "Uninstall an application"), 1, 1, one("app-uninstall"))}})
	cmds = append(cmds, more...)
	examples, notes := app.commandExamples(), app.commandNotes()
	for _, c := range cmds {
		for _, child := range c.children {
			old := child.name
			child.name = c.name + " " + old
			child.usage = strings.Replace(child.usage, "/"+old, "/"+child.name, 1)
			child.example = strings.Replace(examples[old], "/"+old, "/"+child.name, 1)
			child.notes = notes[old]
		}
		c.example, c.notes = examples[c.name], notes[c.name]
	}
	sort.SliceStable(cmds, func(i, j int) bool {
		return slices.Index(app.commandGroups, cmds[i].group) < slices.Index(app.commandGroups, cmds[j].group)
	})
	return cmds
}
func (c *tuiCommand) usageText() string {
	if c.usage != "" {
		return c.usage
	}
	return "/" + c.name
}

// parse turns a line into a command call; @tokens become the destination and --k v pairs become flags.
func (m *tuiModel) parse(line string) (call, error) {
	c := call{line: line, args: []string{}, flags: map[string]string{}}
	if strings.HasPrefix(line, "!") {
		line = "/shell " + strings.TrimPrefix(line, "!")
	}
	toks := m.app.commandTokens(m.app.tokenize(line))
	if len(toks) == 0 {
		return c, errors.New(m.app.Service.L("Línea vacía", "Empty line"))
	}
	c.cmd = m.app.lookupCommand(toks[0].text)
	if c.cmd == nil {
		return c, m.app.unknownCommand(toks[0].text)
	}
	if len(c.cmd.children) > 0 && len(toks) > 1 && !strings.HasPrefix(toks[1].text, "@") {
		c.cmd = nil
		return c, fmt.Errorf(m.app.Service.L("Acción desconocida: %s · usa /motion tap|swipe|key|text", "Unknown action: %s · use /motion tap|swipe|key|text"), toks[1].text)
	}
	var mentions []string
	var spans [][2]int
	for i := 1; i < len(toks); i++ {
		t := toks[i]
		switch {
		case !t.quoted && len(t.text) > 1 && t.text[0] == '@' && (!c.cmd.raw || m.resolvable(t.text[1:])):
			mentions = append(mentions, t.text[1:])
			spans = append(spans, [2]int{t.start, t.end})
		case !c.cmd.raw && !t.quoted && strings.HasPrefix(t.text, "--") && len(t.text) > 2:
			k, v, ok := strings.Cut(t.text[2:], "=")
			if k == "parallel" || k == "timeout" {
				return c, errors.New(m.app.Service.L("Usa /settings para este ajuste", "Use /settings for this setting"))
			}
			if k == "dry-run" {
				return c, errors.New(m.app.Service.L("La opción --dry-run se ha retirado", "The --dry-run option has been removed"))
			}
			if !ok && i+1 < len(toks) && !strings.HasPrefix(toks[i+1].text, "@") && !strings.HasPrefix(toks[i+1].text, "--") && !m.app.boolFlags[k] {
				i++
				v = toks[i].text
			}
			c.flags[k] = v
		default:
			c.args = append(c.args, t.text)
		}
	}
	if c.cmd.raw {
		var b strings.Builder
		last := toks[0].end
		for _, sp := range spans {
			b.WriteString(line[last:sp[0]])
			last = sp[1]
		}
		b.WriteString(line[last:])
		c.raw = strings.TrimSpace(b.String())
		if t := m.app.tokenize(c.raw); len(t) == 1 && t[0].quoted && t[0].end == len(c.raw) {
			c.raw = t[0].text
		}
		c.args = []string{}
		if c.raw != "" {
			c.args = []string{c.raw}
		}
	}
	if len(mentions) > 0 && !c.cmd.sel {
		return c, fmt.Errorf(m.app.Service.L("/%s no admite destino", "/%s takes no target"), c.cmd.name)
	}
	sel, e := m.selection(mentions)
	if e != nil {
		return c, e
	}
	c.sel = sel
	if len(c.args) < c.cmd.min || (c.cmd.max >= 0 && len(c.args) > c.cmd.max) {
		if c.cmd.example != "" {
			return c, usageError{fmt.Sprintf(m.app.Service.L("Uso: %s · ejemplo: %s", "Usage: %s · example: %s"), c.cmd.usageText(), c.cmd.example)}
		}
		return c, usageError{m.app.Service.L("Uso: ", "Usage: ") + c.cmd.usageText()}
	}
	return c, nil
}
func (app *Interface) unknownCommand(name string) error {
	if app.retiredSetting(name) {
		return fmt.Errorf(app.Service.L("Usa /settings para configurar %s", "Use /settings to configure %s"), name)
	}
	name = strings.ToLower(strings.TrimPrefix(name, "/"))
	if slices.Contains([]string{"tap", "swipe", "key", "text"}, name) {
		return fmt.Errorf(app.Service.L("Usa /motion %s", "Use /motion %s"), name)
	}
	best, score := "", 3
	for _, c := range app.allTUICommands(app.tuiRegistry) {
		for _, n := range append([]string{c.name}, c.aliases...) {
			if d := app.levenshtein(name, strings.TrimPrefix(n, "motion ")); d < score || (d == score && strings.HasPrefix(c.name, "motion ")) {
				best, score = c.name, d
			}
		}
	}
	if best != "" {
		return fmt.Errorf(app.Service.L("Comando desconocido: %s · ¿quisiste decir /%s?", "Unknown command: %s · did you mean /%s?"), name, best)
	}
	return fmt.Errorf(app.Service.L("Comando desconocido: %s · /help muestra la lista", "Unknown command: %s · /help shows the list"), name)
}
func (app *Interface) levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}
func (m *tuiModel) findDevice(id string) (core.Device, bool) {
	id = strings.TrimSpace(id)
	for _, d := range m.devices {
		if d.Serial == id || (d.Alias != "" && strings.EqualFold(d.Alias, id)) {
			return d, true
		}
	}
	return core.Device{}, false
}
func (m *tuiModel) resolvable(x string) bool {
	if strings.EqualFold(x, "all") {
		return true
	}
	if _, ok := m.groups[x]; ok {
		return true
	}
	for _, p := range strings.Split(x, ",") {
		if _, ok := m.findDevice(p); !ok {
			return false
		}
	}
	return true
}
func (m *tuiModel) selection(mentions []string) (core.Selection, error) {
	var devs []string
	for _, x := range mentions {
		_, isGroup := m.groups[x]
		switch {
		case strings.EqualFold(x, "all"):
			if len(mentions) > 1 {
				return core.Selection{}, errors.New(m.app.Service.L("@all no se combina con otros destinos", "@all cannot be combined with other targets"))
			}
			return core.Selection{All: true}, nil
		case isGroup:
			if len(mentions) > 1 {
				return core.Selection{}, errors.New(m.app.Service.L("Usa un único @grupo, o una lista de placas", "Use a single @group, or a list of devices"))
			}
			return core.Selection{Group: x}, nil
		}
		for _, p := range strings.Split(x, ",") {
			if p = strings.TrimSpace(p); p != "" {
				devs = append(devs, p)
			}
		}
	}
	return core.Selection{Devices: strings.Join(devs, ",")}, nil
}

// runOnTargets resolves the destination against a fresh inventory and hands the batch to the session mode.
func (m *tuiModel) runOnTargets(c call, title string, steps []core.Spec) tea.Cmd {
	sel := c.sel
	if sel.Empty() {
		sel = m.target
	}
	if sel.Empty() {
		m.printErr(m.app.Service.L("Indica un destino: @grupo, @DEV-001,DEV-002 o @all · /settings lo fija para la sesión", "Give a target: @group, @DEV-001,DEV-002 or @all · /settings sets one for the session"))
		return nil
	}
	if m.task != nil {
		m.printErr(m.app.Service.L("Hay un lote en curso; espera a que termine o pulsa esc para cancelarlo", "A batch is running; wait for it or press esc to cancel it"))
		return nil
	}
	if e := m.app.Service.PrepareBatch(steps, core.BatchOptions{Parallel: m.parallel, Timeout: m.timeout}); e != nil {
		m.printErr(e)
		return nil
	}
	return m.async(m.app.Service.L("Buscando dispositivos", "Looking for devices"), func() func(*tuiModel) tea.Cmd {
		d, e := m.app.tuiDiscover()
		var t []core.Device
		if e == nil {
			t, e = m.app.Service.ResolveTargets(d, sel)
		}
		return func(m *tuiModel) tea.Cmd {
			if d != nil {
				m.setDevices(d)
			}
			if e != nil {
				m.printErr(e)
				return nil
			}
			return m.dispatchBatch(title, sel.String(), t, steps)
		}
	})
}
func (m *tuiModel) cmdDevices(call) tea.Cmd {
	return m.async(m.app.Service.L("Actualizando inventario", "Refreshing the inventory"), func() func(*tuiModel) tea.Cmd {
		d, e := m.app.tuiDiscover()
		return func(m *tuiModel) tea.Cmd {
			if e != nil {
				m.printErr(e)
				return nil
			}
			m.setDevices(d)
			if len(d) == 0 {
				m.printInfo(m.app.Service.L("No hay placas en el inventario. Conecta alguna por USB y vuelve a probar.", "There are no devices in the inventory. Connect one over USB and try again."))
				return nil
			}
			m.panel = &devicesPanel{app: m.app, marked: map[string]bool{}}
			return m.healthCmd()
		}
	})
}
func (m *tuiModel) cmdDoctor(call) tea.Cmd {
	parallel := m.parallel
	return m.async(m.app.Service.L("Comprobando herramientas", "Checking tools"), func() func(*tuiModel) tea.Cmd {
		ver, verErr := m.app.Service.Command(m.app.Service.Ctx, "version")
		backends := m.app.Service.BackendDiagnostics(m.app.Service.Ctx)
		d, e := m.app.tuiDiscover()
		var health map[string]core.Health
		if e == nil {
			health = m.app.tuiHealth(d, parallel)
		}
		return func(m *tuiModel) tea.Cmd {
			if d != nil {
				m.setDevices(d)
			}
			if health != nil {
				m.health = health
			}
			m.print(m.renderDoctor(ver, verErr, d, e))
			for _, platform := range []string{"android", "ios"} {
				m.printInfo(platform + ": " + backends[platform])
			}
			if summary := m.app.Service.IOSSetupSummary(d); summary != "" {
				for _, line := range strings.Split(summary, "\n") {
					m.printInfo("iPhone " + line)
				}
			}
			return nil
		}
	})
}
func (m *tuiModel) cmdRename(c call) tea.Cmd {
	from, to := c.args[0], c.args[1]
	return m.async(m.app.Service.L("Renombrando", "Renaming"), func() func(*tuiModel) tea.Cmd {
		v, e := m.app.Service.RenameDevice(from, to)
		return func(m *tuiModel) tea.Cmd {
			if e != nil {
				m.printErr(e)
				return nil
			}
			m.print(m.app.tree(m.app.head(m.app.stOK, glyphOK, m.app.Service.L("Alias cambiado", "Alias changed")), fmt.Sprintf(m.app.Service.L("%s ahora es %s", "%s is now %s"), m.app.stDim.Render(v.Serial), m.app.stBold.Render(v.Alias))))
			return m.refreshCmd()
		}
	})
}
func (m *tuiModel) cmdGroup(c call) tea.Cmd {
	sub := "list"
	if len(c.args) > 0 {
		sub = strings.ToLower(c.args[0])
	}
	name := ""
	if len(c.args) > 1 {
		name = c.args[1]
	}
	m.reloadGroups()
	switch sub {
	case "list", "ls":
		m.print(m.renderGroups())
		return nil
	case "delete", "rm", "borrar":
		if name == "" {
			m.printErr(m.app.Service.L("Uso: ", "Usage: ") + "/group delete NAME")
			return nil
		}
		if _, ok := m.groups[name]; !ok {
			m.printErr(m.app.Service.L("Grupo desconocido: ", "Unknown group: ") + name)
			return nil
		}
		if e := m.app.Service.DeleteGroup(name); e != nil {
			m.printErr(e)
			return nil
		}
		m.reloadGroups()
		if m.target.Group == name {
			m.target = core.Selection{}
		}
		m.print(m.app.tree(m.app.head(m.app.stOK, glyphOK, m.app.Service.L("Grupo borrado", "Group deleted")), "@"+name))
		return nil
	case "rename", "renombrar", "mv":
		if name == "" || len(c.args) < 3 {
			m.printErr(m.app.Service.L("Uso: ", "Usage: ") + "/group rename NAME NEW_NAME")
			return nil
		}
		to := strings.TrimPrefix(c.args[2], "@")
		if e := m.app.Service.RenameGroup(name, to); e != nil {
			m.printErr(fmt.Errorf("@%s: %w", name, e))
			return nil
		}
		m.reloadGroups()
		if m.target.Group == name {
			m.target = core.Selection{Group: to}
		}
		m.print(m.app.tree(m.app.head(m.app.stOK, glyphOK, m.app.Service.L("Grupo renombrado", "Group renamed")), "@"+name+" → @"+to))
		return nil
	case "create", "new", "crear", "set":
		if name == "" {
			m.printErr(m.app.Service.L("Uso: ", "Usage: ") + "/group " + sub + " NAME @target")
			return nil
		}
		sel := c.sel
		if sel.Empty() {
			sel = m.target
		}
		if sel.Empty() {
			m.printErr(m.app.Service.L("Indica las placas del grupo: ", "Give the devices of the group: ") + "/group " + sub + " " + name + " @DEV-001,DEV-002")
			return nil
		}
		create := sub != "set"
		return m.async(m.app.Service.L("Buscando dispositivos", "Looking for devices"), func() func(*tuiModel) tea.Cmd {
			d, e := m.app.tuiDiscover()
			var t []core.Device
			if e == nil {
				t, e = m.app.Service.ResolveTargets(d, sel)
			}
			return func(m *tuiModel) tea.Cmd {
				if e != nil {
					m.printErr(e)
					return nil
				}
				ids := []string{}
				for _, v := range t {
					ids = append(ids, v.Serial)
				}
				verb := m.app.Service.L("Crear grupo", "Create group")
				if !create {
					verb = m.app.Service.L("Sustituir miembros del grupo", "Replace group members")
				}
				if create {
					e = m.app.Service.CreateGroup(name, ids)
				} else {
					_, e = m.app.Service.SetGroup(name, ids)
				}
				if e != nil {
					m.printErr(e)
					return nil
				}
				m.reloadGroups()
				m.print(m.app.tree(m.app.head(m.app.stOK, glyphOK, verb), fmt.Sprintf("@%s · %s: %s", name, m.app.plural(len(t), m.app.Service.L("placa", "device"), m.app.Service.L("placas", "devices")), m.app.aliasList(t, 10)), m.app.stDim.Render(m.app.Service.L("Elige este grupo como destino en /settings", "Choose this group as the target in /settings"))))
				return nil
			}
		})
	}
	m.printErr(m.app.Service.L("Uso: ", "Usage: ") + c.cmd.usageText())
	return nil
}
func (m *tuiModel) cmdHelp(c call) tea.Cmd {
	if len(c.args) > 0 {
		name := strings.Join(c.args, " ")
		x := m.app.lookupCommand(name)
		if x == nil {
			m.printErr(m.app.unknownCommand(name))
			return nil
		}
		body := []string{x.help, "", m.app.stDim.Render(m.app.pad(m.app.Service.L("Uso", "Usage"), 9)) + x.usageText()}
		for _, child := range x.children {
			body = append(body, child.usageText()+" · "+child.help)
		}
		if x.example != "" {
			body = append(body, m.app.stDim.Render(m.app.pad(m.app.Service.L("Ejemplo", "Example"), 9))+m.app.stAccent.Render(x.example))
		}
		if x.notes != "" {
			body = append(body, "", m.app.stDim.Render(x.notes))
		}
		if x.target {
			body = append(body, m.app.stDim.Render(m.app.Service.L("Necesita destino: @all, @grupo o @DEV-001 al final, o uno fijo con /settings.", "Needs a target: @all, @group or @DEV-001 at the end, or a fixed one with /settings.")))
		}
		m.print(m.app.tree(m.app.head(m.app.stAccent, glyphInfo, "/"+x.name), body...))
		return nil
	}
	m.print(m.renderHelp())
	return nil
}

type usageError struct{ msg string }

func (e usageError) Error() string {
	return e.msg
}
func (app *Interface) isUsageError(e error) bool {
	_, ok := e.(usageError)
	return ok
}

// cmdCopy copies the latest result block as plain text; a batch summary is copied with its full output.
func (m *tuiModel) cmdCopy(call) tea.Cmd {
	for i := len(m.transcript) - 1; i >= 0; i-- {
		block := m.transcript[i]
		if strings.HasPrefix(ansi.Strip(block), "\n"+glyphInfo+" ") {
			continue
		}
		if block == m.lastShort && m.lastFull != "" {
			block = m.lastFull
		}
		text := strings.Trim(ansi.Strip(block), "\n")
		m.copyText(text, fmt.Sprintf(m.app.Service.L("Resultado copiado (%s)", "Result copied (%s)"), m.app.plural(strings.Count(text, "\n")+1, m.app.Service.L("línea", "line"), m.app.Service.L("líneas", "lines"))))
		return nil
	}
	m.printInfo(m.app.Service.L("Todavía no hay resultados que copiar", "There are no results to copy yet"))
	return nil
}
func (m *tuiModel) cmdJobs(c call) tea.Cmd {
	if len(c.args) == 1 {
		return m.cmdJob(c)
	}
	j, e := m.app.Service.ListJobs(50)
	if e != nil {
		m.printErr(e)
		return nil
	}
	if len(j) == 0 {
		m.printInfo(m.app.Service.L("Todavía no hay lotes guardados en output/logs/jobs", "There are no saved batches in output/logs/jobs yet"))
		return nil
	}
	m.jobs = j
	m.panel = &jobsPanel{app: m.app}
	return nil
}
func (m *tuiModel) cmdJob(c call) tea.Cmd {
	j, live := m.sessionJobs[c.args[0]]
	if !live {
		if e := m.app.Service.ReadJSON(m.app.Service.JobPath(c.args[0]), &j); e != nil {
			m.printErr(m.app.Service.L("No se encuentra el lote ", "Batch not found: ") + c.args[0])
			return nil
		}
	}
	m.lastShort = m.renderJob(m.app.describeSteps(j.Steps), "", j, 0, false, false)
	m.print(m.lastShort)
	m.lastFull = m.renderJob(m.app.describeSteps(j.Steps), "", j, 0, true, false)
	return nil
}
func (m *tuiModel) cmdRetry(c call) tea.Cmd {
	id := m.lastJob
	if len(c.args) == 1 {
		id = c.args[0]
	}
	if id == "" {
		if j, _ := m.app.Service.ListJobs(1); len(j) > 0 {
			id = j[0].ID
		}
	}
	if id == "" {
		m.printErr(m.app.Service.L("No hay lotes que reintentar", "There are no batches to retry"))
		return nil
	}
	if m.task != nil {
		m.printErr(m.app.Service.L("Hay un lote en curso; espera a que termine o pulsa esc para cancelarlo", "A batch is running; wait for it or press esc to cancel it"))
		return nil
	}
	j, ok := m.sessionJobs[id]
	if !ok {
		m.printErr(m.app.Service.L("Este lote ya no est\u00e1 en memoria; repite la orden original.", "This batch is no longer in memory; repeat the original command."))
		return nil
	}
	return m.async(m.app.Service.L("Buscando dispositivos", "Looking for devices"), func() func(*tuiModel) tea.Cmd {
		d, e := m.app.tuiDiscover()
		return func(m *tuiModel) tea.Cmd {
			if e != nil {
				m.printErr(e)
				return nil
			}
			m.setDevices(d)
			var target []core.Device
			for _, r := range j.Results {
				if !r.OK {
					for _, v := range d {
						if v.Serial == r.Serial {
							target = append(target, v)
						}
					}
				}
			}
			if len(target) == 0 {
				m.printInfo(m.app.Service.L("No hay dispositivos fallidos para reintentar en ", "No failed devices to retry in ") + m.app.shortID(id))
				return nil
			}
			if e := m.app.Service.PrepareBatch(j.Steps, core.BatchOptions{Parallel: m.parallel, Timeout: m.timeout}); e != nil {
				m.printErr(e)
				return nil
			}
			return m.dispatchBatch(m.app.Service.L("Reintento · ", "Retry · ")+m.app.describeSteps(j.Steps), m.app.Service.L("fallidas de ", "failed in ")+m.app.shortID(id), target, j.Steps)
		}
	})
}

// cmdView opens read-only screens of the destination, tiled in a grid; nothing is sent to the devices.
func (m *tuiModel) cmdView(c call) tea.Cmd {
	if len(c.args) == 1 {
		switch strings.ToLower(c.args[0]) {
		case "cerrar", "close", "stop", "off":
		default:
			m.printErr(m.app.Service.L("Uso: ", "Usage: ") + c.cmd.usageText())
			return nil
		}
		if m.wall == nil {
			m.printInfo(m.app.Service.L("No hay pantallas abiertas", "There are no open screens"))
			return nil
		}
		w := m.wall
		return m.async(m.app.Service.L("Cerrando pantallas", "Closing screens"), func() func(*tuiModel) tea.Cmd {
			w.Close()
			return func(*tuiModel) tea.Cmd {
				return nil
			}
		})
	}
	if m.wall != nil {
		m.printErr(fmt.Sprintf(m.app.Service.L("Ya hay %s abiertas · /view close las cierra", "%s already open · /view close closes them"), m.app.plural(m.wall.Alive(), m.app.Service.L("pantalla", "screen"), m.app.Service.L("pantallas", "screens"))))
		return nil
	}
	_, readOnly := c.flags["read-only"]
	if _, old := c.flags["solo-ver"]; old {
		readOnly = true
	}
	sel := c.sel
	if sel.Empty() {
		sel = m.target
	}
	if sel.Empty() {
		m.printErr(m.app.Service.L("Indica qué placas ver: /view @all, /view @grupo o /view @DEV-001,DEV-002", "Say which devices to view: /view @all, /view @group or /view @DEV-001,DEV-002"))
		return nil
	}
	return m.async(m.app.Service.L("Buscando dispositivos", "Looking for devices"), func() func(*tuiModel) tea.Cmd {
		d, e := m.app.tuiDiscover()
		var t []core.Device
		if e == nil {
			t, e = m.app.Service.ResolveTargets(d, sel)
		}
		return func(m *tuiModel) tea.Cmd {
			if e != nil {
				m.printErr(e)
				return nil
			}
			return m.async(m.app.Service.L("Abriendo pantallas", "Opening screens"), func() func(*tuiModel) tea.Cmd {
				w, e := m.app.tuiOpenWall(m.app.Service.Ctx, t, readOnly)
				return func(m *tuiModel) tea.Cmd {
					if e != nil {
						m.printErr(e)
						return nil
					}

					m.wall = w
					body := []string{fmt.Sprintf(m.app.Service.L("%s de %d×%d px en %d columnas: %s", "%s of %d×%d px in %d columns: %s"), m.app.plural(len(w.Devices), m.app.Service.L("ventana", "window"), m.app.Service.L("ventanas", "windows")), w.Layout[0].W, w.Layout[0].H, w.Cols, m.app.aliasList(w.Devices, 10))}
					if off := m.app.notConnected(t); len(off) > 0 {
						body = append(body, m.app.stWarn.Render(m.app.Service.L("! No disponibles, no se muestran: ", "! Unavailable, not shown: ")+m.app.aliasList(off, 8)))
					}
					if w.ReadOnly {
						body = append(body, m.app.stDim.Render(m.app.Service.L("Sólo vista: no se envía nada a las placas · /view close las cierra", "Watch only: nothing is sent to the devices · /view close closes them")))
					} else {
						body = append(body, m.app.Service.L("Haz clic en cualquier ventana para manejar ese móvil. Arrastra una por su título para mover el bloque entero.", "Click any window to control that device. Drag one by its title to move the whole block."), m.app.stDim.Render(m.app.Service.L("/view close las cierra · /view @all --read-only abre sólo para mirar", "/view close closes them · /view @all --read-only opens them to watch only")))
					}
					m.print(m.app.tree(m.app.head(m.app.stOK, glyphOK, m.app.Service.L("Pantallas abiertas", "Screens open")), body...))
					send := m.send
					go func() {
						<-w.Done()
						send(wallEndMsg{w})
					}()
					return nil
				}
			})
		}
	})
}
func (m *tuiModel) cmdSync(c call) tea.Cmd {
	sub := ""
	if len(c.args) > 0 {
		sub = strings.ToLower(c.args[0])
	}
	arg := ""
	if len(c.args) > 1 {
		arg = c.args[1]
	}
	live := m.sync != nil && m.sync.s != nil
	switch sub {
	case "":
		if m.sync == nil {
			m.printInfo(m.app.Service.L("No hay réplica activa. Uso: ", "No live replica. Usage: ") + c.cmd.usageText())
			return nil
		}
		m.panel = &syncPanel{app: m.app}
		return nil
	case "start":
		if m.sync != nil {
			m.printErr(m.app.Service.L("Ya hay una réplica activa; /sync stop la termina", "A replica is already live; /sync stop ends it"))
			return nil
		}
		master := c.flags["master"]
		if master == "" {
			master = arg
		}
		if master == "" {
			m.printErr(m.app.Service.L("Indica la principal: /sync start DEV-001 @pruebas", "Give the master: /sync start DEV-001 @tests"))
			return nil
		}
		views := c.flags["views"]
		if views == "" {
			views = "all"
		}
		if views != "all" && views != "master" {
			m.printErr(m.app.Service.L("--views debe ser all o master", "--views must be all or master"))
			return nil
		}
		sel := c.sel
		if extra := c.args[min(2, len(c.args)):]; len(extra) > 0 {
			if !sel.Empty() {
				m.printErr(m.app.Service.L("Usa placas sueltas o un @destino, no ambos", "Use single devices or a @target, not both"))
				return nil
			}
			sel = core.Selection{Devices: strings.Join(extra, ",")}
		}
		if sel.Empty() {
			sel = m.target
		}
		if sel.Empty() {
			m.printErr(m.app.Service.L("Indica las seguidoras: /sync start ", "Give the followers: /sync start ") + master + m.app.Service.L(" DEV-002 DEV-003, o /sync start ", " DEV-002 DEV-003, or /sync start ") + master + m.app.Service.L(" @grupo", " @group"))
			return nil
		}
		return m.async(m.app.Service.L("Buscando dispositivos", "Looking for devices"), func() func(*tuiModel) tea.Cmd {
			d, e := m.app.tuiDiscover()
			var t []core.Device
			if e == nil {
				t, e = m.app.Service.ResolveTargets(d, sel)
			}
			return func(m *tuiModel) tea.Cmd {
				if e != nil {
					m.printErr(e)
					return nil
				}
				var md core.Device
				for _, v := range d {
					if v.Serial == master || strings.EqualFold(v.Alias, master) {
						md = v
					}
				}
				if md.Serial == "" {
					m.printErr(m.app.Service.L("Placa principal desconocida: ", "Unknown master device: ") + master)
					return nil
				}
				if md.State != "device" {
					m.printErr(fmt.Sprintf(m.app.Service.L("La principal %s no está conectada (%s)", "The master %s is not connected (%s)"), md.Alias, m.app.stateLabel(md.State)))
					return nil
				}
				var followers []core.Device
				for _, v := range t {
					if v.Serial != md.Serial {
						followers = append(followers, v)
					}
				}
				if len(followers) == 0 {
					m.printErr(m.app.Service.L("Faltan seguidoras: indica al menos otra placa además de ", "Followers missing: give at least one device besides ") + md.Alias)
					return nil
				}
				t = append([]core.Device{md}, followers...)
				m.printInfo(m.app.Service.L("Pon todas en la misma pantalla y orientación antes de empezar · /sync para pausar o detener", "Put them all on the same screen and orientation first · /sync to pause or stop"))
				return m.startSync(md, t, views)
			}
		})
	case "pause", "resume", "stop", "status":
		if arg == "" && live {
			s := m.sync.s
			switch sub {
			case "pause":
				s.Pause()
				m.printInfo(m.app.Service.L("Réplica en pausa · /sync resume para continuar", "Replica paused · /sync resume to continue"))
			case "resume":
				s.Resume()
				m.printInfo(m.app.Service.L("Réplica reanudada", "Replica resumed"))
			case "stop":
				s.Stop()
				m.printInfo(m.app.Service.L("Deteniendo réplica…", "Stopping the replica…"))
			case "status":
				m.print(m.renderSyncStatus())
			}
			m.sync.info = s.Info()
			return nil
		}
		if arg == "" {
			ids := m.app.syncSessionIDs()
			if len(ids) != 1 {
				m.printErr(m.app.Service.L("Indica el ID de la sesión: ", "Give the session ID: ") + "/sync " + sub + " ID")
				return nil
			}
			arg = ids[0]
		}
		id := arg
		return m.async(m.app.Service.L("Contactando con la sesión", "Contacting the session"), func() func(*tuiModel) tea.Cmd {
			out, e := m.app.Service.SyncRequest(id, sub)
			return func(m *tuiModel) tea.Cmd {
				if e != nil {
					m.printErr(e)
				} else {
					m.print(m.app.tree(m.app.head(m.app.stAccent, glyphInfo, m.app.Service.L("Sesión ", "Session ")+id), m.app.stDim.Render(strings.TrimSpace(out))))
				}
				return nil
			}
		})
	}
	m.printErr(m.app.Service.L("Uso: ", "Usage: ") + c.cmd.usageText())
	return nil
}
func (app *Interface) syncSessionIDs() []string {
	files, _ := filepath.Glob(app.Service.SessionsPath("*.json"))
	var ids []string
	for _, f := range files {
		ids = append(ids, strings.TrimSuffix(filepath.Base(f), ".json"))
	}
	return ids
}
func (m *tuiModel) sugVisible() bool {
	return !m.sugHidden && len(m.sugs) > 0
}
func (m *tuiModel) prefix() string {
	r := []rune(m.input.Value())
	p := min(m.input.Position(), len(r))
	return string(r[:p])
}
func (m *tuiModel) currentToken() string {
	p := m.prefix()
	if m.sugStart > len(p) {
		return ""
	}
	return p[m.sugStart:]
}
func (m *tuiModel) updateSuggestions() {
	m.sugs = nil
	m.hint = ""
	m.sugFree = false
	if m.form != nil {
		m.formSuggestions()
		return
	}
	prefix := m.prefix()
	if strings.TrimSpace(prefix) == "" {
		return
	}
	toks := m.app.tokenize(prefix)
	cur := token{start: len(prefix), end: len(prefix)}
	if !strings.HasSuffix(prefix, " ") && len(toks) > 0 {
		cur = toks[len(toks)-1]
		toks = toks[:len(toks)-1]
	}
	toks = m.app.commandTokens(toks)
	m.sugStart = cur.start
	if len(toks) == 1 && m.app.lookupCommand(toks[0].text) == m.app.lookupCommand("motion") && cur.text != "" && !strings.HasPrefix(cur.text, "@") {
		m.sugStart = 0
		m.sugs = m.paletteSuggestions("/motion " + cur.text)
		return
	}
	if m.rawSuggestions(prefix, cur) {
		return
	}
	if len(toks) == 0 {
		if c := m.app.lookupCommand(cur.text); c != nil && strings.HasPrefix(cur.text, "/") {
			m.hint = m.commandHint(c, nil, false, false)
		}
	}
	var all []suggestion
	switch {
	case len(toks) == 0 && strings.HasPrefix(cur.text, "/"):
		all = m.paletteSuggestions(cur.text)
	case len(toks) == 0:
		return
	default:
		name := toks[0].text
		if strings.HasPrefix(name, "!") {
			name = "shell"
		}
		c := m.app.lookupCommand(name)
		if c == nil {
			return
		}
		var prior []string
		mention := false
		typedFlags := map[string]bool{}
		flagValue := false
		for _, t := range toks[1:] {
			isMention := !t.quoted && strings.HasPrefix(t.text, "@")
			isFlag := !t.quoted && strings.HasPrefix(t.text, "--")
			switch {
			case flagValue && !isMention && !isFlag:
				flagValue = false
			case isMention:
				mention, flagValue = true, false
			case isFlag:
				k, _, hasValue := strings.Cut(t.text, "=")
				typedFlags[k] = true
				flagValue = !hasValue && !m.app.boolFlags[strings.TrimPrefix(k, "--")]
			default:
				prior = append(prior, t.text)
			}
		}
		var flags []suggestion
		for _, f := range m.app.commandFlags(c.name, prior) {
			if k, _, _ := strings.Cut(f.value, " "); !typedFlags[k] {
				flags = append(flags, f)
			}
		}
		typingMention := !cur.quoted && strings.HasPrefix(cur.text, "@")
		if c.raw {
			prior = nil
			if len(toks) > 1 || cur.text != "" {
				prior = []string{"…"}
			}
		}
		m.hint = m.commandHint(c, prior, mention, typingMention)
		params, dest := m.app.commandParams(m, c.name, prior)
		switch {
		case typingMention:
			all = m.mentionSuggestions(cur.text)
		case !cur.quoted && strings.HasPrefix(cur.text, "--"):
			all = m.app.filterSuggestions(flags, cur.text)
		case c.raw:
		case len(prior) < len(params):
			p := params[len(prior)]
			m.sugFree = p.freeForm() || (c.name == "jobs" && cur.text == "")
			list := m.paramSuggestions(p, prior, cur.text, false)
			if p.optional && dest != nil && !mention {
				list = append(list, m.destSuggestions(false)...)
			}
			all = m.app.filterSuggestions(list, cur.text)
		case dest != nil && !mention:
			all = m.app.filterSuggestions(append(m.destSuggestions(false), flags...), cur.text)
		default:
			all = m.app.filterSuggestions(flags, cur.text)
		}
	}
	if len(all) == 1 && all[0].value == cur.text {
		all = nil
	}
	if len(all) > 200 {
		all = all[:200]
	}
	m.sugs = all
	if m.sugIdx >= len(all) {
		m.sugIdx = 0
	}
}

// filterSuggestions keeps prefix matches, falling back to substring matches, ignoring case and quotes.
// Accents and case are ignored, and from three letters on it also searches the descriptions,
// so "/captura" finds /screenshot and "chrome" finds com.android.chrome.
func (app *Interface) filterSuggestions(all []suggestion, cur string) []suggestion {
	c := app.normalize(strings.Trim(cur, `"'`))
	q := strings.TrimLeft(c, "/@!")
	var pre, sub, meaning []suggestion
	for _, s := range all {
		v := app.normalize(strings.Trim(s.value, `"'`))
		switch {
		case strings.HasPrefix(v, c):
			pre = append(pre, s)
		case q != "" && strings.Contains(v, q):
			sub = append(sub, s)
		case len([]rune(q)) >= 3 && strings.Contains(app.normalize(s.label+" "+s.detail+" "+s.keywords), q):
			meaning = append(meaning, s)
		}
	}
	switch {
	case len(pre) > 0:
		return pre
	case len(sub) > 0:
		return sub
	}
	return meaning
}

// rawSuggestions handles free-text commands (/shell, /motion text and "!"): the rest of the line is a single
// answer, so ideas are matched against all of it. It returns false for any other kind of line.
func (m *tuiModel) rawSuggestions(prefix string, cur token) bool {
	if !cur.quoted && strings.HasPrefix(cur.text, "@") {
		return false
	}
	var c *tuiCommand
	start := 0
	if strings.HasPrefix(prefix, "!") {
		c, start = m.app.lookupCommand("shell"), 1
	} else {
		toks := m.app.commandTokens(m.app.tokenize(prefix))
		if len(toks) == 0 {
			return false
		}
		c = m.app.lookupCommand(toks[0].text)
		if c == nil || !c.raw || len(prefix) <= toks[0].end {
			return false
		}
		start = toks[0].end
		for start < len(prefix) && prefix[start] == ' ' {
			start++
		}
	}
	rest := prefix[start:]
	var prior []string
	if strings.TrimSpace(rest) != "" {
		prior = []string{"…"}
	}
	m.hint = m.commandHint(c, prior, false, false)
	params, _ := m.app.commandParams(m, c.name, nil)
	m.sugStart, m.sugFree = start, true
	var all []suggestion
	if len(params) > 0 {
		all = m.app.filterSuggestions(m.paramSuggestions(params[0], nil, rest, false), rest)
	}
	if len(all) == 1 && all[0].value == rest {
		all = nil
	}
	m.sugs = all
	if m.sugIdx >= len(all) {
		m.sugIdx = 0
	}
	return true
}

// formSuggestions lists the answers for the assistant's current step; the whole input is the answer.
func (m *tuiModel) formSuggestions() {
	m.sugStart = 0
	cur := m.input.Value()
	var all []suggestion
	p, dest := m.formStep()
	m.sugFree = p != nil && p.freeForm()
	switch {
	case p != nil:
		all = m.paramSuggestions(*p, nil, cur, true)
	case dest != nil:
		all = m.destSuggestions(true)
	}
	all = m.app.filterSuggestions(all, cur)
	if len(all) == 1 && all[0].value == cur {
		all = nil
	}
	m.sugs = all
	if m.sugIdx >= len(all) {
		m.sugIdx = 0
	}
}
func (m *tuiModel) applySuggestion(s suggestion) {
	if s.pick {
		start := m.sugStart
		m.openPicker(func(sel core.Selection) tea.Cmd {
			v := m.input.Value()
			start = min(start, len(v))
			m.input.SetValue(v[:start] + sel.String() + " " + strings.TrimLeft(v[start:], " "))
			m.input.SetCursor(len([]rune(v[:start] + sel.String() + " ")))
			m.updateSuggestions()
			return nil
		})
		return
	}
	prefix := m.prefix()
	full := m.input.Value()
	suffix := full[len(prefix):]
	head := prefix[:min(m.sugStart, len(prefix))] + s.value
	if !s.noSpace && !strings.HasPrefix(suffix, " ") {
		head += " "
	}
	m.input.SetValue(head + suffix)
	m.input.SetCursor(len([]rune(head)))
	m.sugIdx = 0
	m.updateSuggestions()
}
func (app *Interface) staticSuggestions(values []string, noSpace bool) []suggestion {
	var out []suggestion
	for _, v := range values {
		out = append(out, suggestion{value: v, label: v, noSpace: noSpace})
	}
	return out
}
func (m *tuiModel) mentionSuggestions(cur string) []suggestion {
	body := strings.TrimPrefix(cur, "@")
	if i := strings.LastIndex(body, ","); i >= 0 {
		base := "@" + body[:i+1]
		taken := strings.Split(body[:i], ",")
		return m.app.filterSuggestions(m.deviceSuggestions(base, taken), cur)
	}
	connected := 0
	for _, d := range m.devices {
		if d.State == "device" {
			connected++
		}
	}
	out := []suggestion{{value: "@all", label: "@all", detail: fmt.Sprintf(m.app.Service.L("todas las conectadas (%d)", "every connected device (%d)"), connected)}}
	out = append(out, m.groupSuggestions("@")...)
	out = append(out, m.deviceSuggestions("@", nil)...)
	return m.app.filterSuggestions(out, cur)
}
func (m *tuiModel) groupSuggestions(base string) []suggestion {
	var names []string
	for k := range m.groups {
		names = append(names, k)
	}
	sort.Strings(names)
	var out []suggestion
	for _, k := range names {
		on := 0
		for _, s := range m.groups[k] {
			if d, ok := m.findDevice(s); ok && d.State == "device" {
				on++
			}
		}
		out = append(out, suggestion{value: base + k, label: base + k, detail: fmt.Sprintf(m.app.Service.L("grupo · %d/%d conectadas", "group · %d/%d connected"), on, len(m.groups[k]))})
	}
	return out
}
func (m *tuiModel) deviceSuggestions(base string, taken []string) []suggestion {
	var out []suggestion
	for _, d := range m.devices {
		name := d.Alias
		if name == "" {
			name = d.Serial
		}
		if slices.ContainsFunc(taken, func(t string) bool {
			return strings.EqualFold(t, name) || t == d.Serial
		}) {
			continue
		}
		out = append(out, suggestion{value: base + name, label: base + name, detail: strings.TrimSuffix(m.app.stateGlyph(d.State)+" "+m.app.stateLabel(d.State)+" · "+d.Model, " · ")})
	}
	return out
}
func (app *Interface) routineSuggestions() []suggestion {
	files, _ := filepath.Glob(app.Service.RoutinesPath("*.json"))
	var out []suggestion
	for _, f := range files {
		var r struct {
			Steps    []core.Spec
			Platform string
		}
		detail := ""
		if app.Service.ReadJSON(f, &r) == nil {
			if r.Platform == "" {
				r.Platform = "android"
			}
			detail = "[" + r.Platform + "] " + app.plural(len(r.Steps), app.Service.L("paso", "step"), app.Service.L("pasos", "steps")) + " · " + app.stepsList(r.Steps, 3)
		}
		n := strings.TrimSuffix(filepath.Base(f), ".json")
		out = append(out, suggestion{value: n, label: n, detail: detail})
	}
	return out
}
func (app *Interface) jobComplete(m *tuiModel, n int, _ []string, _ string) []suggestion {
	if n != 0 {
		return nil
	}
	if m.jobs == nil {
		m.jobs, _ = app.Service.ListJobs(20)
	}
	var out []suggestion
	for _, j := range m.jobs {
		out = append(out, suggestion{value: j.ID, label: app.shortID(j.ID), detail: fmt.Sprintf(app.Service.L("%s · %d/%d correctas · %s", "%s · %d/%d succeeded · %s"), app.describeSteps(j.Steps), len(j.Results)-j.Failed(), len(j.Results), app.relTime(j.Started))})
	}
	return out
}
func (app *Interface) pathSuggestions(cur, ext string) []suggestion {
	cur = strings.Trim(cur, `"`)
	dir, base := filepath.Split(cur)
	look := dir
	if look == "" {
		look = "."
	}
	entries, e := os.ReadDir(look)
	if e != nil {
		return nil
	}
	var dirs, files []suggestion
	for _, en := range entries {
		n := en.Name()
		if strings.HasPrefix(n, ".") || !strings.HasPrefix(strings.ToLower(n), strings.ToLower(base)) {
			continue
		}
		full := dir + n
		s := suggestion{label: full}
		if en.IsDir() {
			full += string(filepath.Separator)
			s.label, s.detail, s.noSpace = full, app.Service.L("carpeta", "folder"), true
		} else if ext != "" && !strings.EqualFold(filepath.Ext(n), ext) {
			continue
		}
		s.value = full
		if strings.ContainsAny(full, " &'") {
			s.value = `"` + full + `"`
		}
		if en.IsDir() {
			dirs = append(dirs, s)
		} else {
			files = append(files, s)
		}
	}
	return append(dirs, files...)
}
