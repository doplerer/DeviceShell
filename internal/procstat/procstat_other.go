//go:build !windows

package procstat

import "time"

var start = time.Now()

func Now() int64        { return int64(time.Since(start)) }
func Self() Usage       { return Usage{} }
func Process(int) Usage { return Usage{} }
