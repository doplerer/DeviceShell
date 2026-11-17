package usb

import (
	"strings"
	"syscall"
	"unsafe"
)

var (
	cfgmgr32                      = syscall.NewLazyDLL("cfgmgr32.dll")
	cmGetDeviceID                 = cfgmgr32.NewProc("CM_Get_Device_IDW")
	cmGetParent                   = cfgmgr32.NewProc("CM_Get_Parent")
	setupapi                      = syscall.NewLazyDLL("setupapi.dll")
	procSetupDiGetClassDevsW      = setupapi.NewProc("SetupDiGetClassDevsW")
	procSetupDiEnumDeviceInfo     = setupapi.NewProc("SetupDiEnumDeviceInfo")
	procSetupDiDestroyDeviceInfo  = setupapi.NewProc("SetupDiDestroyDeviceInfoList")
	procCMGetDevNodeRegistryPropW = cfgmgr32.NewProc("CM_Get_DevNode_Registry_PropertyW")
)

// spDevinfoData is SP_DEVINFO_DATA.
type spDevinfoData struct {
	Size     uint32
	Class    [16]byte
	DevInst  uint32
	Reserved uintptr
}

const (
	cmDrpDeviceDesc   = 0x01
	cmDrpService      = 0x05
	cmDrpFriendlyName = 0x0D
)

func devnodeID(inst uint32) string {
	buf := make([]uint16, 512)
	if r, _, _ := cmGetDeviceID.Call(uintptr(inst), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0); r != 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

func devnodeProperty(inst uint32, property uint32) string {
	buf := make([]uint16, 512)
	size := uint32(len(buf) * 2)
	var kind uint32
	if r, _, _ := procCMGetDevNodeRegistryPropW.Call(uintptr(inst), uintptr(property), uintptr(unsafe.Pointer(&kind)), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0); r != 0 {
		return ""
	}
	return syscall.UTF16ToString(buf)
}

// hostController walks up from a USB device to the host controller it hangs from.
func hostController(inst uint32) (string, string) {
	for range 32 {
		var parent uint32
		if r, _, _ := cmGetParent.Call(uintptr(unsafe.Pointer(&parent)), uintptr(inst), 0); r != 0 {
			return "", ""
		}
		inst = parent
		switch strings.ToLower(devnodeProperty(inst, cmDrpService)) {
		case "usbxhci", "usbehci", "usbohci", "usbuhci":
			name := devnodeProperty(inst, cmDrpFriendlyName)
			if name == "" {
				name = devnodeProperty(inst, cmDrpDeviceDesc)
			}
			return devnodeID(inst), name
		}
	}
	return "", ""
}

// Nodes lists the USB devices present with their host controllers.
func Nodes() []Node {
	enumerator := syscall.StringToUTF16Ptr("USB")
	set, _, _ := procSetupDiGetClassDevsW.Call(0, uintptr(unsafe.Pointer(enumerator)), 0, 0x2|0x4) // DIGCF_PRESENT | DIGCF_ALLCLASSES
	if set == 0 || set == ^uintptr(0) {
		return nil
	}
	defer procSetupDiDestroyDeviceInfo.Call(set)
	var nodes []Node
	for i := 0; ; i++ {
		data := spDevinfoData{}
		data.Size = uint32(unsafe.Sizeof(data))
		if r, _, _ := procSetupDiEnumDeviceInfo.Call(set, uintptr(i), uintptr(unsafe.Pointer(&data))); r == 0 {
			break
		}
		id := devnodeID(data.DevInst)
		// USB\VID_xxxx&PID_yyyy\SERIAL; interfaces of a composite device (…&MI_nn) and
		// generated serials (with '&') are not phones.
		parts := strings.Split(id, `\`)
		if len(parts) != 3 || strings.Contains(parts[1], "&MI_") || strings.Contains(parts[2], "&") {
			continue
		}
		controller, name := hostController(data.DevInst)
		nodes = append(nodes, Node{Serial: parts[2], Controller: controller, ControllerName: name})
	}
	return nodes
}
