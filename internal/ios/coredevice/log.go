package coredevice

import (
	"io"
	"log/slog"

	"github.com/danielpaulus/go-ios/ios"
)

// go-ios logs through slog; by default nothing reaches DeviceShell's console.
func init() { SetLog(nil) }

// SetLog sends go-ios warnings and errors to w (nil discards them).
func SetLog(w io.Writer) {
	if w == nil {
		w = io.Discard
	}
	ios.SetLogger(slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelWarn})))
}
