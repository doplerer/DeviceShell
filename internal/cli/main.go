package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/doplerer/deviceshell/internal/core"
)

func (app *Console) Main() int {
	exe, _ := os.Executable()
	app.Service.Root = filepath.Dir(exe)
	if p := core.Getenv("ROOT"); p != "" {
		app.Service.Root = p
	}
	if _, e := os.Stat(app.Service.ConfigPath()); e != nil {
		fmt.Fprintln(os.Stderr, app.Service.L("No se encuentra config.json junto al ejecutable. Usa DEVICESHELL_ROOT si ejecutas desde fuentes.", "config.json is not next to the executable. Use DEVICESHELL_ROOT when running from sources."))
		return 2
	}
	if e := app.Service.MigrateLayout(); core.IsLayoutConflict(e) {
		fmt.Fprintln(os.Stderr, e)
	} else if e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	loaded, _, configErr := app.Service.ReadSettings()
	if configErr != nil {
		fmt.Fprintln(os.Stderr, configErr)
		return 1
	}
	app.Service.Parallel = loaded.Parallel
	app.Service.Timeout, _ = time.ParseDuration(loaded.Timeout)
	app.Service.StartupCxt = loaded.UseCxtKey
	app.Service.LoadLanguage()
	app.RebuildTexts()
	resetInvocation := false
	skipValue := false
	for _, a := range os.Args[1:] {
		if skipValue {
			skipValue = false
			continue
		}
		if a == "--devices" || a == "--group" {
			skipValue = true
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		resetInvocation = a == "reset"
		break
	}
	if resetInvocation {
		if e := app.Service.AcquireUsage(false); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
	} else {
		if e := app.Service.AcquireUsage(true); e == nil {
			if e = app.Service.ValidateResetPaths(); e == nil {
				e = app.Service.EnsureLayout()
			}
			if e == nil {
				e = app.Service.MigrateStorage()
			}
			if e != nil {
				app.Service.ReleaseUsage()
				fmt.Fprintln(os.Stderr, e)
				return 1
			}
			app.Service.ReleaseUsage()
		}
		if e := app.Service.AcquireUsage(false); e != nil {
			fmt.Fprintln(os.Stderr, e)
			return 1
		}
		if _, e := os.Stat(app.Service.DataPath(".storage-v2")); e != nil {
			app.Service.ReleaseUsage()
			fmt.Fprintln(os.Stderr, "Storage migration requires closing other DeviceShell instances")
			return 1
		}
	}
	defer app.Service.ReleaseUsage()
	defer app.Service.CloseDevices()
	if e := app.Service.ValidateResetPaths(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	if err := app.Service.EnsureLayout(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var manifest struct{ Asset string }
	if e := app.Service.ReadJSON(app.Service.InstallManifestPath(), &manifest); e != nil {
		fmt.Fprintln(os.Stderr, e)
		return 1
	}
	app.Service.ToolDir = app.Service.ToolsPath(strings.TrimSuffix(manifest.Asset, ".zip"))
	app.Service.Adb = filepath.Join(app.Service.ToolDir, "adb.exe")
	app.Service.Scrcpy = filepath.Join(app.Service.ToolDir, "scrcpy.exe")
	var config struct{ UseCxtKey bool }
	_ = app.Service.ReadJSON(app.Service.ConfigPath(), &config)
	if config.UseCxtKey {
		p := app.Service.CxtKeyPath()
		old := os.Getenv("ADB_VENDOR_KEYS")
		if old != "" {
			p = old + ";" + p
		}
		os.Setenv("ADB_VENDOR_KEYS", p)
	}
	var cancel context.CancelFunc
	app.Service.Ctx, cancel = signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	// Closing the console window (also logoff and shutdown) arrives as SIGTERM, and
	// Windows ends the process about 5 s later without running the defers: release the
	// iPhones now, so QuickTime is not left enabled and the next start skips restarting it.
	term := make(chan os.Signal, 1)
	signal.Notify(term, syscall.SIGTERM)
	go func() {
		<-term
		cancel()
		closed := make(chan struct{})
		go func() {
			app.Service.CloseDevices()
			close(closed)
		}()
		select {
		case <-closed:
		case <-time.After(4 * time.Second):
		}
		os.Exit(1)
	}()
	cobra.MousetrapHelpText = ""
	c := &cobra.Command{Use: "deviceshell", Short: app.Service.L("Controla muchos Android e iPhone desde un PC: dispositivos, grupos, lotes y réplica en directo", "Control many Android and iOS devices from one PC: devices, groups, batches and live replica"), SilenceUsage: true, SilenceErrors: true, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if !app.InteractiveTerminal() {
			return cmd.Help()
		}
		return app.RunTUI()
	}}
	c.AddCommand(&cobra.Command{Use: "tui", Short: app.Service.L("Abrir la interfaz interactiva", "Open the interactive interface"), Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		return app.RunTUI()
	}})
	f := c.PersistentFlags()
	f.StringVar(&app.devicesFlag, "devices", "", app.Service.L("Alias o series separados por comas", "Comma-separated aliases or serials"))
	f.StringVar(&app.groupFlag, "group", "", app.Service.L("Grupo de destino", "Target group"))
	f.BoolVar(&app.allFlag, "all", false, app.Service.L("Todos los conectados y autorizados", "Every connected, authorized device"))
	f.BoolVar(&app.jsonFlag, "json", false, app.Service.L("Salida JSON", "JSON output"))
	c.AddCommand(&cobra.Command{Use: "devices", Short: app.Service.L("Actualizar y listar inventario", "Refresh and list the inventory"), RunE: func(*cobra.Command, []string) error {
		d, e := app.Service.Discover()
		if e != nil {
			return e
		}
		// An iPhone may be restarting into Developer Mode: the command must not end
		// half-way. It is then read again, and the ones still waiting are explained.
		// A trusted phone goes on to Developer Mode in the next round, hence the few rounds.
		for round := 0; round < 3 && app.Service.IOSActivating(); round++ {
			if round == 0 && !app.jsonFlag {
				fmt.Fprintln(os.Stderr, app.Service.L("Preparando iPhone conectados por USB (puede reiniciarse)...", "Preparing USB iPhones (one may restart)..."))
			}
			c, cancel := context.WithTimeout(app.Service.Ctx, 4*time.Minute)
			app.Service.WaitIOSActivation(c)
			cancel()
			if d, e = app.Service.Discover(); e != nil {
				return e
			}
		}
		if pending := app.Service.PendingIOS(); len(pending) > 0 && !app.jsonFlag {
			for _, p := range pending {
				fmt.Fprintf(os.Stderr, "%s: %s\n", app.Service.L("iPhone por activar", "iPhone to activate"), p.Message)
			}
		}
		return app.output(core.Listed(d))
	}})
	c.AddCommand(app.groupCommand())
	c.AddCommand(app.resetCommand())
	app.addAction(c, "apk", "install", app.Service.L("Instalar un APK", "Install an APK"), 1, func(a []string) (core.Spec, error) {
		p, e := filepath.Abs(a[0])
		if e == nil {
			_, e = os.Stat(p)
		}
		return core.Spec{Action: "apk-install", Args: []string{p}}, e
	})
	app.addAction(c, "apk", "uninstall", app.Service.L("Desinstalar una aplicacion por paquete", "Uninstall an app by package"), 1, app.simple("apk-uninstall"))
	app.addAction(c, "app", "install", app.Service.L("Instalar APK o IPA firmado", "Install an APK or signed IPA"), 1, app.simple("app-install"))
	app.addAction(c, "app", "uninstall", app.Service.L("Desinstalar aplicación", "Uninstall an application"), 1, app.simple("app-uninstall"))
	app.addAction(c, "app", "list", app.Service.L("Listar aplicaciones instaladas", "List installed apps"), 0, app.simple("app-list"))
	app.addAction(c, "app", "start", app.Service.L("Abrir una aplicacion por paquete", "Open an app by package"), 1, app.simple("app-start"))
	app.addAction(c, "app", "stop", app.Service.L("Detener una aplicacion por paquete", "Stop an app by package"), 1, app.simple("app-stop"))
	app.addAction(c, "app", "clear", app.Service.L("Borrar los datos de una aplicacion", "Clear the data of an app"), 1, app.simple("app-clear"))
	c.AddCommand(&cobra.Command{Use: "perm", Short: app.Service.L("Permisos de aplicaciones", "App permissions")})
	app.addAction(c, "perm", "grant", app.Service.L("Conceder un permiso: paquete permiso", "Grant a permission: package permission"), 2, app.simple("perm-grant"))
	app.addAction(c, "perm", "revoke", app.Service.L("Retirar un permiso: paquete permiso", "Revoke a permission: package permission"), 2, app.simple("perm-revoke"))
	for _, x := range []struct {
		name, help string
		n          int
	}{{"url", app.Service.L("Abrir URL", "Open a URL"), 1}, {"text", app.Service.L("Pegar texto UTF-8", "Paste UTF-8 text"), 1}, {"key", app.Service.L("Pulsar HOME, BACK, ENTER, POWER, VOLUME_UP… o un codigo Android", "Press HOME, BACK, ENTER, POWER, VOLUME_UP… or an Android key code"), 1}, {"push", app.Service.L("Enviar archivo: archivo destino-Android", "Send a file: file android-destination"), 2}, {"pull", app.Service.L("Traer un archivo o carpeta del movil a output/downloads/ALIAS/", "Bring a file or folder from the phone to output/downloads/ALIAS/"), 1}, {"shell", app.Service.L("Ejecutar orden explicita en Android", "Run an explicit Android command"), 1}, {"screenshot", app.Service.L("Guardar capturas en output/screenshots/", "Save screenshots to output/screenshots/"), 0}, {"screenrecord", app.Service.L("Grabar la pantalla N segundos (1-180) en output/screenshots/", "Record the screen for N seconds (1-180) to output/screenshots/"), 1}, {"wait", app.Service.L("Esperar milisegundos", "Wait milliseconds"), 1},
		{"wifi", app.Service.L("Encender o apagar el Wi-Fi: on|off", "Turn Wi-Fi on or off: on|off"), 1}, {"rotation", app.Service.L("Orientacion: auto|0|90|180|270", "Orientation: auto|0|90|180|270"), 1}, {"setting", app.Service.L("Cambiar un ajuste de Android: system|secure|global clave valor", "Change an Android setting: system|secure|global key value"), 3}} {
		v := x
		c.AddCommand(&cobra.Command{Use: v.name, Short: v.help, Args: cobra.ExactArgs(v.n), RunE: func(_ *cobra.Command, a []string) error {
			return app.runSelected([]core.Spec{{Action: v.name, Args: a}})
		}})
	}
	c.AddCommand(&cobra.Command{Use: "resolution [WIDTHxHEIGHT|reset] [DPI]", Short: app.Service.L("Ver o fijar la resolucion de pantalla; reset vuelve a la fisica", "Show or set the screen resolution; reset goes back to the physical one"), Args: cobra.MaximumNArgs(2), RunE: func(_ *cobra.Command, a []string) error {
		s, e := app.Service.ResolutionSpec(a)
		if e != nil {
			return e
		}
		return app.runSelected([]core.Spec{s})
	}})
	// reboot and logcat take an optional argument with a default.
	for _, x := range []struct{ use, action, help, def string }{
		{"reboot [system|recovery|bootloader]", "reboot", app.Service.L("Reiniciar las placas (por defecto, normal)", "Reboot the devices (normal by default)"), "system"},
		{"logcat [LINES]", "logcat", app.Service.L("Guardar las ultimas lineas del registro en output/logcat/ (500 por defecto)", "Save the last log lines to output/logcat/ (500 by default)"), "500"},
	} {
		v := x
		c.AddCommand(&cobra.Command{Use: v.use, Short: v.help, Args: cobra.MaximumNArgs(1), RunE: func(_ *cobra.Command, a []string) error {
			if len(a) == 0 {
				a = []string{v.def}
			}
			return app.runSelected([]core.Spec{{Action: v.action, Args: a}})
		}})
	}
	c.AddCommand(&cobra.Command{Use: "tap X Y", Short: app.Service.L("Toque en coordenadas normalizadas (0..1)", "Tap at normalized coordinates (0..1)"), Args: cobra.ExactArgs(2), RunE: func(_ *cobra.Command, a []string) error {
		s, e := app.Service.CoordinateSpec("tap", a)
		if e != nil {
			return e
		}
		return app.runSelected([]core.Spec{s})
	}})
	c.AddCommand(&cobra.Command{Use: "swipe X Y X2 Y2 [ms]", Short: app.Service.L("Deslizamiento normalizado", "Normalized swipe"), Args: cobra.RangeArgs(4, 5), RunE: func(_ *cobra.Command, a []string) error {
		s, e := app.Service.CoordinateSpec("swipe", a)
		if e != nil {
			return e
		}
		return app.runSelected([]core.Spec{s})
	}})
	rc := &cobra.Command{Use: "routine", Short: app.Service.L("Rutinas JSON", "JSON routines")}
	rc.AddCommand(&cobra.Command{Use: "run NAME [key=value...]", Short: app.Service.L("Ejecutar una rutina", "Run a routine"), Args: cobra.MinimumNArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		s, e := app.Service.LoadRoutine(a)
		if e != nil {
			return e
		}
		return app.runSelected(s)
	}})
	c.AddCommand(rc)
	jc := &cobra.Command{Use: "job", Short: app.Service.L("Consultar y reintentar lotes", "Show and retry batches")}
	jc.AddCommand(&cobra.Command{Use: "show ID", Short: app.Service.L("Detalle de un lote", "Show a batch"), Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		var j core.Job
		e := app.Service.ReadJSON(app.Service.JobPath(a[0]), &j)
		if e != nil {
			return e
		}
		return app.output(j)
	}}, &cobra.Command{Use: "retry ID", Short: app.Service.L("Reintentar las placas fallidas", "Retry the failed devices"), Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		return errors.New(app.Service.L("Los reintentos solo est\u00e1n disponibles durante la sesi\u00f3n interactiva; repite la orden original.", "Retries are only available during the interactive session; repeat the original command."))
	}})
	c.AddCommand(jc)
	c.AddCommand(app.syncCommand())
	var readOnly bool
	view := &cobra.Command{Use: "view", Short: app.Service.L("Pantallas en cuadricula; cada ventana maneja su movil (Ctrl+C para cerrar)", "Screens in a grid; each window controls its device (Ctrl+C to close)"), Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		d, e := app.Service.Discover()
		if e != nil {
			return e
		}
		t, e := app.selectTargets(d)
		if e != nil {
			return e
		}
		w, e := app.Service.OpenScreenWall(app.Service.Ctx, t, readOnly)
		if e != nil {
			return e
		}
		fmt.Fprintf(os.Stderr, app.Service.L("%d pantallas abiertas en %d columnas. Ctrl+C para cerrar.\n", "%d screens open in %d columns. Ctrl+C to close.\n"), len(w.Devices), w.Cols)
		select {
		case <-app.Service.Ctx.Done():
		case <-w.Done():
		}
		w.Close()
		return nil
	}}
	view.Flags().BoolVar(&readOnly, "read-only", false, app.Service.L("Solo mirar: las ventanas no controlan los moviles", "Watch only: windows do not control the devices"))
	c.AddCommand(view)
	app.addExtras(c)
	c.AddCommand(&cobra.Command{Use: "doctor", Short: app.Service.L("Comprobar herramientas y conexion", "Check tools and connection"), RunE: func(*cobra.Command, []string) error {
		out, _ := app.Service.Command(app.Service.Ctx, "version")
		d, e := app.Service.Discover()
		if e != nil {
			return e
		}
		s, _, e := app.Service.ReadSettings()
		if e != nil {
			return e
		}
		health := app.Service.CollectHealth(app.Service.Ctx, d, app.Service.Parallel)
		alerts := map[string][]string{}
		for serial, h := range health {
			if a := app.Service.HealthAlerts(h, s.AlertTemp, s.AlertBattery); len(a) > 0 {
				alerts[serial] = a
			}
		}
		return app.output(map[string]any{"backends": app.Service.BackendDiagnostics(app.Service.Ctx), "iphoneSetup": app.Service.IOSSetupSummary(d), "adb": out, "devices": d, "health": health, "alerts": alerts, "usb": app.Service.USBReport(d), "scrcpy": app.Service.Scrcpy, "root": app.Service.Root})
	}})
	if e := c.ExecuteContext(app.Service.Ctx); e != nil {
		if errors.Is(e, app.errBatchFailed) {
			return 1
		}
		if app.jsonFlag {
			b, _ := json.Marshal(map[string]string{"error": e.Error()})
			fmt.Println(string(b))
		} else {
			fmt.Fprintln(os.Stderr, "ERROR:", e)
		}
		return 1
	}
	return 0
}
func (app *Console) output(v any) error {
	if app.jsonFlag {
		b, e := json.Marshal(v)
		if e == nil {
			fmt.Println(string(b))
		}
		return e
	}
	switch d := v.(type) {
	case []core.Device:
		fmt.Printf("%-9s %-20s %-15s %s\n", "ALIAS", app.Service.L("SERIE", "SERIAL"), app.Service.L("MODELO", "MODEL"), app.Service.L("ESTADO", "STATE"))
		for _, v := range d {
			fmt.Printf("%-9s %-20s %-15s %s\n", v.Alias, v.Serial, v.Model+" ["+v.OS()+"]", v.State)
		}
	default:
		b, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(b))
	}
	return nil
}
func (app *Console) selectTargets(d []core.Device) ([]core.Device, error) {
	return app.Service.ResolveTargets(d, core.Selection{Devices: app.devicesFlag, Group: app.groupFlag, All: app.allFlag})
}
func (app *Console) groupCommand() *cobra.Command {
	g := &cobra.Command{Use: "group", Short: app.Service.L("Grupos persistentes", "Saved groups")}
	g.AddCommand(&cobra.Command{Use: "list", Short: app.Service.L("Ver los grupos", "List groups"), RunE: func(*cobra.Command, []string) error {
		x, e := app.Service.Groups()
		if e != nil {
			return e
		}
		return app.output(x)
	}}, &cobra.Command{Use: "create NAME", Short: app.Service.L("Crear un grupo con la seleccion", "Create a group with the selection"), Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		d, e := app.Service.Discover()
		if e != nil {
			return e
		}
		target, e := app.selectTargets(d)
		if e != nil {
			return e
		}
		ids := []string{}
		for _, v := range target {
			ids = append(ids, v.Serial)
		}
		return app.Service.CreateGroup(a[0], ids)
	}}, &cobra.Command{Use: "rename NAME NEW_NAME", Short: app.Service.L("Cambiar el nombre de un grupo", "Rename a group"), Args: cobra.ExactArgs(2), RunE: func(_ *cobra.Command, a []string) error {
		return app.Service.RenameGroup(a[0], a[1])
	}}, &cobra.Command{Use: "delete NAME", Short: app.Service.L("Borrar un grupo", "Delete a group"), Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, a []string) error {
		return app.Service.DeleteGroup(a[0])
	}})
	return g
}
func (app *Console) simple(action string) func([]string) (core.Spec, error) {
	return func(a []string) (core.Spec, error) {
		return core.Spec{Action: action, Args: a}, nil
	}
}
func (app *Console) addAction(rootCmd *cobra.Command, parent, name, help string, n int, makeSpec func([]string) (core.Spec, error)) {
	var p *cobra.Command
	for _, c := range rootCmd.Commands() {
		if c.Name() == parent {
			p = c
		}
	}
	if p == nil {
		short := map[string]string{
			"apk":  app.Service.L("Instalar y desinstalar APK", "Install and uninstall APKs"),
			"app":  app.Service.L("Gestionar aplicaciones: listar, abrir, cerrar, borrar datos, instalar", "Manage apps: list, start, stop, clear, install"),
			"perm": app.Service.L("Permisos de aplicaciones", "App permissions"),
		}[parent]
		p = &cobra.Command{Use: parent, Short: short}
		rootCmd.AddCommand(p)
	}
	p.AddCommand(&cobra.Command{Use: name, Short: help, Args: cobra.ExactArgs(n), RunE: func(_ *cobra.Command, a []string) error {
		s, e := makeSpec(a)
		if e != nil {
			return e
		}
		return app.runSelected([]core.Spec{s})
	}})
}
func (app *Console) runSelected(steps []core.Spec) error {
	d, e := app.Service.Discover()
	if e != nil {
		return e
	}
	t, e := app.selectTargets(d)
	if e != nil {
		return e
	}
	return app.runBatch(t, steps)
}
func (app *Console) runBatch(target []core.Device, steps []core.Spec) error {
	o := core.BatchOptions{Parallel: app.Service.Parallel, Timeout: app.Service.Timeout}
	if e := app.Service.PrepareBatch(steps, o); e != nil {
		return e
	}
	if !app.jsonFlag {
		o.OnResult = func(_ int, r core.Result) {
			fmt.Fprintf(os.Stderr, "%s: %v %s\n", r.Alias, r.OK, r.Error)
		}
	}
	j, e := app.Service.ExecuteJob(app.Service.Ctx, target, steps, o)
	if e != nil {
		return e
	}
	_ = app.output(j)
	if j.Failed() > 0 {
		return app.errBatchFailed
	}
	return nil
}
