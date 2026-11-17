package cli

import (
	"testing"

	"github.com/doplerer/deviceshell/internal/core"
)

func newTestRuntime() *Console {
	return New(core.New())
}
func TestDevicesAndSelection(t *testing.T) {
	newTestRuntime().testDevicesAndSelection(t)
}
func TestBatchPartialFailure(t *testing.T) {
	newTestRuntime().testBatchPartialFailure(t)
}
