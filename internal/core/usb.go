package core

import (
	"fmt"
	"sort"
	"strings"

	"github.com/doplerer/deviceshell/internal/usb"
)

// USB limits for doctor: phones share the PC's USB host controllers, and an xHCI
// controller only has so many device slots and endpoints (Windows then says "not
// enough USB controller resources" and a phone drops or loses its video). Each iPhone
// sends its QuickTime picture at 10-44 Mbit/s, which phones behind one USB 2.0 hub
// (480 Mbit/s for all) share.

// usbPhonesWarn is the number of phones on one controller from which doctor advises
// spreading them over more controllers.
const usbPhonesWarn = 12

// QuickTime bit rate of one iPhone, Mbit/s.
const qtMbpsLow, qtMbpsHigh = 10, 44

type USBController struct {
	Name          string   `json:"name"`
	Android       int      `json:"android"`
	IPhone        int      `json:"iphone"`
	Devices       []string `json:"devices"`
	QuickTimeMbps string   `json:"quicktimeMbps,omitempty"`
	Warning       string   `json:"warning,omitempty"`
}

type USBReport struct {
	Controllers []USBController `json:"controllers"`
	NotOnUSB    []string        `json:"notOnUsb,omitempty"` // phones not found on a USB port (ADB over Wi-Fi...)
}

// usbSerial is the USB serial number a device presents: an iPhone's is its UDID
// without the dash.
func usbSerial(d Device) string {
	if d.OS() == "ios" {
		return strings.ReplaceAll(d.NativeID(), "-", "")
	}
	return d.Serial
}

// L picks the text of the active language (Service.L).
func buildUSBReport(L func(es, en string) string, nodes []usb.Node, devices []Device) USBReport {
	bySerial := map[string]usb.Node{}
	for _, n := range nodes {
		bySerial[strings.ToUpper(n.Serial)] = n
	}
	controllers := map[string]*USBController{}
	var order []string
	var report USBReport
	for _, d := range devices {
		n, ok := bySerial[strings.ToUpper(usbSerial(d))]
		if !ok || n.Controller == "" {
			report.NotOnUSB = append(report.NotOnUSB, d.Alias)
			continue
		}
		c := controllers[n.Controller]
		if c == nil {
			c = &USBController{Name: n.ControllerName}
			controllers[n.Controller] = c
			order = append(order, n.Controller)
		}
		if d.OS() == "ios" {
			c.IPhone++
		} else {
			c.Android++
		}
		c.Devices = append(c.Devices, d.Alias)
	}
	sort.Strings(order)
	for _, id := range order {
		c := controllers[id]
		var warnings []string
		if phones := c.Android + c.IPhone; phones >= usbPhonesWarn {
			warnings = append(warnings, fmt.Sprintf(L("%d móviles en un controlador: si alguno desaparece o pierde el vídeo, repártelos entre otros controladores (puertos de otra zona del equipo o una tarjeta PCIe USB)", "%d phones on one controller: if any disappears or loses video, spread them over other controllers (ports in another part of the PC or a PCIe USB card)"), phones))
		}
		if c.IPhone > 0 {
			c.QuickTimeMbps = fmt.Sprintf("%d-%d", c.IPhone*qtMbpsLow, c.IPhone*qtMbpsHigh)
			if c.IPhone*qtMbpsHigh > 350 {
				warnings = append(warnings, fmt.Sprintf(L("el vídeo de %d iPhone puede llegar a %d Mbit/s: si comparten un hub USB 2.0 (480 Mbit/s en total) el vídeo se cortará", "video from %d iPhones can reach %d Mbit/s: if they share a USB 2.0 hub (480 Mbit/s in total) the video will drop"), c.IPhone, c.IPhone*qtMbpsHigh))
			}
		}
		c.Warning = strings.Join(warnings, "; ")
		report.Controllers = append(report.Controllers, *c)
	}
	return report
}

// USBReport says how the phones are spread over the USB host controllers.
func (app *Service) USBReport(devices []Device) USBReport {
	return buildUSBReport(app.L, usb.Nodes(), devices)
}
