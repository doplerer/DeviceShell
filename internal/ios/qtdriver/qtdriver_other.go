//go:build !windows

package qtdriver

import "context"

// QuickTime's USB capture is read through winusb.dll, so there is no driver to
// install elsewhere (and no picture: the window reports it).

func Pending() bool                 { return false }
func Install(context.Context) error { return nil }
func RunInstall(string) int         { return 1 }
