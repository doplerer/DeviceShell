package core

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Health is a snapshot of a device, read on demand and never stored in the inventory.
// Battery is -1 and TempC 0 when the device does not report them.
type Health struct {
	Battery     int     `json:"battery"`
	TempC       float64 `json:"tempC"`
	Charging    bool    `json:"charging"`
	Android     string  `json:"android"`
	SDK         int     `json:"sdk"`
	StorageFree int64   `json:"storageFreeMB"`
	MemFree     int64   `json:"memFreeMB"`
	IP          string  `json:"ip,omitempty"`
	ScreenOn    bool    `json:"screenOn"`
}

const healthSeparator = "---DEVICESHELL---"

// DeviceHealth asks everything in a single adb call; sections that fail are left empty.
func (app *Service) DeviceHealth(c context.Context, serial string) (Health, error) {
	script := strings.Join([]string{"dumpsys battery", "getprop ro.build.version.release", "getprop ro.build.version.sdk", "df /data", "grep MemAvailable /proc/meminfo", "ip -f inet addr show wlan0", "dumpsys power | grep mWakefulness="}, "; echo "+healthSeparator+"; ") + "; true"
	out, e := app.Command(c, "-s", serial, "shell", script)
	if e != nil {
		return Health{}, e
	}
	return app.ParseHealth(out), nil
}

// ParseHealth reads the output of DeviceHealth; anything missing keeps its "unknown" value.
func (app *Service) ParseHealth(out string) Health {
	h := Health{Battery: -1}
	sec := strings.Split(strings.ReplaceAll(out, "\r", ""), healthSeparator)
	part := func(i int) string {
		if i < len(sec) {
			return strings.TrimSpace(sec[i])
		}
		return ""
	}
	for _, l := range strings.Split(part(0), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch k {
		case "level":
			if n, e := strconv.Atoi(v); e == nil {
				h.Battery = n
			}
		case "temperature":
			if n, e := strconv.Atoi(v); e == nil {
				h.TempC = float64(n) / 10
			}
		case "AC powered", "USB powered", "Wireless powered", "Dock powered":
			h.Charging = h.Charging || v == "true"
		}
	}
	h.Android = part(1)
	h.SDK, _ = strconv.Atoi(part(2))
	h.StorageFree = parseDfFree(part(3))
	if f := strings.Fields(part(4)); len(f) >= 2 && f[0] == "MemAvailable:" {
		kb, _ := strconv.ParseInt(f[1], 10, 64)
		h.MemFree = kb / 1024
	}
	if m := regexp.MustCompile(`inet (\d+\.\d+\.\d+\.\d+)`).FindStringSubmatch(part(5)); m != nil {
		h.IP = m[1]
	}
	h.ScreenOn = strings.Contains(part(6), "mWakefulness=Awake")
	return h
}

// parseDfFree returns the free MB of the last df line, in the 1K-blocks format of toybox or the sized one of older Android.
func parseDfFree(out string) int64 {
	lines := strings.Split(out, "\n")
	if len(lines) < 2 {
		return 0
	}
	f := strings.Fields(lines[len(lines)-1])
	if strings.Contains(lines[0], "Available") && len(f) >= 4 {
		kb, _ := strconv.ParseInt(f[len(f)-3], 10, 64)
		return kb / 1024
	}
	if len(f) < 4 {
		return 0
	}
	v := strings.ToUpper(f[3])
	unit := map[byte]float64{'K': 1.0 / 1024, 'M': 1, 'G': 1024, 'T': 1024 * 1024}[v[len(v)-1]]
	n, e := strconv.ParseFloat(strings.TrimRight(v, "KMGT"), 64)
	if e != nil || unit == 0 {
		return 0
	}
	return int64(n * unit)
}

// CollectHealth reads every connected device in parallel; devices that do not answer in time are left out.
func (app *Service) CollectHealth(parent context.Context, devices []Device, parallel int) map[string]Health {
	out := map[string]Health{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	queue := make(chan Device)
	for w := 0; w < max(1, parallel); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range queue {
				c, cancel := context.WithTimeout(parent, 10*time.Second)
				h, e := app.DeviceHealth(c, d.Serial)
				cancel()
				if e == nil {
					mu.Lock()
					out[d.Serial] = h
					mu.Unlock()
				}
			}
		}()
	}
	for _, d := range devices {
		if d.State == "device" && d.OS() == "android" {
			queue <- d
		}
	}
	close(queue)
	wg.Wait()
	return out
}

// TooHot and LowBattery compare with the limits of the settings; a limit of 0 is off.
func (h Health) TooHot(maxTemp int) bool {
	return maxTemp > 0 && h.TempC > float64(maxTemp)
}
func (h Health) LowBattery(minBattery int) bool {
	return minBattery > 0 && h.Battery >= 0 && h.Battery < minBattery
}

// HealthAlerts lists what is beyond the limits: above maxTemp °C or below minBattery %.
func (app *Service) HealthAlerts(h Health, maxTemp, minBattery int) []string {
	var out []string
	if h.TooHot(maxTemp) {
		out = append(out, fmt.Sprintf("%.0f °C", h.TempC))
	}
	if h.LowBattery(minBattery) {
		out = append(out, fmt.Sprintf(app.L("batería %d %%", "battery %d %%"), h.Battery))
	}
	return out
}
