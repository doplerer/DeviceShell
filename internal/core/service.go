package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type Device struct {
	Serial, Alias, Model, State, FirstSeen, LastSeen string
	Platform                                         string `json:"platform,omitempty"`
	OSVersion                                        string `json:"osVersion,omitempty"`
	// Transport is how an iPhone is connected now: "usb" or "wifi".
	Transport string `json:"transport,omitempty"`
}
type Spec struct {
	RoutinePlatform string   `json:"routinePlatform,omitempty"`
	Action          string   `json:"action"`
	Args            []string `json:"args"`
	X, Y, X2, Y2    float64
	Duration        int `json:"duration,omitempty"`
	// Error policy of the step: extra attempts, "continue" to go on after a failure, and its own timeout.
	Retry   int    `json:"retry,omitempty"`
	OnError string `json:"onError,omitempty"`
	Timeout string `json:"timeout,omitempty"`
}
type Result struct {
	Platform   string `json:"platform,omitempty"`
	ErrorCode  string `json:"errorCode,omitempty"`
	Serial     string `json:"serial"`
	Alias      string `json:"alias"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	Output     string `json:"output,omitempty"`
	DurationMS int64  `json:"durationMs"`
	Retries    int    `json:"retries,omitempty"`
	Warnings   int    `json:"warnings,omitempty"`
}
type Job struct {
	Version int      `json:"version,omitempty"`
	ID      string   `json:"id"`
	Started string   `json:"started"`
	Steps   []Spec   `json:"steps"`
	Results []Result `json:"results"`
}

func (app *Service) ReadJSON(p string, v any) error {
	b, e := os.ReadFile(p)
	if e != nil {
		return e
	}
	return json.Unmarshal([]byte(strings.TrimPrefix(string(b), "\ufeff")), v)
}
func (app *Service) WriteJSON(p string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(p), 0755); e != nil {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if e != nil {
		return e
	}
	n := f.Name()
	defer os.Remove(n)
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(n, p)
}
func (app *Service) Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
func (app *Service) Command(c context.Context, args ...string) (string, error) {
	if out, ok, e := app.adbNative(c, args); ok {
		return out, e
	}
	cmd := exec.CommandContext(c, app.Adb, args...)
	b, e := cmd.CombinedOutput()
	if e != nil {
		return string(b), fmt.Errorf("ADB: %w: %s", e, strings.TrimSpace(string(b)))
	}
	return string(b), nil
}
func (app *Service) Remote(c context.Context, serial string, args ...string) (string, error) {
	var q []string
	for _, a := range args {
		q = append(q, app.Quote(a))
	}
	return app.Command(c, "-s", serial, "shell", strings.Join(q, " "))
}
func (app *Service) LockInventory() (func(), error) {
	p := app.DataPath(".inventory.lock")
	end := time.Now().Add(10 * time.Second)
	for {
		e := os.Mkdir(p, 0755)
		if e == nil {
			return func() {
				os.Remove(p)
			}, nil
		}
		if !os.IsExist(e) {
			return nil, e
		}
		if time.Now().After(end) {
			return nil, errors.New(app.L("Inventario ocupado; revisa data/.inventory.lock si hubo un cierre inesperado", "Inventory busy; check data/.inventory.lock after an unexpected shutdown"))
		}
		time.Sleep(50 * time.Millisecond)
	}
}
func (app *Service) ParseDevices(out string) []Device {
	var d []Device
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] == "List" || strings.HasPrefix(f[0], "*") {
			continue
		}
		switch f[1] {
		case "device", "unauthorized", "offline", "recovery", "sideload", "bootloader":
		default:
			continue
		}
		v := Device{Serial: f[0], State: f[1], Platform: "android"}
		for _, x := range f[2:] {
			if strings.HasPrefix(x, "model:") {
				v.Model = strings.TrimPrefix(x, "model:")
			}
		}
		d = append(d, v)
	}
	return d
}
func (app *Service) Discover() ([]Device, error) {
	c, cancel := context.WithTimeout(app.Ctx, 30*time.Second)
	defer cancel()
	live, issues := app.discoverLive(c)
	unlock, e := app.LockInventory()
	if e != nil {
		return nil, e
	}
	defer unlock()
	p := app.InventoryPath()
	d, e := app.LoadInventory(p)
	if e != nil {
		return nil, e
	}
	merged, e := app.mergeDiscovered(d, live, issues, time.Now().Format(time.RFC3339))
	if e != nil {
		return nil, e
	}
	return merged, app.WriteJSON(p, merged)
}
func (app *Service) Groups() (map[string][]string, error) {
	g := map[string][]string{}
	e := app.ReadJSON(app.GroupsPath(), &g)
	if os.IsNotExist(e) {
		e = nil
	}
	return g, e
}
func (app *Service) CoordinateSpec(action string, a []string) (Spec, error) {
	v := []float64{}
	n := 2
	if action == "swipe" {
		n = 4
	}
	for _, s := range a[:n] {
		f, e := strconv.ParseFloat(s, 64)
		if e != nil || f < 0 || f > 1 {
			return Spec{}, errors.New(app.L("Las coordenadas deben estar entre 0 y 1", "Coordinates must be between 0 and 1"))
		}
		v = append(v, f)
	}
	s := Spec{Action: action, X: v[0], Y: v[1], Duration: 300}
	if n == 4 {
		s.X2 = v[2]
		s.Y2 = v[3]
		if len(a) == 5 {
			d, e := strconv.Atoi(a[4])
			if e != nil || d < 1 {
				return s, errors.New(app.L("Duracion no valida", "Invalid duration"))
			}
			s.Duration = d
		}
	}
	return s, nil
}
func (app *Service) LoadRoutine(a []string) ([]Spec, error) {
	p := a[0]
	if !strings.ContainsAny(p, "/\\") && !strings.HasSuffix(p, ".json") {
		p = app.RoutinesPath(p + ".json")
	}
	b, e := os.ReadFile(p)
	if e != nil {
		return nil, e
	}
	var params = map[string]string{}
	for _, x := range a[1:] {
		kv := strings.SplitN(x, "=", 2)
		if len(kv) != 2 {
			return nil, errors.New(app.L("Parametro esperado: clave=valor", "Expected parameter: key=value"))
		}
		if slices.Contains(app.BuiltinVars(), kv[0]) {
			return nil, fmt.Errorf(app.L("${%s} es una variable integrada; usa otro nombre", "${%s} is a built-in variable; use another name"), kv[0])
		}
		params[kv[0]] = kv[1]
	}
	var r struct {
		Platform string `json:"platform"`
		Steps    []Spec `json:"steps"`
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return nil, e
	}
	if r.Platform == "" {
		r.Platform = "android"
	}
	if r.Platform != "android" && r.Platform != "ios" && r.Platform != "both" {
		return nil, errors.New("platform: usa android, ios o both")
	}
	for i := range r.Steps {
		r.Steps[i].RoutinePlatform = r.Platform
		for j, s := range r.Steps[i].Args {
			for k, v := range params {
				s = strings.ReplaceAll(s, "${"+k+"}", v)
			}
			// Built-in variables stay in place: each device fills them in when the batch runs.
			if strings.Contains(app.withoutBuiltinVars(s), "${") {
				return nil, errors.New(app.L("Falta un parametro de rutina", "A routine parameter is missing"))
			}
			r.Steps[i].Args[j] = s
		}
	}
	return r.Steps, nil
}
func (app *Service) JobPath(id string) string {
	return app.JobsPath(filepath.Base(id) + ".json")
}
