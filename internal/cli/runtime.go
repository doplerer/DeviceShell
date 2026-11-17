package cli

import (
	"errors"

	"github.com/doplerer/deviceshell/internal/core"
)

type Console struct {
	*core.Service
	RunTUI              func() error
	InteractiveTerminal func() bool
	RebuildTexts        func()
	devicesFlag         string
	groupFlag           string
	allFlag             bool
	jsonFlag            bool
	errBatchFailed      error
}

func New(service *core.Service) *Console {
	app := &Console{}
	app.Service = service
	app.errBatchFailed = errors.New("batch finished with failures")
	return app
}
