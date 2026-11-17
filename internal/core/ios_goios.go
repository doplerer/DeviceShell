package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/amfi"
	"github.com/danielpaulus/go-ios/ios/diagnostics"
	"github.com/danielpaulus/go-ios/ios/imagemounter"
	"github.com/danielpaulus/go-ios/ios/installationproxy"
	"github.com/danielpaulus/go-ios/ios/zipconduit"
)

// goIOS answers the go-ios commands DeviceShell used to run as ios.exe, with go-ios as a
// library, keeping the JSON each caller parses: list, info, pair, devmode
// get|reveal|enable, apps, install --path=, uninstall and reboot, optionally after --udid=.
func goIOS(c context.Context, args []string) (string, error) {
	type result struct {
		out string
		e   error
	}
	done := make(chan result, 1)
	// go-ios calls take no context; a cancelled caller stops waiting for them.
	go func() {
		out, e := goIOSRun(args)
		done <- result{out, e}
	}()
	select {
	case r := <-done:
		return r.out, r.e
	case <-c.Done():
		return "", c.Err()
	}
}

func goIOSRun(args []string) (string, error) {
	udid := ""
	if len(args) > 0 && strings.HasPrefix(args[0], "--udid=") {
		udid, args = strings.TrimPrefix(args[0], "--udid="), args[1:]
	}
	if len(args) == 1 && args[0] == "list" {
		list, e := ios.ListDevices()
		if e != nil {
			return "", e
		}
		ids, usbmux := []string{}, []int{}
		for _, d := range list.DeviceList {
			ids = append(ids, d.Properties.SerialNumber)
			usbmux = append(usbmux, d.DeviceID)
		}
		// deviceIDs: usbmux's id of each, which changes whenever the device re-enumerates.
		return jsonText(map[string]any{"deviceList": ids, "deviceIDs": usbmux})
	}
	device, e := ios.GetDevice(udid)
	if e != nil {
		return "", e
	}
	switch {
	case len(args) == 1 && args[0] == "info":
		values, e := ios.GetValues(device)
		if e != nil {
			return "", e
		}
		return jsonText(map[string]any{"ProductType": values.Value.ProductType, "ProductVersion": values.Value.ProductVersion, "DeviceName": values.Value.DeviceName})
	case len(args) == 2 && args[0] == "devmode" && args[1] == "get":
		enabled, e := developerModeStatus(device)
		if e != nil {
			return "", e
		}
		return jsonText(map[string]any{"DeveloperModeEnabled": enabled})
	case len(args) == 2 && args[0] == "devmode" && args[1] == "reveal":
		conn, e := amfi.New(device)
		if e != nil {
			return "", e
		}
		defer conn.Close()
		return "", conn.RevealDevMode()
	case len(args) == 2 && args[0] == "devmode" && args[1] == "enable":
		return "", enableDeveloperMode(device)
	case len(args) == 1 && args[0] == "pair":
		// Only a phone this PC holds no valid pairing for is paired: pairing again
		// would replace the record it already trusts.
		if _, e := ios.ReadPairRecord(udid); e == nil {
			_, e = ios.GetValues(device)
			if e == nil {
				return jsonText(map[string]any{"paired": true})
			}
			if !strings.Contains(e.Error(), "InvalidHostID") {
				return "", e
			}
		}
		if e := ios.Pair(device); e != nil {
			return "", e
		}
		return jsonText(map[string]any{"paired": true})
	case len(args) == 1 && args[0] == "apps":
		proxy, e := installationproxy.New(device)
		if e != nil {
			return "", e
		}
		defer proxy.Close()
		apps, e := proxy.BrowseUserApps()
		if e != nil {
			return "", e
		}
		return jsonText(apps)
	case len(args) == 2 && args[0] == "install" && strings.HasPrefix(args[1], "--path="):
		conn, e := zipconduit.New(device)
		if e != nil {
			return "", fmt.Errorf("install: %w", e)
		}
		return "", conn.SendFile(strings.TrimPrefix(args[1], "--path="))
	case len(args) == 2 && args[0] == "uninstall":
		proxy, e := installationproxy.New(device)
		if e != nil {
			return "", e
		}
		defer proxy.Close()
		return "", proxy.Uninstall(args[1])
	case len(args) == 1 && args[0] == "reboot":
		return "", diagnostics.Reboot(device)
	}
	return "", errors.New("go-ios: command not implemented: " + strings.Join(args, " "))
}

// errDeveloperModeRevealed reports that Developer Mode could not be switched on
// from the PC (the phone has a passcode) and its toggle was shown in Settings instead.
var errDeveloperModeRevealed = errors.New("developer mode revealed in Settings")

// enableDeveloperMode switches Developer Mode on as Xcode does. Without a passcode
// AMFI turns it on and restarts the phone; once it is back, the confirmation it asks
// for is answered too. With a passcode that is refused, and the toggle is revealed in
// Settings > Privacy & Security for the user (errDeveloperModeRevealed).
func enableDeveloperMode(device ios.DeviceEntry) error {
	udid := device.Properties.SerialNumber
	conn, e := amfi.New(device)
	if e != nil {
		return e
	}
	e = conn.EnableDevMode()
	conn.Close()
	if e != nil {
		// Both answers are kept: the activation log shows why AMFI refused.
		conn, re := amfi.New(device)
		if re == nil {
			re = conn.RevealDevMode()
			conn.Close()
			if re == nil {
				return fmt.Errorf("%w: %v", errDeveloperModeRevealed, e)
			}
		}
		return fmt.Errorf("%v; reveal: %v", e, re)
	}
	// The phone restarts and comes back under another usbmux id; its services
	// start a little after it is listed.
	if _, id := waitUsbmux(udid, device.DeviceID, 3*time.Minute); id == 0 {
		return errors.New("the iPhone did not come back after restarting to turn on Developer Mode")
	}
	deadline := time.Now().Add(time.Minute)
	for {
		back, e := ios.GetDevice(udid)
		if e == nil {
			var conn *amfi.Connection
			if conn, e = amfi.New(back); e == nil {
				e = conn.EnableDevModePostRestart()
				conn.Close()
				if e == nil {
					return nil
				}
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("confirm Developer Mode on the iPhone: %w", e)
		}
		time.Sleep(3 * time.Second)
	}
}

// developerModeStatus asks the image mounter whether Developer Mode is on; a locked
// phone refuses it, which is how discovery finds it locked (iosConnectError). When the
// mounter fails for any other reason, the answer is read from lockdown instead
// (DeveloperModeStatus in the AMFI domain, no service needed), so a phone without
// Developer Mode is not taken for one with it.
func developerModeStatus(device ios.DeviceEntry) (bool, error) {
	enabled, e := imagemounter.IsDevModeEnabled(device)
	if e == nil || strings.Contains(e.Error(), "PasswordProtected") || strings.Contains(e.Error(), "DeviceLocked") {
		return enabled, e
	}
	conn, le := ios.ConnectLockdownWithSession(device)
	if le != nil {
		return false, fmt.Errorf("%w; lockdown: %v", e, le)
	}
	defer conn.Close()
	v, le := conn.GetValueForDomain("DeveloperModeStatus", "com.apple.security.mac.amfi")
	if on, ok := v.(bool); ok && le == nil {
		return on, nil
	}
	return false, fmt.Errorf("%w; lockdown DeveloperModeStatus: %v (%v)", e, v, le)
}

func jsonText(v any) (string, error) {
	b, e := json.Marshal(v)
	return string(b), e
}

// usbmuxID is the id usbmux gives udid while attached, or 0 if it does not list it.
// The id changes every time the device re-enumerates.
func usbmuxID(udid string) int {
	list, e := ios.ListDevices()
	if e != nil {
		return 0
	}
	for _, d := range list.DeviceList {
		if d.Properties.SerialNumber == udid {
			return d.DeviceID
		}
	}
	return 0
}

// waitUsbmux waits until usbmux lists udid under an id other than old, polling every
// 100 ms up to limit, and returns how long it took and the new id (0 if none).
func waitUsbmux(udid string, old int, limit time.Duration) (time.Duration, int) {
	started := time.Now()
	for {
		if id := usbmuxID(udid); id != 0 && id != old {
			return time.Since(started), id
		}
		if time.Since(started) >= limit {
			return time.Since(started), 0
		}
		time.Sleep(100 * time.Millisecond)
	}
}
