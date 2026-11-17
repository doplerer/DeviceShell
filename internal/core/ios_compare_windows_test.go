package core

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/diagnostics"

	"github.com/doplerer/deviceshell/internal/procstat"
)

// Compares the two pictures an iPhone on a cable can give: QuickTime (today's) and
// the DisplayService H.264 stream (the Wi-Fi one, here over the USB tunnel). Opt-in,
// with the phone on a cable, unlocked on its home screen, and DeviceShell closed:
//
//	DEVICESHELL_COMPARE_UDID=<UDID> DEVICESHELL_ROOT=<DeviceShell folder> go test ./internal/core -run TestCompareCableVideo -v -timeout 20m
//
// It swipes between home screen pages by itself. Report and pictures go to
// docs/research/video-comparison/.

type compareResult struct {
	source                   string
	firstFrame               time.Duration
	restFPS, restMbit        float64
	motionFPS, motionMbit    float64
	latencies                []time.Duration
	restSharp, motionSharp   float64
	cores, decodeMs          float64
	sustainFPS, sustainCores float64
	tempStart, tempEnd       float64 // °C, NaN if not read
	width, height            int
}

func TestCompareCableVideo(t *testing.T) {
	udid := os.Getenv("DEVICESHELL_COMPARE_UDID")
	if udid == "" || os.Getenv("DEVICESHELL_ROOT") == "" {
		t.Skip("set DEVICESHELL_COMPARE_UDID and DEVICESHELL_ROOT")
	}
	app, e := benchService()
	if e != nil {
		t.Fatal(e)
	}
	out, _ := filepath.Abs(filepath.Join("..", "..", "docs", "research", "video-comparison"))
	if e := os.MkdirAll(out, 0o755); e != nil {
		t.Fatal(e)
	}
	d := Device{Serial: "ios:" + udid, Alias: "iPhone", Platform: "ios", State: "device", Transport: "usb"}
	var results []compareResult
	for i, source := range []string{"quicktime", "displayservice"} {
		if i > 0 {
			time.Sleep(10 * time.Second) // QuickTime gives the USB back to its normal mode
		}
		results = append(results, compareSource(t, app, d, source, out))
	}
	report := compareReport(results)
	t.Log("\n" + report)
	os.WriteFile(filepath.Join(out, "report.md"), []byte(report), 0o644)
}

func batteryTemp(udid string) float64 {
	device, e := ios.GetDevice(udid)
	if e != nil {
		return math.NaN()
	}
	conn, e := diagnostics.New(device)
	if e != nil {
		return math.NaN()
	}
	defer conn.Close()
	b, e := conn.Battery()
	if e != nil || b.Temperature == 0 {
		return math.NaN()
	}
	return float64(b.Temperature) / 100
}

// hubLuma returns the newest frame's number and a 48x104 grid of mean luma.
func hubLuma(h *frameHub) (uint64, []float64, int, int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	img := h.frame
	if img == nil {
		return h.seq, nil, 0, 0
	}
	const gw, gh = 48, 104
	out := make([]float64, gw*gh)
	for gy := range gh {
		for gx := range gw {
			sum, n := 0, 0
			y0, x0 := gy*img.h/gh, gx*img.w/gw
			for y := y0; y < y0+img.h/gh; y += 4 {
				for x := x0; x < x0+img.w/gw; x += 4 {
					sum += int(img.y[y*img.ys+x])
					n++
				}
			}
			out[gy*gw+gx] = float64(sum) / float64(max(1, n))
		}
	}
	return h.seq, out, img.w, img.h
}

// hubSharpness is the variance of the Laplacian of the luma in the middle third of
// the newest frame (higher: more detail).
func hubSharpness(h *frameHub) float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	img := h.frame
	if img == nil {
		return 0
	}
	var sum, sum2 float64
	n := 0
	for y := img.h / 3; y < 2*img.h/3; y++ {
		for x := 1; x < img.w-1; x++ {
			c := int(img.y[y*img.ys+x])
			l := float64(int(img.y[(y-1)*img.ys+x]) + int(img.y[(y+1)*img.ys+x]) + int(img.y[y*img.ys+x-1]) + int(img.y[y*img.ys+x+1]) - 4*c)
			sum += l
			sum2 += l * l
			n++
		}
	}
	mean := sum / float64(n)
	return sum2/float64(n) - mean*mean
}

func hubPNG(h *frameHub, path string) {
	h.mu.Lock()
	img := h.frame
	if img == nil {
		h.mu.Unlock()
		return
	}
	out := image.NewYCbCr(image.Rect(0, 0, img.w, img.h), image.YCbCrSubsampleRatio420)
	for y := range img.h {
		copy(out.Y[y*out.YStride:y*out.YStride+img.w], img.y[y*img.ys:])
	}
	for y := range min(img.ch, (img.h+1)/2) {
		copy(out.Cb[y*out.CStride:y*out.CStride+min(img.cw, out.CStride)], img.u[y*img.cs:])
		copy(out.Cr[y*out.CStride:y*out.CStride+min(img.cw, out.CStride)], img.v[y*img.cs:])
	}
	h.mu.Unlock()
	f, e := os.Create(path)
	if e != nil {
		return
	}
	png.Encode(f, out)
	f.Close()
}

func lumaDiff(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var s float64
	for i := range a {
		s += math.Abs(a[i] - b[i])
	}
	return s / float64(len(a))
}

func compareSource(t *testing.T, app *Service, d Device, source, out string) compareResult {
	r := compareResult{source: source, tempStart: batteryTemp(d.NativeID())}
	t.Logf("── %s ──", source)
	start := time.Now()
	var video *iosVideo
	if source == "quicktime" {
		video = app.openIOSVideo(d.NativeID(), 5*time.Second)
	} else {
		video = app.openDisplayVideo(d.NativeID())
	}
	c, cancel := context.WithTimeout(context.Background(), time.Minute)
	n, e := app.connectIOSNative(c, d, true, video)
	cancel()
	if e != nil {
		video.close(true)
		t.Fatalf("%s: connect: %v", source, e)
	}
	defer func() {
		n.close()
		video.close(true)
	}()
	hub := &video.frameHub
	for video.decoded.Load() == 0 && time.Since(start) < 30*time.Second {
		time.Sleep(10 * time.Millisecond)
	}
	if video.decoded.Load() == 0 {
		t.Fatalf("%s: no picture after 30 s (%s)", source, video.errorText())
	}
	r.firstFrame = time.Since(start)
	swipe := func(left bool, steps int) {
		from, to := 0.85, 0.15
		if !left {
			from, to = 0.15, 0.85
		}
		bc := context.Background()
		n.liveTouch(bc, "down", from, 0.55)
		for i := 1; i <= steps; i++ {
			time.Sleep(12 * time.Millisecond)
			n.liveTouch(bc, "move", from+(to-from)*float64(i)/float64(steps), 0.55)
		}
		n.liveTouch(bc, "up", to, 0.55)
	}
	time.Sleep(3 * time.Second)
	// At rest.
	measure := func(d time.Duration, during func(stop <-chan struct{})) (fps, mbit, cores, decodeMs float64) {
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			if during != nil {
				during(stop)
			}
		}()
		f0, b0, s0 := video.decoded.Load(), video.received(), takePerfSnapshot(procstat.Usage{})
		time.Sleep(d)
		f1, b1, s1 := video.decoded.Load(), video.received(), takePerfSnapshot(procstat.Usage{})
		close(stop)
		<-done
		p := perfBetween(s0, s1)
		return float64(f1-f0) / d.Seconds(), float64(b1-b0) * 8 / d.Seconds() / 1e6, p.cores, p.decodeMs
	}
	r.restFPS, r.restMbit, _, _ = measure(5*time.Second, nil)
	r.restSharp = hubSharpness(hub)
	_, _, r.width, r.height = hubLuma(hub)
	hubPNG(hub, filepath.Join(out, source+"-idle.png"))
	// Touch to picture: from the finger down until the picture moves.
	left := true
	for range 15 {
		_, base, _, _ := hubLuma(hub)
		t0 := time.Now()
		go swipe(left, 12)
		left = !left
		for time.Since(t0) < 2*time.Second {
			hub.mu.Lock()
			changed := hub.changed
			hub.mu.Unlock()
			select {
			case <-changed:
			case <-time.After(100 * time.Millisecond):
				continue
			}
			if _, now, _, _ := hubLuma(hub); lumaDiff(base, now) > 2 {
				r.latencies = append(r.latencies, time.Since(t0))
				break
			}
		}
		time.Sleep(1500 * time.Millisecond)
	}
	// Moving: page after page for 10 s.
	var sharp []float64
	shot := false
	r.motionFPS, r.motionMbit, r.cores, r.decodeMs = measure(10*time.Second, func(stop <-chan struct{}) {
		for left := true; ; left = !left {
			select {
			case <-stop:
				return
			default:
			}
			go swipe(left, 10)
			time.Sleep(90 * time.Millisecond) // mid-swipe
			sharp = append(sharp, hubSharpness(hub))
			if !shot {
				hubPNG(hub, filepath.Join(out, source+"-swiping.png"))
				shot = true
			}
			time.Sleep(450 * time.Millisecond)
		}
	})
	for _, s := range sharp {
		r.motionSharp += s / float64(len(sharp))
	}
	// Two minutes of swiping, for the phone's temperature and the sustained load.
	r.sustainFPS, _, r.sustainCores, _ = measure(2*time.Minute, func(stop <-chan struct{}) {
		for left := true; ; left = !left {
			select {
			case <-stop:
				return
			default:
			}
			swipe(left, 10)
			time.Sleep(400 * time.Millisecond)
		}
	})
	r.tempEnd = batteryTemp(d.NativeID())
	t.Logf("%s: %+v", source, r)
	return r
}

func compareReport(rs []compareResult) string {
	ms := func(d time.Duration) string { return fmt.Sprintf("%d ms", d.Milliseconds()) }
	pct := func(l []time.Duration, p float64) string {
		if len(l) == 0 {
			return "—"
		}
		s := append([]time.Duration(nil), l...)
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		return ms(s[min(len(s)-1, int(p*float64(len(s))))])
	}
	temp := func(v float64) string {
		if math.IsNaN(v) {
			return "—"
		}
		return fmt.Sprintf("%.1f °C", v)
	}
	rows := [][]string{{"", "QuickTime", "DisplayService H.264"}}
	add := func(name string, f func(r compareResult) string) {
		row := []string{name}
		for _, r := range rs {
			row = append(row, f(r))
		}
		rows = append(rows, row)
	}
	add("Primera imagen", func(r compareResult) string { return ms(r.firstFrame) })
	add("Resolution", func(r compareResult) string { return fmt.Sprintf("%dx%d", r.width, r.height) })
	add("fps idle", func(r compareResult) string { return fmt.Sprintf("%.1f", r.restFPS) })
	add("fps swiping", func(r compareResult) string { return fmt.Sprintf("%.1f", r.motionFPS) })
	add("fps swiping for 2 min", func(r compareResult) string { return fmt.Sprintf("%.1f", r.sustainFPS) })
	add("Mbit/s idle", func(r compareResult) string { return fmt.Sprintf("%.2f", r.restMbit) })
	add("Mbit/s swiping", func(r compareResult) string { return fmt.Sprintf("%.2f", r.motionMbit) })
	add("Latencia toque→imagen (mediana)", func(r compareResult) string { return pct(r.latencies, 0.5) })
	add("Latencia toque→imagen (p90)", func(r compareResult) string { return pct(r.latencies, 0.9) })
	add("Sharpness idle", func(r compareResult) string { return fmt.Sprintf("%.0f", r.restSharp) })
	add("Sharpness swiping", func(r compareResult) string { return fmt.Sprintf("%.0f", r.motionSharp) })
	add("PC CPU swiping (cores)", func(r compareResult) string { return fmt.Sprintf("%.2f", r.cores) })
	add("Descodificar un fotograma", func(r compareResult) string { return fmt.Sprintf("%.2f ms", r.decodeMs) })
	add("PC CPU over 2 min (cores)", func(r compareResult) string { return fmt.Sprintf("%.2f", r.sustainCores) })
	add("iPhone battery temperature at start", func(r compareResult) string { return temp(r.tempStart) })
	add("iPhone battery temperature after ~3 min", func(r compareResult) string { return temp(r.tempEnd) })
	var b strings.Builder
	b.WriteString("# iPhone video on cable: QuickTime vs DisplayService H.264\n\n")
	b.WriteString(time.Now().Format("2006-01-02 15:04") + ". Measured by TestCompareCableVideo with automatic swipes between home screen pages.\n\n")
	for i, row := range rows {
		b.WriteString("| " + strings.Join(row, " | ") + " |\n")
		if i == 0 {
			b.WriteString("|---|---|---|\n")
		}
	}
	b.WriteString("\nSharpness: variance of the Laplacian of the luminance in the central third (higher means more detail). Frames: quicktime-*.png and displayservice-*.png in this folder (not committed: they show the phone's screen).\n")
	return b.String()
}
