package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func (app *Service) testNewActionsValidation(t *testing.T) {
	good := []Spec{
		{Action: "apk-uninstall", Args: []string{"com.x"}}, {Action: "app-clear", Args: []string{"com.x"}}, {Action: "pull", Args: []string{"/sdcard/a.txt"}},
		{Action: "reboot", Args: []string{"recovery"}}, {Action: "perm-grant", Args: []string{"com.x", "android.permission.CAMERA"}},
		{Action: "setting", Args: []string{"System", "screen_brightness", "10"}}, {Action: "wifi", Args: []string{"OFF"}}, {Action: "rotation", Args: []string{"90"}},
		{Action: "screenrecord", Args: []string{"180"}}, {Action: "logcat", Args: []string{"500"}},
		{Action: "tap", Retry: 10, OnError: "continue", Timeout: "30s"},
	}
	for _, s := range good {
		if e := app.ValidateSpec(s); e != nil {
			t.Errorf("%s %v: %v", s.Action, s.Args, e)
		}
	}
	bad := []Spec{
		{Action: "reboot", Args: []string{"fastboot"}}, {Action: "wifi", Args: []string{"maybe"}}, {Action: "rotation", Args: []string{"45"}},
		{Action: "setting", Args: []string{"vendor", "a", "b"}}, {Action: "screenrecord", Args: []string{"181"}}, {Action: "logcat", Args: []string{"0"}},
		{Action: "perm-grant", Args: []string{"com.x"}}, {Action: "tap", Retry: 11}, {Action: "tap", OnError: "ignore"}, {Action: "tap", Timeout: "-1s"},
	}
	for _, s := range bad {
		if app.ValidateSpec(s) == nil {
			t.Errorf("accepted %s %v retry=%d onError=%q timeout=%q", s.Action, s.Args, s.Retry, s.OnError, s.Timeout)
		}
	}
	for name, code := range map[string]string{"POWER": "26", "VOLUME_UP": "24", "VOLUME_DOWN": "25", "DEL": "67"} {
		if app.KeyCodes()[name] != code {
			t.Errorf("key %s", name)
		}
	}
	if s := app.SummarizeJob(Job{Steps: []Spec{{Action: "pull", Args: []string{"/sdcard/secreto"}}, {Action: "rm-rf"}}, Results: []Result{{Alias: "DEV-001", OK: true, Output: "x", Retries: 2, Warnings: 1}}}); s.Steps[0].Action != "pull" || s.Steps[0].Args != nil || s.Steps[1].Action != "unknown" || s.Results[0].Retries != 2 || s.Results[0].Warnings != 1 || s.Results[0].Output != "" {
		t.Fatal(s)
	}
}

// batchFixture runs steps on fake devices; fail decides, per call, whether a step fails.
func (app *Service) batchFixture(t *testing.T, fail func(d Device, s Spec, call int) error) (*[]Spec, func(c context.Context, devices []Device, steps []Spec) Job) {
	t.Helper()
	app.Root = t.TempDir()
	app.RetryPause = time.Millisecond
	var mu sync.Mutex
	var seen []Spec
	calls := map[string]int{}
	app.ExecuteStep = func(c context.Context, d Device, s Spec) (string, error) {
		mu.Lock()
		seen = append(seen, s)
		calls[d.Serial]++
		n := calls[d.Serial]
		mu.Unlock()
		return "ok\n", fail(d, s, n)
	}
	t.Cleanup(func() { app.ExecuteStep = app.Execute })
	return &seen, func(c context.Context, devices []Device, steps []Spec) Job {
		j, e := app.ExecuteJob(c, devices, steps, BatchOptions{Parallel: 2, Timeout: time.Second})
		if e != nil {
			t.Fatal(e)
		}
		return j
	}
}

func (app *Service) testStepRetryAndContinue(t *testing.T) {
	devices := []Device{{Serial: "a", Alias: "DEV-001", State: "device"}}
	_, run := app.batchFixture(t, func(_ Device, _ Spec, call int) error {
		if call <= 2 {
			return errors.New("busy")
		}
		return nil
	})
	j := run(context.Background(), devices, []Spec{{Action: "wait", Args: []string{"1"}, Retry: 2}})
	if r := j.Results[0]; !r.OK || r.Retries != 2 {
		t.Fatalf("retry: %+v", r)
	}
	_, run = app.batchFixture(t, func(_ Device, s Spec, _ int) error {
		if s.Action == "tap" {
			return errors.New("no screen")
		}
		return nil
	})
	j = run(context.Background(), devices, []Spec{{Action: "tap", OnError: "continue", Retry: 1}, {Action: "screenshot", Args: []string{}}})
	if r := j.Results[0]; !r.OK || r.Warnings != 1 || r.Retries != 1 || !strings.Contains(r.Output, "! paso 1 (tap): no screen") {
		t.Fatalf("continue: %+v", r)
	}
	j = run(context.Background(), devices, []Spec{{Action: "tap"}, {Action: "screenshot", Args: []string{}}})
	if r := j.Results[0]; r.OK || r.Error != "no screen" {
		t.Fatalf("abort: %+v", r)
	}
}

func (app *Service) testStepTimeoutAndCancel(t *testing.T) {
	devices := []Device{{Serial: "a", Alias: "DEV-001", State: "device"}}
	app.batchFixture(t, nil)
	app.ExecuteStep = func(c context.Context, _ Device, _ Spec) (string, error) {
		<-c.Done()
		return "", c.Err()
	}
	start := time.Now()
	j, _ := app.ExecuteJob(context.Background(), devices, []Spec{{Action: "wait", Args: []string{"1"}, Timeout: "20ms"}}, BatchOptions{Parallel: 1, Timeout: time.Minute})
	if j.Results[0].OK || time.Since(start) > 5*time.Second {
		t.Fatal("step timeout ignored", j.Results[0])
	}
	// Canceling during the pause between attempts ends the device at once.
	app.RetryPause = time.Hour
	c, cancel := context.WithCancel(context.Background())
	app.ExecuteStep = func(context.Context, Device, Spec) (string, error) {
		go cancel()
		return "", errors.New("busy")
	}
	start = time.Now()
	j, _ = app.ExecuteJob(c, devices, []Spec{{Action: "wait", Args: []string{"1"}, Retry: 5}}, BatchOptions{Parallel: 1, Timeout: time.Minute})
	if r := j.Results[0]; r.OK || r.Error != "Cancelado" || time.Since(start) > 5*time.Second {
		t.Fatalf("cancel: %+v", r)
	}
}

func (app *Service) testBuiltinVars(t *testing.T) {
	devices := []Device{{Serial: "s1", Alias: "DEV-001", Model: "A52", State: "device"}, {Serial: "s2", Alias: "DEV-002", Model: "A52", State: "device"}}
	seen, run := app.batchFixture(t, func(Device, Spec, int) error { return nil })
	steps := []Spec{{Action: "text", Args: []string{"${alias}/${serial}/${model}/${index}"}}}
	run(context.Background(), devices, steps)
	got := map[string]bool{}
	for _, s := range *seen {
		got[s.Args[0]] = true
	}
	if !got["DEV-001/s1/A52/1"] || !got["DEV-002/s2/A52/2"] || steps[0].Args[0] != "${alias}/${serial}/${model}/${index}" {
		t.Fatal(got, steps)
	}
	p := filepath.Join(t.TempDir(), "r.json")
	os.WriteFile(p, []byte(`{"steps":[{"action":"push","args":["C:\\x\\${alias}.txt","/sdcard/${name}-${index}.txt"]}]}`), 0600)
	s, e := app.LoadRoutine([]string{p, "name=hola"})
	if e != nil || s[0].Args[1] != "/sdcard/hola-${index}.txt" {
		t.Fatal(s, e)
	}
	if _, e := app.LoadRoutine([]string{p, "name=a", "alias=b"}); e == nil {
		t.Fatal("reserved parameter accepted")
	}
	// A per-device local file is checked when the step runs, not before.
	if e := app.PrepareBatch(s, BatchOptions{Parallel: 1, Timeout: time.Second}); e != nil {
		t.Fatal(e)
	}
}
