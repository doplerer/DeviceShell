// Package qtdriver installs the WinUSB driver of the iPhone's QuickTime capture
// interface (USB interface 2 once QuickTime is on), as Zadig/libwdi do: a catalog for
// qt-driver.inf signed with a throwaway certificate this PC trusts. Only Windows needs
// it; elsewhere Pending is always false.
package qtdriver

import _ "embed"

//go:embed qt-driver.inf
var driverINF []byte
