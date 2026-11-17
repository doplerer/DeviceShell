package core

import (
	"context"
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doplerer/deviceshell/internal/procstat"
)

// frameHub hands the newest decoded picture of one device to every window that shows
// it: each window gets it fitted to its own size, and a slow window skips to the newest
// frame. The iPhone (QuickTime) and Android (scrcpy server) pictures both go through it.

// Why a source has no picture, as the window title says it (noVideoStatus).
const (
	videoNoPicture    = 1
	videoWedged       = 2
	videoQTFailed     = 4
	videoQTMissing    = 8
	videoSourceFailed = 16 // the Android picture stopped; details in android-video-*.log
)

type videoDecoder interface {
	decode(data []byte, emit func(yuvView)) error
	close()
}

// timedDecode decodes one unit and adds the time it took, without the time spent
// publishing its pictures, to the process totals.
func timedDecode(dec videoDecoder, data []byte, emit func(yuvView)) error {
	started := procstat.Now()
	var publishing int64
	e := dec.decode(data, func(p yuvView) {
		at := procstat.Now()
		emit(p)
		publishing += procstat.Now() - at
	})
	perf.decodeNs.Add(procstat.Now() - started - publishing)
	return e
}

// yuvView is a decoded picture in planar YUV, borrowed from the decoder during emit.
type yuvView struct {
	w, h, cw int // luma size and chroma width
	sx, sy   uint
	full     bool // full range (JPEG levels)
	y, u, v  []byte
	ys, cs   int // strides
	// own, when set, takes the decoder's reference to the planes, so they stay valid
	// after emit until the returned function releases them: no copy (FFmpeg frames).
	own func() (release func())
}

// yuvImage is a picture shared by the windows that show it: the decoder's own planes
// (release set) or a copy of them.
type yuvImage struct {
	w, h, cw, ch int
	sx, sy       uint
	full         bool
	y, u, v      []byte
	ys, cs       int    // strides
	refs         int    // guarded by frameHub.mu
	release      func() // gives the planes back to the decoder; nil for copies
}

// frameSource is what the hub asks of the device's picture source.
type frameSource interface {
	live() bool      // pictures can arrive
	flags() uint32   // why there is no picture (videoQTFailed...)
	received() int64 // bytes received from the device so far
}

type frameHub struct {
	watchers atomic.Int32 // windows showing it now (not minimized)
	mu       sync.Mutex
	frame    *yuvImage
	seq      uint64
	changed  chan struct{} // closed and replaced on every new frame
	spare    []*yuvImage
	decoded  atomic.Int64
	opened   time.Time
	source   string // the source's name in the stats lines
	log      func(string, ...any)
}

func (h *frameHub) init(source string, log func(string, ...any)) {
	h.changed, h.opened, h.source, h.log = make(chan struct{}), time.Now(), source, log
}

// watch counts a window that starts (on) or stops showing the pictures.
func (h *frameHub) watch(on bool) {
	if on {
		h.watchers.Add(1)
	} else {
		h.watchers.Add(-1)
	}
}

func (h *frameHub) say(format string, args ...any) {
	if h.log != nil {
		h.log(format, args...)
	}
}

var sharedImages = sync.Pool{New: func() any { return new(yuvImage) }}

// publish makes a decoded picture the newest frame: the decoder's own planes when it
// lets them go (no copy), or else a copy.
func (h *frameHub) publish(p yuvView) {
	ch := (p.h + (1 << p.sy) - 1) >> p.sy
	var img *yuvImage
	if p.own != nil {
		img = sharedImages.Get().(*yuvImage)
		*img = yuvImage{w: p.w, h: p.h, cw: p.cw, ch: ch, sx: p.sx, sy: p.sy, full: p.full, y: p.y, u: p.u, v: p.v, ys: p.ys, cs: p.cs, release: p.own()}
	} else {
		img = h.take(p.w, p.h, p.cw, ch)
		img.sx, img.sy, img.full = p.sx, p.sy, p.full
		for y := range p.h {
			copy(img.y[y*p.w:(y+1)*p.w], p.y[y*p.ys:])
		}
		for y := range ch {
			copy(img.u[y*p.cw:(y+1)*p.cw], p.u[y*p.cs:])
			copy(img.v[y*p.cw:(y+1)*p.cw], p.v[y*p.cs:])
		}
	}
	perf.decoded.Add(1)
	if h.decoded.Add(1) == 1 {
		h.say("first frame after %d ms", time.Since(h.opened).Milliseconds())
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.frame != nil {
		h.drop(h.frame)
	}
	img.refs = 1
	h.frame = img
	h.seq++
	close(h.changed)
	h.changed = make(chan struct{})
}

// close lets go of the newest frame, once the source decodes no more: a decoder's
// planes must go back before it is gone for good. Windows keep theirs until released.
func (h *frameHub) close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.frame != nil {
		h.drop(h.frame)
		h.frame = nil
	}
	h.spare = nil
}

// take reuses a spare buffer of the same size; the copy runs outside the lock.
func (h *frameHub) take(w, ht, cw, ch int) *yuvImage {
	h.mu.Lock()
	for i, img := range h.spare {
		if img.w == w && img.h == ht && img.cw == cw && img.ch == ch {
			h.spare = append(h.spare[:i], h.spare[i+1:]...)
			h.mu.Unlock()
			return img
		}
	}
	h.mu.Unlock()
	return &yuvImage{w: w, h: ht, cw: cw, ch: ch, ys: w, cs: cw, y: make([]byte, w*ht), u: make([]byte, cw*ch), v: make([]byte, cw*ch)}
}

// drop releases one reference; caller holds h.mu.
func (h *frameHub) drop(img *yuvImage) {
	if img.refs--; img.refs != 0 {
		return
	}
	if img.release != nil {
		img.release()
		*img = yuvImage{}
		sharedImages.Put(img)
		return
	}
	if len(h.spare) < 3 {
		h.spare = append(h.spare, img)
	}
}

func (h *frameHub) release(img *yuvImage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.drop(img)
}

// next waits up to timeout for a frame newer than last and takes a reference to it.
func (h *frameHub) next(c context.Context, last uint64, timeout time.Duration) (*yuvImage, uint64) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		h.mu.Lock()
		if h.frame != nil && h.seq != last {
			img, seq := h.frame, h.seq
			img.refs++
			h.mu.Unlock()
			return img, seq
		}
		changed := h.changed
		h.mu.Unlock()
		select {
		case <-changed:
		case <-deadline.C:
			return nil, 0
		case <-c.Done():
			return nil, 0
		}
	}
}

// fitSize fits w x h inside maxW x maxH keeping its aspect.
func fitSize(w, h, maxW, maxH int) (int, int) {
	scale := math.Min(float64(maxW)/float64(w), float64(maxH)/float64(h))
	return max(1, int(math.Round(float64(w)*scale))), max(1, int(math.Round(float64(h)*scale)))
}

// fitScaled is fitSize for a window: when the picture is about twice the fitted size
// (Android encodes twice the window) it takes exactly half, a pixel or two off, so the
// scaler can average plain 2x2 blocks.
func fitScaled(w, h, maxW, maxH int) (int, int) {
	fw, fh := fitSize(w, h, maxW, maxH)
	if hw, hh := w/2, h/2; w%2 == 0 && h%2 == 0 && abs(hw-fw) <= 2 && abs(hh-fh) <= 2 && hw <= maxW && hh <= maxH {
		return hw, hh
	}
	return fw, fh
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// spans maps each of dst cells onto the source range it covers, at least one sample.
func spans(src, dst int) (lo, hi []int) {
	lo, hi = make([]int, dst), make([]int, dst)
	for i := range dst {
		lo[i] = i * src / dst
		hi[i] = min(src, max(lo[i]+1, (i+1)*src/dst))
	}
	return lo, hi
}

// BT.709 in 16.16 fixed point, for video and full range.
var (
	yuvLimited = [5]int{76309, 117489, 13975, 34925, 138438}
	yuvFull    = [5]int{65536, 103206, 12276, 30679, 121609}
)

func clamp8(v int) byte {
	v = (v + 32768) >> 16
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return byte(v)
}

// parallelScalePixels is the output size from which one picture is scaled by several
// goroutines; below it the windows themselves are the parallelism.
const parallelScalePixels = 250_000

// yuvScaler fits pictures into w x h top-down BGRA. It keeps the source ranges of the
// last sizes it scaled, so a window allocates nothing per frame.
type yuvScaler struct {
	srcW, srcH, dstW, dstH int
	sx, sy                 uint
	xlo, xhi, ylo, yhi     []int
	cx                     []int // chroma column of each output column
	half                   bool  // exact 2:1 on both axes from 4:2:0
}

func (s *yuvScaler) prepare(img *yuvImage, w, h int) {
	if s.srcW == img.w && s.srcH == img.h && s.dstW == w && s.dstH == h && s.sx == img.sx && s.sy == img.sy {
		return
	}
	s.srcW, s.srcH, s.dstW, s.dstH, s.sx, s.sy = img.w, img.h, w, h, img.sx, img.sy
	s.xlo, s.xhi = spans(img.w, w)
	s.ylo, s.yhi = spans(img.h, h)
	s.cx = make([]int, w)
	for ox := range w {
		s.cx[ox] = ((s.xlo[ox] + s.xhi[ox] - 1) / 2) >> img.sx
	}
	s.half = img.w == 2*w && img.h == 2*h && img.sx == 1 && img.sy == 1
}

// scale fits img into w x h top-down BGRA in out (grown if needed): luma averaged over
// each output pixel's source area (no aliasing on the usual 3x downscale), chroma from
// its centre. The 2:1 path gives the same bytes as the general one.
func (s *yuvScaler) scale(img *yuvImage, out []byte, w, h int) []byte {
	if need := w * h * 4; cap(out) < need {
		out = make([]byte, need)
	} else {
		out = out[:need]
	}
	s.prepare(img, w, h)
	k, offset := yuvLimited, 16
	if img.full {
		k, offset = yuvFull, 0
	}
	put := func(d []byte, luma, ci int, crow int) {
		u, v := int(img.u[crow+ci])-128, int(img.v[crow+ci])-128
		d[0] = clamp8(luma + k[4]*u)
		d[1] = clamp8(luma - k[2]*u - k[3]*v)
		d[2] = clamp8(luma + k[1]*v)
		d[3] = 255
	}
	rows := func(from, to int) {
		if s.half {
			for oy := from; oy < to; oy++ {
				r0, r1 := img.y[2*oy*img.ys:], img.y[(2*oy+1)*img.ys:]
				crow := oy * img.cs
				dst := out[oy*w*4 : (oy+1)*w*4]
				for ox := range w {
					sum := int(r0[2*ox]) + int(r0[2*ox+1]) + int(r1[2*ox]) + int(r1[2*ox+1])
					put(dst[ox*4:ox*4+4], (sum/4-offset)*k[0], ox, crow)
				}
			}
			return
		}
		for oy := from; oy < to; oy++ {
			y0, y1 := s.ylo[oy], s.yhi[oy]
			crow := (((y0 + y1 - 1) / 2) >> img.sy) * img.cs
			dst := out[oy*w*4 : (oy+1)*w*4]
			for ox := range w {
				x0, x1 := s.xlo[ox], s.xhi[ox]
				sum := 0
				for yy := y0; yy < y1; yy++ {
					for _, p := range img.y[yy*img.ys+x0 : yy*img.ys+x1] {
						sum += int(p)
					}
				}
				put(dst[ox*4:ox*4+4], (sum/((y1-y0)*(x1-x0))-offset)*k[0], s.cx[ox], crow)
			}
		}
	}
	if w*h < parallelScalePixels {
		rows(0, h)
		return out
	}
	workers := min(runtime.NumCPU(), 8, h)
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rows(i*h/workers, (i+1)*h/workers)
		}()
	}
	wg.Wait()
	return out
}

// scaleYUV fits img into w x h top-down BGRA with a scaler of its own.
func scaleYUV(img *yuvImage, out []byte, w, h int) []byte {
	var s yuvScaler
	return s.scale(img, out, w, h)
}
