//go:build !windows

package core

import "github.com/doplerer/deviceshell/internal/ios/quicktime"

// The FFmpeg decoder is Windows only.
func newVideoDecoder(string, bool) (videoDecoder, error) { return nil, quicktime.ErrUnavailable }
