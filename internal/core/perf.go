package core

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doplerer/deviceshell/internal/procstat"
)

// perf holds process-wide totals of the video path, read by the bench and by
// performance.log: pictures decoded and painted and the time spent on each stage.
var perf struct {
	decoded, painted           atomic.Int64
	decodeNs, scaleNs, paintNs atomic.Int64
	windows                    atomic.Int32
}

// perfSnapshot is one reading of perf and of the process.
type perfSnapshot struct {
	at                         time.Time
	usage                      procstat.Usage
	decoded, painted           int64
	decodeNs, scaleNs, paintNs int64
}

func takePerfSnapshot(extra procstat.Usage) perfSnapshot {
	return perfSnapshot{at: time.Now(), usage: procstat.Self().Add(extra), decoded: perf.decoded.Load(), painted: perf.painted.Load(),
		decodeNs: perf.decodeNs.Load(), scaleNs: perf.scaleNs.Load(), paintNs: perf.paintNs.Load()}
}

// perfInterval is what happened between two snapshots.
type perfInterval struct {
	seconds                    float64
	cores                      float64 // CPU time / wall time
	workingSet, private        uint64  // at the end
	decodedFPS, paintedFPS     float64 // totals
	decodeMs, scaleMs, paintMs float64 // per picture
}

func perfBetween(a, b perfSnapshot) perfInterval {
	s := b.at.Sub(a.at).Seconds()
	if s <= 0 {
		s = 1
	}
	decoded, painted := b.decoded-a.decoded, b.painted-a.painted
	per := func(ns, n int64) float64 { return float64(ns) / 1e6 / float64(max(1, n)) }
	return perfInterval{seconds: s, cores: (b.usage.CPU - a.usage.CPU).Seconds() / s, workingSet: b.usage.WorkingSet, private: b.usage.Private,
		decodedFPS: float64(decoded) / s, paintedFPS: float64(painted) / s,
		decodeMs: per(b.decodeNs-a.decodeNs, decoded), scaleMs: per(b.scaleNs-a.scaleNs, painted), paintMs: per(b.paintNs-a.paintNs, painted)}
}

const mb = 1 << 20

// perfLogger writes output/logs/performance.log, one line every 30 s while
// any window is open.
type perfLogger struct {
	mu      sync.Mutex
	running bool
}

// windowOpened counts a window and starts the log if it is the first one; the
// returned function counts it closed.
func (app *Service) windowOpened() func() {
	tuneRuntime()
	perf.windows.Add(1)
	app.perfLog.mu.Lock()
	if !app.perfLog.running && app.Root != "" {
		app.perfLog.running = true
		go app.runPerfLog(30 * time.Second)
	}
	app.perfLog.mu.Unlock()
	var once sync.Once
	return func() { once.Do(func() { perf.windows.Add(-1) }) }
}

func (app *Service) runPerfLog(every time.Duration) {
	last := takePerfSnapshot(procstat.Usage{})
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for range ticker.C {
		now := takePerfSnapshot(procstat.Usage{})
		windows := int(perf.windows.Load())
		android, ios := app.devicesWithWindows()
		app.writePerfLine(perfBetween(last, now), windows, android, ios)
		last = now
		if windows == 0 {
			app.perfLog.mu.Lock()
			// A window opened since the read keeps the log running.
			if perf.windows.Load() == 0 {
				app.perfLog.running = false
				app.perfLog.mu.Unlock()
				return
			}
			app.perfLog.mu.Unlock()
		}
	}
}

// devicesWithWindows counts the phones that have a window open now.
func (app *Service) devicesWithWindows() (android, ios int) {
	app.androidMu.Lock()
	sessions := make([]*androidDeviceSession, 0, len(app.androidSessions))
	for _, s := range app.androidSessions {
		sessions = append(sessions, s)
	}
	app.androidMu.Unlock()
	for _, s := range sessions {
		s.mu.Lock()
		if s.viewRefs > 0 {
			android++
		}
		s.mu.Unlock()
	}
	app.iosMu.Lock()
	phones := make([]*iosDeviceSession, 0, len(app.iosSessions))
	for _, s := range app.iosSessions {
		phones = append(phones, s)
	}
	app.iosMu.Unlock()
	for _, s := range phones {
		if s.views.Load() > 0 {
			ios++
		}
	}
	return android, ios
}

func (app *Service) writePerfLine(p perfInterval, windows, android, ios int) {
	f, e := os.OpenFile(app.LogsPath("performance.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return
	}
	defer f.Close()
	if st, e := f.Stat(); e == nil && st.Size() > 8<<20 {
		f.Truncate(0)
	}
	fmt.Fprintf(f, "%s cpu=%.2f cores (%.1f%%) ram=%d MB private=%d MB windows=%d android=%d iphone=%d decoded=%.1f fps painted=%.1f fps (%.1f per window) ms: decode=%.2f scale=%.2f paint=%.2f\n",
		time.Now().Format("2006-01-02 15:04:05"), p.cores, 100*p.cores/float64(runtime.NumCPU()), p.workingSet/mb, p.private/mb,
		windows, android, ios, p.decodedFPS, p.paintedFPS, p.paintedFPS/float64(max(1, windows)), p.decodeMs, p.scaleMs, p.paintMs)
}

var tuneOnce sync.Once

// tuneRuntime sets the Go collector for a wall of windows, once: the pictures live in
// FFmpeg and GDI memory, so the Go heap stays small and collecting it every time it
// doubles only costs CPU. It may grow four times before a collection, and the soft
// limit collects earlier should it ever reach 2 GiB.
func tuneRuntime() {
	tuneOnce.Do(func() {
		if os.Getenv("GOGC") == "" {
			debug.SetGCPercent(400)
		}
		if os.Getenv("GOMEMLIMIT") == "" {
			debug.SetMemoryLimit(2 << 30)
		}
	})
}
