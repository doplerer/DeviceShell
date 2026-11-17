//go:build !windows

package core

import "os"

func (app *Service) IsRedirect(info os.FileInfo) bool {
	return info.Mode()&os.ModeSymlink != 0
}
