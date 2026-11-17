package core

import (
	"sync"
	"time"
)

// iosInfoCache keeps what each iPhone is (model, iOS, Developer Mode) while usbmux
// lists it under the same id: asking means two lockdown sessions per phone, which on
// every 3 s scan also crossed the QuickTime mode switches. A phone that cannot be used
// yet (Developer Mode off, old iOS) is asked again after iosInfoRetry, in case it
// changed without re-enumerating.
type iosInfoCache struct {
	mu      sync.Mutex
	entries map[string]iosKnown
}

type iosKnown struct {
	usbmux int
	device Device
	until  time.Time // zero: until the id changes
}

const iosInfoRetry = 30 * time.Second

// get returns what is known of udid listed as usbmux; usbmux 0 (unknown) never matches.
func (k *iosInfoCache) get(udid string, usbmux int) (Device, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	e, ok := k.entries[udid]
	if !ok || usbmux == 0 || e.usbmux != usbmux || !e.until.IsZero() && time.Now().After(e.until) {
		return Device{}, false
	}
	return e.device, true
}

// put keeps an answer: a usable phone until it re-enumerates, one that is not
// available (locked, Developer Mode off, old iOS) for iosInfoRetry, so it is only
// read again every 30 s, and nothing when lockdown did not answer (it may be a
// passing failure of a working phone).
func (k *iosInfoCache) put(udid string, usbmux int, d Device) {
	if usbmux == 0 {
		return
	}
	e := iosKnown{usbmux: usbmux, device: d}
	switch d.State {
	case "device":
	case iosLocked, iosDeveloperOff, iosOutdated:
		e.until = time.Now().Add(iosInfoRetry)
	default:
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.entries == nil {
		k.entries = map[string]iosKnown{}
	}
	k.entries[udid] = e
}

// rebind moves what is known from the old id to the new one: DeviceShell itself made the
// phone re-enumerate (QuickTime), it is still the same phone.
func (k *iosInfoCache) rebind(udid string, old, now int) {
	if now == 0 {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if e, ok := k.entries[udid]; ok && e.usbmux == old {
		e.usbmux = now
		k.entries[udid] = e
	}
}

// forget drops what is known of udid, so the next scan asks again.
func (k *iosInfoCache) forget(udid string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.entries, udid)
}
