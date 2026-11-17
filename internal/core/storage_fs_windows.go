package core

import (
	"os"
	"syscall"

	"golang.org/x/sys/windows"
)

func (app *Service) IsRedirect(info os.FileInfo) bool {
	return info.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 || (info.Sys().(*syscall.Win32FileAttributeData).FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0
}
