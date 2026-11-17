package tui

import (
	"os"
	"syscall"
)

var procSetStdHandle = syscall.NewLazyDLL("kernel32.dll").NewProc("SetStdHandle")

// stdErrorHandle is STD_ERROR_HANDLE ((DWORD)-12) as the call's argument.
var stdErrorHandle = uintptr(uint32(syscall.STD_ERROR_HANDLE & 0xffffffff))

// setErrorHandle also points the Windows standard error handle at f, for code that
// does not go through os.Stderr (DLLs loaded later, such as FFmpeg's).
func setErrorHandle(f *os.File) func() {
	old, e := syscall.GetStdHandle(syscall.STD_ERROR_HANDLE)
	if e != nil {
		return func() {}
	}
	procSetStdHandle.Call(stdErrorHandle, f.Fd())
	return func() { procSetStdHandle.Call(stdErrorHandle, uintptr(old)) }
}
