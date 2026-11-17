package core

import (
	"encoding/binary"
	"time"
)

func (p *Peer) writeFrame(b []byte) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	p.conn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond))
	_, e := p.conn.Write(b)
	if e != nil {
		return e
	}
	if b[0] == 0 {
		if p.keys == nil {
			p.keys = map[uint32][]byte{}
		}
		k := binary.BigEndian.Uint32(b[2:6])
		if b[1] == 0 {
			p.keys[k] = append([]byte(nil), b...)
		} else {
			delete(p.keys, k)
		}
	}
	if b[0] == 2 {
		if b[1] == 1 || b[1] == 3 {
			p.touch = nil
		} else {
			p.touch = append([]byte(nil), b...)
		}
	}
	return nil
}
func (p *Peer) release() {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if p.conn == nil {
		return
	}
	p.conn.SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
	for _, b := range p.keys {
		v := append([]byte(nil), b...)
		v[1] = 1
		p.conn.Write(v)
	}
	p.keys = nil
	if p.touch != nil {
		v := append([]byte(nil), p.touch...)
		v[1] = 3
		p.conn.Write(v)
		p.touch = nil
	}
}
func (s *LiveSession) broadcast(frame []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Paused.Load() {
		return
	}
	s.decodeControl(frame)
	switch frame[0] {
	case 0, 1, 2, 3, 4, 5, 6, 7, 9, 10, 11:
	default:
		return
	}
	for _, p := range s.Peers {
		if p.Failed() {
			continue
		}
		packet := s.app.ScaleFrame(frame, p.w, p.h)
		if packet == nil {
			continue
		}
		if e := p.writeFrame(packet); e != nil {
			p.fail()
			s.Errors[p.serial] = e.Error()
			s.log(p.serial + ": " + e.Error())
		}
	}
	s.Sent.Add(1)
}
