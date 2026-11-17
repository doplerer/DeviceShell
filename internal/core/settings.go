package core

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

type AppSettings struct {
	app          *Service
	Language     string `json:"language"`
	Parallel     int    `json:"parallel"`
	Timeout      string `json:"timeout"`
	UseCxtKey    bool   `json:"useCxtKey"`
	WindowArea   []int  `json:"windowArea"`
	AlertTemp    int    `json:"alertTemp"`    // °C; 0 turns the alert off
	AlertBattery int    `json:"alertBattery"` // %; 0 turns the alert off
	// IPhoneVideo is the picture of an iPhone on a cable: "displayservice" (default)
	// or "quicktime". Over Wi-Fi it is always DisplayService.
	IPhoneVideo string `json:"iphoneVideo"`
}

const (
	IPhoneVideoDisplayService = "displayservice"
	IPhoneVideoQuickTime      = "quicktime"
)

func (app *Service) ReadSettings() (AppSettings, map[string]json.RawMessage, error) {
	c := AppSettings{app: app, Language: "en", Parallel: 4, Timeout: "180s", AlertTemp: 42, AlertBattery: 15, IPhoneVideo: IPhoneVideoDisplayService}
	raw := map[string]json.RawMessage{}
	err := app.ReadJSON(app.ConfigPath(), &raw)
	if os.IsNotExist(err) {
		return c, raw, nil
	}
	if err != nil {
		return c, nil, err
	}
	if raw == nil {
		return c, nil, fmt.Errorf("config.json: %s", app.L("se esperaba un objeto", "expected an object"))
	}
	b, err := json.Marshal(raw)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	if err == nil {
		err = c.validate()
	}
	return c, raw, err
}
func (c AppSettings) validate() error {
	_, valid := c.app.ValidLanguage(c.Language)
	d, err := time.ParseDuration(c.Timeout)
	if !valid || c.Parallel < 1 || c.Parallel > 64 || err != nil || d <= 0 {
		return fmt.Errorf("config.json: %s", c.app.L("valores de configuración no válidos", "invalid settings values"))
	}
	if c.WindowArea != nil && (len(c.WindowArea) != 4 || c.WindowArea[2] <= 0 || c.WindowArea[3] <= 0) {
		return fmt.Errorf("windowArea: %s", c.app.L("X e Y enteros; ancho y alto positivos", "integer X and Y; positive width and height"))
	}
	if c.AlertTemp < 0 || c.AlertTemp > 90 {
		return fmt.Errorf("alertTemp: %s", c.app.L("entre 0 (sin aviso) y 90 °C", "between 0 (no alert) and 90 °C"))
	}
	if c.IPhoneVideo != "" && c.IPhoneVideo != IPhoneVideoDisplayService && c.IPhoneVideo != IPhoneVideoQuickTime {
		return fmt.Errorf("iphoneVideo: %s", c.app.L("displayservice o quicktime", "displayservice or quicktime"))
	}
	if c.AlertBattery < 0 || c.AlertBattery > 100 {
		return fmt.Errorf("alertBattery: %s", c.app.L("entre 0 (sin aviso) y 100 %", "between 0 (no alert) and 100 %"))
	}
	return nil
}
func (app *Service) SaveSetting(key string, value any) (AppSettings, error) {
	c, raw, err := app.ReadSettings()
	if err != nil {
		return c, err
	}
	if value == nil {
		delete(raw, key)
	} else {
		b, e := json.Marshal(value)
		if e != nil {
			return c, e
		}
		raw[key] = b
	}
	b, err := json.Marshal(raw)
	if err == nil {
		err = json.Unmarshal(b, &c)
	}
	if key == "windowArea" && value == nil {
		c.WindowArea = nil
	}
	if err == nil {
		err = c.validate()
	}
	if err == nil {
		err = app.WriteJSON(app.ConfigPath(), raw)
	}
	return c, err
}
