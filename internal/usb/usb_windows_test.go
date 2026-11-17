package usb

import "testing"

// The USB tree is read without failing; phones are only matched by serial.
func TestNodesReadsTheSystem(t *testing.T) {
	nodes := Nodes()
	placed := 0
	for _, n := range nodes {
		if n.Controller != "" {
			placed++
		}
	}
	t.Logf("%d USB devices with a serial, %d under a known host controller", len(nodes), placed)
	if len(nodes) > 0 && placed == 0 {
		t.Error("no USB device found its host controller")
	}
}
