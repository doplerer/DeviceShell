package core

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/doplerer/deviceshell/internal/ios/coredevice"
)

// Decodes motion.rtp then still.rtp (one stream) and saves pictures to look at:
// the last of the motion and 1, 3 and 5 s into the still part. Opt-in, it writes big
// PNGs: DEVICESHELL_FRAMES=1 [DEVICESHELL_CODEC=h264-] [DEVICESHELL_THREADS=1].
func TestDisplayServiceFramesToPNG(t *testing.T) {
	if os.Getenv("DEVICESHELL_FRAMES") == "" {
		t.Skip("set DEVICESHELL_FRAMES to save decoded pictures")
	}
	dir, _ := filepath.Abs(filepath.Join("..", "..", ".cache", "wifi-probe"))
	codec := os.Getenv("DEVICESHELL_CODEC") // "h264-" for the H.264 recording
	motion, e1 := os.ReadFile(filepath.Join(dir, codec+"motion.rtp"))
	still, e2 := os.ReadFile(filepath.Join(dir, codec+"still.rtp"))
	if e1 != nil || e2 != nil {
		t.Skip("no motion.rtp/still.rtp")
	}
	prefix := ""
	if th := os.Getenv("DEVICESHELL_THREADS"); th != "" {
		decoderThreads, prefix = th, "t"+th+"-"
		defer func() { decoderThreads = "auto" }()
	}
	prefix += codec
	dec, e := newVideoDecoder(scrcpyToolDir(t), codec == "")
	if e != nil {
		t.Fatal(e)
	}
	defer dec.close()
	var hevc coredevice.HEVCAssembler
	var h264 coredevice.H264Assembler
	push := hevc.Push
	if codec != "" {
		push = h264.Push
	}
	save := func(name string, v yuvView) {
		img := image.NewYCbCr(image.Rect(0, 0, v.w, v.h), image.YCbCrSubsampleRatio420)
		for y := range v.h {
			copy(img.Y[y*img.YStride:y*img.YStride+v.w], v.y[y*v.ys:])
		}
		for y := range (v.h + 1) / 2 {
			copy(img.Cb[y*img.CStride:y*img.CStride+v.cw], v.u[y*v.cs:])
			copy(img.Cr[y*img.CStride:y*img.CStride+v.cw], v.v[y*v.cs:])
		}
		f, _ := os.Create(filepath.Join(dir, prefix+name))
		png.Encode(f, img)
		f.Close()
	}
	run := func(data []byte, at map[int]string, last string) int {
		pictures, sizes, errs := 0, 0, 0
		var lastView func()
		for len(data) >= 4 {
			n := int(binary.BigEndian.Uint32(data))
			p := data[4 : 4+n]
			data = data[4+n:]
			au, _ := push(p)
			if au == nil {
				continue
			}
			sizes += len(au)
			if e := dec.decode(au, func(v yuvView) {
				pictures++
				if name, ok := at[pictures]; ok {
					save(name, v)
				}
				if len(data) < 4 && last != "" {
					save(last, v)
					lastView = func() {}
				}
			}); e != nil {
				errs++
			}
		}
		_ = lastView
		t.Logf("%d pictures, %d KB, %d decode errors, %d lost", pictures, sizes/1024, errs, hevc.Lost+h264.Lost)
		return pictures
	}
	run(motion, map[int]string{42: "m042.png", 60: "m060.png", 100: "m100.png", 200: "m200.png"}, "motion-last.png")
	// 30-40 fps while still: roughly 1, 3 and 5 s.
	run(still, map[int]string{30: "still-1s.png", 100: "still-3s.png", 170: "still-5s.png"}, "still-last.png")
	fmt.Println("saved in", dir)
}
