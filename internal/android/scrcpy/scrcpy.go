// Package scrcpy is the wire format of scrcpy-server 5.0.1: the headers of its video
// socket and the control messages that inject touches, scrolls, keys and text.
//
// Video socket (send_device_meta, send_stream_meta and send_frame_meta): after the
// dummy byte, a 64-byte device name, the codec id (u32 BE) and then 12-byte headers.
// A session header (bit 63 set) gives the video size, the space touches are given in,
// whenever the encoder (re)starts: u32 flags, u32 width, u32 height. A packet header is
// u64 pts and flags (bit 62 config, bit 61 key frame) and u32 size, followed by its
// data; a config packet goes before the next picture.
package scrcpy

import (
	"encoding/binary"
	"fmt"
)

// HeaderSize is the size of every header after the codec id.
const HeaderSize = 12

const (
	DeviceNameSize = 64
	CodecH264      = 0x68323634 // "h264"
	FlagSession    = 1 << 63
	FlagConfig     = 1 << 62
	PacketLimit    = 1 << 24
)

// Header is one 12-byte header of the video socket.
type Header struct {
	Session       bool
	Config        bool
	Width, Height int // session headers
	Size          int // packet headers
}

// ParseHeader reads a header and rejects sizes the server never sends.
func ParseHeader(b []byte) (Header, error) {
	ptsFlags := binary.BigEndian.Uint64(b[:8])
	if ptsFlags&FlagSession != 0 {
		h := Header{Session: true, Width: int(binary.BigEndian.Uint32(b[4:8])), Height: int(binary.BigEndian.Uint32(b[8:12]))}
		if h.Width < 1 || h.Height < 1 || h.Width > 8192 || h.Height > 8192 {
			return h, fmt.Errorf("invalid video size: %dx%d", h.Width, h.Height)
		}
		return h, nil
	}
	h := Header{Config: ptsFlags&FlagConfig != 0, Size: int(binary.BigEndian.Uint32(b[8:12]))}
	if h.Size < 1 || h.Size > PacketLimit {
		return h, fmt.Errorf("invalid video packet: %d bytes", h.Size)
	}
	return h, nil
}

// Control messages of scrcpy-server 5.0.1, big endian. A position is x, y (i32) and
// the video size they refer to (u16 each); the server drops events for another size.
const (
	// Touch actions.
	MotionDown, MotionUp, MotionMove = 0, 1, 2
	pointerFinger                    = ^uint64(1) // -2: a finger on the touchscreen
)

func position(b []byte, x, y float64, w, h int) {
	binary.BigEndian.PutUint32(b[0:], uint32(int32(x*float64(w-1)+.5)))
	binary.BigEndian.PutUint32(b[4:], uint32(int32(y*float64(h-1)+.5)))
	binary.BigEndian.PutUint16(b[8:], uint16(w))
	binary.BigEndian.PutUint16(b[10:], uint16(h))
}

// Touch: type 2, action, pointer id (u64), position, pressure (u16 fixed point,
// 0xffff = 1), action button and buttons (u32, unused by fingers).
func Touch(action byte, x, y float64, w, h int) []byte {
	b := make([]byte, 32)
	b[0], b[1] = 2, action
	binary.BigEndian.PutUint64(b[2:], pointerFinger)
	position(b[10:], x, y, w, h)
	if action != MotionUp {
		binary.BigEndian.PutUint16(b[22:], 0xffff)
	}
	return b
}

// Scroll: type 3, position, horizontal and vertical scroll (i16 fixed point of
// value/16, so one wheel notch is 1/16) and buttons. Positive scrolls up, as the wheel.
func Scroll(x, y float64, w, h, steps int) []byte {
	b := make([]byte, 21)
	b[0] = 3
	position(b[1:], x, y, w, h)
	v := max(-1, min(1, float64(steps)/16))
	binary.BigEndian.PutUint16(b[15:], uint16(int16(min(0x7fff, int(v*0x8000)))))
	return b
}

// Key: type 0, action (0 down, 1 up), Android key code, repeat and meta state.
func Key(action byte, code int) []byte {
	b := make([]byte, 14)
	b[1] = action
	binary.BigEndian.PutUint32(b[2:], uint32(code))
	return b
}

// Text: type 1, length (u32) and UTF-8. The server types it key by key, so it
// only suits characters of the device keymap: plain ASCII.
func Text(text string) []byte {
	b := make([]byte, 5, 5+len(text))
	b[0] = 1
	binary.BigEndian.PutUint32(b[1:], uint32(len(text)))
	return append(b, text...)
}

// Paste: type 9, sequence (0: no acknowledgement), paste flag, length and UTF-8.
// It sets the device clipboard and pastes it, which types any text at once.
func Paste(text string) []byte {
	b := make([]byte, 14, 14+len(text))
	b[0], b[9] = 9, 1
	binary.BigEndian.PutUint32(b[10:], uint32(len(text)))
	return append(b, text...)
}

// Back: type 4, action. BACK, or it wakes the screen when it is off.
func Back(action byte) []byte {
	return []byte{4, action}
}

// ResetVideo: type 17 alone. The server restarts its encoder, which sends a new
// session header, the codec config and a key frame.
func ResetVideo() []byte {
	return []byte{17}
}
