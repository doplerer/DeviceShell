package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/doplerer/deviceshell/internal/android/adb"
)

// adbNative runs args natively when it can; ok is false when adb.exe must run them.
// It covers what DeviceShell calls on every device and step: device listing, shell commands
// and the forwards of the screen and sync sessions.
func (app *Service) adbNative(c context.Context, args []string) (out string, ok bool, err error) {
	switch {
	case len(args) == 2 && args[0] == "devices" && args[1] == "-l":
		out, err = adb.HostQuery(c, "host:devices-l", true)
		out = "List of devices attached\n" + out
	case len(args) > 3 && args[0] == "-s" && args[2] == "shell":
		out, err = adb.Shell(c, args[1], strings.Join(args[3:], " "))
	// host-serial needs the serial without colons to be unambiguous (no IP:port serials).
	case len(args) == 5 && args[0] == "-s" && args[2] == "forward" && args[3] == "--remove" && !strings.Contains(args[1], ":"):
		out, err = adb.HostQuery(c, "host-serial:"+args[1]+":killforward:"+args[4], false)
	case len(args) == 5 && args[0] == "-s" && args[2] == "forward" && args[3] == "tcp:0" && !strings.Contains(args[1], ":"):
		out, err = adb.HostQuery(c, "host-serial:"+args[1]+":forward:tcp:0;"+args[4], false)
		if err == nil && out != "" {
			out += "\n"
		}
	default:
		return "", false, nil
	}
	if errors.Is(err, adb.ErrUnserved) && c.Err() == nil {
		return "", false, nil
	}
	if err != nil {
		err = fmt.Errorf("ADB: %w: %s", err, strings.TrimSpace(out))
	}
	return out, true, err
}
