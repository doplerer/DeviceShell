package main

import (
	"github.com/doplerer/deviceshell/internal/cli"
	"github.com/doplerer/deviceshell/internal/core"
	"github.com/doplerer/deviceshell/internal/ios/qtdriver"
	"github.com/doplerer/deviceshell/internal/tui"
	"os"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--internal-bench" {
		os.Exit(core.RunBench(os.Args[2]))
	}
	if len(os.Args) == 3 && os.Args[1] == "--internal-install-qt-driver" {
		os.Exit(qtdriver.RunInstall(os.Args[2]))
	}
	service := core.New()
	service.WiFi = true
	ui := tui.New(service)
	console := cli.New(service)
	console.RunTUI = ui.Run
	console.InteractiveTerminal = ui.InteractiveTerminal
	console.RebuildTexts = ui.RebuildTexts
	os.Exit(console.Main())
}
