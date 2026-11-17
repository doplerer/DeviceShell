package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doplerer/deviceshell/internal/procstat"
)

// The bench (deviceshell.exe --internal-bench '<json>', hidden) opens real windows on fake
// connectors, so a wall of 200+ phones can be measured without 200 phones. Each fake
// connector replays a recording of .cache/bench at its real pace through the real
// decoder or, without recordings, publishes synthetic pictures of the phone's size.
// Every 5 s it writes CPU, memory, frame rates and the time of each stage.

type BenchOptions struct {
	Android     int    `json:"android"`
	IPhone      int    `json:"iphone"`
	Seconds     int    `json:"seconds"`
	FPS         int    `json:"fps"`
	Recordings  string `json:"recordings"` // folder with android.dsrec and iphone.dsrec
	ReadOnly    bool   `json:"readOnly"`
	AllMonitors bool   `json:"allMonitors"`
	Profile     string `json:"profile"`  // CPU profile (pprof) of the measured part
	Baseline    bool   `json:"baseline"` // without tuneRuntime, to compare
}

// benchSource is a fake connector: a frame hub fed by a recording or by synthetic pictures.
type benchSource struct {
	frameHub
	bytes atomic.Int64
}

func (s *benchSource) live() bool      { return true }
func (s *benchSource) flags() uint32   { return 0 }
func (s *benchSource) received() int64 { return s.bytes.Load() }

// syntheticFrames builds a few YUV 4:2:0 pictures of w x h with moving bars and text-like
// detail, so the scaler does real work. They are shared, read only, by every source.
func syntheticFrames(w, h, n int) []yuvView {
	cw, ch := (w+1)/2, (h+1)/2
	out := make([]yuvView, n)
	for i := range out {
		y, u, v := make([]byte, w*h), make([]byte, cw*ch), make([]byte, cw*ch)
		for r := range h {
			for c := range w {
				val := 16 + (c*219/max(1, w-1)+i*7)%220
				if (r/24+i)%5 == 0 && c%9 < 6 { // "lines of text"
					val = 30
				}
				y[r*w+c] = byte(val)
			}
		}
		for r := range ch {
			for c := range cw {
				u[r*cw+c] = byte(128 + (r+i*3)%60 - 30)
				v[r*cw+c] = byte(128 + (c+i*5)%60 - 30)
			}
		}
		out[i] = yuvView{w: w, h: h, cw: cw, sx: 1, sy: 1, y: y, u: u, v: v, ys: w, cs: cw}
	}
	return out
}

// feedSynthetic publishes the pictures at fps until c ends.
func (s *benchSource) feedSynthetic(c context.Context, frames []yuvView, fps int) {
	ticker := time.NewTicker(time.Second / time.Duration(fps))
	defer ticker.Stop()
	size := int64(frames[0].w * frames[0].h / 20) // about the bytes of an H.264 frame
	for i := 0; ; i++ {
		select {
		case <-c.Done():
			return
		case <-ticker.C:
		}
		s.bytes.Add(size)
		// As FFmpeg's pictures since the hub takes them by reference: no copy.
		f := frames[i%len(frames)]
		f.own = func() func() { return func() {} }
		s.publish(f)
	}
}

// feedRecording decodes the recording at its real pace, over and over, until c ends.
func (s *benchSource) feedRecording(c context.Context, toolDir string, units []recordedUnit, hevc bool) error {
	dec, e := newVideoDecoder(toolDir, hevc)
	if e != nil {
		return e
	}
	defer dec.close()
	for c.Err() == nil {
		started := time.Now()
		for _, u := range units {
			if wait := u.at - time.Since(started); wait > 0 {
				select {
				case <-c.Done():
					return nil
				case <-time.After(wait):
				}
			}
			s.bytes.Add(int64(len(u.data)))
			timedDecode(dec, u.data, s.publish)
		}
	}
	return nil
}

// benchRoot finds the DeviceShell folder as the CLI does.
func benchService() (*Service, error) {
	app := New()
	exe, _ := os.Executable()
	app.Root = filepath.Dir(exe)
	if p := Getenv("ROOT"); p != "" {
		app.Root = p
	}
	if e := app.MigrateLayout(); e != nil && !IsLayoutConflict(e) {
		return nil, e
	}
	var manifest struct{ Asset string }
	if e := app.ReadJSON(app.InstallManifestPath(), &manifest); e != nil {
		return nil, e
	}
	app.ToolDir = app.ToolsPath(strings.TrimSuffix(manifest.Asset, ".zip"))
	return app, app.EnsureLayout()
}

// RunBench runs the bench described by raw (BenchOptions as JSON) and returns the exit code.
func RunBench(raw string) int {
	var o BenchOptions
	if e := json.Unmarshal([]byte(raw), &o); e != nil {
		fmt.Fprintln(os.Stderr, "bench:", e)
		return 2
	}
	app, e := benchService()
	if e != nil {
		fmt.Fprintln(os.Stderr, "bench:", e)
		return 1
	}
	if e = app.runBench(o, os.Stdout); e != nil {
		fmt.Fprintln(os.Stderr, "bench:", e)
		return 1
	}
	return 0
}

func (app *Service) runBench(o BenchOptions, console io.Writer) error {
	o.Seconds = max(10, o.Seconds)
	if o.FPS <= 0 {
		o.FPS = viewFPS
	}
	o.FPS = min(120, o.FPS)
	n := o.Android + o.IPhone
	if n < 1 {
		return fmt.Errorf("no windows: set android or iphone")
	}
	logFile, e := os.Create(app.LogsPath(fmt.Sprintf("bench-%s.log", time.Now().Format("20060102-150405"))))
	if e != nil {
		return e
	}
	defer logFile.Close()
	out := io.MultiWriter(console, logFile)
	say := func(format string, args ...any) { fmt.Fprintf(out, format+"\n", args...) }
	if !o.Baseline {
		tuneRuntime()
	} else {
		tuneOnce.Do(func() {})
	}
	layout, areas := app.benchLayout(n, o.AllMonitors)
	if len(layout) != n {
		return fmt.Errorf("%d windows do not fit on the screen", n)
	}
	say("bench: %d Android + %d iPhone, %d fps, %d s, %d threads, windows of %dx%d", o.Android, o.IPhone, o.FPS, o.Seconds, runtime.NumCPU(), layout[0].W, layout[0].H)
	c, cancel := context.WithCancel(context.Background())
	defer cancel()
	load := func(name string) ([]recordedUnit, bool) {
		if o.Recordings == "" {
			return nil, false
		}
		units, hevc, e := readStreamRecording(filepath.Join(o.Recordings, name))
		if e != nil {
			say("no recording %s (%v): synthetic frames", name, e)
			return nil, false
		}
		say("recording %s: %d units, %.1f s", name, len(units), units[len(units)-1].at.Seconds())
		return units, hevc
	}
	androidUnits, androidHEVC := load("android.dsrec")
	iphoneUnits, iphoneHEVC := load("iphone.dsrec")
	androidSize := androidVideoSize(layout[0])
	aw := (androidSize*1080/2340 + 1) &^ 1
	var androidFrames, iphoneFrames []yuvView
	if o.Android > 0 && androidUnits == nil {
		androidFrames = syntheticFrames(aw, androidSize, 8)
	}
	if o.IPhone > 0 && iphoneUnits == nil {
		iphoneFrames = syntheticFrames(1320, 2868, 8)
	}
	packs := app.StartWallPacks(layout, areas)
	defer packs.Stop()
	var wg sync.WaitGroup
	var children benchChildren
	opened := time.Now()
	for i := range n {
		src := &benchSource{}
		platform := "android"
		if i >= o.Android {
			platform = "ios"
		}
		src.init("bench-"+platform, nil)
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch {
			case platform == "android" && androidUnits != nil:
				if e := src.feedRecording(c, app.ToolDir, androidUnits, androidHEVC); e != nil {
					say("decoder: %v", e)
				}
			case platform == "android":
				src.feedSynthetic(c, androidFrames, o.FPS)
			case iphoneUnits != nil:
				if e := src.feedRecording(c, app.ToolDir, iphoneUnits, iphoneHEVC); e != nil {
					say("decoder: %v", e)
				}
			default:
				src.feedSynthetic(c, iphoneFrames, o.FPS)
			}
		}()
		title := fmt.Sprintf("banco %s %d", platform, i+1)
		pack, slot := packs.For(i)
		if e := app.benchWindow(c, &wg, slot, pack, &children, layout[i], title, platform, o.ReadOnly, src); e != nil {
			cancel()
			wg.Wait()
			return e
		}
	}
	// Wait for every window, then measure.
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); time.Sleep(100 * time.Millisecond) {
		all := true
		for i := range n {
			all = all && packs.HasWindow(i)
		}
		if all {
			break
		}
	}
	say("windows opened in %d ms", time.Since(opened).Milliseconds())
	if o.Profile != "" {
		if f, e := os.Create(o.Profile); e == nil {
			if pprof.StartCPUProfile(f) == nil {
				defer func() { pprof.StopCPUProfile(); f.Close() }()
			} else {
				f.Close()
			}
		}
	}
	var gcBefore debug.GCStats
	debug.ReadGCStats(&gcBefore)
	var samples []perfInterval
	last := takePerfSnapshot(children.usage())
	ticker := time.NewTicker(5 * time.Second)
	for end := time.Now().Add(time.Duration(o.Seconds) * time.Second); time.Now().Before(end); {
		<-ticker.C
		now := takePerfSnapshot(children.usage())
		p := perfBetween(last, now)
		last = now
		samples = append(samples, p)
		say("%s", benchLine(p, n))
	}
	ticker.Stop()
	if len(samples) > 1 {
		samples = samples[1:] // the first interval warms up
	}
	var avg perfInterval
	for _, p := range samples {
		avg.cores += p.cores / float64(len(samples))
		avg.decodedFPS += p.decodedFPS / float64(len(samples))
		avg.paintedFPS += p.paintedFPS / float64(len(samples))
		avg.decodeMs += p.decodeMs / float64(len(samples))
		avg.scaleMs += p.scaleMs / float64(len(samples))
		avg.paintMs += p.paintMs / float64(len(samples))
		avg.workingSet, avg.private = max(avg.workingSet, p.workingSet), max(avg.private, p.private)
	}
	say("AVERAGE %s", benchLine(avg, n))
	var gcAfter debug.GCStats
	debug.ReadGCStats(&gcAfter)
	say("Go garbage collections: %d (total pause %v)", gcAfter.NumGC-gcBefore.NumGC, gcAfter.PauseTotal-gcBefore.PauseTotal)
	cancel()
	packs.Stop()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		say("warning: some windows did not close within 15 s")
	}
	return nil
}

func benchLine(p perfInterval, windows int) string {
	return fmt.Sprintf("cpu=%.2f cores (%.1f%%) ram=%d MB private=%d MB decoded=%.0f fps painted=%.0f fps (%.1f per window) ms: decode=%.2f scale=%.3f paint=%.3f",
		p.cores, 100*p.cores/float64(runtime.NumCPU()), p.workingSet/mb, p.private/mb, p.decodedFPS, p.paintedFPS, p.paintedFPS/float64(max(1, windows)), p.decodeMs, p.scaleMs, p.paintMs)
}

// benchChildren are the window processes the bench started, if windows are processes.
type benchChildren struct {
	mu   sync.Mutex
	pids []int
}

func (b *benchChildren) add(pid int) {
	b.mu.Lock()
	b.pids = append(b.pids, pid)
	b.mu.Unlock()
}

func (b *benchChildren) usage() procstat.Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	var u procstat.Usage
	for _, pid := range b.pids {
		u = u.Add(procstat.Process(pid))
	}
	return u
}
