package core

import (
	"fmt"
	"strings"
	"testing"

	"github.com/doplerer/deviceshell/internal/usb"
)

func TestUSBReportGroupsPhonesByController(t *testing.T) {
	var nodes []usb.Node
	var devices []Device
	for i := range 13 {
		serial := fmt.Sprintf("R58M%03d", i)
		nodes = append(nodes, usb.Node{Serial: serial, Controller: `PCI\VEN_1022&DEV_15B6\A`, ControllerName: "AMD USB 3.10 eXtensible Host Controller"})
		devices = append(devices, Device{Serial: serial, Alias: fmt.Sprintf("DEV-%03d", i), Platform: "android"})
	}
	for i := range 9 {
		udid := fmt.Sprintf("00008110-00%014d", i)
		nodes = append(nodes, usb.Node{Serial: strings.ReplaceAll(udid, "-", ""), Controller: `PCI\VEN_1022&DEV_43F7\B`, ControllerName: "Other controller"})
		devices = append(devices, Device{Serial: "ios:" + udid, Alias: fmt.Sprintf("IPH-%d", i), Platform: "ios"})
	}
	devices = append(devices, Device{Serial: "192.168.1.20:5555", Alias: "WIFI", Platform: "android"})
	r := buildUSBReport(New().L, nodes, devices)
	if len(r.Controllers) != 2 || len(r.NotOnUSB) != 1 || r.NotOnUSB[0] != "WIFI" {
		t.Fatalf("%+v", r)
	}
	android, iphone := r.Controllers[0], r.Controllers[1]
	if android.Android != 13 || android.IPhone != 0 || !strings.Contains(android.Warning, "13 móviles") || android.QuickTimeMbps != "" {
		t.Fatalf("%+v", android)
	}
	if iphone.IPhone != 9 || iphone.QuickTimeMbps != "90-396" || !strings.Contains(iphone.Warning, "USB 2.0") || strings.Contains(iphone.Warning, "móviles en un controlador") {
		t.Fatalf("%+v", iphone)
	}
	// Few phones: no warning.
	if r := buildUSBReport(New().L, nodes[:3], devices[:3]); r.Controllers[0].Warning != "" {
		t.Fatal(r.Controllers[0].Warning)
	}
}
