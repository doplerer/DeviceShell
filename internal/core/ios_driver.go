package core

import (
	"context"

	"github.com/doplerer/deviceshell/internal/ios/qtdriver"
)

// ensureIOSQuickTimeDriver gives the connected iPhones' QuickTime capture interface
// the WinUSB driver, asking for administrator rights only when one still lacks it.
// QuickTime's USB capture is the viewer's only picture source.
func (app *Service) ensureIOSQuickTimeDriver(c context.Context, notify func(IOSSetupProgress)) error {
	if app.StartIOSConnector != nil || !qtdriver.Pending() {
		return nil // a test's stand-in connector, or nothing to install
	}
	if notify != nil {
		notify(IOSSetupProgress{Stage: "components", Message: app.L("Instalando el controlador de vídeo USB del iPhone; acepta el aviso de Windows...", "Installing the iPhone USB video driver; accept the Windows prompt...")})
	}
	return qtdriver.Install(c)
}
