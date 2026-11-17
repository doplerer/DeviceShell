package core

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/doplerer/deviceshell/internal/android/scrcpy"
)

// A stream recording (.dsrec) keeps the access units a decoder got from a real phone,
// with their arrival time, so the bench replays real video at its real pace:
//
//	"BFREC001", codec ("h264" or "hevc"),
//	then per unit: u32 LE milliseconds since the first one, u32 LE size, Annex B data.
//
// Parameter sets travel inside the units, as the decoder received them, so a replay
// that starts over from the first unit decodes again.
const benchRecordMagic = "BFREC001"

// videoRecorder, when set (only by the opt-in recording tests), receives every access
// unit a connector decodes. source names the connector ("android-<serial>", "ios-<udid>").
var videoRecorder func(source string, hevc bool) func(unit []byte)

type streamRecorder struct {
	mu      sync.Mutex
	w       *bufio.Writer
	f       *os.File
	started time.Time
	units   int
}

func createStreamRecording(path string, hevc bool) (*streamRecorder, error) {
	f, e := os.Create(path)
	if e != nil {
		return nil, e
	}
	r := &streamRecorder{f: f, w: bufio.NewWriterSize(f, 1<<20)}
	codec := "h264"
	if hevc {
		codec = "hevc"
	}
	r.w.WriteString(benchRecordMagic + codec)
	return r, nil
}

func (r *streamRecorder) add(unit []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return
	}
	if r.units == 0 {
		r.started = time.Now()
	}
	r.units++
	var head [8]byte
	binary.LittleEndian.PutUint32(head[:], uint32(time.Since(r.started).Milliseconds()))
	binary.LittleEndian.PutUint32(head[4:], uint32(len(unit)))
	r.w.Write(head[:])
	r.w.Write(unit)
}

func (r *streamRecorder) close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	e := r.w.Flush()
	if c := r.f.Close(); e == nil {
		e = c
	}
	r.f = nil
	return e
}

// recordedUnit is one access unit of a recording.
type recordedUnit struct {
	at   time.Duration
	data []byte
}

// readStreamRecording loads a whole recording; they are about 20 s long.
func readStreamRecording(path string) (units []recordedUnit, hevc bool, err error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, false, e
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	head := make([]byte, len(benchRecordMagic)+4)
	if _, e = io.ReadFull(r, head); e != nil || string(head[:len(benchRecordMagic)]) != benchRecordMagic {
		return nil, false, errors.New("invalid recording: " + path)
	}
	switch string(head[len(benchRecordMagic):]) {
	case "h264":
	case "hevc":
		hevc = true
	default:
		return nil, false, errors.New("unknown codec in " + path)
	}
	var unitHead [8]byte
	for {
		if _, e = io.ReadFull(r, unitHead[:]); e == io.EOF {
			break
		} else if e != nil {
			return nil, false, e
		}
		size := binary.LittleEndian.Uint32(unitHead[4:])
		if size > scrcpy.PacketLimit {
			return nil, false, errors.New("access unit too large in " + path)
		}
		data := make([]byte, size)
		if _, e = io.ReadFull(r, data); e != nil {
			return nil, false, e
		}
		units = append(units, recordedUnit{at: time.Duration(binary.LittleEndian.Uint32(unitHead[:])) * time.Millisecond, data: data})
	}
	if len(units) == 0 {
		return nil, false, errors.New("empty recording: " + path)
	}
	return units, hevc, nil
}
