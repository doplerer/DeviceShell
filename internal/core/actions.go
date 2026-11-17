package core

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/doplerer/deviceshell/internal/android/adb"
)

// ActionArity is the number of arguments of every action; reports keep only these action names.
func (app *Service) ActionArity() map[string]int {
	return map[string]int{"app-install": 1, "app-uninstall": 1, "apk-install": 1, "apk-uninstall": 1, "app-list": 0, "app-start": 1, "app-stop": 1, "app-clear": 1, "url": 1, "text": 1, "key": 1, "push": 2, "pull": 1, "shell": 1, "screenshot": 0, "screenrecord": 1, "logcat": 1, "wait": 1, "tap": 0, "swipe": 0,
		"reboot": 1, "perm-grant": 2, "perm-revoke": 2, "setting": 3, "wifi": 1, "rotation": 1, "resolution": 2}
}

// ActionChoices are the closed values of the first argument of some actions.
func (app *Service) ActionChoices() map[string][]string {
	return map[string][]string{"reboot": {"system", "recovery", "bootloader"}, "wifi": {"on", "off"}, "rotation": {"auto", "0", "90", "180", "270"}, "setting": {"system", "secure", "global"}}
}

// KeyCodes are the key names "key" understands; any Android key code number works too.
func (app *Service) KeyCodes() map[string]string {
	return map[string]string{"HOME": "3", "BACK": "4", "ENTER": "66", "RECENTS": "187", "WAKEUP": "224", "POWER": "26", "VOLUME_UP": "24", "VOLUME_DOWN": "25", "MENU": "82", "TAB": "61", "DEL": "67", "CAMERA": "27"}
}

func (app *Service) ValidateSpec(s Spec) error {
	if s.RoutinePlatform != "" && s.RoutinePlatform != "android" && s.RoutinePlatform != "ios" && s.RoutinePlatform != "both" {
		return errors.New("routinePlatform: usa android, ios o both")
	}
	v, ok := app.ActionArity()[s.Action]
	if !ok || len(s.Args) != v {
		return fmt.Errorf(app.L("Accion/argumentos no validos: %s", "Invalid action/arguments: %s"), s.Action)
	}
	if c, ok := app.ActionChoices()[s.Action]; ok && !slices.Contains(c, strings.ToLower(s.Args[0])) {
		return fmt.Errorf(app.L("%s: usa %s", "%s: use %s"), s.Action, strings.Join(c, ", "))
	}
	if s.Action == "screenrecord" || s.Action == "logcat" {
		limit := map[string]int{"screenrecord": 180, "logcat": 100000}[s.Action]
		if v, e := strconv.Atoi(s.Args[0]); e != nil || v < 1 || v > limit {
			return fmt.Errorf(app.L("%s: un número entre 1 y %d", "%s: a number between 1 and %d"), s.Action, limit)
		}
	}
	if s.Action == "resolution" {
		if size := strings.ToLower(s.Args[0]); size != "show" && size != "reset" && !resolutionFormat.MatchString(size) {
			return errors.New(app.L("resolution: usa ANCHOxALTO (p. ej. 1080x1920), reset o show", "resolution: use WIDTHxHEIGHT (e.g. 1080x1920), reset or show"))
		}
		if dpi := strings.ToLower(s.Args[1]); dpi != "keep" && dpi != "reset" {
			if v, e := strconv.Atoi(dpi); e != nil || v < 72 || v > 1000 {
				return errors.New(app.L("resolution: densidad entre 72 y 1000, keep o reset", "resolution: density between 72 and 1000, keep or reset"))
			}
		}
	}
	if s.Retry < 0 || s.Retry > 10 {
		return errors.New(app.L("retry: entre 0 y 10 reintentos", "retry: between 0 and 10 retries"))
	}
	if s.OnError != "" && s.OnError != "abort" && s.OnError != "continue" {
		return errors.New(app.L("onError: usa abort o continue", "onError: use abort or continue"))
	}
	if s.Timeout != "" {
		if d, e := time.ParseDuration(s.Timeout); e != nil || d <= 0 {
			return errors.New(app.L("timeout del paso no valido; ejemplos: 30s, 2m", "Invalid step timeout; examples: 30s, 2m"))
		}
	}
	if s.Action == "tap" || s.Action == "swipe" {
		for _, x := range []float64{s.X, s.Y, s.X2, s.Y2} {
			if math.IsNaN(x) || math.IsInf(x, 0) || x < 0 || x > 1 {
				return errors.New(app.L("Coordenadas fuera de 0..1", "Coordinates outside 0..1"))
			}
		}
	}
	if s.Action == "wait" {
		v, e := strconv.Atoi(s.Args[0])
		if e != nil || v < 0 {
			return errors.New(app.L("Espera invalida", "Invalid wait"))
		}
	}
	return nil
}

// normalizeURL adds https:// to bare addresses: Android resolves no app for "google.com".
func (app *Service) NormalizeURL(u string) string {
	u = strings.TrimSpace(u)
	if strings.Contains(u, "://") {
		return u
	}
	l := strings.ToLower(u)
	for _, p := range []string{"mailto:", "tel:", "sms:", "geo:", "intent:", "market:"} {
		if strings.HasPrefix(l, p) {
			return u
		}
	}
	return "https://" + u
}
func (app *Service) Dimensions(c context.Context, serial string) (int, int, error) {
	out, e := app.Remote(c, serial, "wm", "size")
	if e != nil {
		return 0, 0, e
	}
	m := regexp.MustCompile(`(\d+)x(\d+)`).FindAllStringSubmatch(out, -1)
	if len(m) == 0 {
		return 0, 0, errors.New(app.L("No se puede leer la resolucion", "Cannot read the screen resolution"))
	}
	last := m[len(m)-1]
	w, _ := strconv.Atoi(last[1])
	h, _ := strconv.Atoi(last[2])
	rot, _ := app.Remote(c, serial, "dumpsys", "input")
	r := regexp.MustCompile(`SurfaceOrientation:\s*([0-3])`).FindStringSubmatch(rot)
	if len(r) > 1 && (r[1] == "1" || r[1] == "3") {
		w, h = h, w
	}
	return w, h, nil
}
func (app *Service) executeAndroid(c context.Context, d Device, s Spec) (string, error) {
	switch s.Action {
	case "apk-install":
		// A folder holds the parts of a split APK, installed together.
		if st, e := os.Stat(s.Args[0]); e == nil && st.IsDir() {
			apks, _ := filepath.Glob(filepath.Join(s.Args[0], "*.apk"))
			if len(apks) == 0 {
				return "", errors.New(app.L("La carpeta no contiene archivos .apk", "The folder has no .apk files"))
			}
			return app.Command(c, append([]string{"-s", d.Serial, "install-multiple", "-r"}, apks...)...)
		}
		return app.Command(c, "-s", d.Serial, "install", "-r", s.Args[0])
	case "apk-uninstall":
		return app.Command(c, "-s", d.Serial, "uninstall", s.Args[0])
	case "app-list":
		return app.Remote(c, d.Serial, "pm", "list", "packages", "-3")
	case "app-start":
		return app.Remote(c, d.Serial, "monkey", "-p", s.Args[0], "-c", "android.intent.category.LAUNCHER", "1")
	case "app-stop":
		return app.Remote(c, d.Serial, "am", "force-stop", s.Args[0])
	case "app-clear":
		return app.Remote(c, d.Serial, "pm", "clear", s.Args[0])
	case "perm-grant", "perm-revoke":
		return app.Remote(c, d.Serial, "pm", strings.TrimPrefix(s.Action, "perm-"), s.Args[0], s.Args[1])
	case "setting":
		return app.Remote(c, d.Serial, "settings", "put", strings.ToLower(s.Args[0]), s.Args[1], s.Args[2])
	case "wifi":
		state := "enable"
		if strings.EqualFold(s.Args[0], "off") {
			state = "disable"
		}
		return app.Remote(c, d.Serial, "svc", "wifi", state)
	case "resolution":
		size, dpi := strings.ToLower(s.Args[0]), strings.ToLower(s.Args[1])
		if size != "show" {
			if out, e := app.Remote(c, d.Serial, "wm", "size", size); e != nil {
				return out, e
			}
		}
		if dpi != "keep" {
			if out, e := app.Remote(c, d.Serial, "wm", "density", dpi); e != nil {
				return out, e
			}
		}
		st, e := app.ReadDisplay(c, d.Serial)
		if e != nil {
			return "", e
		}
		out := st.String()
		if st.SizeOverride || st.DensityOverride {
			out += fmt.Sprintf(app.L(" (física %d×%d · %d dpi)", " (physical %d×%d · %d dpi)"), st.PhysW, st.PhysH, st.PhysDensity)
		}
		return out, nil
	case "rotation":
		if strings.EqualFold(s.Args[0], "auto") {
			return app.Remote(c, d.Serial, "settings", "put", "system", "accelerometer_rotation", "1")
		}
		if out, e := app.Remote(c, d.Serial, "settings", "put", "system", "accelerometer_rotation", "0"); e != nil {
			return out, e
		}
		deg, _ := strconv.Atoi(s.Args[0])
		return app.Remote(c, d.Serial, "settings", "put", "system", "user_rotation", strconv.Itoa(deg/90))
	case "reboot":
		a := []string{"-s", d.Serial, "reboot"}
		if mode := strings.ToLower(s.Args[0]); mode != "system" {
			a = append(a, mode)
		}
		return app.Command(c, a...)
	case "pull":
		dir := app.DownloadsPath(d.Alias)
		if e := os.MkdirAll(dir, 0755); e != nil {
			return "", e
		}
		if out, e := app.Command(c, "-s", d.Serial, "pull", s.Args[0], dir); e != nil {
			return out, e
		}
		return filepath.Join(dir, path.Base(strings.TrimRight(s.Args[0], "/"))) + "\n", nil
	case "logcat":
		out, e := app.Command(c, "-s", d.Serial, "logcat", "-d", "-t", s.Args[0])
		if e != nil {
			return "", e
		}
		p := app.LogcatPath(d.Alias + "-" + time.Now().Format("20060102-150405.000000000") + ".txt")
		if e = os.MkdirAll(filepath.Dir(p), 0755); e == nil {
			e = os.WriteFile(p, []byte(out), 0644)
		}
		return p + "\n", e
	case "screenrecord":
		// Android records to a file on the phone; it is brought to output/screenshots and removed there.
		tmp := "/data/local/tmp/deviceshell-" + time.Now().Format("20060102-150405") + ".mp4"
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			app.Remote(cleanup, d.Serial, "rm", "-f", tmp)
		}()
		if out, e := app.Remote(c, d.Serial, "screenrecord", "--time-limit", s.Args[0], tmp); e != nil {
			return out, e
		}
		p := app.CapturesPath(d.Alias + "-" + time.Now().Format("20060102-150405.000000000") + ".mp4")
		if out, e := app.Command(c, "-s", d.Serial, "pull", tmp, p); e != nil {
			return out, e
		}
		return p + "\n", nil
	case "url":
		u := app.NormalizeURL(s.Args[0])
		out, e := app.Remote(c, d.Serial, "am", "start", "-a", "android.intent.action.VIEW", "-d", u)
		if e != nil && strings.Contains(out, "unable to resolve Intent") {
			return out, fmt.Errorf(app.L("Ninguna app del móvil abre %s (¿hay navegador instalado?)", "No app on the phone opens %s (is a browser installed?)"), u)
		}
		return out, e
	case "push":
		return app.Command(c, "-s", d.Serial, "push", s.Args[0], s.Args[1])
	case "shell":
		return app.Command(c, "-s", d.Serial, "shell", s.Args[0])
	case "wait":
		ms, _ := strconv.Atoi(s.Args[0])
		select {
		case <-time.After(time.Duration(ms) * time.Millisecond):
			return "", nil
		case <-c.Done():
			return "", c.Err()
		}
	case "key":
		k := strings.ToUpper(s.Args[0])
		if v, ok := app.KeyCodes()[k]; ok {
			k = v
		}
		if _, e := strconv.Atoi(k); e != nil {
			return "", errors.New(app.L("Tecla desconocida; usa HOME, BACK, ENTER, RECENTS, WAKEUP, POWER, VOLUME_UP, VOLUME_DOWN, MENU, TAB, DEL, CAMERA o codigo numerico", "Unknown key; use HOME, BACK, ENTER, RECENTS, WAKEUP, POWER, VOLUME_UP, VOLUME_DOWN, MENU, TAB, DEL, CAMERA or a numeric code"))
		}
		return app.Remote(c, d.Serial, "input", "keyevent", k)
	case "tap", "swipe":
		w, h, e := app.Dimensions(c, d.Serial)
		if e != nil {
			return "", e
		}
		a := []string{"input", s.Action, strconv.Itoa(int(s.X * float64(w-1))), strconv.Itoa(int(s.Y * float64(h-1)))}
		if s.Action == "swipe" {
			duration := s.Duration
			if duration <= 0 {
				duration = 300
			}
			a = append(a, strconv.Itoa(int(s.X2*float64(w-1))), strconv.Itoa(int(s.Y2*float64(h-1))), strconv.Itoa(duration))
		}
		return app.Remote(c, d.Serial, a...)
	case "text":
		p, e := app.OpenPeer(c, d.Serial)
		if e != nil {
			return "", e
		}
		defer p.Close()
		b := make([]byte, 14+len(s.Args[0]))
		b[0] = 9
		b[9] = 1
		binary.BigEndian.PutUint32(b[10:14], uint32(len(s.Args[0])))
		copy(b[14:], s.Args[0])
		if len(b) > 262144 {
			return "", errors.New(app.L("Texto demasiado largo", "Text too long"))
		}
		_, e = p.conn.Write(b)
		if e == nil {
			select {
			case <-time.After(200 * time.Millisecond):
			case <-c.Done():
				e = c.Err()
			}
		}
		return app.L("Texto enviado por portapapeles UTF-8\n", "Text sent through the UTF-8 clipboard\n"), e
	case "screenshot":
		p := app.CapturesPath(d.Alias + "-" + time.Now().Format("20060102-150405.000000000") + ".png")
		f, e := os.Create(p)
		if e != nil {
			return "", e
		}
		var stderr bytes.Buffer
		ok, e := adb.ExecOut(c, d.Serial, "screencap -p", f)
		if !ok {
			cmd := exec.CommandContext(c, app.Adb, "-s", d.Serial, "exec-out", "screencap", "-p")
			cmd.Stdout = f
			cmd.Stderr = &stderr
			e = cmd.Run()
		}
		f.Close()
		if e != nil {
			os.Remove(p)
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				e = fmt.Errorf("%w: %s", e, msg)
			}
		}
		return p + "\n", e
	}
	return "", errors.New(app.L("Accion no implementada", "Action not implemented"))
}
