package core

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"github.com/doplerer/deviceshell/internal/ios/coredevice"
	"github.com/doplerer/deviceshell/internal/ios/quicktime"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// iosVideo is the iPhone picture handled in Go: the QuickTime USB capture, FFmpeg
// decoding and, for each window, the newest frame fitted to its size, at the up to
// 60 fps the phone sends. Touch goes through the DisplayService session instead.
type iosVideo struct {
	frameHub
	udid, toolDir string
	capture       *quicktime.Capture
	missing       bool
	failure       atomic.Value // string: why there is no picture
	logMu         sync.Mutex
	logFile       *os.File
	closed        chan struct{} // closed by close or a stall: windows then ask for the next connector
	closeOnce     sync.Once
	stalled       atomic.Bool    // the watchdog saw no data: the session starts a new capture
	display       *displaySource // over Wi-Fi: the DisplayService stream instead of QuickTime
}

// openIOSVideo starts the capture before the connector opens its tunnel: enabling
// QuickTime re-enumerates the device. It never fails; without a picture the windows
// show why. A capture that sends nothing for stallAfter while running is restarted.
func (app *Service) openIOSVideo(udid string, stallAfter time.Duration) *iosVideo {
	v := &iosVideo{udid: udid, toolDir: app.ToolDir, closed: make(chan struct{})}
	v.init("quicktime", v.logf)
	if f, e := os.OpenFile(app.LogsPath("ios-video-"+udid+".log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); e == nil {
		v.logFile = f
	}
	// Only a few iPhones switch USB mode at once.
	qtSwitches.acquire(context.Background())
	defer qtSwitches.release()
	old := usbmuxID(udid)
	capture, e := quicktime.Open(udid, v.logf)
	switch {
	case errors.Is(e, quicktime.ErrUnavailable):
		v.missing = true
		v.failure.Store(app.L("falta el controlador de vídeo USB; reconecta el iPhone para instalarlo", "the USB video driver is missing; reconnect the iPhone to install it"))
		v.logf("no video: QuickTime %v", e)
	case e != nil:
		v.failure.Store(e.Error())
		v.logf("no video: QuickTime failed: %v", e)
	default:
		// Enabling re-enumerated the device; usbmux lists it again, under a new id,
		// about a second later.
		took, id := waitUsbmux(udid, old, 5*time.Second)
		v.logf("usbmux back in %d ms (found=%v)", took.Milliseconds(), id != 0)
		// The phone is the same one: the inventory keeps what it knows under the new id.
		app.iosInfo.rebind(udid, old, id)
		capture.Start()
		v.capture = capture
		go v.decodeLoop(capture.Session)
		go watchStallEvery(v.closed, capture.Session.Received.Load, capture.Session.Running, stallAfter, func() {
			v.logf("watchdog: no QuickTime data for %v; restarting the capture", stallAfter)
			v.stalled.Store(true)
			v.closeOnce.Do(func() { close(v.closed) })
		})
		v.logf("video source: quicktime")
	}
	return v
}

func (v *iosVideo) logf(format string, args ...any) {
	v.logMu.Lock()
	defer v.logMu.Unlock()
	if v.logFile != nil {
		fmt.Fprintf(v.logFile, "%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), fmt.Sprintf(format, args...))
	}
}

func (v *iosVideo) failed() string {
	s, _ := v.failure.Load().(string)
	return s
}

// live reports whether pictures can arrive: the capture runs and decoding works.
func (v *iosVideo) live() bool {
	if v.display != nil {
		return v.failed() == ""
	}
	return v.capture != nil && v.capture.Session.Running() && v.failed() == ""
}

// errorText is why there is no picture, for the connector status.
func (v *iosVideo) errorText() string {
	if s := v.failed(); s != "" {
		return s
	}
	if v.capture != nil && !v.capture.Session.Running() {
		return "capture interrupted: " + cmp.Or(v.capture.Session.Failure(), "no data")
	}
	return ""
}

func (v *iosVideo) flags() uint32 {
	switch {
	case v.missing:
		return videoQTMissing
	case v.errorText() != "":
		return videoQTFailed
	}
	return 0
}

// close stops the capture; disable turns QuickTime off, which re-enumerates the
// device, so it runs after the connector closed its tunnel.
func (v *iosVideo) close(disable bool) {
	v.closeOnce.Do(func() {
		if v.closed != nil {
			close(v.closed)
		}
	})
	if v.display != nil {
		v.display.stopOnce.Do(func() { close(v.display.stop) })
		<-v.display.done
		v.logf("DisplayService video stopped")
	}
	if v.capture != nil {
		if disable {
			qtSwitches.acquire(context.Background())
			defer qtSwitches.release()
		}
		v.capture.Stop(disable, v.logf)
		v.logf("QuickTime capture stopped")
	}
	v.logMu.Lock()
	defer v.logMu.Unlock()
	if v.logFile != nil {
		v.logFile.Close()
		v.logFile = nil
	}
}

func (v *iosVideo) decodeLoop(q *quicktime.Session) {
	var (
		dec        videoDecoder
		format     quicktime.Format
		record     []byte
		params     []byte
		lengthSize int
		unit       []byte
		failures   int
		keep       func([]byte) // the opt-in recording tests keep the stream
	)
	defer func() {
		v.frameHub.close()
		if dec != nil {
			dec.close()
		}
	}()
	for sbuf := range q.Samples {
		data, f, e := quicktime.Sample(sbuf)
		if e != nil {
			continue
		}
		if f != nil {
			format = *f
		}
		if dec == nil && v.failed() != "" {
			continue // no decoder: keep the capture answering until it stops
		}
		if !bytes.Equal(format.Record, record) {
			record = format.Record
			if dec != nil {
				dec.close()
				dec = nil
			}
			if params, lengthSize, e = quicktime.AnnexBParameters(record, format.HEVC()); e == nil {
				dec, e = newVideoDecoder(v.toolDir, format.HEVC())
			}
			if e != nil {
				v.failure.Store("video decoder: " + e.Error())
				v.logf("QuickTime decoder failed: %v", e)
				continue
			}
			v.logf("QuickTime decoder: %s %dx%d", format.Codec, format.Width, format.Height)
			if videoRecorder != nil {
				keep = videoRecorder("ios-"+v.udid, format.HEVC())
			}
			// The parameter sets travel out of band: they go before the next picture.
			unit = append(unit[:0], params...)
		} else {
			unit = unit[:0]
		}
		if dec == nil || len(data) == 0 {
			continue
		}
		if unit, e = quicktime.AnnexB(unit, data, lengthSize); e == nil {
			if keep != nil {
				keep(unit)
			}
			e = timedDecode(dec, unit, v.publish)
		}
		if e != nil {
			if failures++; failures <= 10 || failures%100 == 0 {
				v.logf("QuickTime decode error (%d): %v", failures, e)
			}
		}
	}
}

func (v *iosVideo) received() int64 {
	if v.display != nil {
		return v.display.bytes.Load()
	}
	if v.capture != nil {
		return v.capture.Session.Received.Load()
	}
	return 0
}

// displaySource is the picture over Wi-Fi: the RTP of the engine's DisplayService
// stream (H.264, asked for by iosNative.showVideo; HEVC decodes too), decoded here.
// Each new stream (the engine restarts it when it stops) starts with its parameter
// sets and gets a new decoder.
type displaySource struct {
	packets  chan displayPacket
	gen      atomic.Int64
	current  atomic.Pointer[coredevice.Display]
	bytes    atomic.Int64
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

type displayPacket struct {
	gen  int64
	h264 bool
	data []byte
}

var rtpBuffers = sync.Pool{New: func() any { return make([]byte, 0, 1500) }}

// openDisplayVideo is the DisplayService picture of an iPhone (Wi-Fi, or a cable
// unless QuickTime is chosen); the engine attaches its
// DisplayService streams to it (attach).
func (app *Service) openDisplayVideo(udid string) *iosVideo {
	v := &iosVideo{udid: udid, toolDir: app.ToolDir, closed: make(chan struct{}),
		display: &displaySource{packets: make(chan displayPacket, 4096), stop: make(chan struct{}), done: make(chan struct{})}}
	v.init("displayservice", v.logf)
	if f, e := os.OpenFile(app.LogsPath("ios-video-"+udid+".log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600); e == nil {
		v.logFile = f
	}
	v.logf("video source: displayservice")
	go v.displayLoop()
	return v
}

// attach takes the picture of a new DisplayService stream.
func (v *iosVideo) attach(d *coredevice.Display) {
	src := v.display
	if src == nil {
		return
	}
	gen := src.gen.Add(1)
	src.current.Store(d)
	h264 := d.PayloadType != 100
	v.logf("DisplayService stream %d: payload type %d", gen, d.PayloadType)
	d.SetTap(func(p []byte) {
		src.bytes.Add(int64(len(p)))
		b := append(rtpBuffers.Get().([]byte)[:0], p...)
		select {
		case src.packets <- displayPacket{gen, h264, b}:
		default:
			rtpBuffers.Put(b[:0]) // the decoder fell behind: the assembler sees the gap
		}
	})
}

func (v *iosVideo) displayLoop() {
	src := v.display
	var (
		dec      videoDecoder
		h264     bool
		hevcA    coredevice.HEVCAssembler
		h264A    coredevice.H264Assembler
		gen      int64
		lastPLI  time.Time
		failures int
	)
	defer func() {
		v.frameHub.close()
		if dec != nil {
			dec.close()
		}
		close(src.done)
	}()
	for {
		var p displayPacket
		select {
		case <-src.stop:
			return
		case p = <-src.packets:
		}
		if p.gen != gen || dec == nil {
			hevcA, h264A, gen, h264 = coredevice.HEVCAssembler{}, coredevice.H264Assembler{}, p.gen, p.h264
			if dec != nil {
				dec.close()
			}
			var e error
			if dec, e = newVideoDecoder(v.toolDir, !h264); e != nil {
				v.failure.Store("video decoder: " + e.Error())
				v.logf("decoder failed: %v", e)
				dec = nil
				rtpBuffers.Put(p.data[:0])
				continue
			}
		}
		var au []byte
		var broken bool
		if h264 {
			au, broken = h264A.Push(p.data)
		} else {
			au, broken = hevcA.Push(p.data)
		}
		if au != nil {
			if broken && time.Since(lastPLI) > time.Second {
				// Ask for a keyframe instead of showing artifacts until the next one.
				if d := src.current.Load(); d != nil {
					d.PictureLossIndication()
				}
				lastPLI = time.Now()
				v.logf("packets lost (%d so far): asked for a keyframe", h264A.Lost+hevcA.Lost)
			}
			if e := timedDecode(dec, au, v.publish); e != nil {
				if failures++; failures <= 10 || failures%100 == 0 {
					v.logf("decode error (%d): %v", failures, e)
				}
			}
		}
		rtpBuffers.Put(p.data[:0])
	}
}
