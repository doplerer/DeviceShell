//go:build !windows

package usb

// The USB controllers are only read on Windows.
func Nodes() []Node { return nil }
