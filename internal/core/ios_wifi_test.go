package core

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/doplerer/deviceshell/internal/ios/remotepairing"
)

func wifiTestService(t *testing.T, usb []string) *Service {
	app := New()
	app.Root = t.TempDir()
	if e := app.EnsureLayout(); e != nil {
		t.Fatal(e)
	}
	app.wifi.reachable = func(string, int) bool { return false } // no network in tests
	app.wifi.sweep = func() []string { return nil }
	app.RunIOS = func(_ context.Context, args ...string) (string, error) {
		switch {
		case strings.Join(args, " ") == "list":
			ids := `"` + strings.Join(usb, `","`) + `"`
			if len(usb) == 0 {
				ids = ""
			}
			return `{"deviceList":[` + ids + `]}`, nil
		case len(args) == 2 && args[1] == "info":
			return `{"ProductType":"iPhone18,2","ProductVersion":"27.2"}`, nil
		case len(args) == 3 && args[1] == "devmode":
			return `{"DeveloperModeEnabled":true}`, nil
		}
		return "", nil
	}
	return app
}

// The scanner checks a new address over its tunnel once, registers the phone and
// discovery lists it over Wi-Fi; a phone with a cable is listed over USB only, and an
// address whose phone does not trust this PC is not checked again for a while.
func TestWiFiScanListsPairedPhonesAndPrefersUSB(t *testing.T) {
	app := wifiTestService(t, nil)
	probes := map[string]int{}
	app.wifi.browse = func(context.Context, []string) []remotepairing.Advert {
		return []remotepairing.Advert{{Identifier: "x", Host: "10.0.0.5", Port: 49152}, {Identifier: "y", Host: "10.0.0.9", Port: 49152}}
	}
	app.wifi.probe = func(_ context.Context, host string, _ int) (wifiProbe, error) {
		probes[host]++
		if host == "10.0.0.9" {
			return wifiProbe{}, errWiFiNotOurs
		}
		return wifiProbe{udid: "U1", model: "iPhone18,2", ios: "27.2", developer: true}, nil
	}
	app.wifiScan(context.Background())
	app.wifiScan(context.Background())
	if probes["10.0.0.5"] != 1 || probes["10.0.0.9"] != 1 {
		t.Fatalf("probes %v: a known or foreign address is not checked every round", probes)
	}
	if info := app.readIOSOptions().WiFi["U1"]; info.Host != "10.0.0.5" || info.Model != "iPhone18,2" || info.IOS != "27.2" {
		t.Fatalf("registry %+v", info)
	}
	ds, e := iosBackend{app}.Discover(context.Background())
	if e != nil || len(ds) != 1 || ds[0].Serial != "ios:U1" || ds[0].State != "device" || ds[0].Transport != "wifi" {
		t.Fatalf("%+v %v", ds, e)
	}
	if e := (iosBackend{app}).Supports(ds[0], Spec{Action: "app-install", Args: []string{"a.ipa"}}); e == nil || !strings.Contains(e.Error(), "Wi-Fi") {
		t.Fatalf("app-install over Wi-Fi: %v", e)
	}
	if e := (iosBackend{app}).Supports(ds[0], Spec{Action: "tap"}); e != nil {
		t.Fatal(e)
	}
	usb := wifiTestService(t, []string{"U1"})
	usb.wifi.browse, usb.wifi.probe = app.wifi.browse, app.wifi.probe
	usb.wifiScan(context.Background())
	ds, e = iosBackend{usb}.Discover(context.Background())
	if e != nil || len(ds) != 1 || ds[0].Transport != "usb" {
		t.Fatalf("with a cable: %+v %v", ds, e)
	}
}

// A phone that restarted (no developer image) stays out of the lists until it is
// connected by cable once; one that stops answering is forgotten.
func TestWiFiPhoneNeedingCableIsHiddenAndGonePhoneForgotten(t *testing.T) {
	app := wifiTestService(t, nil)
	answer := true
	app.wifi.browse = func(context.Context, []string) []remotepairing.Advert {
		if !answer {
			return nil
		}
		return []remotepairing.Advert{{Host: "10.0.0.5", Port: 1}}
	}
	app.wifi.probe = func(context.Context, string, int) (wifiProbe, error) {
		return wifiProbe{udid: "U1", ios: "27.2"}, nil
	}
	app.wifiScan(context.Background())
	ds, _ := iosBackend{app}.Discover(context.Background())
	if len(ds) != 1 || ds[0].State != iosNeedsUSB || len(Listed(ds)) != 0 {
		t.Fatalf("%+v", ds)
	}
	answer = false
	app.wifi.mu.Lock()
	app.wifi.present["U1"].seen = app.wifi.present["U1"].seen.Add(-2 * wifiForget)
	app.wifi.mu.Unlock()
	app.wifiScan(context.Background())
	if ds, _ = (iosBackend{app}).Discover(context.Background()); len(ds) != 0 {
		t.Fatalf("gone phone still listed: %+v", ds)
	}
	if lines := app.WiFiStatus(nil); len(lines) != 1 || !strings.Contains(lines[0], "no está en la red") {
		t.Fatalf("doctor: %v", lines)
	}
}

// A phone just paired has no address in the registry: while it is missing every
// local address is asked directly, it is found and its address kept, and the sweep
// stops. The address it had while it still had to answer «Confiar» is checked again
// once it is paired.
func TestWiFiFindsJustPairedPhoneWithoutItsAddress(t *testing.T) {
	app := wifiTestService(t, nil)
	if e := app.updateIOSOptions(func(cfg *iosOptions) {
		cfg.WiFi = map[string]iosWiFiInfo{"U1": {Model: "iPhone18,2", Paired: "2026-11-02T18:00:00+01:00"}}
	}); e != nil {
		t.Fatal(e)
	}
	app.wifi.sweep = func() []string { return []string{"192.168.1.71", "192.168.1.72"} }
	var asked [][]string
	trusted := false
	app.wifi.browse = func(_ context.Context, hints []string) []remotepairing.Advert {
		asked = append(asked, hints)
		// Multicast does not reach the phone: only a direct query finds it.
		if slices.Contains(hints, "192.168.1.72") {
			return []remotepairing.Advert{{Identifier: "x", Host: "192.168.1.72", Port: 49152}}
		}
		return nil
	}
	probes := 0
	app.wifi.probe = func(context.Context, string, int) (wifiProbe, error) {
		probes++
		if !trusted {
			return wifiProbe{}, errWiFiNotOurs
		}
		return wifiProbe{udid: "U1", model: "iPhone18,2", ios: "27.2", developer: true}, nil
	}
	// Checked before «Confiar»: refused, and not checked again for a while.
	app.wifiScan(context.Background())
	app.wifiScan(context.Background())
	if probes != 1 {
		t.Fatal("probes", probes)
	}
	trusted = true
	app.wifiPaired()
	app.wifiScan(context.Background())
	if probes != 2 || !slices.Contains(asked[2], "192.168.1.71") {
		t.Fatal(probes, asked)
	}
	info := app.readIOSOptions().WiFi["U1"]
	if info.Host != "192.168.1.72" || info.Port != 49152 || info.Paired == "" || info.Seen == "" {
		t.Fatalf("registry %+v", info)
	}
	ds, _ := iosBackend{app}.Discover(context.Background())
	if len(ds) != 1 || ds[0].State != "device" || ds[0].Transport != "wifi" {
		t.Fatalf("%+v", ds)
	}
	// Present: only its own address is asked.
	app.wifiScan(context.Background())
	if last := asked[len(asked)-1]; len(last) != 1 || last[0] != "192.168.1.72" {
		t.Fatal(last)
	}
	// Every check that answers updates when it was last seen.
	app.wifi.mu.Lock()
	app.wifi.present["U1"].checked = time.Now().Add(-2 * wifiCheckEvery)
	app.wifi.mu.Unlock()
	if e := app.updateIOSOptions(func(cfg *iosOptions) {
		v := cfg.WiFi["U1"]
		v.Seen = "2026-01-01T00:00:00Z"
		cfg.WiFi["U1"] = v
	}); e != nil {
		t.Fatal(e)
	}
	app.wifiScan(context.Background())
	if seen := app.readIOSOptions().WiFi["U1"].Seen; seen == "2026-01-01T00:00:00Z" {
		t.Fatal("seen not updated")
	}
}

// The picture of an iPhone: DisplayService over Wi-Fi always, and on a cable unless
// QuickTime is chosen in the settings; other values are refused.
func TestIOSVideoSourceFollowsSettings(t *testing.T) {
	app := wifiTestService(t, nil)
	usb, wifi := Device{Transport: "usb"}, Device{Transport: "wifi"}
	if app.iosVideoSource(usb) != IPhoneVideoDisplayService || app.iosVideoSource(wifi) != IPhoneVideoDisplayService {
		t.Fatal("default is not DisplayService")
	}
	if _, e := app.SaveSetting("iphoneVideo", "quicktime"); e != nil {
		t.Fatal(e)
	}
	if app.iosVideoSource(usb) != IPhoneVideoQuickTime || app.iosVideoSource(wifi) != IPhoneVideoDisplayService {
		t.Fatal("QuickTime setting not followed on cable only")
	}
	if _, e := app.SaveSetting("iphoneVideo", "vlc"); e == nil {
		t.Fatal("unknown video source accepted")
	}
}

// A renamed group keeps its devices; unknown groups, taken names and names a target
// could not use are refused.
func TestRenameGroup(t *testing.T) {
	app := wifiTestService(t, nil)
	if e := app.CreateGroup("a", []string{"S1", "S2"}); e != nil {
		t.Fatal(e)
	}
	if e := app.CreateGroup("b", []string{"S3"}); e != nil {
		t.Fatal(e)
	}
	if e := app.RenameGroup("a", "c"); e != nil {
		t.Fatal(e)
	}
	g, _ := app.Groups()
	if len(g["c"]) != 2 || g["a"] != nil || len(g["b"]) != 1 {
		t.Fatal(g)
	}
	for _, bad := range [][2]string{{"x", "y"}, {"c", "b"}, {"c", "con espacio"}, {"c", "a,b"}, {"c", ""}} {
		if e := app.RenameGroup(bad[0], bad[1]); e == nil {
			t.Fatal("accepted", bad)
		}
	}
}

// How each device reaches the PC, as the device list shows it.
func TestDeviceLink(t *testing.T) {
	for _, c := range []struct {
		d    Device
		want string
	}{
		{Device{Serial: "R5CT00TEST1"}, "usb"},
		{Device{Serial: "192.168.1.40:5555"}, "wifi"},
		{Device{Serial: "adb-R5CT00TEST1-AbCdEf._adb-tls-connect._tcp"}, "wifi"},
		{Device{Serial: "ios:U1", Transport: "usb"}, "usb"},
		{Device{Serial: "ios:U1", Transport: "wifi"}, "wifi"},
		{Device{Serial: "ios:U1"}, "usb"},
	} {
		if got := c.d.Link(); got != c.want {
			t.Errorf("%s/%s: %s, want %s", c.d.Serial, c.d.Transport, got, c.want)
		}
	}
}
