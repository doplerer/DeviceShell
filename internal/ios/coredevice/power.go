package coredevice

import (
	"github.com/danielpaulus/go-ios/ios"
)

const assertionService = "com.apple.mobile.assertion_agent.shim.remote"

// KeepAwake stops auto-lock for seconds: a locked phone stops its video and refuses
// a new stream. The phone drops the assertion on its own when it expires.
func (t *Tunnel) KeepAwake(seconds int) error {
	conn, e := ios.ConnectToShimService(t.Device, assertionService)
	if e != nil {
		return e
	}
	defer conn.Close()
	rw := ios.NewPlistCodecReadWriter(conn.Reader(), conn.Writer())
	if e = rw.Write(map[string]any{
		"CommandKey":          "CommandCreateAssertion",
		"AssertionTypeKey":    "PreventUserIdleSystemSleep",
		"AssertionNameKey":    "deviceshell",
		"AssertionTimeoutKey": seconds,
		"AssertionDetailKey":  "DeviceShell keeping the screen awake for live view",
	}); e != nil {
		return e
	}
	var reply map[string]any
	return rw.Read(&reply)
}
