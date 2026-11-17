package core

import (
	"errors"

	"github.com/doplerer/deviceshell/internal/i18n"
)

// L returns the text in the active language. Texts are written as pairs where they are used,
// so each translation stays next to its context.
func (app *Service) L(es, en string) string {
	return i18n.Text(app.Lang, es, en)
}
func (app *Service) ValidLanguage(l string) (string, bool) {
	return i18n.Normalize(l)
}

// loadLanguage picks DEVICESHELL_LANG (or the older BOTFARM_LANG), then "language" in config.json, then English.
func (app *Service) LoadLanguage() {
	if l, ok := app.ValidLanguage(Getenv("LANG")); ok {
		app.Lang = l
		return
	}
	if config, _, err := app.ReadSettings(); err == nil {
		if l, ok := app.ValidLanguage(config.Language); ok {
			app.Lang = l
			return
		}
	}
	app.Lang = "en"
}

// saveLanguage stores the choice in config.json, keeping every other setting as it was.
func (app *Service) SaveLanguage(l string) error {
	l, ok := app.ValidLanguage(l)
	if !ok {
		return errors.New(app.L("Idioma no válido: usa es o en", "Unknown language: use es or en"))
	}
	if _, e := app.SaveSetting("language", l); e != nil {
		return e
	}
	app.Lang = l
	return nil
}
func (app *Service) LanguageName(l string) string {
	return i18n.Name(l)
}
