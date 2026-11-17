package coredevice

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func rtp(seq uint16, marker bool, payload string) []byte {
	p := []byte{0x80, 123, byte(seq >> 8), byte(seq), 0, 0, 0, 0, 0, 0, 0, 1}
	if marker {
		p[1] |= 0x80
	}
	b, _ := hex.DecodeString(payload)
	return append(p, b...)
}

// The phone's H.264 stream: SPS/PPS in its format description packet (avcC), then
// FU-A fragments of a picture.
func TestH264AssemblerReadsFormatDescriptionAndFragments(t *testing.T) {
	var a H264Assembler
	desc := "92e6c0a3000000866176633100" + hex.EncodeToString([]byte("avcC")) + "01640033ffe10004" + "27640033" + "010002" + "28ee"
	if au, _ := a.Push(rtp(1, false, desc)); au != nil {
		t.Fatal("no picture yet")
	}
	a.Push(rtp(2, false, "3c85aabb")) // FU-A start, IDR
	au, lost := a.Push(rtp(3, true, "3c45ccdd"))
	want, _ := hex.DecodeString("00000001" + "27640033" + "00000001" + "28ee" + "00000001" + "25aabbccdd")
	if lost || !bytes.Equal(au, want) {
		t.Fatalf("au %x lost %v", au, lost)
	}
	if au, lost = a.Push(rtp(5, true, "21d0")); !lost || a.Lost != 1 || !bytes.Equal(au, []byte{0, 0, 0, 1, 0x21, 0xd0}) {
		t.Fatalf("gap: au %x lost %v (%d)", au, lost, a.Lost)
	}
}
