// Package procstat measures the CPU time and memory of this and other processes, and
// keeps a fine clock for timing the stages of the video path.
package procstat

import "time"

// Usage is the CPU time and memory of a process.
type Usage struct {
	CPU                 time.Duration
	WorkingSet, Private uint64
}

func (u Usage) Add(o Usage) Usage {
	return Usage{u.CPU + o.CPU, u.WorkingSet + o.WorkingSet, u.Private + o.Private}
}
