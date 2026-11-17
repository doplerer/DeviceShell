package core

import (
	"slices"
	"testing"
)

// With every service published, the Go engine offers what the Python connector
// reported for an iPhone on iOS 27.2.
func TestNativeCapabilitiesMatchConnector(t *testing.T) {
	want := []string{"app-start", "app-stop", "key:DEL", "key:ENTER", "key:HOME", "key:LOCK", "key:MUTE", "key:POWER", "key:VOLUME_DOWN", "key:VOLUME_UP", "live-touch", "screenshot", "swipe", "tap", "text", "url"}
	if got := nativeCapabilities(func(string) bool { return true }); !slices.Equal(got, want) {
		t.Fatalf("capabilities\n got %v\nwant %v", got, want)
	}
	// Without the developer image's touch service there is no typing or URL.
	got := nativeCapabilities(func(s string) bool { return s == "apps" || s == "dvt" })
	if !slices.Equal(got, []string{"app-start", "app-stop", "screenshot"}) {
		t.Fatal(got)
	}
}

func TestHIDUsages(t *testing.T) {
	for _, c := range []struct {
		ch    rune
		usage byte
		shift bool
	}{{'a', 0x04, false}, {'z', 0x1D, false}, {'B', 0x05, true}, {'1', 0x1E, false}, {'9', 0x26, false}, {'0', 0x27, false}, {' ', 0x2C, false}} {
		if u, s, ok := hidUsage(c.ch); !ok || u != c.usage || s != c.shift {
			t.Errorf("%q = %#x %v %v", c.ch, u, s, ok)
		}
	}
	for _, ch := range ":ñé/." {
		if _, _, ok := hidUsage(ch); ok {
			t.Errorf("%q must be pasted, not typed", ch)
		}
	}
}
