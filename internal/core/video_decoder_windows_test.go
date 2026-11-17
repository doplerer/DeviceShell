package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// bitWriter writes the H.264 bit syntax: fixed fields and Exp-Golomb codes.
type bitWriter struct {
	out  []byte
	cur  byte
	bits int
}

func (b *bitWriter) bit(v int) {
	b.cur = b.cur<<1 | byte(v&1)
	if b.bits++; b.bits == 8 {
		b.out, b.cur, b.bits = append(b.out, b.cur), 0, 0
	}
}
func (b *bitWriter) u(n int, v int) {
	for i := n - 1; i >= 0; i-- {
		b.bit(v >> i)
	}
}
func (b *bitWriter) ue(v int) {
	v++
	n := 0
	for x := v; x > 1; x >>= 1 {
		n++
	}
	b.u(n, 0)
	b.u(n+1, v)
}
func (b *bitWriter) align() {
	for b.bits != 0 {
		b.bit(0)
	}
}
func (b *bitWriter) trailing() []byte {
	b.bit(1)
	b.align()
	return b.out
}

// nal adds the start code and header and the emulation prevention bytes.
func nal(header byte, rbsp []byte) []byte {
	out := []byte{0, 0, 0, 1, header}
	zeros := 0
	for _, c := range rbsp {
		if zeros >= 2 && c <= 3 {
			out, zeros = append(out, 3), 0
		}
		out = append(out, c)
		if c == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

// pcmPicture is one lossless H.264 IDR picture of w x h (multiples of 16) made of I_PCM
// macroblocks, so a real decoder's output is known to the byte: luma(x, y) and 128 chroma.
func pcmPicture(w, h, idr int, luma func(x, y int) byte) []byte {
	var sps bitWriter
	sps.u(8, 66) // Baseline
	sps.u(8, 0)
	sps.u(8, 30)
	sps.ue(0)        // seq_parameter_set_id
	sps.ue(0)        // log2_max_frame_num_minus4
	sps.ue(2)        // pic_order_cnt_type
	sps.ue(1)        // max_num_ref_frames
	sps.u(1, 0)      // gaps_in_frame_num_value_allowed_flag
	sps.ue(w/16 - 1) // pic_width_in_mbs_minus1
	sps.ue(h/16 - 1) // pic_height_in_map_units_minus1
	sps.u(1, 1)      // frame_mbs_only_flag
	sps.u(1, 1)      // direct_8x8_inference_flag
	sps.u(1, 0)      // frame_cropping_flag
	sps.u(1, 0)      // vui_parameters_present_flag
	var pps bitWriter
	pps.ue(0)   // pic_parameter_set_id
	pps.ue(0)   // seq_parameter_set_id
	pps.u(1, 0) // CAVLC
	pps.u(1, 0)
	pps.ue(0) // num_slice_groups_minus1
	pps.ue(0)
	pps.ue(0)
	pps.u(1, 0)
	pps.u(2, 0)
	pps.ue(0)   // pic_init_qp_minus26 (se 0 = ue 0)
	pps.ue(0)   // pic_init_qs_minus26
	pps.ue(0)   // chroma_qp_index_offset
	pps.u(1, 1) // deblocking_filter_control_present_flag
	pps.u(1, 0)
	pps.u(1, 0)
	var slice bitWriter
	slice.ue(0)   // first_mb_in_slice
	slice.ue(7)   // I slice
	slice.ue(0)   // pic_parameter_set_id
	slice.u(4, 0) // frame_num
	slice.ue(idr) // idr_pic_id
	slice.u(1, 0) // no_output_of_prior_pics_flag
	slice.u(1, 0) // long_term_reference_flag
	slice.ue(0)   // slice_qp_delta
	slice.ue(1)   // disable_deblocking_filter_idc
	for my := 0; my < h/16; my++ {
		for mx := 0; mx < w/16; mx++ {
			slice.ue(25) // I_PCM
			slice.align()
			for y := range 16 {
				for x := range 16 {
					slice.u(8, int(luma(mx*16+x, my*16+y)))
				}
			}
			for range 2 * 64 {
				slice.u(8, 128)
			}
		}
	}
	out := nal(0x67, sps.trailing())
	out = append(out, nal(0x68, pps.trailing())...)
	return append(out, nal(0x65, slice.trailing())...)
}

func scrcpyToolDir(t *testing.T) string {
	dir, _ := filepath.Abs(filepath.Join("..", "..", "dist", "tools", "scrcpy-win64-v5.0.1"))
	if _, e := os.Stat(filepath.Join(dir, "scrcpy.exe")); e != nil {
		t.Skip("no FFmpeg next to the sources:", dir)
	}
	return dir
}

// Decoded pictures reach the windows by reference, with no copy, and every AVFrame
// lent goes back once the last window and the hub let go of it.
func TestDecoderLendsFramesWithoutLeaks(t *testing.T) {
	dec, e := newVideoDecoder(scrcpyToolDir(t), false)
	if e != nil {
		t.Fatal(e)
	}
	pool := dec.(*ffmpegDecoder).pool
	var hub frameHub
	hub.init("test", nil)
	const w, h = 48, 32
	luma := func(i int) func(x, y int) byte {
		return func(x, y int) byte { return byte(16 + (x*3+y*5+i*7)%200) }
	}
	c := context.Background()
	var held *yuvImage
	var seq uint64
	for i := range 12 {
		if e := dec.decode(pcmPicture(w, h, i%2, luma(i)), hub.publish); e != nil {
			t.Fatal(e)
		}
		img, s := hub.next(c, seq, time.Second)
		if img == nil {
			t.Fatalf("picture %d not decoded", i)
		}
		seq = s
		if img.release == nil {
			t.Fatal("FFmpeg pictures must be shared, not copied")
		}
		if img.w != w || img.h != h || img.ys < w {
			t.Fatal("size", img.w, img.h, img.ys)
		}
		for y := range h {
			for x := range w {
				if got, want := img.y[y*img.ys+x], luma(i)(x, y); got != want {
					t.Fatalf("picture %d (%d,%d) = %d, want %d", i, x, y, got, want)
				}
			}
		}
		if out := scaleYUV(img, nil, w/2, h/2); len(out) != w*h {
			t.Fatal("scaled size", len(out))
		}
		// A window keeps picture 3 while newer ones arrive.
		if i == 3 {
			held = img
		} else {
			hub.release(img)
		}
	}
	if n := pool.lent(); n != 2 {
		t.Fatalf("the hub's newest picture and the held one are lent, have %d", n)
	}
	hub.release(held)
	hub.close()
	if n := pool.lent(); n != 0 {
		t.Fatalf("frames still lent after release: %d", n)
	}
	dec.close()
}
