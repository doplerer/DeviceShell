package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/doplerer/deviceshell/internal/core"
)

func (app *Console) testDevicesAndSelection(t *testing.T) {
	for _, n := range []int{0, 1, 16, 20, 24} {
		var b strings.Builder
		b.WriteString("List of devices attached\n")
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "SERIAL%d device model:SM_S901B\n", i)
		}
		d := app.Service.ParseDevices(b.String())
		if len(d) != n {
			t.Fatal(n, len(d))
		}
		app.allFlag = true
		app.groupFlag = ""
		app.devicesFlag = ""
		got, e := app.selectTargets(d)
		if n == 0 {
			if e == nil {
				t.Fatal("Empty selection accepted")
			}
		} else if e != nil || len(got) != n {
			t.Fatal(n, e)
		}
	}
	app.allFlag = false
}
func (app *Console) testBatchPartialFailure(t *testing.T) {
	app.Service.Root = t.TempDir()
	app.Service.Ctx = context.Background()
	app.Service.Parallel = 2
	app.Service.Timeout = 1000000000
	app.jsonFlag = true
	targets := []core.Device{{Serial: "a", Alias: "DEV-001", State: "device"}, {Serial: "b", Alias: "DEV-002", State: "offline"}}
	if e := app.runBatch(targets, []core.Spec{{Action: "wait", Args: []string{"1"}}}); e == nil {
		t.Fatal("Missing partial failure")
	}
	files, _ := filepath.Glob(app.Service.JobsPath("*.json"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	b, _ := os.ReadFile(files[0])
	var j core.Job
	json.Unmarshal(b, &j)
	if !j.Results[0].OK || j.Results[1].OK {
		t.Fatal(j)
	}
	app.jsonFlag = false
}
