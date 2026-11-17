package tui

// rebuildTexts regenerates everything that holds translated text built ahead of time.
func (app *Interface) RebuildTexts() {
	app.tuiRegistry = app.buildTUICommands()
}
