package coredevice

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"howett.net/plist"
)

// The captures (testdata/capture-*.jsonl) are the reference: what this package sends
// must match what the phone accepted from the previous connector.

type event struct {
	Kind string         `json:"kind"`
	Name string         `json:"name"`
	Msg  map[string]any `json:"msg"`
	Head string         `json:"head"`
}

func loadCapture(t *testing.T, name string) []event {
	t.Helper()
	f, e := os.Open("testdata/" + name)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	var events []event
	s := bufio.NewScanner(f)
	s.Buffer(nil, 4<<20)
	for s.Scan() {
		var ev event
		if e := json.Unmarshal(s.Bytes(), &ev); e != nil {
			t.Fatal(e)
		}
		events = append(events, ev)
	}
	return events
}

// typed turns the capture's tagged JSON ({"$uint64": 5}) back into XPC Go values.
func typed(v any) any {
	switch v := v.(type) {
	case map[string]any:
		if len(v) == 1 {
			for k, x := range v {
				switch k {
				case "$uint64":
					return uint64(x.(float64))
				case "$int64":
					return int64(x.(float64))
				case "$int":
					return int64(x.(float64))
				case "$double":
					return x.(float64)
				case "$data":
					b, _ := hex.DecodeString(x.(string))
					return b
				case "$uuid":
					return uuid.MustParse(x.(string))
				}
			}
		}
		out := map[string]any{}
		for k, x := range v {
			out[k] = typed(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = typed(x)
		}
		return out
	}
	return v
}

// shape is a value's keys and XPC types, without the values themselves.
func shape(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, x := range v {
			out[k] = shape(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = shape(x)
		}
		return out
	case int:
		return "int64"
	}
	return reflect.TypeOf(v).String()
}

func sent(events []event, match func(map[string]any) bool) []map[string]any {
	var out []map[string]any
	for _, ev := range events {
		if ev.Kind == "xpc_out" && len(ev.Msg) > 0 {
			m := typed(ev.Msg).(map[string]any)
			if match(m) {
				out = append(out, m)
			}
		}
	}
	return out
}

// payload is m["payload"][key], or nil.
func payload(m map[string]any, key string) map[string]any {
	p, _ := m["payload"].(map[string]any)
	v, _ := p[key].(map[string]any)
	return v
}

func hidReports(events []event, serviceID uint64) [][]byte {
	var out [][]byte
	for _, m := range sent(events, func(m map[string]any) bool { return payload(m, "send") != nil }) {
		send := payload(m, "send")
		if send["_1"].(uint64) == serviceID {
			out = append(out, send["_0"].([]byte))
		}
	}
	return out
}

func TestTouchReportsMatchCapture(t *testing.T) {
	events := loadCapture(t, "capture-active.jsonl")
	reports := hidReports(events, touchServiceID)
	if len(reports) < 50 {
		t.Fatalf("only %d touch reports in the capture", len(reports))
	}
	for _, r := range reports {
		x, y := float64(binary.LittleEndian.Uint16(r[4:]))/65535, float64(binary.LittleEndian.Uint16(r[6:]))/65535
		if got := touchReport(r[3] == 0xc2, x, y, binary.LittleEndian.Uint64(r[44:])); !bytes.Equal(got, r) {
			t.Fatalf("touch report\n got % x\nwant % x", got, r)
		}
	}
	// The status-bar tap at (0.5, 0.015): contact then release at the same point.
	if got := touchReport(true, 0.5, 0.015, binary.LittleEndian.Uint64(reports[0][44:])); !bytes.Equal(got, reports[0]) {
		t.Fatalf("tap report\n got % x\nwant % x", got, reports[0])
	}
}

func TestKeyboardReportsMatchCapture(t *testing.T) {
	events := loadCapture(t, "capture-active.jsonl")
	reports := hidReports(events, keyboardServiceID)
	if len(reports) < 40 {
		t.Fatalf("only %d keyboard reports in the capture", len(reports))
	}
	seen := map[byte]bool{}
	for _, r := range reports {
		var usages []byte
		for u := 0; u < 240; u++ {
			if r[1+u/8]&(1<<(u%8)) != 0 {
				usages = append(usages, byte(u))
				seen[byte(u)] = true
			}
		}
		if got := keyboardReport(usages, binary.LittleEndian.Uint64(r[31:])); !bytes.Equal(got, r) {
			t.Fatalf("keyboard report\n got % x\nwant % x", got, r)
		}
	}
	for _, u := range []byte{KeyEnter, KeyBackspace, KeyLeftGUI, KeyV, KeyT, KeyL, 0x05 /* b */} {
		if !seen[u] {
			t.Errorf("usage %#x never captured", u)
		}
	}
}

func TestButtonEventsMatchCapture(t *testing.T) {
	events := loadCapture(t, "capture-active.jsonl")
	captured := sent(events, func(m map[string]any) bool { return m["messageType"] == "IndigoButtonEvent" })
	if len(captured) != 10 {
		t.Fatalf("%d button events", len(captured))
	}
	home := Buttons["HOME"]
	if got := buttonEvent(home.Page, home.Code, true); !reflect.DeepEqual(got, captured[0]) {
		t.Fatalf("HOME down\n got %v\nwant %v", got, captured[0])
	}
	if got := buttonEvent(home.Page, home.Code, false); !reflect.DeepEqual(got, captured[1]) {
		t.Fatalf("HOME up\n got %v\nwant %v", got, captured[1])
	}
	up := Buttons["VOLUME_UP"]
	if got := buttonEvent(up.Page, up.Code, true); !reflect.DeepEqual(got, captured[2]) {
		t.Fatalf("VOLUME_UP\n got %v\nwant %v", got, captured[2])
	}
}

func TestKeyboardServiceRequestShape(t *testing.T) {
	events := loadCapture(t, "capture-active.jsonl")
	captured := sent(events, func(m map[string]any) bool { return payload(m, "createService") != nil })
	if len(captured) != 1 {
		t.Fatalf("%d createService requests", len(captured))
	}
	if got, want := shape(keyboardServiceRequest()), shape(captured[0]); !reflect.DeepEqual(got, want) {
		t.Fatalf("createService shape\n got %v\nwant %v", got, want)
	}
}

func TestCoreDeviceRequestShape(t *testing.T) {
	events := loadCapture(t, "capture-passive.jsonl")
	for _, feature := range []string{"getdisplayinfo", "getmediastreamserverstatus", "stopmediastream"} {
		captured := sent(events, func(m map[string]any) bool {
			return m["CoreDevice.featureIdentifier"] == "com.apple.coredevice.feature."+feature
		})
		if len(captured) == 0 {
			t.Fatalf("no %s in the capture", feature)
		}
		action := ""
		if a, ok := captured[0]["CoreDevice.actionIdentifier"].(string); ok {
			action = strings.TrimPrefix(a, "com.apple.coredevice.action.")
		}
		got := coreDeviceRequest(feature, action, captured[0]["CoreDevice.input"].(map[string]any))
		if !reflect.DeepEqual(shape(got), shape(captured[0])) {
			t.Fatalf("%s shape\n got %v\nwant %v", feature, shape(got), shape(captured[0]))
		}
	}
}

func capturedOffer(t *testing.T, events []event) (map[string]any, []byte) {
	t.Helper()
	start := sent(events, func(m map[string]any) bool {
		return m["CoreDevice.featureIdentifier"] == "com.apple.coredevice.feature.startmediastream"
	})
	if len(start) != 1 {
		t.Fatalf("%d startmediastream", len(start))
	}
	var offer map[string]any
	if _, e := plist.Unmarshal(start[0]["CoreDevice.input"].(map[string]any)["negotiatorOffer"].([]byte), &offer); e != nil {
		t.Fatal(e)
	}
	z, e := zlib.NewReader(bytes.NewReader(offer["avcMediaStreamNegotiatorMediaBlob"].([]byte)))
	if e != nil {
		t.Fatal(e)
	}
	blob, _ := io.ReadAll(z)
	return offer, blob
}

func TestNegotiatorOfferMatchesCapture(t *testing.T) {
	for _, name := range []string{"capture-passive.jsonl", "capture-active.jsonl"} {
		events := loadCapture(t, name)
		offer, blob := capturedOffer(t, events)
		// Our SSRC is the first field of the video description (offset 8).
		ssrc, n := binary.Uvarint(blob[8:])
		if n <= 0 {
			t.Fatal("no SSRC in the captured blob")
		}
		if got := mediaBlob(uint32(ssrc), false); !bytes.Equal(got, blob) {
			t.Fatalf("%s media blob\n got %x\nwant %x", name, got, blob)
		}
		raw, e := negotiatorOffer(uint32(ssrc), offer["avcMediaStreamOptionCallID"].(string), false)
		if e != nil {
			t.Fatal(e)
		}
		var ours map[string]any
		if _, e = plist.Unmarshal(raw, &ours); e != nil {
			t.Fatal(e)
		}
		keys := func(m map[string]any) []string {
			var k []string
			for x := range m {
				k = append(k, x)
			}
			sort.Strings(k)
			return k
		}
		if !reflect.DeepEqual(keys(ours), keys(offer)) || ours["avcMediaStreamNegotiatorMode"] != offer["avcMediaStreamNegotiatorMode"] ||
			!bytes.Equal(ours["avcMediaStreamOptionRemoteEndpointInfo"].([]byte), offer["avcMediaStreamOptionRemoteEndpointInfo"].([]byte)) {
			t.Fatalf("offer\n got %v\nwant %v", ours, offer)
		}
	}
}

func TestRTCPReportsMatchCapture(t *testing.T) {
	events := loadCapture(t, "capture-active.jsonl")
	n := 0
	for _, ev := range events {
		if ev.Kind != "udp_out" {
			continue
		}
		want, _ := hex.DecodeString(ev.Head)
		got := rtcpReport(binary.BigEndian.Uint32(want[4:]), binary.BigEndian.Uint32(want[8:]), binary.BigEndian.Uint32(want[16:]))
		if !bytes.Equal(got, want) {
			t.Fatalf("RTCP\n got % x\nwant % x", got, want)
		}
		n++
	}
	if n < 10 {
		t.Fatalf("only %d RTCP reports", n)
	}
}
