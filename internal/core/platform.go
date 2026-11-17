package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// DeviceBackend keeps transport details out of selection, routines and batches.
type DeviceBackend interface {
	Discover(context.Context) ([]Device, error)
	Execute(context.Context, Device, Spec) (string, error)
	Supports(Device, Spec) error
}

func (d Device) OS() string {
	if d.Platform != "" {
		return d.Platform
	}
	if strings.HasPrefix(d.Serial, "ios:") {
		return "ios"
	}
	return "android"
}
func (d Device) NativeID() string { return strings.TrimPrefix(d.Serial, "ios:") }

// Link is how a device reaches the PC: "wifi" or "usb". iPhones say it in Transport;
// an Android over wireless ADB has a network serial ("192.168.1.5:5555", or the
// "adb-…._adb-tls-connect._tcp" of wireless debugging).
func (d Device) Link() string {
	if d.OS() == "ios" {
		if d.Transport == "wifi" {
			return "wifi"
		}
		return "usb"
	}
	if strings.Contains(d.Serial, ":") || strings.Contains(d.Serial, "._adb-tls-connect") || strings.Contains(d.Serial, "._tcp") {
		return "wifi"
	}
	return "usb"
}

// viewTitle names a device window: "DEV-019 · iOS Wi-Fi", "DEV-001 · Android USB".
func (d Device) viewTitle() string {
	system := "Android"
	if d.OS() == "ios" {
		system = "iOS"
	}
	link := "USB"
	if d.Link() == "wifi" {
		link = "Wi-Fi"
	}
	return d.Alias + " · " + system + " " + link
}

type androidBackend struct{ app *Service }

func (b androidBackend) Discover(c context.Context) ([]Device, error) {
	out, e := b.app.Command(c, "devices", "-l")
	return b.app.ParseDevices(out), e
}
func (b androidBackend) Execute(c context.Context, d Device, s Spec) (string, error) {
	if s.Action == "app-install" {
		s.Action = "apk-install"
	}
	if s.Action == "app-uninstall" {
		s.Action = "apk-uninstall"
	}
	return b.app.executeAndroid(c, d, s)
}
func (b androidBackend) Supports(d Device, s Spec) error {
	if s.Action == "app-install" && strings.EqualFold(filepath.Ext(s.Args[0]), ".ipa") {
		return fmt.Errorf("IPA files are not supported on Android")
	}
	return nil
}
func (app *Service) backend(d Device) DeviceBackend {
	if d.OS() == "ios" {
		return iosBackend{app}
	}
	return androidBackend{app}
}
func (app *Service) CheckDeviceSteps(d Device, steps []Spec) error {
	return app.checkDeviceStepsAt(d, steps, 1)
}
func (app *Service) checkDeviceStepsAt(d Device, steps []Spec, index int) error {
	for i, s := range steps {
		if e := app.ValidateSpec(s); e != nil {
			return e
		}
		if d.OS() != "android" && d.OS() != "ios" {
			return fmt.Errorf("plataforma desconocida: %s", d.OS())
		}
		if s.RoutinePlatform != "" && s.RoutinePlatform != "both" && s.RoutinePlatform != d.OS() {
			return fmt.Errorf(app.L("rutina %s incompatible con %s (%s)", "routine %s is not compatible with %s (%s)"), s.RoutinePlatform, d.Alias, d.OS())
		}
		s = app.expandSpec(s, d, index)
		if e := app.backend(d).Supports(d, s); e != nil {
			return fmt.Errorf("%s (%s), paso %d: %w", d.Alias, d.OS(), i+1, e)
		}
	}
	return nil
}
func (app *Service) Execute(c context.Context, d Device, s Spec) (string, error) {
	if e := app.CheckDeviceSteps(d, []Spec{s}); e != nil {
		return "", e
	}
	if s.Action == "wait" {
		return app.executeAndroid(c, d, s)
	}
	return app.backend(d).Execute(c, d, s)
}

// Discovery errors are kept per platform. A failed backend must not erase its inventory.
func (app *Service) discoverLive(c context.Context) ([]Device, map[string]error) {
	live := []Device{}
	issues := map[string]error{}
	for _, p := range []string{"android", "ios"} {
		ds, e := app.backend(Device{Platform: p}).Discover(c)
		if e != nil {
			issues[p] = e
			continue
		}
		live = append(live, ds...)
	}
	return live, issues
}
func (app *Service) mergeDiscovered(old, live []Device, issues map[string]error, now string) ([]Device, error) {
	app.invalidateDisconnectedIOS(live, issues)
	merged, e := app.MergeInventory(old, live, now)
	if e != nil {
		return nil, e
	}
	for i := range merged {
		if issues[merged[i].OS()] != nil {
			merged[i].State = "offline"
		}
	}
	return merged, nil
}
