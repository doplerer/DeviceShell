package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type paramKind int

const (
	pText paramKind = iota
	pRest
	pCoord
	pNumber
	pDuration
	pChoice
	pPath
	pAPK
	pAndroidPath
	pDevice
	pGroup
	pGroupNew
	pAlias
	pRoutine
	pRoutineVar
	pPackage
	pJob
	pSession
	pCommand
)

type choice struct{ value, help string }
type param struct {
	name, help string
	kind       paramKind
	choices    []choice
	strict     bool
	optional   bool
	def        string
	key        string
	numeric    bool
	min, max   int
	aliases    map[string]string
}

// freeForm is true for values the user makes up (numbers, coordinates, text): suggestions are only ideas
// there, so Enter keeps what was typed instead of replacing it with a near match.
func (p param) freeForm() bool {
	switch p.kind {
	case pCoord, pNumber, pDuration, pText, pRest, pGroupNew, pAlias, pRoutineVar:
		return true
	}
	return false
}

type destStep struct{ name, help string }

func (app *Interface) coordX() []choice {
	return []choice{{"0.5", app.Service.L("centro", "center")}, {"0.1", app.Service.L("cerca del borde izquierdo", "near the left edge")}, {"0.25", app.Service.L("un cuarto desde la izquierda", "a quarter from the left")}, {"0.75", app.Service.L("tres cuartos", "three quarters")}, {"0.9", app.Service.L("cerca del borde derecho", "near the right edge")}}
}
func (app *Interface) coordY() []choice {
	return []choice{{"0.5", app.Service.L("centro", "center")}, {"0.1", app.Service.L("cerca de arriba", "near the top")}, {"0.25", app.Service.L("un cuarto desde arriba", "a quarter from the top")}, {"0.75", app.Service.L("tres cuartos", "three quarters")}, {"0.9", app.Service.L("cerca de abajo", "near the bottom")}}
}

// commandParams returns the arguments of a command given those already chosen, and its destination step if any.
func (app *Interface) commandParams(m *tuiModel, name string, prior []string) ([]param, *destStep) {
	at := func(i int) string {
		if i < len(prior) {
			return strings.ToLower(prior[i])
		}
		return ""
	}
	devices := &destStep{app.Service.L("Destino", "Target"), app.Service.L("a qué placas: todas, un grupo o algunas", "which devices: all, a group or some")}
	if strings.HasPrefix(name, "motion ") {
		name = strings.TrimPrefix(name, "motion ")
	}
	switch name {
	case "motion":
		return []param{{name: app.Service.L("Acción", "Action"), help: app.Service.L("elige un gesto, una tecla o texto", "choose a gesture, key or text"), kind: pChoice, strict: true, choices: []choice{{"tap", app.Service.L("toque en la pantalla", "tap the screen")}, {"swipe", app.Service.L("deslizar", "swipe")}, {"key", app.Service.L("pulsar una tecla", "press a key")}, {"text", app.Service.L("escribir texto", "type text")}}}}, nil
	case "rename":
		return []param{{name: app.Service.L("Placa", "Device"), help: app.Service.L("la placa a renombrar", "the device to rename"), kind: pDevice}, {name: app.Service.L("Nuevo alias", "New alias"), help: app.Service.L("nombre nuevo, p. ej. Caja-1 (sin comas ni barras)", "new name, e.g. Box-1 (no commas or slashes)"), kind: pAlias}}, nil
	case "group":
		ps := []param{{name: app.Service.L("Acción", "Action"), help: app.Service.L("qué hacer con los grupos", "what to do with groups"), kind: pChoice, strict: true, optional: true, def: "list", choices: []choice{{"list", app.Service.L("ver los grupos y sus placas", "show groups and their devices")}, {"create", app.Service.L("crear un grupo nuevo", "create a new group")}, {"set", app.Service.L("cambiar las placas de un grupo", "change the devices of a group")}, {"rename", app.Service.L("cambiar el nombre de un grupo", "rename a group")}, {"delete", app.Service.L("borrar un grupo", "delete a group")}}}}
		switch at(0) {
		case "create", "new", "crear":
			return append(ps, param{name: app.Service.L("Nombre", "Name"), help: app.Service.L("nombre del grupo nuevo, sin espacios, p. ej. pruebas", "name of the new group, no spaces, e.g. tests"), kind: pGroupNew}), &destStep{app.Service.L("Placas", "Devices"), app.Service.L("qué placas forman el grupo", "which devices make up the group")}
		case "set":
			return append(ps, param{name: app.Service.L("Grupo", "Group"), help: app.Service.L("el grupo que vas a cambiar", "the group to change"), kind: pGroup}), &destStep{app.Service.L("Placas", "Devices"), app.Service.L("las placas que tendrá a partir de ahora", "the devices it will have from now on")}
		case "rename", "renombrar", "mv":
			return append(ps, param{name: app.Service.L("Grupo", "Group"), help: app.Service.L("el grupo que vas a renombrar", "the group to rename"), kind: pGroup}, param{name: app.Service.L("Nombre nuevo", "New name"), help: app.Service.L("sin espacios, comas ni barras, p. ej. caja-2", "no spaces, commas or slashes, e.g. box-2"), kind: pGroupNew}), nil
		case "delete", "rm", "borrar":
			return append(ps, param{name: app.Service.L("Grupo", "Group"), help: app.Service.L("el grupo que vas a borrar", "the group to delete"), kind: pGroup}), nil
		}
		return ps, nil
	case "app":
		return []param{{name: app.Service.L("Acción", "Action"), kind: pChoice, strict: true, choices: []choice{{"install", app.Service.L("Instalar APK/IPA", "Install APK/IPA")}, {"uninstall", app.Service.L("Desinstalar", "Uninstall")}}}}, nil
	case "app install":
		return []param{{name: app.Service.L("Archivo APK/IPA", "APK/IPA file"), kind: pPath}}, devices
	case "app uninstall":
		return []param{{name: app.Service.L("ID de aplicación", "Application ID"), kind: pPackage}}, devices
	case "apk":
		return []param{{name: "APK", help: app.Service.L("archivo .apk de este PC; Tab entra en carpetas", ".apk file on this PC; Tab enters folders"), kind: pAPK}}, devices
	case "apps", "screenshot":
		return nil, devices
	case "start", "stop":
		help := app.Service.L("la app a abrir, p. ej. com.android.settings (/apps las lista)", "the app to open, e.g. com.android.settings (/apps lists them)")
		if name == "stop" {
			help = app.Service.L("la app a cerrar, p. ej. com.android.settings (/apps las lista)", "the app to close, e.g. com.android.settings (/apps lists them)")
		}
		return []param{{name: app.Service.L("Paquete", "Package"), help: help, kind: pPackage}}, devices
	case "url":
		return []param{{name: app.Service.L("Dirección", "Address"), help: app.Service.L("página que se abrirá, p. ej. example.com o https://example.com", "page to open, e.g. example.com or https://example.com"), kind: pText, choices: []choice{{"https://www.google.com", "Google"}, {"https://www.youtube.com", "YouTube"}, {"https://www.example.com", app.Service.L("página de prueba", "test page")}}}}, devices
	case "text":
		return []param{{name: app.Service.L("Texto", "Text"), help: app.Service.L("lo que se escribirá en el campo activo (acentos y emojis valen)", "what will be typed in the focused field (accents and emojis work)"), kind: pRest}}, devices
	case "shell":
		return []param{{name: app.Service.L("Orden", "Command"), help: app.Service.L("orden de Android, p. ej. getprop ro.product.model", "Android command, e.g. getprop ro.product.model"), kind: pRest, choices: app.shellIdeas()}}, devices
	case "key":
		return []param{{name: app.Service.L("Tecla", "Key"), help: app.Service.L("la tecla que se pulsará", "the key to press"), kind: pChoice, numeric: true, choices: []choice{{"HOME", app.Service.L("ir a la pantalla de inicio", "go to the home screen")}, {"BACK", app.Service.L("atrás", "back")}, {"ENTER", app.Service.L("aceptar", "enter")}, {"RECENTS", app.Service.L("aplicaciones recientes", "recent apps")}, {"WAKEUP", app.Service.L("encender la pantalla", "turn the screen on")}, {"POWER", app.Service.L("botón de encendido (apaga o enciende la pantalla)", "power button (screen off or on)")}, {"VOLUME_UP", app.Service.L("subir volumen", "volume up")}, {"VOLUME_DOWN", app.Service.L("bajar volumen", "volume down")}, {"DEL", app.Service.L("borrar (retroceso)", "delete (backspace)")}, {"TAB", app.Service.L("tabulador (siguiente campo)", "tab (next field)")}, {"MENU", app.Service.L("menú", "menu")}, {"CAMERA", app.Service.L("abrir la cámara", "open the camera")}}}}, devices
	case "uninstall", "wipe":
		help := app.Service.L("la app a desinstalar, p. ej. com.ejemplo.app (/apps las lista)", "the app to uninstall, e.g. com.example.app (/apps lists them)")
		if name == "wipe" {
			help = app.Service.L("la app cuyos datos se borran: queda como recién instalada", "the app whose data is cleared: it is left as freshly installed")
		}
		return []param{{name: app.Service.L("Paquete", "Package"), help: help, kind: pPackage}}, devices
	case "pull":
		return []param{{name: app.Service.L("Ruta del móvil", "Phone path"), help: app.Service.L("archivo o carpeta del móvil; se guarda en output/downloads/ALIAS", "file or folder on the phone; saved to output/downloads/ALIAS"), kind: pAndroidPath, choices: []choice{{"/sdcard/Download/", app.Service.L("Descargas", "Downloads")}, {"/sdcard/DCIM/Camera/", app.Service.L("fotos de la cámara", "camera photos")}, {"/sdcard/Documents/", app.Service.L("documentos", "documents")}}}}, devices
	case "reboot":
		return []param{{name: app.Service.L("Modo", "Mode"), help: app.Service.L("cómo reiniciar; vacío: reinicio normal", "how to reboot; empty: normal reboot"), kind: pChoice, strict: true, optional: true, def: "system", choices: []choice{{"system", app.Service.L("reinicio normal", "normal reboot")}, {"recovery", app.Service.L("modo de recuperación", "recovery mode")}, {"bootloader", app.Service.L("cargador de arranque (fastboot)", "bootloader (fastboot)")}}}}, devices
	case "wifi":
		return []param{{name: "Wi-Fi", help: app.Service.L("encender o apagar el Wi-Fi", "turn Wi-Fi on or off"), kind: pChoice, strict: true, choices: []choice{{"on", app.Service.L("encender", "turn on")}, {"off", app.Service.L("apagar", "turn off")}}}}, devices
	case "resolution":
		return []param{{name: app.Service.L("Resolución", "Resolution"), help: app.Service.L("ANCHOxALTO, reset para la física; vacío: ver la actual", "WIDTHxHEIGHT, reset for the physical one; empty: show the current one"), kind: pChoice, optional: true, choices: []choice{{"1080x1920", "16:9"}, {"1080x2340", "19.5:9"}, {"reset", app.Service.L("resolución física", "physical resolution")}}},
			{name: "DPI", help: app.Service.L("densidad; vacío: no cambiarla", "density; empty: leave it as is"), kind: pNumber, optional: true}}, devices
	case "rotation":
		return []param{{name: app.Service.L("Orientación", "Orientation"), help: app.Service.L("auto sigue al sensor; los grados la fijan", "auto follows the sensor; degrees lock it"), kind: pChoice, strict: true, choices: []choice{{"auto", app.Service.L("girar con el sensor", "rotate with the sensor")}, {"0", app.Service.L("vertical", "portrait")}, {"90", app.Service.L("horizontal", "landscape")}, {"180", app.Service.L("vertical invertida", "upside-down portrait")}, {"270", app.Service.L("horizontal invertida", "reverse landscape")}}}}, devices
	case "android-setting":
		ps := []param{{name: app.Service.L("Tabla", "Namespace"), help: app.Service.L("dónde guarda Android el ajuste", "where Android keeps the setting"), kind: pChoice, strict: true, choices: []choice{{"system", app.Service.L("pantalla, sonido, brillo…", "screen, sound, brightness…")}, {"global", app.Service.L("todo el dispositivo: animaciones, pantalla encendida al cargar…", "whole device: animations, stay awake while charging…")}, {"secure", app.Service.L("ubicación y seguridad", "location and security")}}}}
		keys := map[string][]choice{
			"system": {{"screen_brightness", app.Service.L("brillo de 0 a 255", "brightness from 0 to 255")}, {"screen_brightness_mode", app.Service.L("0 manual · 1 automático", "0 manual · 1 automatic")}, {"screen_off_timeout", app.Service.L("milisegundos hasta apagar la pantalla", "milliseconds until the screen turns off")}},
			"global": {{"stay_on_while_plugged_in", app.Service.L("pantalla encendida mientras carga (7 siempre · 0 nunca)", "screen on while charging (7 always · 0 never)")}, {"window_animation_scale", app.Service.L("animaciones de ventanas (0 sin animación)", "window animations (0 no animation)")}, {"transition_animation_scale", app.Service.L("animaciones de transición", "transition animations")}, {"animator_duration_scale", app.Service.L("duración de animaciones", "animation duration")}},
			"secure": {{"location_mode", app.Service.L("ubicación: 0 apagada · 3 alta precisión", "location: 0 off · 3 high accuracy")}},
		}
		values := map[string][]choice{"screen_brightness": {{"0", app.Service.L("mínimo", "minimum")}, {"128", app.Service.L("medio", "medium")}}, "screen_brightness_mode": {{"0", app.Service.L("manual", "manual")}, {"1", app.Service.L("automático", "automatic")}},
			"screen_off_timeout": {{"600000", app.Service.L("10 minutos", "10 minutes")}, {"1800000", app.Service.L("30 minutos", "30 minutes")}}, "stay_on_while_plugged_in": {{"7", app.Service.L("siempre", "always")}, {"0", app.Service.L("nunca", "never")}},
			"window_animation_scale": {{"0", app.Service.L("sin animación", "no animation")}, {"1", app.Service.L("normal", "normal")}}, "transition_animation_scale": {{"0", app.Service.L("sin animación", "no animation")}, {"1", app.Service.L("normal", "normal")}},
			"animator_duration_scale": {{"0", app.Service.L("sin animación", "no animation")}, {"1", app.Service.L("normal", "normal")}}, "location_mode": {{"0", app.Service.L("apagada", "off")}, {"3", app.Service.L("alta precisión", "high accuracy")}}}
		return append(ps, param{name: app.Service.L("Clave", "Key"), help: app.Service.L("nombre del ajuste de Android", "name of the Android setting"), kind: pText, choices: keys[at(0)]},
			param{name: app.Service.L("Valor", "Value"), help: app.Service.L("el valor nuevo", "the new value"), kind: pText, choices: values[at(1)]}), devices
	case "perm":
		return []param{{name: app.Service.L("Acción", "Action"), help: app.Service.L("conceder o retirar", "grant or revoke"), kind: pChoice, strict: true, choices: []choice{{"grant", app.Service.L("conceder el permiso", "grant the permission")}, {"revoke", app.Service.L("retirar el permiso", "revoke the permission")}}},
			{name: app.Service.L("Paquete", "Package"), help: app.Service.L("la app, p. ej. com.ejemplo.app (/apps las lista)", "the app, e.g. com.example.app (/apps lists them)"), kind: pPackage},
			{name: app.Service.L("Permiso", "Permission"), help: app.Service.L("permiso de Android, p. ej. android.permission.CAMERA", "Android permission, e.g. android.permission.CAMERA"), kind: pText, choices: app.permissionIdeas()}}, devices
	case "logcat":
		return []param{{name: app.Service.L("Líneas", "Lines"), help: app.Service.L("últimas líneas del registro que se guardan", "last log lines to save"), kind: pNumber, optional: true, def: "500", min: 1, max: 100000, choices: []choice{{"200", app.Service.L("pocas", "a few")}, {"500", app.Service.L("normal", "normal")}, {"5000", app.Service.L("muchas", "many")}}}}, devices
	case "record":
		return []param{{name: app.Service.L("Segundos", "Seconds"), help: app.Service.L("duración de la grabación, de 1 a 180", "recording length, from 1 to 180"), kind: pNumber, min: 1, max: 180, choices: []choice{{"10", app.Service.L("diez segundos", "ten seconds")}, {"30", app.Service.L("medio minuto", "half a minute")}, {"60", app.Service.L("un minuto", "one minute")}}}}, devices
	case "push":
		return []param{{name: app.Service.L("Archivo", "File"), help: app.Service.L("archivo de este PC que se copiará", "file on this PC to copy"), kind: pPath}, {name: app.Service.L("Carpeta del móvil", "Phone folder"), help: app.Service.L("dónde se guardará en cada móvil", "where it is saved on each phone"), kind: pAndroidPath, choices: []choice{{"/sdcard/Download/", app.Service.L("Descargas", "Downloads")}, {"/sdcard/DCIM/", app.Service.L("fotos", "photos")}, {"/sdcard/Documents/", app.Service.L("documentos", "documents")}, {"/data/local/tmp/", app.Service.L("temporal", "temporary")}}}}, devices
	case "tap":
		return []param{{name: "X", help: app.Service.L("posición horizontal: 0 izquierda · 0.5 centro · 1 derecha", "horizontal position: 0 left · 0.5 center · 1 right"), kind: pCoord, choices: app.coordX()}, {name: "Y", help: app.Service.L("posición vertical: 0 arriba · 0.5 centro · 1 abajo", "vertical position: 0 top · 0.5 center · 1 bottom"), kind: pCoord, choices: app.coordY()}}, devices
	case "swipe":
		return []param{{name: app.Service.L("X inicio", "Start X"), help: app.Service.L("dónde empieza, en horizontal (0 izquierda · 1 derecha)", "where it starts, horizontally (0 left · 1 right)"), kind: pCoord, choices: app.coordX()}, {name: app.Service.L("Y inicio", "Start Y"), help: app.Service.L("dónde empieza, en vertical (0 arriba · 1 abajo)", "where it starts, vertically (0 top · 1 bottom)"), kind: pCoord, choices: app.coordY()}, {name: app.Service.L("X final", "End X"), help: app.Service.L("dónde termina, en horizontal", "where it ends, horizontally"), kind: pCoord, choices: app.coordX()}, {name: app.Service.L("Y final", "End Y"), help: app.Service.L("dónde termina, en vertical (de 0.8 a 0.2 sube la pantalla)", "where it ends, vertically (0.8 to 0.2 scrolls the screen up)"), kind: pCoord, choices: app.coordY()}, {name: app.Service.L("Duración", "Duration"), help: app.Service.L("milisegundos que dura el gesto", "milliseconds the gesture lasts"), kind: pNumber, optional: true, def: "300", min: 1, choices: []choice{{"300", app.Service.L("normal", "normal")}, {"150", app.Service.L("rápido", "fast")}, {"800", app.Service.L("lento", "slow")}}}}, devices
	case "wait":
		return []param{{name: app.Service.L("Milisegundos", "Milliseconds"), help: app.Service.L("cuánto esperar; 1000 es un segundo", "how long to wait; 1000 is one second"), kind: pNumber, choices: []choice{{"500", app.Service.L("medio segundo", "half a second")}, {"1000", app.Service.L("un segundo", "one second")}, {"3000", app.Service.L("tres segundos", "three seconds")}}}}, devices
	case "routine":
		ps := []param{{name: app.Service.L("Rutina", "Routine"), help: app.Service.L("archivo de routines/ que se ejecutará", "file in routines/ to run"), kind: pRoutine}}
		if len(prior) > 0 {
			for _, k := range app.routineVars(prior[0]) {
				ps = append(ps, param{name: k, help: app.Service.L("valor de ${"+k+"} en la rutina", "value of ${"+k+"} in the routine"), kind: pRoutineVar, key: k})
			}
		}
		return ps, devices
	case "jobs":
		return []param{{name: app.Service.L("Lote", "Batch"), help: app.Service.L("el lote que quieres ver (vacío: lista de lotes recientes)", "the batch to show (empty: recent batches list)"), kind: pJob, optional: true}}, nil
	case "retry":
		return []param{{name: app.Service.L("Lote", "Batch"), help: app.Service.L("el lote a reintentar (vacío: el último)", "the batch to retry (empty: the last one)"), kind: pJob, optional: true}}, nil
	case "view":
		closeIt := param{name: app.Service.L("Acción", "Action"), help: app.Service.L("cerrar las pantallas abiertas, o elegir placas para abrir otras", "close the open screens, or pick devices to open others"), kind: pChoice, optional: true, aliases: map[string]string{"cerrar": "close"}, choices: []choice{{"close", app.Service.L("cerrar las pantallas abiertas", "close the open screens")}}}
		screens := &destStep{app.Service.L("Placas", "Devices"), app.Service.L("qué pantallas abrir en cuadrícula", "which screens to open in a grid")}
		if at(0) == "close" || at(0) == "cerrar" {
			return []param{closeIt}, nil
		}
		if m != nil && m.wall != nil {
			return []param{closeIt}, screens
		}
		return nil, screens
	case "sync":
		ps := []param{{name: app.Service.L("Acción", "Action"), help: app.Service.L("qué hacer con la réplica", "what to do with the replica"), kind: pChoice, strict: true, optional: m != nil && m.sync != nil, choices: []choice{{"start", app.Service.L("empezar: manejas una placa y las demás la imitan", "start: you control one device and the others copy it")}, {"pause", app.Service.L("pausar", "pause")}, {"resume", app.Service.L("reanudar", "resume")}, {"stop", app.Service.L("terminar", "stop")}, {"status", app.Service.L("ver el estado", "show the status")}}}}
		switch at(0) {
		case "start":
			return append(ps, param{name: app.Service.L("Principal", "Master"), help: app.Service.L("la placa que manejarás desde su ventana", "the device you will control from its window"), kind: pDevice}), &destStep{app.Service.L("Seguidoras", "Followers"), app.Service.L("las placas que repetirán lo que hagas en la principal", "the devices that repeat what you do on the master")}
		case "pause", "resume", "stop", "status":
			return append(ps, param{name: app.Service.L("Sesión", "Session"), help: app.Service.L("vacío: la réplica de esta ventana", "empty: this window's replica"), kind: pSession, optional: true}), nil
		}
		return ps, nil
	case "help":
		if len(prior) > 0 && prior[0] == "motion" {
			ps, _ := app.commandParams(m, "motion", nil)
			ps[0].optional = true
			return append([]param{{name: app.Service.L("Comando", "Command"), kind: pCommand}}, ps...), nil
		}
		return []param{{name: app.Service.L("Comando", "Command"), help: app.Service.L("vacío: todos los comandos", "empty: every command"), kind: pCommand, optional: true}}, nil
	}
	return nil, nil
}
func (app *Interface) routineVars(name string) []string {
	b, e := os.ReadFile(app.Service.RoutinesPath(filepath.Base(strings.TrimSuffix(name, ".json")) + ".json"))
	if e != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, p := range app.routineVarRe.FindAllStringSubmatch(string(b), -1) {
		// Built-in variables are filled in by each device, so they are never asked.
		if !seen[p[1]] && !slices.Contains(app.Service.BuiltinVars(), p[1]) {
			seen[p[1]] = true
			out = append(out, p[1])
		}
	}
	return out
}
func (app *Interface) choiceSuggestions(cs []choice) []suggestion {
	var out []suggestion
	for _, c := range cs {
		out = append(out, suggestion{value: c.value, label: c.value, detail: c.help})
	}
	return out
}

// paramSuggestions lists the likely answers for one argument.
func (m *tuiModel) paramSuggestions(p param, prior []string, cur string, form bool) []suggestion {
	switch p.kind {
	case pChoice, pCoord, pNumber, pAndroidPath, pText, pRest:
		out := m.app.choiceSuggestions(p.choices)
		if p.kind == pAndroidPath {
			for i := range out {
				out[i].noSpace = !form
			}
		}
		return out
	case pDuration:
		return m.app.choiceSuggestions([]choice{{"90s", m.app.Service.L("minuto y medio", "a minute and a half")}, {"3m", m.app.Service.L("tres minutos (por defecto)", "three minutes (default)")}, {"10m", m.app.Service.L("diez minutos", "ten minutes")}})
	case pPath:
		return m.app.pathSuggestions(cur, "")
	case pAPK:
		return m.app.pathSuggestions(cur, ".apk")
	case pDevice:
		return m.deviceSuggestions("", prior)
	case pGroup:
		return m.groupSuggestions("")
	case pRoutine:
		return m.app.routineSuggestions()
	case pRoutineVar:
		if form {
			return nil
		}
		return []suggestion{{value: p.key + "=", label: p.key + "=", detail: p.help, noSpace: true}}
	case pPackage:
		known := map[string]string{}
		for _, c := range m.app.commonPackages() {
			known[c.value] = c.help
		}
		var ps []string
		for k := range m.packages {
			ps = append(ps, k)
		}
		sort.Strings(ps)
		var out []suggestion
		for _, k := range ps {
			out = append(out, suggestion{value: k, label: k, detail: strings.TrimSpace(known[k] + m.app.Service.L(" · en las placas", " · on the devices"))})
		}
		for _, c := range m.app.commonPackages() {
			if !m.packages[c.value] {
				out = append(out, suggestion{value: c.value, label: c.value, detail: c.help})
			}
		}
		return out
	case pJob:
		return m.app.jobComplete(m, 0, nil, "")
	case pSession:
		return m.app.staticSuggestions(m.app.syncSessionIDs(), false)
	case pCommand:
		var names []string
		for _, c := range m.app.tuiRegistry {
			names = append(names, c.name)
		}
		return m.app.staticSuggestions(names, false)
	}
	return nil
}

// destSuggestions offers destinations without needing to know the @ syntax.
func (m *tuiModel) destSuggestions(form bool) []suggestion {
	connected := 0
	for _, d := range m.devices {
		if d.State == "device" {
			connected++
		}
	}
	var out []suggestion
	if form && !m.target.Empty() {
		out = append(out, suggestion{value: m.target.String(), label: m.target.String(), detail: m.app.Service.L("el destino fijo de la sesión", "the session's fixed target")})
	}
	out = append(out, suggestion{value: "@all", label: "@all", detail: fmt.Sprintf(m.app.Service.L("todas las conectadas (%d)", "every connected device (%d)"), connected)})
	out = append(out, m.groupSuggestions("@")...)
	if len(m.lastFailed) > 0 {
		v := "@" + strings.Join(m.lastFailed, ",")
		out = append(out, suggestion{value: v, label: m.app.Service.L("@fallidas", "@failed"), detail: fmt.Sprintf(m.app.Service.L("las que fallaron en el último lote (%d): %s", "the ones that failed in the last batch (%d): %s"), len(m.lastFailed), strings.Join(m.lastFailed, ", ")), keywords: "fallidas fallaron errores failed errors"})
	}
	out = append(out, suggestion{value: "", label: m.app.Service.L("elegir varias…", "choose several…"), detail: m.app.Service.L("abre la lista de placas para marcarlas", "opens the device list to mark them"), pick: true})
	out = append(out, m.deviceSuggestions("@", nil)...)
	return out
}

// checkParam validates and normalizes one answer; the error explains what is expected.
func (m *tuiModel) checkParam(p param, v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		if p.def != "" {
			return p.def, nil
		}
		if p.optional {
			return "", nil
		}
		return "", fmt.Errorf(m.app.Service.L("Falta %s: %s", "Missing %s: %s"), strings.ToLower(p.name), p.help)
	}
	switch p.kind {
	case pCoord:
		v = strings.ReplaceAll(v, ",", ".")
		f, e := strconv.ParseFloat(v, 64)
		if e != nil || f < 0 || f > 1 {
			return "", fmt.Errorf(m.app.Service.L("%s debe ser un número entre 0 y 1 (0.5 es el centro)", "%s must be a number between 0 and 1 (0.5 is the center)"), p.name)
		}
	case pNumber:
		n, e := strconv.Atoi(v)
		if e != nil || n < p.min || (p.max > 0 && n > p.max) {
			if p.max > 0 {
				return "", fmt.Errorf(m.app.Service.L("%s debe ser un número entero entre %d y %d", "%s must be a whole number between %d and %d"), p.name, p.min, p.max)
			}
			return "", fmt.Errorf(m.app.Service.L("%s debe ser un número entero (sin decimales)", "%s must be a whole number (no decimals)"), p.name)
		}
	case pDuration:
		if _, e := strconv.Atoi(v); e == nil {
			v += "s"
		}
		if d, e := time.ParseDuration(v); e != nil || d <= 0 {
			return "", fmt.Errorf("%s", m.app.Service.L("Duración no válida; ejemplos: 90s, 3m, 1m30s", "Invalid duration; examples: 90s, 3m, 1m30s"))
		}
	case pChoice:
		for _, c := range p.choices {
			if strings.EqualFold(c.value, v) || p.aliases[strings.ToLower(v)] == c.value {
				return c.value, nil
			}
		}
		if p.strict {
			var vs []string
			for _, c := range p.choices {
				vs = append(vs, c.value)
			}
			return "", fmt.Errorf(m.app.Service.L("Elige una opción: %s", "Pick one option: %s"), strings.Join(vs, ", "))
		}
		if p.numeric {
			if _, named := m.app.Service.KeyCodes()[strings.ToUpper(v)]; named {
				return strings.ToUpper(v), nil
			}
			if _, e := strconv.Atoi(v); e != nil {
				return "", fmt.Errorf("%s", m.app.Service.L("Tecla desconocida: usa HOME, BACK, POWER, VOLUME_UP… o un código numérico", "Unknown key: use HOME, BACK, POWER, VOLUME_UP… or a numeric code"))
			}
		}
	case pPath, pAPK:
		v = strings.Trim(v, `"`)
		st, e := os.Stat(v)
		// A folder of .apk files is a split APK, installed in one go.
		if apks, _ := filepath.Glob(filepath.Join(v, "*.apk")); p.kind == pAPK && e == nil && st.IsDir() && len(apks) > 0 {
			return v, nil
		}
		if e != nil || st.IsDir() {
			return "", fmt.Errorf(m.app.Service.L("No encuentro el archivo %s", "File not found: %s"), v)
		}
	case pDevice:
		d, ok := m.findDevice(v)
		if !ok {
			return "", fmt.Errorf(m.app.Service.L("Placa desconocida: %s", "Unknown device: %s"), v)
		}
		if d.Alias != "" {
			return d.Alias, nil
		}
		return d.Serial, nil
	case pGroup:
		if _, ok := m.groups[v]; !ok {
			return "", fmt.Errorf(m.app.Service.L("Grupo desconocido: %s", "Unknown group: %s"), v)
		}
	case pGroupNew:
		if strings.ContainsAny(v, ",@/\\ ") {
			return "", fmt.Errorf("%s", m.app.Service.L("El nombre no puede tener espacios, comas, @ ni barras", "The name cannot contain spaces, commas, @ or slashes"))
		}
		if _, ok := m.groups[v]; ok {
			return "", fmt.Errorf(m.app.Service.L("Ya existe el grupo %s", "The group %s already exists"), v)
		}
	case pAlias:
		if strings.ContainsAny(v, ",/\\") {
			return "", fmt.Errorf("%s", m.app.Service.L("El alias no puede tener comas ni barras", "The alias cannot contain commas or slashes"))
		}
	case pRoutine:
		v = strings.TrimSuffix(v, ".json")
		if _, e := os.Stat(m.app.Service.RoutinesPath(filepath.Base(v) + ".json")); e != nil {
			return "", fmt.Errorf(m.app.Service.L("No existe la rutina %s en routines/", "There is no routine %s in routines/"), v)
		}
	case pJob:
		if _, e := os.Stat(m.app.Service.JobPath(v)); e != nil {
			return "", fmt.Errorf(m.app.Service.L("No encuentro el lote %s", "Batch not found: %s"), v)
		}
	case pCommand:
		if m.app.lookupCommand(v) == nil {
			return "", m.app.unknownCommand(v)
		}
	}
	return v, nil
}

// arrowStep is how much an arrow changes a number being edited: ↑↓ one, ←→ ten.
func (app *Interface) arrowStep(s string) (int, bool) {
	switch s {
	case "up":
		return 1, true
	case "down":
		return -1, true
	case "right":
		return 10, true
	case "left":
		return -10, true
	}
	return 0, false
}

// stepInt adds step to a typed integer (base when it is not one), kept within lo and hi and never below 0;
// hi 0 means no upper limit.
func (app *Interface) stepInt(v string, base, step, lo, hi int) string {
	n, e := strconv.Atoi(strings.TrimSpace(v))
	if e != nil {
		n = base
	}
	n = max(n+step, lo, 0)
	if hi > 0 {
		n = min(n, hi)
	}
	return strconv.Itoa(n)
}

// stepDuration adds step seconds to a typed duration (a bare number counts as seconds), never below one second.
func (app *Interface) stepDuration(v string, base time.Duration, step int) string {
	v = strings.TrimSpace(v)
	if _, e := strconv.Atoi(v); e == nil {
		v += "s"
	}
	d, e := time.ParseDuration(v)
	if e != nil || d <= 0 {
		d = base
	}
	return max(d.Truncate(time.Second)+time.Duration(step)*time.Second, time.Second).String()
}

// quoteArg writes an answer back as it would be typed, quoting values with spaces.
func (app *Interface) quoteArg(v string) string {
	if v == "" || strings.ContainsAny(v, " \t'\"") {
		if strings.Contains(v, `"`) {
			return "'" + v + "'"
		}
		return `"` + v + `"`
	}
	return v
}
