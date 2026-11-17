package procstat

import (
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32                 = syscall.NewLazyDLL("kernel32.dll")
	procGetProcessTimes      = kernel32.NewProc("GetProcessTimes")
	procGetCurrentProcess    = kernel32.NewProc("GetCurrentProcess")
	procOpenProcess          = kernel32.NewProc("OpenProcess")
	procCloseHandle          = kernel32.NewProc("CloseHandle")
	procK32GetProcessMemInfo = kernel32.NewProc("K32GetProcessMemoryInfo")
	procQueryPerfCounter     = kernel32.NewProc("QueryPerformanceCounter")
	procQueryPerfFrequency   = kernel32.NewProc("QueryPerformanceFrequency")
	perfFrequency            = func() int64 {
		var f int64
		procQueryPerfFrequency.Call(uintptr(unsafe.Pointer(&f)))
		return max(1, f)
	}()
	processQueryLimitedVMRead = uintptr(0x1000 | 0x0010) // PROCESS_QUERY_LIMITED_INFORMATION | PROCESS_VM_READ
)

// processMemoryCounters is PROCESS_MEMORY_COUNTERS_EX.
type processMemoryCounters struct {
	Size, PageFaults                          uint32
	PeakWorkingSet, WorkingSet                uintptr
	PeakPagedPool, PagedPool                  uintptr
	PeakNonPagedPool, NonPagedPool            uintptr
	PagefileUsage, PeakPagefile, PrivateUsage uintptr
}

func usageOfHandle(h uintptr) Usage {
	var created, exited, kernel, user syscall.Filetime
	var u Usage
	if r, _, _ := procGetProcessTimes.Call(h, uintptr(unsafe.Pointer(&created)), uintptr(unsafe.Pointer(&exited)), uintptr(unsafe.Pointer(&kernel)), uintptr(unsafe.Pointer(&user))); r != 0 {
		ticks := func(f syscall.Filetime) int64 { return int64(f.HighDateTime)<<32 | int64(f.LowDateTime) }
		u.CPU = time.Duration((ticks(kernel) + ticks(user)) * 100)
	}
	m := processMemoryCounters{}
	m.Size = uint32(unsafe.Sizeof(m))
	if r, _, _ := procK32GetProcessMemInfo.Call(h, uintptr(unsafe.Pointer(&m)), uintptr(m.Size)); r != 0 {
		u.WorkingSet, u.Private = uint64(m.WorkingSet), uint64(m.PrivateUsage)
	}
	return u
}

// Now is a fine clock in nanoseconds for the time of each video stage: Go's own
// monotonic clock on Windows only advances every half millisecond or so.
func Now() int64 {
	var t int64
	procQueryPerfCounter.Call(uintptr(unsafe.Pointer(&t)))
	return int64(float64(t) * 1e9 / float64(perfFrequency))
}

// Self is the CPU time and memory of this process.
func Self() Usage {
	h, _, _ := procGetCurrentProcess.Call()
	return usageOfHandle(h)
}

// Process is the CPU time and memory of another process, zero if it cannot be read.
func Process(pid int) Usage {
	h, _, _ := procOpenProcess.Call(processQueryLimitedVMRead, 0, uintptr(pid))
	if h == 0 {
		return Usage{}
	}
	defer procCloseHandle.Call(h)
	return usageOfHandle(h)
}
