//go:build !windows

package core

import (
	"context"
	"errors"
)

// DeviceShell's own windows are Windows only.
func openNativeWindow(context.Context, windowConfig, viewLink, func()) (viewHandle, error) {
	return nil, errors.New("device windows are only available on Windows")
}
