package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Selection struct {
	Devices, Group string
	All            bool
}

func (s Selection) Empty() bool {
	return !s.All && s.Group == "" && s.Devices == ""
}
func (s Selection) String() string {
	switch {
	case s.All:
		return "@all"
	case s.Group != "":
		return "@" + s.Group
	case s.Devices != "":
		return "@" + s.Devices
	}
	return ""
}
func (app *Service) ResolveTargets(d []Device, s Selection) ([]Device, error) {
	n := 0
	if s.All {
		n++
	}
	if s.Group != "" {
		n++
	}
	if s.Devices != "" {
		n++
	}
	if n != 1 {
		return nil, errors.New(app.L("Indica exactamente uno: --devices, --group o --all", "Give exactly one: --devices, --group or --all"))
	}
	var ids []string
	if s.Group != "" {
		g, e := app.Groups()
		if e != nil {
			return nil, e
		}
		var ok bool
		ids, ok = g[s.Group]
		if !ok {
			return nil, errors.New(app.L("Grupo desconocido", "Unknown group"))
		}
	} else if s.Devices != "" {
		ids = strings.Split(s.Devices, ",")
	}
	seen := map[string]bool{}
	var result []Device
	if s.All {
		for _, v := range d {
			if v.State == "device" {
				result = append(result, v)
			}
		}
	} else {
		for _, id := range ids {
			found := false
			for _, v := range d {
				if v.Serial == strings.TrimSpace(id) || strings.EqualFold(v.Alias, strings.TrimSpace(id)) {
					found = true
					if !seen[v.Serial] {
						result = append(result, v)
						seen[v.Serial] = true
					}
					break
				}
			}
			if !found {
				return nil, fmt.Errorf(app.L("Dispositivo desconocido: %s", "Unknown device: %s"), id)
			}
		}
	}
	if len(result) == 0 {
		return nil, errors.New(app.L("Seleccion vacia", "Empty selection"))
	}
	return result, nil
}

type BatchOptions struct {
	Parallel int
	Timeout  time.Duration
	OnStart  func(i int)
	OnStep   func(i, step int)
	OnRetry  func(i, step, attempt int)
	OnResult func(i int, r Result)
}

// BuiltinVars are the ${names} any step can use; each device fills them in when the batch runs.
func (app *Service) BuiltinVars() []string {
	return []string{"alias", "serial", "model", "index"}
}
func (app *Service) withoutBuiltinVars(s string) string {
	for _, v := range app.BuiltinVars() {
		s = strings.ReplaceAll(s, "${"+v+"}", "")
	}
	return s
}

// expandSpec returns a copy of s for one device: the steps are shared by every worker.
func (app *Service) expandSpec(s Spec, d Device, index int) Spec {
	r := strings.NewReplacer("${alias}", d.Alias, "${serial}", d.Serial, "${model}", d.Model, "${index}", strconv.Itoa(index))
	args := make([]string, len(s.Args))
	for i, a := range s.Args {
		args[i] = r.Replace(a)
	}
	s.Args = args
	return s
}

// runStep runs one step with its own timeout, repeating it after a growing pause while attempts remain.
func (app *Service) runStep(c context.Context, d Device, s Spec, o BatchOptions, i, n int, r *Result) (string, error) {
	limit := o.Timeout
	if s.Timeout != "" {
		limit, _ = time.ParseDuration(s.Timeout)
	}
	pause := app.RetryPause
	for attempt := 0; ; attempt++ {
		sc, cancel := context.WithTimeout(c, limit)
		out, e := app.ExecuteStep(sc, d, s)
		cancel()
		if e == nil || attempt >= s.Retry || c.Err() != nil {
			return out, e
		}
		r.Retries++
		if o.OnRetry != nil {
			o.OnRetry(i, n, attempt+1)
		}
		select {
		case <-time.After(pause):
		case <-c.Done():
			return out, e
		}
		pause = min(pause*2, 30*time.Second)
	}
}

// prepareBatch validates the whole batch before any device is touched and makes local paths absolute.
func (app *Service) PrepareBatch(steps []Spec, o BatchOptions) error {
	if o.Parallel < 1 || o.Parallel > 64 {
		return errors.New(app.L("Concurrencia: debe ser 1..64 dispositivos (/settings)", "Concurrency: must be 1..64 devices (/settings)"))
	}
	if o.Timeout <= 0 {
		return errors.New(app.L("Timeout no valido", "Invalid timeout"))
	}
	if len(steps) == 0 {
		return errors.New(app.L("Rutina vacia", "Empty routine"))
	}
	for i, s := range steps {
		if e := app.ValidateSpec(s); e != nil {
			return e
		}
		if s.Action == "apk-install" || s.Action == "app-install" || s.Action == "push" {
			p, e := filepath.Abs(s.Args[0])
			if e != nil {
				return e
			}
			// A path with ${alias} or similar names a different file per device; it is checked when it runs.
			if app.withoutBuiltinVars(p) == p {
				if _, e = os.Stat(p); e != nil {
					return e
				}
			}
			steps[i].Args[0] = p
		}
	}
	return nil
}
func (app *Service) ExecuteJob(c context.Context, target []Device, steps []Spec, o BatchOptions) (Job, error) {
	j := Job{ID: time.Now().Format("20060102-150405.000000000"), Started: time.Now().Format(time.RFC3339), Steps: steps, Results: make([]Result, len(target))}
	queue := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < o.Parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				d := target[i]
				if o.OnStart != nil {
					o.OnStart(i)
				}
				r := Result{Serial: d.Serial, Alias: d.Alias, Platform: d.OS(), OK: true}
				start := time.Now()
				if d.State == iosDeveloperOff {
					r.OK = false
					r.Error = app.L("Modo de desarrollador desactivado: activalo en Ajustes > Privacidad y seguridad > Modo de desarrollador", "Developer Mode is off: enable it in Settings > Privacy & Security > Developer Mode")
				} else if d.State == iosOutdated {
					r.OK = false
					r.Error = fmt.Sprintf(app.L("Se requiere iOS %d o posterior (tiene iOS %s): actualiza el dispositivo", "iOS %d or later required (device has iOS %s): update the device"), iosMinimumMajor, d.OSVersion)
				} else if d.State == iosLocked {
					r.OK = false
					r.Error = app.L("iPhone bloqueado: desbloquéalo (se comprueba solo cada 30 s)", "iPhone locked: unlock it (checked again every 30 s)")
				} else if d.State != "device" {
					r.OK = false
					r.Error = app.L("Dispositivo no autorizado/conectado: ", "Device not authorized/connected: ") + d.State
				} else if e := app.checkDeviceStepsAt(d, steps, i+1); e != nil {
					r.OK = false
					r.Error = e.Error()
					r.ErrorCode = "incompatible"
				} else {
					for n, s := range steps {
						if c.Err() != nil {
							r.OK = false
							r.Error = app.L("Cancelado", "Canceled")
							break
						}
						if o.OnStep != nil {
							o.OnStep(i, n)
						}
						s = app.expandSpec(s, d, i+1)
						out, e := app.runStep(c, d, s, o, i, n, &r)
						r.Output += out
						if e == nil {
							continue
						}
						if c.Err() != nil {
							r.OK = false
							r.Error = app.L("Cancelado", "Canceled")
							break
						}
						// onError "continue" keeps the device going: the failure is noted as a warning.
						if s.OnError == "continue" {
							r.Warnings++
							r.Output += fmt.Sprintf(app.L("! paso %d (%s): %s\n", "! step %d (%s): %s\n"), n+1, s.Action, strings.TrimSpace(e.Error()))
							continue
						}
						r.OK = false
						r.Error = e.Error()
						break
					}
				}
				r.DurationMS = time.Since(start).Milliseconds()
				j.Results[i] = r
				if o.OnResult != nil {
					o.OnResult(i, r)
				}
			}
		}()
	}
	for i := range target {
		queue <- i
	}
	close(queue)
	wg.Wait()
	return j, app.SaveJob(j)
}
func (j Job) Failed() int {
	n := 0
	for _, r := range j.Results {
		if !r.OK {
			n++
		}
	}
	return n
}

// listJobs returns the most recent saved jobs first; IDs are timestamps, so name order is time order.
func (app *Service) ListJobs(limit int) ([]Job, error) {
	files, e := filepath.Glob(app.JobsPath("*.json"))
	if e != nil {
		return nil, e
	}
	sort.Sort(sort.Reverse(sort.StringSlice(files)))
	var out []Job
	for _, f := range files {
		if limit > 0 && len(out) >= limit {
			break
		}
		var j Job
		if app.ReadJSON(f, &j) == nil {
			if j.ID == "" {
				j.ID = strings.TrimSuffix(filepath.Base(f), ".json")
			}
			out = append(out, j)
		}
	}
	return out, nil
}

// liveDevices merges the live ADB list with the inventory without writing it or taking the lock.
func (app *Service) LiveDevices(parent context.Context) ([]Device, error) {
	c, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	live, issues := app.discoverLive(c)
	d, e := app.LoadInventory(app.InventoryPath())
	if e != nil {
		return nil, e
	}
	return app.mergeDiscovered(d, live, issues, time.Now().Format(time.RFC3339))
}

// validGroupName rejects names that a target (@name, lists with commas) could not use.
func (app *Service) validGroupName(name string) error {
	if strings.TrimSpace(name) == "" || strings.ContainsAny(name, ",@/\\\r\n ") {
		return errors.New(app.L("Nombre de grupo no valido", "Invalid group name"))
	}
	return nil
}
func (app *Service) CreateGroup(name string, ids []string) error {
	if e := app.validGroupName(name); e != nil {
		return e
	}
	unlock, e := app.LockInventory()
	if e != nil {
		return e
	}
	defer unlock()
	x, e := app.Groups()
	if e != nil {
		return e
	}
	if _, ok := x[name]; ok {
		return errors.New(app.L("El grupo ya existe", "The group already exists"))
	}
	x[name] = ids
	return app.WriteJSON(app.GroupsPath(), x)
}
func (app *Service) SetGroup(name string, ids []string) (map[string][]string, error) {
	unlock, e := app.LockInventory()
	if e != nil {
		return nil, e
	}
	defer unlock()
	x, e := app.Groups()
	if e != nil {
		return nil, e
	}
	if _, ok := x[name]; !ok {
		return nil, errors.New(app.L("Grupo desconocido", "Unknown group"))
	}
	x[name] = ids
	e = app.WriteJSON(app.GroupsPath(), x)
	return x, e
}

// RenameGroup gives a group a new name; its devices stay the same.
func (app *Service) RenameGroup(old, name string) error {
	if e := app.validGroupName(name); e != nil {
		return e
	}
	unlock, e := app.LockInventory()
	if e != nil {
		return e
	}
	defer unlock()
	x, e := app.Groups()
	if e != nil {
		return e
	}
	ids, ok := x[old]
	if !ok {
		return errors.New(app.L("Grupo desconocido", "Unknown group"))
	}
	if old == name {
		return nil
	}
	if _, ok := x[name]; ok {
		return errors.New(app.L("Ya hay un grupo con ese nombre", "There is already a group with that name"))
	}
	x[name] = ids
	delete(x, old)
	return app.WriteJSON(app.GroupsPath(), x)
}
func (app *Service) DeleteGroup(name string) error {
	unlock, e := app.LockInventory()
	if e != nil {
		return e
	}
	defer unlock()
	x, e := app.Groups()
	if e != nil {
		return e
	}
	delete(x, name)
	return app.WriteJSON(app.GroupsPath(), x)
}
func (app *Service) RenameDevice(id, alias string) (Device, error) {
	if strings.ContainsAny(alias, ",/\\\r\n") || strings.TrimSpace(alias) == "" {
		return Device{}, errors.New(app.L("Alias no valido", "Invalid alias"))
	}
	all, e := app.Discover()
	if e != nil {
		return Device{}, e
	}
	unlock, e := app.LockInventory()
	if e != nil {
		return Device{}, e
	}
	defer unlock()
	found := -1
	for i, v := range all {
		if strings.EqualFold(v.Alias, alias) && v.Serial != id && !strings.EqualFold(v.Alias, id) {
			return Device{}, errors.New(app.L("Alias repetido", "Alias already in use"))
		}
		if v.Serial == id || strings.EqualFold(v.Alias, id) {
			found = i
		}
	}
	if found < 0 {
		return Device{}, errors.New(app.L("Dispositivo desconocido", "Unknown device"))
	}
	all[found].Alias = alias
	if e = app.WriteJSON(app.InventoryPath(), all); e != nil {
		return Device{}, e
	}
	return all[found], nil
}
