package scrcpy

import (
	"encoding/binary"
	"testing"
)

func session(w, h int) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint32(b, 1<<31)
	binary.BigEndian.PutUint32(b[4:], uint32(w))
	binary.BigEndian.PutUint32(b[8:], uint32(h))
	return b
}

func packet(config bool, data []byte) []byte {
	b := make([]byte, 12)
	flags := uint64(123456)
	if config {
		flags = FlagConfig
	}
	binary.BigEndian.PutUint64(b, flags)
	binary.BigEndian.PutUint32(b[8:], uint32(len(data)))
	return append(b, data...)
}

func TestParseHeader(t *testing.T) {
	if h, e := ParseHeader(session(496, 1072)); e != nil || !h.Session || h.Width != 496 || h.Height != 1072 {
		t.Fatal(h, e)
	}
	if h, e := ParseHeader(packet(true, []byte{1, 2, 3})); e != nil || h.Session || !h.Config || h.Size != 3 {
		t.Fatal(h, e)
	}
	if h, e := ParseHeader(packet(false, []byte{1})); e != nil || h.Config || h.Size != 1 {
		t.Fatal(h, e)
	}
	if _, e := ParseHeader(packet(false, nil)); e == nil {
		t.Fatal("an empty packet must be rejected")
	}
	if _, e := ParseHeader(session(0, 10)); e == nil {
		t.Fatal("a session without size must be rejected")
	}
}
