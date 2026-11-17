//go:build !windows

package tui

import "os"

func setErrorHandle(*os.File) func() { return func() {} }
