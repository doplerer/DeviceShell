package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"syscall"
	"unsafe"
)

// The iPhone stream is decoded with the FFmpeg that scrcpy ships (avcodec/avutil DLLs in
// its folder), called through syscall so the build stays without cgo. It is the same
// decoder PyAV used. Only stable parts of the ABI are touched: the head of AVFrame and
// AVPacket.data/size, which have not moved since FFmpeg 5; everything else goes through
// functions and AVOptions.

type ffmpegAPI struct {
	findDecoder, allocContext, open, freeContext, sendPacket, receiveFrame *syscall.Proc
	packetAlloc, packetFree, newPacket, packetUnref                        *syscall.Proc
	frameAlloc, frameFree, frameUnref, frameMoveRef, dictSet, dictFree     *syscall.Proc
}

var (
	loadLibraryEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LoadLibraryExW")
	ffmpegOnce    sync.Once
	ffmpegLib     *ffmpegAPI
	ffmpegErr     error
)

// Oldest majors with the AVFrame/AVPacket layout read here (FFmpeg 5).
const (
	minAvcodecMajor = 59
	minAvutilMajor  = 57
)

func loadFFmpeg(dir string) (*ffmpegAPI, error) {
	ffmpegOnce.Do(func() { ffmpegLib, ffmpegErr = loadFFmpegDLLs(dir) })
	return ffmpegLib, ffmpegErr
}

func loadFFmpegDLLs(dir string) (*ffmpegAPI, error) {
	load := func(pattern string) (*syscall.DLL, error) {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		if len(matches) == 0 {
			return nil, fmt.Errorf("falta %s en %s", pattern, dir)
		}
		// avcodec finds avutil and its other dependencies next to itself.
		const searchDLLDir = 0x00000100 | 0x00001000 // LOAD_LIBRARY_SEARCH_DLL_LOAD_DIR | LOAD_LIBRARY_SEARCH_DEFAULT_DIRS
		name, e := syscall.UTF16PtrFromString(matches[0])
		if e != nil {
			return nil, e
		}
		h, _, e := loadLibraryEx.Call(uintptr(unsafe.Pointer(name)), 0, searchDLLDir)
		if h == 0 {
			return nil, fmt.Errorf("%s: %w", filepath.Base(matches[0]), e)
		}
		return &syscall.DLL{Name: matches[0], Handle: syscall.Handle(h)}, nil
	}
	avutil, e := load("avutil-*.dll")
	if e != nil {
		return nil, e
	}
	avcodec, e := load("avcodec-*.dll")
	if e != nil {
		return nil, e
	}
	var missing error
	proc := func(d *syscall.DLL, name string) *syscall.Proc {
		p, e := d.FindProc(name)
		if e != nil {
			missing = e
		}
		return p
	}
	api := &ffmpegAPI{
		findDecoder: proc(avcodec, "avcodec_find_decoder_by_name"), allocContext: proc(avcodec, "avcodec_alloc_context3"),
		open: proc(avcodec, "avcodec_open2"), freeContext: proc(avcodec, "avcodec_free_context"),
		sendPacket: proc(avcodec, "avcodec_send_packet"), receiveFrame: proc(avcodec, "avcodec_receive_frame"),
		packetAlloc: proc(avcodec, "av_packet_alloc"), packetFree: proc(avcodec, "av_packet_free"),
		newPacket: proc(avcodec, "av_new_packet"), packetUnref: proc(avcodec, "av_packet_unref"),
		frameAlloc: proc(avutil, "av_frame_alloc"), frameFree: proc(avutil, "av_frame_free"), frameUnref: proc(avutil, "av_frame_unref"),
		frameMoveRef: proc(avutil, "av_frame_move_ref"),
		dictSet:      proc(avutil, "av_dict_set"), dictFree: proc(avutil, "av_dict_free"),
	}
	codecVersion, utilVersion := proc(avcodec, "avcodec_version"), proc(avutil, "avutil_version")
	if missing != nil {
		return nil, missing
	}
	// FFmpeg writes its own messages ("[h264 @ …] …") to the process's error output,
	// which is the terminal the interface draws on: silence them (AV_LOG_QUIET = -8).
	// Decoding errors still reach DeviceShell's logs through the return codes.
	if quiet, e := avutil.FindProc("av_log_set_level"); e == nil {
		quiet.Call(^uintptr(7))
	}
	cv, _, _ := codecVersion.Call()
	uv, _, _ := utilVersion.Call()
	if cv>>16 < minAvcodecMajor || uv>>16 < minAvutilMajor {
		return nil, fmt.Errorf("FFmpeg demasiado antiguo: avcodec %d, avutil %d", cv>>16, uv>>16)
	}
	return api, nil
}

const (
	averrorEAGAIN = -11
	averrorEOF    = -0x20464F45 // -MKTAG('E','O','F',' ')
	// AVPacket: data at 24, size at 32. AVFrame: data[8] at 0, linesize[8] at 64,
	// width at 104, height at 108, format at 116.
	packetDataOffset = 24
	frameLinesize    = 64
	frameWidth       = 104
	frameHeight      = 108
	frameFormat      = 116
)

type ffmpegDecoder struct {
	api             *ffmpegAPI
	ctx, pkt, frame uintptr
	pool            *framePool
}

// framePool keeps spare AVFrames to hand decoded pictures to the windows by reference:
// av_frame_move_ref takes the decoder's planes without copying them and av_frame_unref,
// when the last window lets go, gives them back to the decoder's own buffer pool. Only
// exported avutil functions touch the AVFrames held here.
type framePool struct {
	api    *ffmpegAPI
	mu     sync.Mutex
	free   []uintptr
	out    int // frames lent to the hub and its windows
	closed bool
}

// get returns an empty AVFrame, 0 if FFmpeg has no memory for one.
func (p *framePool) get() uintptr {
	p.mu.Lock()
	if n := len(p.free); n > 0 {
		f := p.free[n-1]
		p.free = p.free[:n-1]
		p.mu.Unlock()
		return f
	}
	p.mu.Unlock()
	f, _, _ := p.api.frameAlloc.Call()
	return f
}

// put takes back an empty AVFrame; once the decoder closed it is freed instead.
func (p *framePool) put(f uintptr) {
	p.mu.Lock()
	if !p.closed && len(p.free) < 8 {
		p.free = append(p.free, f)
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	p.api.frameFree.Call(uintptr(unsafe.Pointer(&f)))
}

// lend moves the picture of src into f and returns what gives it back.
func (p *framePool) lend(f, src uintptr) func() {
	p.api.frameMoveRef.Call(f, src)
	p.mu.Lock()
	p.out++
	p.mu.Unlock()
	return func() {
		p.api.frameUnref.Call(f)
		p.mu.Lock()
		p.out--
		p.mu.Unlock()
		p.put(f)
	}
}

func (p *framePool) lent() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out
}

// close frees the spare frames; frames still lent are freed when given back.
func (p *framePool) close() {
	p.mu.Lock()
	p.closed = true
	free := p.free
	p.free = nil
	p.mu.Unlock()
	for _, f := range free {
		p.api.frameFree.Call(uintptr(unsafe.Pointer(&f)))
	}
}

// cPtr turns an address returned by FFmpeg into a pointer; FFmpeg memory is not
// managed by the Go collector.
func cPtr(r uintptr) unsafe.Pointer { return *(*unsafe.Pointer)(unsafe.Pointer(&r)) }

func cString(s string) uintptr {
	b, _ := syscall.BytePtrFromString(s)
	return uintptr(unsafe.Pointer(b))
}

// decoderThreads is FFmpeg's "threads" option (tests compare).
var decoderThreads = "auto"

// newVideoDecoder opens an H.264 or HEVC decoder with the FFmpeg in dir. Slice threads
// keep the latency of a single frame, as PyAV did; frame threads would add one per thread.
func newVideoDecoder(dir string, hevc bool) (videoDecoder, error) {
	api, e := loadFFmpeg(dir)
	if e != nil {
		return nil, e
	}
	name := "h264"
	if hevc {
		name = "hevc"
	}
	codec, _, _ := api.findDecoder.Call(cString(name))
	if codec == 0 {
		return nil, fmt.Errorf("FFmpeg has no %s decoder", name)
	}
	d := &ffmpegDecoder{api: api, pool: &framePool{api: api}}
	d.ctx, _, _ = api.allocContext.Call(codec)
	d.pkt, _, _ = api.packetAlloc.Call()
	d.frame, _, _ = api.frameAlloc.Call()
	if d.ctx == 0 || d.pkt == 0 || d.frame == 0 {
		d.close()
		return nil, errors.New("FFmpeg: out of memory")
	}
	var opts uintptr
	for _, kv := range [][2]string{{"threads", decoderThreads}, {"thread_type", "slice"}, {"flags", "+low_delay"}} {
		api.dictSet.Call(uintptr(unsafe.Pointer(&opts)), cString(kv[0]), cString(kv[1]), 0)
	}
	r, _, _ := api.open.Call(d.ctx, codec, uintptr(unsafe.Pointer(&opts)))
	api.dictFree.Call(uintptr(unsafe.Pointer(&opts)))
	if int32(r) < 0 {
		d.close()
		return nil, fmt.Errorf("FFmpeg: cannot open %s (%d)", name, int32(r))
	}
	return d, nil
}

// decode sends one Annex B access unit and passes every picture it completes to emit.
// The planes belong to FFmpeg and are only valid during emit.
func (d *ffmpegDecoder) decode(data []byte, emit func(yuvView)) error {
	if len(data) == 0 {
		return nil
	}
	if r, _, _ := d.api.newPacket.Call(d.pkt, uintptr(len(data))); int32(r) < 0 {
		return fmt.Errorf("av_new_packet: %d", int32(r))
	}
	copy(unsafe.Slice(*(**byte)(unsafe.Add(cPtr(d.pkt), packetDataOffset)), len(data)), data)
	r, _, _ := d.api.sendPacket.Call(d.ctx, d.pkt)
	d.api.packetUnref.Call(d.pkt)
	if sent := int32(r); sent < 0 && sent != averrorEAGAIN {
		return fmt.Errorf("avcodec_send_packet: %d", sent)
	}
	for {
		r, _, _ := d.api.receiveFrame.Call(d.ctx, d.frame)
		switch got := int32(r); {
		case got == averrorEAGAIN || got == averrorEOF:
			return nil
		case got < 0:
			return fmt.Errorf("avcodec_receive_frame: %d", got)
		}
		v, e := d.view()
		if e == nil {
			// The hub may keep the planes without copying them; otherwise they are
			// only valid during emit.
			spare := d.pool.get()
			taken := false
			if spare != 0 {
				v.own = func() func() {
					taken = true
					return d.pool.lend(spare, d.frame)
				}
			}
			emit(v)
			if spare != 0 && !taken {
				d.pool.put(spare)
			}
		}
		d.api.frameUnref.Call(d.frame)
		if e != nil {
			return e
		}
	}
}

func (d *ffmpegDecoder) view() (yuvView, error) {
	at := func(off int) int32 { return *(*int32)(unsafe.Add(cPtr(d.frame), off)) }
	v := yuvView{w: int(at(frameWidth)), h: int(at(frameHeight))}
	switch at(frameFormat) {
	case 0, 12: // YUV420P, YUVJ420P
		v.sx, v.sy = 1, 1
	case 4, 13: // YUV422P, YUVJ422P
		v.sx = 1
	case 5, 14: // YUV444P, YUVJ444P
	default:
		return v, fmt.Errorf("unsupported pixel format: %d", at(frameFormat))
	}
	v.full = at(frameFormat) >= 12
	if v.w < 1 || v.h < 1 || v.w > 8192 || v.h > 8192 {
		return v, fmt.Errorf("invalid frame size: %dx%d", v.w, v.h)
	}
	cw, ch := (v.w+(1<<v.sx)-1)>>v.sx, (v.h+(1<<v.sy)-1)>>v.sy
	plane := func(i int, rows int) ([]byte, int) {
		data := *(**byte)(unsafe.Add(cPtr(d.frame), i*8))
		stride := int(at(frameLinesize + i*4))
		return unsafe.Slice(data, stride*rows), stride
	}
	v.y, v.ys = plane(0, v.h)
	v.u, v.cs = plane(1, ch)
	v.v, _ = plane(2, ch)
	v.cw = cw
	return v, nil
}

func (d *ffmpegDecoder) close() {
	if d.ctx != 0 {
		d.api.freeContext.Call(uintptr(unsafe.Pointer(&d.ctx)))
	}
	if d.pkt != 0 {
		d.api.packetFree.Call(uintptr(unsafe.Pointer(&d.pkt)))
	}
	if d.frame != 0 {
		d.api.frameFree.Call(uintptr(unsafe.Pointer(&d.frame)))
	}
	d.pool.close()
}
