package core

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func (app *Service) testHandshake(t *testing.T) {
	a, b := net.Pipe()
	go func() {
		b.Write([]byte{0})
		b.Close()
	}()
	if e := app.ReadHandshake(a); e != nil {
		t.Fatal(e)
	}
	a.Close()
	a, b = net.Pipe()
	b.Close()
	if e := app.ReadHandshake(a); e == nil {
		t.Fatal("EOF handshake accepted")
	}
	a.Close()
}
func (app *Service) testReleaseHeldInput(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	p := &Peer{app: app, conn: a}
	key := make([]byte, 14)
	key[0] = 0
	key[1] = 0
	binary.BigEndian.PutUint32(key[2:], 3)
	received := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 28)
		io.ReadFull(b, buf)
		received <- buf
	}()
	if e := p.writeFrame(key); e != nil {
		t.Fatal(e)
	}
	p.release()
	select {
	case data := <-received:
		if data[15] != 1 {
			t.Fatal("No KEY_UP on pause")
		}
	case <-time.After(time.Second):
		t.Fatal("Release blocked")
	}
}
func (app *Service) testProtocolFragmentation(t *testing.T) {
	frames := [][]byte{make([]byte, 14), make([]byte, 32), make([]byte, 21)}
	frames[1][0] = 2
	frames[2][0] = 3
	for _, b := range frames {
		for i := 0; i < len(b); i++ {
			n, e := app.FrameLength(b[:i])
			if e != nil || n != 0 {
				t.Fatal(i, n, e)
			}
		}
		n, e := app.FrameLength(b)
		if n != len(b) || e != nil {
			t.Fatal(n, e)
		}
	}
	if _, e := app.FrameLength([]byte{255}); e == nil {
		t.Fatal("Unknown message accepted")
	}
	b := make([]byte, 14)
	b[0] = 9
	binary.BigEndian.PutUint32(b[10:], 300000)
	if _, e := app.FrameLength(b); e == nil {
		t.Fatal("Oversized message accepted")
	}
}
func (app *Service) testScale(t *testing.T) {
	b := make([]byte, 32)
	b[0] = 2
	binary.BigEndian.PutUint32(b[10:], 288)
	binary.BigEndian.PutUint32(b[14:], 512)
	binary.BigEndian.PutUint16(b[18:], 576)
	binary.BigEndian.PutUint16(b[20:], 1024)
	v := app.ScaleFrame(b, 1080, 1920)
	if binary.BigEndian.Uint32(v[10:]) != 540 || binary.BigEndian.Uint32(v[14:]) != 960 {
		t.Fatal(v)
	}
	if binary.BigEndian.Uint32(b[10:]) != 288 {
		t.Fatal("Input mutated")
	}
}
func (app *Service) testQuoteAndRoutine(t *testing.T) {
	if app.Quote("a'b & c") != "'a'\"'\"'b & c'" {
		t.Fatal("quote")
	}
	p := filepath.Join(t.TempDir(), "r.json")
	os.WriteFile(p, []byte(`{"steps":[{"action":"text","args":["${mensaje}"]}]}`), 0600)
	s, e := app.LoadRoutine([]string{p, "mensaje=Hola, mañana 😀\nsegunda linea"})
	if e != nil || s[0].Args[0] != "Hola, mañana 😀\nsegunda linea" {
		t.Fatal(s, e)
	}
	if _, e := app.LoadRoutine([]string{p}); e == nil {
		t.Fatal("Unresolved parameter accepted")
	}
}
func (app *Service) testCancel(t *testing.T) {
	c, cancel := context.WithCancel(context.Background())
	cancel()
	_, e := app.Execute(c, Device{}, Spec{Action: "wait", Args: []string{"5000"}})
	if e == nil {
		t.Fatal("Cancel ignored")
	}
}
func (app *Service) testNormalizeURL(t *testing.T) {
	for in, want := range map[string]string{"google.com": "https://google.com", "http://x.com": "http://x.com", "HTTPS://X": "HTTPS://X", "mailto:a@b.c": "mailto:a@b.c", "  www.x.com ": "https://www.x.com"} {
		if got := app.NormalizeURL(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
