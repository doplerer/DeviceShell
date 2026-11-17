package quicktime

import (
	"errors"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

// The capture interface (interface 2 once QuickTime is enabled, class 0xFF subclass 0x2A)
// needs the WinUSB driver of DeviceShell's automatic iPhone preparation (internal/ios/qtdriver).
// winusb.dll is used directly: libusb opens every interface of the composite device and
// fails on Apple's usbmux one, which Apple Mobile Device Service holds. Nothing here
// touches the USB configuration, so usbmux, the CoreDevice tunnel and touch keep working.

var (
	cfgmgr32                     = syscall.NewLazyDLL("cfgmgr32.dll")
	winusb                       = syscall.NewLazyDLL("winusb.dll")
	cmGetInterfaceListSize       = cfgmgr32.NewProc("CM_Get_Device_Interface_List_SizeW")
	cmGetInterfaceList           = cfgmgr32.NewProc("CM_Get_Device_Interface_ListW")
	cmLocateDevNode              = cfgmgr32.NewProc("CM_Locate_DevNodeW")
	cmGetParent                  = cfgmgr32.NewProc("CM_Get_Parent")
	cmGetDeviceID                = cfgmgr32.NewProc("CM_Get_Device_IDW")
	winusbInitialize             = winusb.NewProc("WinUsb_Initialize")
	winusbFree                   = winusb.NewProc("WinUsb_Free")
	winusbControlTransfer        = winusb.NewProc("WinUsb_ControlTransfer")
	winusbReadPipe               = winusb.NewProc("WinUsb_ReadPipe")
	winusbWritePipe              = winusb.NewProc("WinUsb_WritePipe")
	winusbSetPipePolicy          = winusb.NewProc("WinUsb_SetPipePolicy")
	winusbResetPipe              = winusb.NewProc("WinUsb_ResetPipe")
	winusbQueryInterfaceSettings = winusb.NewProc("WinUsb_QueryInterfaceSettings")
)

// DeviceInterfaceGUIDs written by qt-driver.inf: {4cf7e09a-414f-43a2-8c9c-b2ef39c9370a}.
var qtInterfaceGUID = syscall.GUID{Data1: 0x4cf7e09a, Data2: 0x414f, Data3: 0x43a2, Data4: [8]byte{0x8c, 0x9c, 0xb2, 0xef, 0x39, 0xc9, 0x37, 0x0a}}

const (
	qtSubclass   = 0x2A
	qtPipeIn     = 0x86
	qtPipeOut    = 0x05
	qtSwitchWait = 10 * time.Second // about 1 s to enable, 4 s to disable
)

var ErrUnavailable = errors.New("capture interface not installed")

func qtInterfacePaths() []string {
	var size uint32
	if r, _, _ := cmGetInterfaceListSize.Call(uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&qtInterfaceGUID)), 0, 0); r != 0 || size <= 1 {
		return nil
	}
	buf := make([]uint16, size)
	if r, _, _ := cmGetInterfaceList.Call(uintptr(unsafe.Pointer(&qtInterfaceGUID)), 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(size), 0); r != 0 {
		return nil
	}
	return multiSz(buf)
}

// qtParentID is the instance id of the composite parent; it ends with the device serial.
func qtParentID(path string) string {
	instance := strings.ReplaceAll(strings.SplitN(path[min(4, len(path)):], "#{", 2)[0], "#", `\`)
	name, e := syscall.UTF16PtrFromString(instance)
	if e != nil {
		return ""
	}
	var node, parent uint32
	buf := make([]uint16, 512)
	if r, _, _ := cmLocateDevNode.Call(uintptr(unsafe.Pointer(&node)), uintptr(unsafe.Pointer(name)), 0); r != 0 {
		return ""
	}
	if r, _, _ := cmGetParent.Call(uintptr(unsafe.Pointer(&parent)), uintptr(node), 0); r != 0 {
		return ""
	}
	if r, _, _ := cmGetDeviceID.Call(uintptr(parent), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0); r != 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// qtFind is the path of this device's capture interface, or "".
func qtFind(udid string) string {
	serial := `\` + strings.ToUpper(strings.ReplaceAll(udid, "-", ""))
	for _, p := range qtInterfacePaths() {
		if strings.HasSuffix(strings.ToUpper(qtParentID(p)), serial) {
			return p
		}
	}
	return ""
}

type winusbInterface struct {
	file   syscall.Handle
	handle uintptr
}

func openWinUSB(path string) (*winusbInterface, error) {
	name, e := syscall.UTF16PtrFromString(path)
	if e != nil {
		return nil, e
	}
	// Read/write, shared, overlapped: the calls pass no OVERLAPPED and complete synchronously.
	file, e := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_OVERLAPPED, 0)
	if e != nil {
		return nil, e
	}
	w := &winusbInterface{file: file}
	if r, _, e := winusbInitialize.Call(uintptr(file), uintptr(unsafe.Pointer(&w.handle))); r == 0 {
		syscall.CloseHandle(file)
		return nil, e
	}
	return w, nil
}

func (w *winusbInterface) subclass() (int, error) {
	var d [9]byte // USB_INTERFACE_DESCRIPTOR
	if r, _, e := winusbQueryInterfaceSettings.Call(w.handle, 0, uintptr(unsafe.Pointer(&d[0]))); r == 0 {
		return 0, e
	}
	return int(d[6]), nil
}

func (w *winusbInterface) control(requestType, request byte, value, index uint16) error {
	// WINUSB_SETUP_PACKET is 8 bytes, passed by value in one register.
	setup := uint64(requestType) | uint64(request)<<8 | uint64(value)<<16 | uint64(index)<<32
	var n uint32
	if r, _, e := winusbControlTransfer.Call(w.handle, uintptr(setup), 0, 0, uintptr(unsafe.Pointer(&n)), 0); r == 0 {
		return e
	}
	return nil
}

func (w *winusbInterface) prepare() {
	timeout := uint32(500)
	winusbSetPipePolicy.Call(w.handle, qtPipeIn, 0x03, 4, uintptr(unsafe.Pointer(&timeout))) // PIPE_TRANSFER_TIMEOUT
	for _, pipe := range []uintptr{qtPipeIn, qtPipeOut} {
		winusbResetPipe.Call(w.handle, pipe)
	}
}

// read returns 0 bytes when the 500 ms pipe timeout passes.
func (w *winusbInterface) read(buf []byte) (int, error) {
	var n uint32
	if r, _, e := winusbReadPipe.Call(w.handle, qtPipeIn, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&n)), 0); r == 0 {
		if e == syscall.Errno(121) { // ERROR_SEM_TIMEOUT
			return 0, nil
		}
		return 0, e
	}
	return int(n), nil
}

func (w *winusbInterface) write(data []byte) error {
	var n uint32
	if r, _, e := winusbWritePipe.Call(w.handle, qtPipeOut, uintptr(unsafe.Pointer(&data[0])), uintptr(len(data)), uintptr(unsafe.Pointer(&n)), 0); r == 0 {
		return e
	}
	return nil
}

func (w *winusbInterface) close() {
	if w.handle != 0 {
		winusbFree.Call(w.handle)
		w.handle = 0
	}
	if w.file != 0 {
		syscall.CloseHandle(w.file)
		w.file = 0
	}
}

// Capture is one QuickTime session on its claimed interface.
type Capture struct {
	iface   *winusbInterface
	Session *Session
	started bool
}

// qtSwitch turns QuickTime on or off and releases the interface; the device
// re-enumerates and often drops the request as it does.
func qtSwitch(w *winusbInterface, on bool, log func(string, ...any)) {
	index := uint16(0)
	if on {
		index = 2
	}
	if e := w.control(0x40, 0x52, 0, index); e != nil {
		log("QuickTime switch: %v", e)
	}
	w.close()
}

// qtWait is the capture interface once the device re-enumerated into the wanted mode.
func qtWait(udid string, ready func(subclass int) bool) (*winusbInterface, error) {
	deadline := time.Now().Add(qtSwitchWait)
	for ; time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		path := qtFind(udid)
		if path == "" {
			continue
		}
		w, e := openWinUSB(path)
		if e != nil {
			continue
		}
		if sub, e := w.subclass(); e == nil && ready(sub) {
			return w, nil
		}
		w.close()
	}
	return nil, errors.New("QuickTime capture interface did not switch mode")
}

// Open enables QuickTime and claims the capture interface. Enabling
// re-enumerates the device (usbmux drops it for a moment), so it runs before the
// connector opens its tunnel.
func Open(udid string, log func(string, ...any)) (*Capture, error) {
	path := qtFind(udid)
	if path == "" {
		return nil, ErrUnavailable
	}
	w, e := openWinUSB(path)
	if e != nil {
		return nil, e
	}
	sub, e := w.subclass()
	if e != nil {
		w.close()
		return nil, e
	}
	if sub == qtSubclass {
		// Left enabled by a connector that did not stop: the device starts no new
		// session on it until QuickTime is turned off and on.
		log("QuickTime was left enabled; restarting it")
		qtSwitch(w, false, log)
		if w, e = qtWait(udid, func(s int) bool { return s != qtSubclass }); e != nil {
			return nil, e
		}
	}
	qtSwitch(w, true, log)
	if w, e = qtWait(udid, func(s int) bool { return s == qtSubclass }); e != nil {
		return nil, e
	}
	w.prepare()
	return &Capture{iface: w, Session: newQTSession(w, log)}, nil
}

// start begins the protocol once usbmux has the re-enumerated device again.
func (q *Capture) Start() {
	q.started = true
	go q.Session.readLoop()
}

// stop asks the device to stop and releases the interface. disable also turns
// QuickTime off, back to the normal USB mode; that re-enumerates the device, so it
// is only done once the connector's tunnel is closed.
func (q *Capture) Stop(disable bool, log func(string, ...any)) {
	if q.started {
		q.Session.stop()
	}
	if disable {
		qtSwitch(q.iface, false, log)
	} else {
		q.iface.close()
	}
}

// multiSz splits a list of NUL-terminated strings ending with an empty one.
func multiSz(buf []uint16) []string {
	var out []string
	start := 0
	for i, c := range buf {
		if c == 0 {
			if i > start {
				out = append(out, syscall.UTF16ToString(buf[start:i]))
			}
			start = i + 1
		}
	}
	return out
}
