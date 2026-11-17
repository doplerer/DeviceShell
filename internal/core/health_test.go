package core

import (
	"strings"
	"testing"
)

func (app *Service) testParseHealth(t *testing.T) {
	sep := "\n" + healthSeparator + "\n"
	out := strings.Join([]string{
		"Current Battery Service state:\r\n  AC powered: false\r\n  USB powered: true\r\n  status: 2\r\n  level: 85\r\n  scale: 100\r\n  temperature: 312",
		"13", "33",
		"Filesystem     1K-blocks    Used Available Use% Mounted on\n/dev/block/dm-5 112115432 2412 109703020   3% /data",
		"MemAvailable:    2097152 kB",
		"3: wlan0: <BROADCAST> mtu 1500\n    inet 192.168.1.23/24 brd 192.168.1.255 scope global wlan0",
		"  mWakefulness=Awake",
	}, sep)
	h := app.ParseHealth(out)
	if h.Battery != 85 || h.TempC != 31.2 || !h.Charging || h.Android != "13" || h.SDK != 33 || h.StorageFree != 107131 || h.MemFree != 2048 || h.IP != "192.168.1.23" || !h.ScreenOn {
		t.Fatalf("%+v", h)
	}
	// Older Android: sized df, no Wi-Fi, screen off, unknown battery.
	old := strings.Join([]string{"", "7.1.2", "25", "Filesystem Size Used Free Blksize\n/data 25.5G 5.1G 20.3G 4096", "", "Device \"wlan0\" does not exist.", "mWakefulness=Asleep"}, sep)
	h = app.ParseHealth(old)
	if h.Battery != -1 || h.StorageFree != 20787 || h.IP != "" || h.ScreenOn || h.Charging {
		t.Fatalf("%+v", h)
	}
	if h := app.ParseHealth("garbage"); h.Battery != -1 || h.SDK != 0 {
		t.Fatalf("%+v", h)
	}
}

func (app *Service) testHealthAlerts(t *testing.T) {
	hot := Health{Battery: 12, TempC: 46.4}
	if a := app.HealthAlerts(hot, 42, 15); len(a) != 2 || a[0] != "46 °C" || a[1] != "batería 12 %" {
		t.Fatal(a)
	}
	if a := app.HealthAlerts(hot, 0, 0); len(a) != 0 {
		t.Fatal("alerts set to 0 still fire", a)
	}
	if a := app.HealthAlerts(Health{Battery: -1, TempC: 30}, 42, 15); len(a) != 0 {
		t.Fatal("unknown battery alerted", a)
	}
}
