package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// errIOSDeveloperImage: the phone publishes no developer services because the
// developer image is not mounted.
var errIOSDeveloperImage = errors.New("developer disk image not mounted")

// iosStatus is what the engine reports about a connected phone.
type iosStatus struct {
	IOS          string   `json:"ios"`
	Capabilities []string `json:"capabilities"`
	Video        string   `json:"video"`
	VideoError   string   `json:"videoError"`
}

// iosConnectorInfo is the last status seen per device, so incompatible steps are
// rejected before a batch starts.
type iosConnectorInfo struct {
	IOS          string   `json:"ios"`
	Capabilities []string `json:"capabilities"`
	Video        string   `json:"video"`
	Checked      string   `json:"checked"`
}

// connectIOS opens the iPhone engine. With video, the picture comes from iosVideo
// and the status reports it.
func (app *Service) connectIOS(c context.Context, d Device, video *iosVideo) (iosConnector, error) {
	var conn iosConnector
	var e error
	switch {
	case app.StartIOSConnector != nil:
		conn, e = app.StartIOSConnector(c, d)
	case d.Transport == "wifi":
		conn, e = app.connectIOSWiFi(c, d, video)
	default:
		conn, e = app.connectIOSNative(c, d, true, video)
	}
	if e != nil {
		return nil, e
	}
	status := conn.info()
	if video != nil {
		status.Video, status.VideoError = "waiting", video.errorText()
		if video.live() {
			status.Video = "stream"
			if !slices.Contains(status.Capabilities, "video") {
				status.Capabilities = append(status.Capabilities, "video")
			}
		}
		if n, ok := conn.(*iosNative); ok {
			n.status = status
		}
	}
	_ = app.updateIOSOptions(func(cfg *iosOptions) {
		if cfg.Connector == nil {
			cfg.Connector = map[string]iosConnectorInfo{}
		}
		cfg.Connector[d.NativeID()] = iosConnectorInfo{IOS: status.IOS, Capabilities: status.Capabilities, Video: status.Video, Checked: time.Now().Format(time.RFC3339)}
	})
	return conn, nil
}

func iosCapability(s Spec) string {
	if s.Action == "key" && len(s.Args) == 1 {
		return "key:" + strings.ToUpper(s.Args[0])
	}
	return s.Action
}

func (app *Service) saveIOSScreenshot(d Device, png []byte) (string, error) {
	// Alias is user supplied; only the basename is used in output paths.
	p := app.CapturesPath(filepath.Base(strings.ReplaceAll(d.Alias, "\\", "/")) + "-" + time.Now().Format("20060102-150405.000000000") + ".png")
	return p, os.WriteFile(p, png, 0600)
}
