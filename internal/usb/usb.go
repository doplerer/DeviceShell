// Package usb lists the USB devices of the system and the host controller each one
// hangs from, so doctor can tell when too many phones share one controller.
package usb

// Node is one USB device and the host controller it hangs from.
type Node struct {
	Serial         string // the USB serial number, last part of the instance id
	Controller     string // the controller's instance id
	ControllerName string
}
