package coredevice

import (
	"encoding/binary"
	"errors"
	"math"
	"sync"
	"time"
)

// Touch, keyboard and buttons (docs/iphone-protocol.md, "Toques y teclado" and
// "Botones"). Touches only reach apps while a DisplayService stream is open.

const (
	hidService     = "com.apple.coredevice.hid.universalhidservice"
	buttonService  = "com.apple.coredevice.hid.indigo"
	touchServiceID = 257
	// keyboardServiceID is the id proposed for the virtual keyboard; the phone answers
	// with the one it assigned (in the captures, the same).
	keyboardServiceID = 0x100002001
)

// HID usage codes DeviceShell types with (USB HID Usage Tables, Keyboard page 0x07).
const (
	KeyEnter     = 0x28
	KeyBackspace = 0x2A
	KeyLeftShift = 0xE1
	KeyLeftGUI   = 0xE3 // Cmd
	KeyT         = 0x17
	KeyL         = 0x0F
	KeyV         = 0x19
)

// Buttons: Consumer page 0x0C usage and how long iOS needs it held.
type Button struct {
	Page, Code uint64
	Hold       time.Duration
}

var Buttons = map[string]Button{
	"HOME":        {0x0C, 0x40, 50 * time.Millisecond},
	"LOCK":        {0x0C, 0x30, 500 * time.Millisecond},
	"POWER":       {0x0C, 0x30, 500 * time.Millisecond},
	"VOLUME_UP":   {0x0C, 0xE9, 50 * time.Millisecond},
	"VOLUME_DOWN": {0x0C, 0xEA, 50 * time.Millisecond},
	"MUTE":        {0x0C, 0xE2, 50 * time.Millisecond},
}

// hidClock is the timestamp in reports: a monotonic nanosecond count, as the host sends.
var hidEpoch = time.Now()

func hidClock() uint64 { return uint64(time.Hour + time.Since(hidEpoch)) }

// touchReport is the 58-byte touchscreen report; x and y are 0..1 of the screen.
func touchReport(contact bool, x, y float64, now uint64) []byte {
	r := make([]byte, 58)
	copy(r, []byte{0x09, 0x01, 0x05})
	r[3] = 0x02
	if contact {
		r[3] = 0xc2
	}
	scale := func(v float64) uint16 { return uint16(math.Round(max(0, min(1, v)) * 65535)) }
	binary.LittleEndian.PutUint16(r[4:], scale(x))
	binary.LittleEndian.PutUint16(r[6:], scale(y))
	r[40] = 0x02
	binary.LittleEndian.PutUint64(r[44:], now)
	return r
}

// keyboardReport is the 39-byte keyboard report: one bit per pressed HID usage.
func keyboardReport(usages []byte, now uint64) []byte {
	r := make([]byte, 39)
	r[0] = 0x01
	for _, u := range usages {
		if i := 1 + int(u)/8; i <= 30 {
			r[i] |= 1 << (u % 8)
		}
	}
	binary.LittleEndian.PutUint64(r[31:], now)
	return r
}

// keyboardDescriptor is the standard boot keyboard report descriptor (USB HID 1.11,
// appendix B.1) with the full 0-255 key range.
var keyboardDescriptor = []byte{
	0x05, 0x01, 0x09, 0x06, 0xa1, 0x01, // Generic Desktop, Keyboard, Application
	0x05, 0x07, 0x19, 0xe0, 0x29, 0xe7, 0x15, 0x00, 0x25, 0x01, 0x95, 0x08, 0x75, 0x01, 0x81, 0x02, // modifiers
	0x95, 0x01, 0x75, 0x08, 0x81, 0x01, // reserved byte
	0x05, 0x07, 0x19, 0x00, 0x29, 0xff, 0x15, 0x00, 0x26, 0xff, 0x00, 0x95, 0x06, 0x75, 0x08, 0x81, 0x00, // keys
	0x05, 0x08, 0x19, 0x01, 0x29, 0x05, 0x15, 0x00, 0x25, 0x01, 0x95, 0x05, 0x75, 0x01, 0x91, 0x02, // LEDs
	0x95, 0x01, 0x75, 0x03, 0x91, 0x01, // LED padding
	0xc0,
}

func keyboardServiceRequest() map[string]any {
	const vendor, product = 1452, 592 // Apple, keyboard
	props := map[string]any{
		"DeviceUsagePairs": []any{map[string]any{"DeviceUsage": int64(6), "DeviceUsagePage": int64(1)}},
		"PrimaryUsage":     uint64(6), "PrimaryUsagePage": uint64(1),
		"Product": "DeviceShell virtual keyboard", "ProductID": int64(product), "VendorID": int64(vendor),
		"_CoreDevice_codablePropertyStorage": map[string]any{
			"Manufacturer":     map[string]any{"string": "DeviceShell"},
			"Product":          map[string]any{"string": "DeviceShell virtual keyboard"},
			"ProductID":        map[string]any{"int": int64(product)},
			"VendorID":         map[string]any{"int": int64(vendor)},
			"PrimaryUsage":     map[string]any{"int": int64(6)},
			"PrimaryUsagePage": map[string]any{"int": int64(1)},
			"DeviceUsagePairs": map[string]any{"array": []any{map[string]any{"dictionary": map[string]any{
				"DeviceUsage": map[string]any{"int": int64(6)}, "DeviceUsagePage": map[string]any{"int": int64(1)}}}}},
			"Transport":                      map[string]any{"string": "USB"},
			"ReportDescriptor":               map[string]any{"data": keyboardDescriptor},
			"UniversalControlVirtualService": map[string]any{"bool": true},
			"_ServiceID":                     map[string]any{"uint": uint64(keyboardServiceID)},
		},
		"_ServiceID": uint64(keyboardServiceID),
	}
	return hidRequest(map[string]any{"createService": map[string]any{"_0": props}})
}

func hidRequest(payload map[string]any) map[string]any {
	return map[string]any{"featureIdentifier": "com.apple.coredevice.feature.remote.universalhidservice", "messageType": "Request", "payload": payload}
}

func hidSend(serviceID uint64, report []byte) map[string]any {
	return hidRequest(map[string]any{"send": map[string]any{"_0": report, "_1": serviceID}})
}

func buttonEvent(page, code uint64, down bool) map[string]any {
	state := uint64(2)
	if down {
		state = 1
	}
	return map[string]any{
		"messageType":       "IndigoButtonEvent",
		"featureIdentifier": "com.apple.coredevice.feature.remote.hid.button",
		"payload":           map[string]any{"usagePage": page, "usageCode": code, "state": state},
	}
}

// HID sends touches, keys and buttons. Each kind reconnects once when its
// connection dropped.
type HID struct {
	t        *Tunnel
	mu       sync.Mutex
	touch    *service
	buttons  *service
	keyboard uint64
}

func (t *Tunnel) HID() *HID { return &HID{t: t} }

// Has reports whether the phone publishes touch (and keyboard) or buttons.
func (t *Tunnel) HasTouch() bool   { return t.Services[hidService].Port != 0 }
func (t *Tunnel) HasButtons() bool { return t.Services[buttonService].Port != 0 }

func (h *HID) touchService() (*service, error) {
	if h.touch == nil {
		s, e := h.t.connect(hidService)
		if e != nil {
			return nil, e
		}
		h.touch, h.keyboard = s, 0
	}
	return h.touch, nil
}

// withTouch runs f on the touch connection, reconnecting once if it fails.
func (h *HID) withTouch(f func(*service) error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for attempt := range 2 {
		s, e := h.touchService()
		if e == nil {
			if e = f(s); e == nil {
				return nil
			}
		}
		if attempt == 1 {
			return e
		}
		h.resetTouch()
	}
	return nil
}

func (h *HID) resetTouch() {
	if h.touch != nil {
		h.touch.close()
	}
	h.touch, h.keyboard = nil, 0
}

// Touch sends one finger sample; contact false lifts the finger.
func (h *HID) Touch(contact bool, x, y float64) error {
	return h.withTouch(func(s *service) error {
		return s.send(hidSend(touchServiceID, touchReport(contact, x, y, hidClock())))
	})
}

// Keys sends the set of keys held down now; no keys releases them all.
func (h *HID) Keys(usages ...byte) error {
	return h.withTouch(func(s *service) error {
		if h.keyboard == 0 {
			c, cancel := h.t.timeout(10 * time.Second)
			reply, e := s.request(c, keyboardServiceRequest())
			cancel()
			if e != nil {
				return e
			}
			id, ok := reply["serviceID"].(uint64)
			if !ok {
				return replyError(reply)
			}
			h.keyboard = id
		}
		return s.send(hidSend(h.keyboard, keyboardReport(usages, hidClock())))
	})
}

// Button presses or releases a hardware button.
func (h *HID) Button(b Button, down bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for attempt := range 2 {
		if h.buttons == nil {
			s, e := h.t.connect(buttonService)
			if e != nil {
				return e
			}
			h.buttons = s
		}
		e := h.buttons.send(buttonEvent(b.Page, b.Code, down))
		if e == nil || attempt == 1 {
			return e
		}
		h.buttons.close()
		h.buttons = nil
	}
	return errors.New("unreachable")
}

// Reconnect drops the touch connection: the phone matches HID input against the
// current DisplayService stream, so a new stream needs a new connection.
func (h *HID) Reconnect() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.resetTouch()
}

func (h *HID) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.resetTouch()
	if h.buttons != nil {
		h.buttons.close()
		h.buttons = nil
	}
}
