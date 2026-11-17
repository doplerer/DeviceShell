package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/doplerer/deviceshell/internal/core"
)

type action struct {
	line, help string
	now        bool
}

func (app *Interface) normalize(s string) string {
	return strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n").Replace(strings.ToLower(s))
}

// commandActions lists the ready-made lines of a command, depending on the current state.
func (m *tuiModel) commandActions(c *tuiCommand) []action {
	a := func(line, help string) action {
		return action{line: line, help: help}
	}
	now := func(line, help string) action {
		return action{line: line, help: help, now: true}
	}
	var out []action
	switch strings.TrimPrefix(c.name, "motion ") {
	case "motion":
		for _, child := range c.children {
			out = append(out, a("/"+child.name, child.help))
		}
	case "settings":
		if !m.target.Empty() {
			out = append(out, now("/settings", m.app.Service.L("Revisar destino", "Review target")))
		}
	case "group":
		out = append(out, a("/group create", m.app.Service.L("crear un grupo nuevo", "create a new group")))
		if len(m.groups) > 0 {
			out = append(out, a("/group set", m.app.Service.L("cambiar las placas de un grupo", "change the devices of a group")), a("/group rename", m.app.Service.L("cambiar el nombre de un grupo", "rename a group")), a("/group delete", m.app.Service.L("borrar un grupo", "delete a group")))
		}
	case "apps":
		out = append(out, a("/apps @all", m.app.Service.L("listar las apps de todas las conectadas", "list the apps of every connected device")))
	case "start":
		out = append(out, a("/start com.android.settings", m.app.Service.L("abrir Ajustes", "open Settings")), a("/start com.android.chrome", m.app.Service.L("abrir Chrome", "open Chrome")))
	case "stop":
		out = append(out, a("/stop com.android.chrome", m.app.Service.L("cerrar Chrome", "close Chrome")), a("/stop com.android.settings", m.app.Service.L("cerrar Ajustes", "close Settings")))
	case "url":
		out = append(out, a("/url https://www.google.com", m.app.Service.L("abrir Google", "open Google")))
	case "key":
		out = append(out, a("/key HOME", m.app.Service.L("ir a la pantalla de inicio", "go to the home screen")), a("/key BACK", m.app.Service.L("atrás", "back")), a("/key WAKEUP", m.app.Service.L("encender la pantalla", "turn the screen on")), a("/key RECENTS", m.app.Service.L("aplicaciones recientes", "recent apps")), a("/key POWER", m.app.Service.L("botón de encendido (apaga o enciende la pantalla)", "power button (screen off or on)")), a("/key VOLUME_UP", m.app.Service.L("subir volumen", "volume up")), a("/key VOLUME_DOWN", m.app.Service.L("bajar volumen", "volume down")))
	case "uninstall":
		out = append(out, a("/uninstall "+m.app.Service.L("com.ejemplo.app", "com.example.app"), m.app.Service.L("⚠ desinstalar una app (cambia el paquete)", "⚠ uninstall an app (change the package)")))
	case "wipe":
		out = append(out, a("/wipe com.android.chrome", m.app.Service.L("⚠ dejar Chrome como recién instalado", "⚠ leave Chrome as freshly installed")))
	case "pull":
		out = append(out, a("/pull /sdcard/Download/", m.app.Service.L("traer la carpeta Descargas de cada placa", "bring the Downloads folder of every device")), a("/pull /sdcard/DCIM/Camera/", m.app.Service.L("traer las fotos de la cámara", "bring the camera photos")))
	case "reboot":
		out = append(out, a("/reboot", m.app.Service.L("⚠ reiniciar las placas", "⚠ reboot the devices")), a("/reboot recovery", m.app.Service.L("⚠ reiniciar en modo de recuperación", "⚠ reboot into recovery mode")))
	case "wifi":
		out = append(out, a("/wifi on", m.app.Service.L("encender el Wi-Fi", "turn Wi-Fi on")), a("/wifi off", m.app.Service.L("apagar el Wi-Fi", "turn Wi-Fi off")))
	case "resolution":
		out = append(out, a("/resolution", m.app.Service.L("ver la resolución de cada placa", "show the resolution of every device")), a("/resolution 1080x1920", m.app.Service.L("fijar 1080×1920 en todas", "set 1080×1920 on all")), a("/resolution reset", m.app.Service.L("volver a la resolución física", "go back to the physical resolution")))
	case "rotation":
		out = append(out, a("/rotation auto", m.app.Service.L("girar con el sensor", "rotate with the sensor")), a("/rotation 0", m.app.Service.L("fijar en vertical", "lock in portrait")), a("/rotation 90", m.app.Service.L("fijar en horizontal", "lock in landscape")))
	case "android-setting":
		out = append(out, a("/android-setting global stay_on_while_plugged_in 7", m.app.Service.L("pantalla siempre encendida mientras carga", "screen always on while charging")), a("/android-setting system screen_brightness 0", m.app.Service.L("brillo al mínimo", "minimum brightness")),
			a("/android-setting global window_animation_scale 0", m.app.Service.L("quitar animaciones de ventanas (más rápido)", "remove window animations (faster)")))
	case "perm":
		out = append(out, a("/perm grant", m.app.Service.L("conceder un permiso a una app", "grant a permission to an app")), a("/perm revoke", m.app.Service.L("retirar un permiso a una app", "revoke a permission from an app")))
	case "logcat":
		out = append(out, a("/logcat 500", m.app.Service.L("guardar las últimas 500 líneas del registro", "save the last 500 log lines")))
	case "record":
		out = append(out, a("/record 10", m.app.Service.L("grabar diez segundos de pantalla", "record ten seconds of screen")))
	case "shell":
		for _, s := range m.app.shellIdeas() {
			out = append(out, a("!"+s.value, s.help))
		}
	case "tap":
		out = append(out, a("/tap 0.5 0.5", m.app.Service.L("toque en el centro de la pantalla", "tap the center of the screen")))
	case "swipe":
		out = append(out, a("/swipe 0.5 0.8 0.5 0.2", m.app.Service.L("deslizar el dedo hacia arriba (bajar en una lista)", "swipe up (move down a list)")), a("/swipe 0.5 0.2 0.5 0.8", m.app.Service.L("deslizar hacia abajo (subir en una lista o abrir notificaciones)", "swipe down (move up a list or open notifications)")), a("/swipe 0.9 0.5 0.1 0.5", m.app.Service.L("deslizar hacia la izquierda (página siguiente)", "swipe left (next page)")), a("/swipe 0.1 0.5 0.9 0.5", m.app.Service.L("deslizar hacia la derecha (página anterior)", "swipe right (previous page)")))
	case "screenshot":
		out = append(out, a("/screenshot @all", m.app.Service.L("una captura de cada placa conectada", "a screenshot of every connected device")))
	case "wait":
		out = append(out, a("/wait 1000", m.app.Service.L("esperar un segundo", "wait one second")))
	case "routine":
		files, _ := filepath.Glob(m.app.Service.RoutinesPath("*.json"))
		for _, f := range files {
			n := strings.TrimSuffix(filepath.Base(f), ".json")
			var r struct{ Steps []core.Spec }
			detail := m.app.Service.L("ejecutar la rutina ", "run the routine ") + n
			if m.app.Service.ReadJSON(f, &r) == nil {
				detail = m.app.plural(len(r.Steps), m.app.Service.L("paso", "step"), m.app.Service.L("pasos", "steps")) + ": " + m.app.stepsList(r.Steps, 4)
			}
			out = append(out, a("/routine "+n, detail))
		}
	case "retry":
		if len(m.lastFailed) > 0 {
			out = append(out, now("/retry", fmt.Sprintf(m.app.Service.L("reintentar las %s que fallaron en el último lote", "retry the %s that failed in the last batch"), m.app.plural(len(m.lastFailed), m.app.Service.L("placa", "device"), m.app.Service.L("placas", "devices")))))
		}
	case "view":
		if m.wall != nil {
			out = append(out, now("/view close", fmt.Sprintf(m.app.Service.L("cerrar las %d pantallas abiertas", "close the %d open screens"), m.wall.Alive())))
		} else {
			out = append(out, a("/view @all", m.app.Service.L("abrir las pantallas de todas, manejables", "open every screen, controllable")), a("/view @all --read-only", m.app.Service.L("abrir las pantallas sólo para mirar", "open the screens to watch only")))
		}
	case "sync":
		switch {
		case m.sync != nil && m.sync.s != nil:
			if m.sync.info.Paused {
				out = append(out, now("/sync resume", m.app.Service.L("reanudar la réplica", "resume the replica")))
			} else {
				out = append(out, now("/sync pause", m.app.Service.L("pausar la réplica (las placas dejan de imitar)", "pause the replica (devices stop copying)")))
			}
			out = append(out, now("/sync stop", m.app.Service.L("terminar la réplica y cerrar sus ventanas", "end the replica and close its windows")), a("/sync", m.app.Service.L("panel de la réplica", "replica panel")), a("/sync status", m.app.Service.L("estado de la réplica", "replica status")))
		case m.sync == nil:
			out = append(out, a("/sync start", m.app.Service.L("empezar: manejas una placa y las demás la imitan", "start: you control one device and the others copy it")))
		}
	case "doctor":
		for _, d := range m.inventory {
			if d.State == "unauthorized" {
				out = append(out, now("/doctor", m.app.Service.L("hay placas sin autorizar: revisar qué pasa", "some devices are unauthorized: check what is wrong")))
				break
			}
		}
		if n := m.alertCount(); n > 0 && len(out) == 0 {
			out = append(out, now("/doctor", fmt.Sprintf(m.app.Service.L("%s con avisos de temperatura o batería", "%s with temperature or battery alerts"), m.app.plural(n, m.app.Service.L("placa", "device"), m.app.Service.L("placas", "devices")))))
		}
	}
	if strings.HasPrefix(c.name, "motion ") {
		for i := range out {
			out[i].line = "/motion " + strings.TrimPrefix(out[i].line, "/")
		}
	}
	return out
}

// paletteSuggestions builds the "/" menu. With "/" alone it lists only the
// commands. Once the typed text points at a command ("/view", "/vi", "/capture"), all of that command's
// ready-made actions unfold under it.
func (m *tuiModel) paletteSuggestions(cur string) []suggestion {
	command := func(c *tuiCommand) suggestion {
		return suggestion{value: "/" + c.name, label: "/" + c.name, detail: c.help, example: c.example, keywords: m.app.commandKeywords[strings.TrimPrefix(c.name, "motion ")], bold: true}
	}
	actions := func(c *tuiCommand) []suggestion {
		var out []suggestion
		for _, a := range m.commandActions(c) {
			if a.line != "/"+c.name {
				out = append(out, suggestion{value: a.line, label: a.line, detail: a.help, keywords: m.app.commandKeywords[strings.TrimPrefix(c.name, "motion ")], indent: true})
			}
		}
		return out
	}
	if strings.TrimSpace(cur) == "/" {
		var cmds []suggestion
		for _, c := range m.app.tuiRegistry {
			cmds = append(cmds, command(c))
		}
		return cmds
	}
	var cmds []suggestion
	for _, c := range m.app.allTUICommands(m.app.tuiRegistry) {
		cmds = append(cmds, command(c))
	}
	matched := m.app.filterSuggestions(cmds, cur)
	exact := m.app.lookupCommand(strings.TrimSpace(cur))
	var out []suggestion
	for _, s := range matched {
		out = append(out, s)
		c := m.app.lookupCommand(s.value)
		if len(c.children) == 0 && ((exact != nil && exact == c) || (exact == nil && len(matched) <= 2)) {
			out = append(out, actions(c)...)
		}
	}
	return out
}

// commandFlags are the --options a command understands, offered after its arguments.
func (app *Interface) commandFlags(name string, prior []string) []suggestion {
	var cs []choice
	switch {
	case name == "view" && (len(prior) == 0 || (prior[0] != "close" && prior[0] != "cerrar")):
		cs = []choice{{"--read-only", app.Service.L("abrir sólo para mirar, sin poder tocarlas", "open to watch only, without control")}}
	case name == "sync" && len(prior) > 0 && prior[0] == "start":
		cs = []choice{{"--views master", app.Service.L("abrir sólo la ventana de la principal (consume menos)", "open only the master window (uses less)")}}
	}
	return app.choiceSuggestions(cs)
}
func (app *Interface) shellIdeas() []choice {
	return []choice{{"getprop ro.product.model", app.Service.L("ver el modelo", "show the model")}, {"dumpsys battery", app.Service.L("estado y nivel de la batería", "battery status and level")}, {"wm size", app.Service.L("resolución de la pantalla", "screen resolution")}, {"df -h /sdcard", app.Service.L("espacio libre", "free space")}, {"settings get system screen_brightness", app.Service.L("brillo de la pantalla", "screen brightness")}, {"date", app.Service.L("fecha y hora del móvil", "phone date and time")}, {"pm list packages -3", app.Service.L("apps instaladas por el usuario", "apps installed by the user")}, {"reboot", app.Service.L("⚠ reiniciar las placas", "⚠ reboot the devices")}}
}
func (app *Interface) permissionIdeas() []choice {
	return []choice{{"android.permission.CAMERA", app.Service.L("cámara", "camera")}, {"android.permission.ACCESS_FINE_LOCATION", app.Service.L("ubicación precisa", "precise location")}, {"android.permission.POST_NOTIFICATIONS", app.Service.L("notificaciones (Android 13+)", "notifications (Android 13+)")}, {"android.permission.RECORD_AUDIO", app.Service.L("micrófono", "microphone")}, {"android.permission.READ_MEDIA_IMAGES", app.Service.L("fotos (Android 13+)", "photos (Android 13+)")}, {"android.permission.READ_CONTACTS", app.Service.L("contactos", "contacts")}}
}
func (app *Interface) commonPackages() []choice {
	return []choice{{"com.android.settings", app.Service.L("Ajustes", "Settings")}, {"com.android.chrome", "Chrome"}, {"com.android.vending", "Play Store"}, {"com.google.android.gm", "Gmail"}, {"com.google.android.apps.maps", "Maps"}, {"com.google.android.youtube", "YouTube"}, {"com.sec.android.app.camera", app.Service.L("Cámara (Samsung)", "Camera (Samsung)")}, {"com.sec.android.app.myfiles", app.Service.L("Mis archivos (Samsung)", "My Files (Samsung)")}}
}

// refreshRecommendation keeps dismissal tied to the suggested command, not polling ticks.
func (m *tuiModel) refreshRecommendation() {
	line := ""
	for _, name := range []string{"doctor", "retry", "view", "sync", "settings"} {
		for _, a := range m.commandActions(m.app.lookupCommand(name)) {
			if a.now {
				line = a.line
				break
			}
		}
		if line != "" {
			break
		}
	}
	if line != m.recommendation {
		m.recommendation = line
		m.dismissedRecommendation = ""
	}
}
func (m *tuiModel) recommendationVisible() bool {
	return m.recommendation != "" && m.recommendation != m.dismissedRecommendation && m.input.Value() == "" && m.form == nil && m.panel == nil && m.task == nil && m.busy == "" && !m.exiting && !m.quitting && !m.resetPending
}
