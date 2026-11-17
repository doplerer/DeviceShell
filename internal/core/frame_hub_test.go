package core

import (
	"bytes"
	"math/rand"
	"testing"
)

// randomImage is a 4:2:0 picture with noise, and rows wider than the picture as
// FFmpeg pads them.
func randomImage(w, h, pad int, full bool) *yuvImage {
	r := rand.New(rand.NewSource(int64(w*h + pad)))
	cw, ch := (w+1)/2, (h+1)/2
	img := &yuvImage{w: w, h: h, cw: cw, ch: ch, sx: 1, sy: 1, full: full, ys: w + pad, cs: cw + pad/2}
	img.y, img.u, img.v = make([]byte, img.ys*h), make([]byte, img.cs*ch), make([]byte, img.cs*ch)
	for _, plane := range [][]byte{img.y, img.u, img.v} {
		r.Read(plane)
	}
	return img
}

// The 2:1 path averages plain 2x2 blocks and must give exactly the general path's bytes.
func TestScalerHalfMatchesGeneral(t *testing.T) {
	for _, c := range []struct{ w, h, pad int }{{392, 846, 0}, {392, 846, 40}, {720, 1600, 64}, {64, 34, 2}} {
		for _, full := range []bool{false, true} {
			img := randomImage(c.w, c.h, c.pad, full)
			var fast, general yuvScaler
			a := fast.scale(img, nil, c.w/2, c.h/2)
			if !fast.half {
				t.Fatalf("%dx%d: the 2:1 path was not taken", c.w, c.h)
			}
			general.prepare(img, c.w/2, c.h/2)
			general.half = false
			b := general.scale(img, nil, c.w/2, c.h/2)
			if !bytes.Equal(a, b) {
				t.Fatalf("%dx%d pad %d full %v: 2:1 path differs from the general one", c.w, c.h, c.pad, full)
			}
		}
	}
}

// A window's scaler keeps its tables between frames and rebuilds them on a new size.
func TestScalerReusesTables(t *testing.T) {
	img := randomImage(100, 200, 0, false)
	var s yuvScaler
	out := s.scale(img, nil, 30, 60)
	xlo := &s.xlo[0]
	out2 := s.scale(img, out, 30, 60)
	if &s.xlo[0] != xlo || &out2[0] != &out[0] {
		t.Fatal("same size: tables and output must be reused")
	}
	if s.scale(img, nil, 40, 80); len(s.xlo) != 40 {
		t.Fatal("a new size must rebuild the tables")
	}
	// The scaler's result does not depend on what it scaled before.
	if a, b := s.scale(img, nil, 30, 60), scaleYUV(img, nil, 30, 60); !bytes.Equal(a, b) {
		t.Fatal("a reused scaler differs from a new one")
	}
}

func TestFitScaledPrefersHalf(t *testing.T) {
	// Android encodes about 2x the window: 392x846 in 200x425 (197x425 by aspect) takes
	// exactly half.
	if w, h := fitScaled(392, 846, 200, 425); w != 196 || h != 423 {
		t.Fatal(w, h)
	}
	if w, h := fitScaled(392, 846, 194, 423); w != 194 || h != 419 {
		t.Fatal("half must fit the window", w, h)
	}
	if w, h := fitScaled(1320, 2868, 300, 652); w != 300 || h != 652 {
		t.Fatal(w, h)
	}
}
func solidImage(w, h int, y, u, v byte, full bool) *yuvImage {
	img := &yuvImage{w: w, h: h, cw: (w + 1) / 2, ch: (h + 1) / 2, sx: 1, sy: 1, full: full}
	img.y = bytes.Repeat([]byte{y}, w*h)
	img.u = bytes.Repeat([]byte{u}, img.cw*img.ch)
	img.v = bytes.Repeat([]byte{v}, img.cw*img.ch)
	return img
}

func TestScaleYUVColorsAndSize(t *testing.T) {
	cases := []struct {
		name    string
		img     *yuvImage
		bgra    [4]byte
		w, h    int
		maxDiff int
	}{
		{"white", solidImage(1320, 2868, 235, 128, 128, false), [4]byte{255, 255, 255, 255}, 442, 960, 0},
		{"black", solidImage(64, 64, 16, 128, 128, false), [4]byte{0, 0, 0, 255}, 64, 64, 0},
		{"red BT.709", solidImage(10, 10, 63, 102, 240, false), [4]byte{0, 0, 255, 255}, 30, 30, 3},
		{"full range grey", solidImage(10, 10, 128, 128, 128, true), [4]byte{128, 128, 128, 255}, 5, 5, 0},
	}
	for _, c := range cases {
		out := scaleYUV(c.img, nil, c.w, c.h)
		if len(out) != c.w*c.h*4 {
			t.Fatalf("%s: %d bytes", c.name, len(out))
		}
		for i := 0; i < len(out); i += 4 {
			for j := range 4 {
				if d := int(out[i+j]) - int(c.bgra[j]); d > c.maxDiff || -d > c.maxDiff {
					t.Fatalf("%s: pixel %d = % x, want % x", c.name, i/4, out[i:i+4], c.bgra)
				}
			}
		}
	}
	// Luma is averaged over each output pixel's area: a fine checkerboard turns grey.
	img := solidImage(4, 4, 16, 128, 128, false)
	for i := range img.y {
		if (i/4+i%4)%2 == 0 {
			img.y[i] = 235
		}
	}
	if out := scaleYUV(img, nil, 2, 2); out[0] < 120 || out[0] > 135 {
		t.Fatalf("checkerboard average = %d", out[0])
	}
	if w, h := fitSize(1320, 2868, 540, 1170); w != 538 || h != 1170 {
		t.Fatalf("fit = %dx%d", w, h)
	}
}
