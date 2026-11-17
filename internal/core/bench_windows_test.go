package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// A recording keeps the units and their times, and the bench replays it through the
// real decoder.
func TestStreamRecordingRoundTripAndReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "android.dsrec")
	rec, e := createStreamRecording(path, false)
	if e != nil {
		t.Fatal(e)
	}
	luma := func(x, y int) byte { return byte(16 + (x+y)%200) }
	for i := range 6 {
		rec.add(pcmPicture(32, 32, i%2, luma))
		time.Sleep(15 * time.Millisecond)
	}
	if e = rec.close(); e != nil {
		t.Fatal(e)
	}
	units, hevc, e := readStreamRecording(path)
	if e != nil || hevc || len(units) != 6 || units[0].at != 0 || units[5].at < 50*time.Millisecond {
		t.Fatal(len(units), hevc, e)
	}
	if string(units[2].data) != string(pcmPicture(32, 32, 0, luma)) {
		t.Fatal("unit changed on the way")
	}
	src := &benchSource{}
	src.init("test", nil)
	c, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if e := src.feedRecording(c, scrcpyToolDir(t), units, hevc); e != nil {
		t.Fatal(e)
	}
	if n := src.decoded.Load(); n < 6 {
		t.Fatal("the recording must be decoded, and replayed in a loop:", n)
	}
	if src.received() == 0 {
		t.Fatal("bytes must be counted")
	}
	src.frameHub.close()
}

// recordFor keeps the units the connectors decode for d into .cache/bench/name in the repository.
func recordFor(t *testing.T, name string) func() {
	dir, _ := filepath.Abs(filepath.Join("..", "..", ".cache", "bench"))
	if e := os.MkdirAll(dir, 0755); e != nil {
		t.Fatal(e)
	}
	var mu sync.Mutex
	var rec *streamRecorder
	videoRecorder = func(source string, hevc bool) func([]byte) {
		mu.Lock()
		defer mu.Unlock()
		if rec != nil {
			rec.close()
		}
		var e error
		if rec, e = createStreamRecording(filepath.Join(dir, name), hevc); e != nil {
			t.Error(e)
			return nil
		}
		return rec.add
	}
	return func() {
		videoRecorder = nil
		mu.Lock()
		defer mu.Unlock()
		if rec != nil {
			t.Logf("%s: %d units", name, rec.units)
			rec.close()
		}
	}
}

// Opt-in: records about 20 s of a real Android stream for the bench. Swipe on the
// phone meanwhile. DEVICESHELL_RECORD_ANDROID=<serial> DEVICESHELL_ROOT=<DeviceShell folder>.
func TestRecordAndroidStream(t *testing.T) {
	serial, root := os.Getenv("DEVICESHELL_RECORD_ANDROID"), os.Getenv("DEVICESHELL_ROOT")
	if serial == "" || root == "" {
		t.Skip("set DEVICESHELL_RECORD_ANDROID and DEVICESHELL_ROOT")
	}
	app := New()
	app.Root = t.TempDir()
	app.EnsureLayout()
	app.ToolDir = filepath.Join(root, "tools", "scrcpy-win64-v5.0.1")
	app.Adb = filepath.Join(app.ToolDir, "adb.exe")
	done := recordFor(t, "android.dsrec")
	defer done()
	// The size of a window of a 20-phone wall.
	v, e := app.openAndroidVideo(context.Background(), serial, Rect{0, 0, 238, 423}, viewFPS)
	if e != nil {
		t.Fatal(e)
	}
	defer v.close()
	v.watch(true) // decode all along, as with a window open
	defer v.watch(false)
	time.Sleep(20 * time.Second)
	t.Logf("decoded %d pictures", v.decoded.Load())
}

// Opt-in: records about 20 s of a real iPhone QuickTime stream for the bench. Swipe on
// the phone meanwhile. DEVICESHELL_RECORD_IPHONE=<udid> DEVICESHELL_ROOT=<DeviceShell folder>.
func TestRecordIPhoneStream(t *testing.T) {
	udid, root := os.Getenv("DEVICESHELL_RECORD_IPHONE"), os.Getenv("DEVICESHELL_ROOT")
	if udid == "" || root == "" {
		t.Skip("set DEVICESHELL_RECORD_IPHONE and DEVICESHELL_ROOT")
	}
	app := New()
	app.Root = t.TempDir()
	app.EnsureLayout()
	app.ToolDir = filepath.Join(root, "tools", "scrcpy-win64-v5.0.1")
	done := recordFor(t, "iphone.dsrec")
	defer done()
	v := app.openIOSVideo(strings.TrimPrefix(udid, "ios:"), time.Minute)
	defer v.close(true)
	if !v.live() {
		t.Fatal("no QuickTime picture:", v.errorText())
	}
	time.Sleep(20 * time.Second)
	t.Logf("decoded %d pictures", v.decoded.Load())
}
