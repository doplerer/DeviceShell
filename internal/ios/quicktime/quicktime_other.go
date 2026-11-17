//go:build !windows

package quicktime

import "errors"

// The QuickTime capture needs WinUSB: it is Windows only.

var ErrUnavailable = errors.New("USB video is only available on Windows")

type Capture struct{ Session *Session }

func Open(string, func(string, ...any)) (*Capture, error) { return nil, ErrUnavailable }
func (q *Capture) Start()                                 {}
func (q *Capture) Stop(bool, func(string, ...any))        {}
