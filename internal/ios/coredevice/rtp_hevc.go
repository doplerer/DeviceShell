package coredevice

import (
	"bytes"
	"encoding/binary"
)

// HEVCAssembler turns the DisplayService RTP packets (HEVC, RFC 7798: single NAL
// units, aggregation packets and fragmentation units, no DONL) into Annex B access
// units, one per picture: the phone sets the marker bit on the last packet of each.
type HEVCAssembler struct {
	au       []byte
	frag     bool // inside a fragmented NAL unit
	broken   bool // a packet of this picture was lost
	started  bool
	lastSeq  uint16
	Lost     int64 // packets lost so far
	Pictures int64 // complete access units handed out
}

var startCode = []byte{0, 0, 0, 1}

// rtpPayload returns the payload of an RTP packet (RFC 3550 5.1, 5.3.1), without padding.
func rtpPayload(p []byte) []byte {
	if len(p) < 12 {
		return nil
	}
	n := 12 + 4*int(p[0]&0x0f)
	if p[0]&0x10 != 0 {
		if len(p) < n+4 {
			return nil
		}
		n += 4 + 4*int(binary.BigEndian.Uint16(p[n+2:]))
	}
	end := len(p)
	if p[0]&0x20 != 0 && end > n {
		end -= int(p[end-1])
	}
	if n >= end {
		return nil
	}
	return p[n:end]
}

// Push adds one RTP packet. When it completes a picture it returns the access unit,
// valid until the next call, and whether packets of it were lost (the decoder can
// still use it, with artifacts, until the next keyframe).
func (a *HEVCAssembler) Push(p []byte) (au []byte, lost bool) {
	seq := binary.BigEndian.Uint16(p[2:])
	if a.started && seq != a.lastSeq+1 {
		gap := int64(uint16(seq - a.lastSeq - 1))
		if gap > 0x7fff {
			return nil, false // late or duplicate: skip it
		}
		a.Lost += gap
		a.broken, a.frag = true, false
	}
	a.started, a.lastSeq = true, seq
	pl := rtpPayload(p)
	if len(pl) >= 2 {
		switch t := (pl[0] >> 1) & 0x3f; t {
		case 48: // aggregation packet: 16-bit sizes and NAL units
			for b := pl[2:]; len(b) >= 2; {
				n := int(binary.BigEndian.Uint16(b))
				if n == 0 || len(b) < 2+n {
					break
				}
				a.au = append(append(a.au, startCode...), b[2:2+n]...)
				b = b[2+n:]
			}
		case 49: // fragmentation unit: rebuild the NAL header from the FU header
			if len(pl) < 3 {
				break
			}
			fu := pl[2]
			if fu&0x80 != 0 {
				a.au = append(a.au, startCode...)
				a.au = append(a.au, pl[0]&0x81|(fu&0x3f)<<1, pl[1])
				a.frag = true
			}
			if a.frag {
				a.au = append(a.au, pl[3:]...)
			}
			if fu&0x40 != 0 {
				a.frag = false
			}
		case 50: // PACI: not used by the phone
		default:
			a.au = append(append(a.au, startCode...), pl...)
		}
	}
	if p[1]&0x80 == 0 {
		return nil, false
	}
	au, lost = a.au, a.broken
	a.au, a.broken, a.frag = a.au[:0], false, false
	if len(au) == 0 {
		return nil, false
	}
	a.Pictures++
	return au, lost
}

// PictureLossIndication is the RTCP PLI (RFC 4585 6.3.1) asking the phone for a new
// keyframe after a loss.
func (d *Display) PictureLossIndication() error {
	b := make([]byte, 12)
	copy(b, []byte{0x81, 0xce, 0x00, 0x02})
	binary.BigEndian.PutUint32(b[4:], d.ours)
	binary.BigEndian.PutUint32(b[8:], d.theirs)
	_, e := d.udp.WriteTo(b, d.dest)
	return e
}

// H264Assembler does the same for H.264 (RFC 6184: single NAL units, STAP-A, FU-A).
type H264Assembler struct{ HEVCAssembler }

func (a *H264Assembler) Push(p []byte) (au []byte, lost bool) {
	seq := binary.BigEndian.Uint16(p[2:])
	if a.started && seq != a.lastSeq+1 {
		gap := int64(uint16(seq - a.lastSeq - 1))
		if gap > 0x7fff {
			return nil, false
		}
		a.Lost += gap
		a.broken, a.frag = true, false
	}
	a.started, a.lastSeq = true, seq
	if pl := rtpPayload(p); len(pl) >= 1 && pl[0]&0x80 != 0 {
		// Not H.264: the phone's format description (an ImageDescription with the
		// avcC record) carries SPS and PPS out of band.
		a.au = append(a.au, avcCParameters(pl)...)
	} else if len(pl) >= 1 {
		switch t := pl[0] & 0x1f; t {
		case 24: // STAP-A
			for b := pl[1:]; len(b) >= 2; {
				n := int(binary.BigEndian.Uint16(b))
				if n == 0 || len(b) < 2+n {
					break
				}
				a.au = append(append(a.au, startCode...), b[2:2+n]...)
				b = b[2+n:]
			}
		case 28: // FU-A
			if len(pl) < 2 {
				break
			}
			fu := pl[1]
			if fu&0x80 != 0 {
				a.au = append(append(a.au, startCode...), pl[0]&0xe0|fu&0x1f)
				a.frag = true
			}
			if a.frag {
				a.au = append(a.au, pl[2:]...)
			}
			if fu&0x40 != 0 {
				a.frag = false
			}
		default:
			if t >= 1 && t <= 23 {
				a.au = append(append(a.au, startCode...), pl...)
			}
		}
	}
	if p[1]&0x80 == 0 {
		return nil, false
	}
	au, lost = a.au, a.broken
	a.au, a.broken, a.frag = a.au[:0], false, false
	if len(au) == 0 {
		return nil, false
	}
	a.Pictures++
	return au, lost
}

// avcCParameters returns the SPS and PPS of the avcC record inside b, in Annex B.
func avcCParameters(b []byte) []byte {
	i := bytes.Index(b, []byte("avcC"))
	if i < 0 || len(b) < i+4+6 {
		return nil
	}
	r := b[i+4:]
	var out []byte
	pos := 5
	for _, mask := range []byte{0x1f, 0xff} { // SPS count (low 5 bits), then PPS count
		if pos >= len(r) {
			return out
		}
		n := int(r[pos] & mask)
		pos++
		for range n {
			if pos+2 > len(r) {
				return out
			}
			l := int(binary.BigEndian.Uint16(r[pos:]))
			pos += 2
			if pos+l > len(r) {
				return out
			}
			out = append(append(out, startCode...), r[pos:pos+l]...)
			pos += l
		}
	}
	return out
}
