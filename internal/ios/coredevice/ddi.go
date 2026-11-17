package coredevice

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/danielpaulus/go-ios/ios"
	"github.com/danielpaulus/go-ios/ios/imagemounter"
)

// The personalized developer image (Xcode's DDI) brings the CoreDevice services.
// Its files come from Apple's Xcode, published at github.com/doronz88/DeveloperDiskImage;
// the phone only mounts it once Apple's TSS signs it for this device, so a modified
// image cannot be mounted.
const ddiURL = "https://raw.githubusercontent.com/doronz88/DeveloperDiskImage/main/PersonalizedImages/Xcode_iOS_DDI_Personalized/"

var ddiFiles = []string{"BuildManifest.plist", "Image.dmg", "Image.dmg.trustcache"}

// EnsureImage makes sure dir holds the image, downloading what is missing.
func EnsureImage(dir string) error {
	if e := os.MkdirAll(dir, 0755); e != nil {
		return e
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	for _, name := range ddiFiles {
		path := filepath.Join(dir, name)
		if info, e := os.Stat(path); e == nil && info.Size() > 0 {
			continue
		}
		resp, e := client.Get(ddiURL + name)
		if e != nil {
			return fmt.Errorf("developer disk image: %w", e)
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return fmt.Errorf("developer disk image: %s: HTTP %d", name, resp.StatusCode)
		}
		f, e := os.CreateTemp(dir, ".download-*")
		if e == nil {
			_, e = io.Copy(f, resp.Body)
			if closeErr := f.Close(); e == nil {
				e = closeErr
			}
			if e == nil {
				e = os.Rename(f.Name(), path)
			}
			os.Remove(f.Name())
		}
		resp.Body.Close()
		if e != nil {
			return fmt.Errorf("developer disk image: %s: %w", name, e)
		}
	}
	return nil
}

// Mounted reports whether a developer image is mounted (it is gone after a reboot).
func Mounted(udid string) (bool, error) {
	device, e := ios.GetDevice(udid)
	if e != nil {
		return false, e
	}
	conn, e := imagemounter.NewImageMounter(device)
	if e != nil {
		return false, e
	}
	defer conn.Close()
	images, e := conn.ListImages()
	return len(images) > 0, e
}

// Mount mounts the image in dir, asking Apple's TSS to sign it for this device
// (needs Internet the first time after each reboot; no Apple account).
func Mount(udid, dir string) error {
	if e := EnsureImage(dir); e != nil {
		return e
	}
	device, e := ios.GetDevice(udid)
	if e != nil {
		return e
	}
	return imagemounter.MountImage(device, dir)
}
