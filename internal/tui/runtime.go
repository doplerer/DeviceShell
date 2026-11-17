package tui

import (
	"context"
	"regexp"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/lipgloss"

	"github.com/doplerer/deviceshell/internal/core"
)

type Interface struct {
	*core.Service
	tuiDiscover     func() ([]core.Device, error)
	tuiLive         func() ([]core.Device, error)
	tuiPrepareIOS   func([]core.Device, func(core.IOSSetupProgress))
	tuiRunJob       func(c context.Context, target []core.Device, steps []core.Spec, o core.BatchOptions) (core.Job, error)
	tuiRunSync      func(parent context.Context, o core.SyncOptions, logf func(string), ready func(*core.LiveSession)) error
	tuiOpenWall     func(parent context.Context, devices []core.Device, readOnly bool) (*screenWall, error)
	tuiHealth       func(devices []core.Device, parallel int) map[string]core.Health
	tuiRegistry     []*tuiCommand
	boolFlags       map[string]bool
	commandGroups   []string
	clipboardWrite  func(string) error
	clipboardRead   func() (string, error)
	stCursor        lipgloss.Style
	stSelection     lipgloss.Style
	commandKeywords map[string]string
	routineVarRe    *regexp.Regexp
	colBackground   lipgloss.Color
	colAccent       lipgloss.Color
	colDim          lipgloss.Color
	colFaint        lipgloss.Color
	colOK           lipgloss.Color
	colErr          lipgloss.Color
	colWarn         lipgloss.Color
	colBadge        lipgloss.Color
	stScreen        lipgloss.Style
	colStateOn      lipgloss.Color
	colStateWarn    lipgloss.Color
	colStateErr     lipgloss.Color
	stStateOn       lipgloss.Style
	stStateWarn     lipgloss.Style
	stStateErr      lipgloss.Style
	stAccent        lipgloss.Style
	stBold          lipgloss.Style
	stDim           lipgloss.Style
	stFaint         lipgloss.Style
	stOK            lipgloss.Style
	stErr           lipgloss.Style
	stWarn          lipgloss.Style
	stSel           lipgloss.Style
	stActive        lipgloss.Style
	spinnerFrames   spinner.Spinner
	frameBorder     lipgloss.Border
}

func New(service *core.Service) *Interface {
	app := &Interface{}
	app.Service = service
	app.tuiDiscover = app.Service.Discover
	app.tuiLive = func() ([]core.Device, error) {
		return app.Service.LiveDevices(app.Service.Ctx)
	}
	app.tuiPrepareIOS = func(devices []core.Device, notify func(core.IOSSetupProgress)) {
		app.Service.AutoPrepareIOS(app.Service.Ctx, devices, notify)
	}
	app.tuiRunJob = app.Service.ExecuteJob
	app.tuiHealth = func(devices []core.Device, parallel int) map[string]core.Health {
		return app.Service.CollectHealth(app.Service.Ctx, devices, parallel)
	}
	app.tuiRunSync = app.Service.RunSyncSession
	app.tuiOpenWall = func(ctx context.Context, devices []core.Device, readOnly bool) (*screenWall, error) {
		w, err := app.Service.OpenScreenWall(ctx, devices, readOnly)
		if err != nil {
			return nil, err
		}
		return &screenWall{actual: w, Devices: w.Devices, Layout: w.Layout, Cols: w.Cols, ReadOnly: w.ReadOnly}, nil
	}
	app.boolFlags = map[string]bool{"read-only": true, "solo-ver": true, "free": true, "verify": true, "check": true}
	app.commandGroups = []string{"help", "inventory", "actions", "system", "routines", "screens", "session"}
	app.clipboardWrite = clipboard.WriteAll
	app.clipboardRead = clipboard.ReadAll
	app.commandKeywords = map[string]string{"reset": "borrar datos restablecer eliminar historial registros capturas delete data reset", "settings": "ajustes configurar configuracion idioma language paralelo parallel timeout tiempo destino target", "devices": "placas moviles telefonos lista dispositivos elegir marcar phones list pick", "doctor": "diagnostico problema error revisar comprobar autorizar diagnose problem check", "rename": "renombrar nombre alias cambiar nombre name", "group": "grupo grupos crear juntar conjunto groups", "apk": "instalar aplicacion app apk install", "apps": "aplicaciones instaladas listar paquetes installed packages", "start": "abrir lanzar iniciar app aplicacion open launch", "stop": "cerrar parar detener matar app aplicacion close kill", "url": "web pagina navegador enlace internet page browser link", "text": "escribir texto teclear mensaje type write message", "key": "tecla boton inicio atras volumen encendido pulsar button home back volume power press", "push": "copiar enviar subir archivo fichero copy send upload file", "shell": "orden comando terminal adb avanzado command advanced", "tap": "tocar toque clic click pulsar pantalla touch", "swipe": "deslizar arrastrar scroll desplazar gesto slide drag gesture", "screenshot": "captura foto pantallazo imagen capture photo picture", "wait": "esperar pausa sleep", "routine": "rutina automatizar pasos secuencia automate steps sequence", "jobs": "historial lotes informes resultados detalle lote informe history batches reports results detail batch report", "retry": "reintentar repetir fallidas fallos repeat failed", "view": "ver pantallas mirar mosaico cuadricula ventanas screens watch grid windows", "sync": "replicar replica espejo controlar todas a la vez imitar replicate mirror control all", "copy": "copiar portapapeles clipboard", "clear": "limpiar borrar pantalla clean screen", "help": "ayuda comandos lista commands", "exit": "salir cerrar terminar quit close", "uninstall": "desinstalar quitar eliminar app aplicacion uninstall remove", "wipe": "borrar datos limpiar app aplicacion restablecer cache clear data reset", "pull": "traer descargar bajar archivo fichero copiar download file fetch", "reboot": "reiniciar apagar encender recuperacion restart recovery bootloader", "wifi": "wifi red internet conexion inalambrica network wireless", "rotation": "girar rotar orientacion horizontal vertical rotate orientation landscape portrait", "resolution": "resolucion pantalla tamaño densidad dpi igualar misma wm size screen density match same", "android-setting": "ajuste android brillo pantalla animaciones tiempo settings brightness animations", "perm": "permiso permisos conceder retirar camara ubicacion permission grant revoke camera location", "logcat": "registro log errores depurar diagnostico debug errors", "record": "grabar video pantalla grabacion screenrecord video recording"}
	app.routineVarRe = regexp.MustCompile(`\$\{([^}]+)\}`)
	app.colBackground = lipgloss.Color("#000000")
	app.colAccent = lipgloss.Color("#F2F2F2")
	app.colDim = lipgloss.Color("#A0A0A0")
	app.colFaint = lipgloss.Color("#606060")
	app.colOK = app.colAccent
	app.colErr = app.colAccent
	app.colWarn = app.colAccent
	app.colBadge = app.colBackground
	app.stScreen = lipgloss.NewStyle().Foreground(app.colAccent).Background(app.colBackground)
	app.colStateOn = lipgloss.Color("#3FB950")
	app.colStateWarn = lipgloss.Color("#D29922")
	app.colStateErr = lipgloss.Color("#F85149")
	app.stStateOn = lipgloss.NewStyle().Foreground(app.colStateOn)
	app.stStateWarn = lipgloss.NewStyle().Foreground(app.colStateWarn)
	app.stStateErr = lipgloss.NewStyle().Foreground(app.colStateErr)
	app.stAccent = lipgloss.NewStyle().Foreground(app.colAccent)
	app.stBold = lipgloss.NewStyle().Foreground(app.colAccent).Bold(true)
	app.stDim = lipgloss.NewStyle().Foreground(app.colDim)
	app.stFaint = lipgloss.NewStyle().Foreground(app.colFaint)
	app.stOK = lipgloss.NewStyle().Foreground(app.colOK)
	app.stErr = lipgloss.NewStyle().Foreground(app.colErr)
	app.stWarn = lipgloss.NewStyle().Foreground(app.colWarn)
	app.stSel = lipgloss.NewStyle().Foreground(app.colAccent).Bold(true)
	app.stActive = lipgloss.NewStyle().Foreground(app.colBadge).Background(app.colAccent).Bold(true)
	app.spinnerFrames = spinner.Spinner{Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}, FPS: time.Second / 10}
	app.frameBorder = lipgloss.NormalBorder()
	app.stCursor = lipgloss.NewStyle().Background(app.colAccent).Foreground(app.colBadge)
	app.stSelection = lipgloss.NewStyle().Background(app.colAccent).Foreground(app.colBadge)
	app.tuiRegistry = app.buildTUICommands()
	return app
}
