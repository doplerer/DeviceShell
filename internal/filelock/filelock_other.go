//go:build !windows

package filelock

import (
	"os"

	"golang.org/x/sys/unix"
)

// Lock takes a shared or exclusive lock on f without waiting.
func Lock(f *os.File, exclusive bool) error {
	flags := unix.LOCK_SH | unix.LOCK_NB
	if exclusive {
		flags = unix.LOCK_EX | unix.LOCK_NB
	}
	return unix.Flock(int(f.Fd()), flags)
}

// Unlock releases a lock taken with Lock.
func Unlock(f *os.File) {
	unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
