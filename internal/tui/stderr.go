package tui

import (
	"os"
)

// redirectErrors sends the process's error output to output/logs/tui.log
// until the returned function restores it.
func (app *Interface) redirectErrors() func() {
	f, e := os.OpenFile(app.Service.LogsPath("tui.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return func() {}
	}
	old := os.Stderr
	os.Stderr = f
	undo := setErrorHandle(f)
	return func() {
		undo()
		os.Stderr = old
		f.Close()
	}
}
