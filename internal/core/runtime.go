package core

import (
	"context"
	"os"
	"sync"
	"time"
)

type Service struct {
	iosSetupMu sync.Mutex
	// iosAuto and iosDriverTried belong to automatic preparation, under iosSetupMu.
	iosAuto        map[string]iosAutoState
	iosDriverTried bool
	// iosLocks holds the serials of iPhones whose last connection failed because they
	// are locked (noteIOSConnect).
	iosLocks sync.Map
	// iosActivation asks USB iPhones to trust this PC and turns on their Developer
	// Mode (ios_activate.go).
	iosActivation iosActivation
	// wifi finds and tracks the iPhones reachable over Wi-Fi (ios_wifi.go);
	// iosTransport is how each iPhone was last discovered ("usb"/"wifi").
	wifi         iosWiFi
	iosTransport sync.Map
	// WiFi enables looking for iPhones on the LAN (the real program; tests leave it off).
	WiFi   bool
	RunIOS func(context.Context, ...string) (string, error)
	// StartIOSConnector replaces the iPhone engine in tests.
	StartIOSConnector func(context.Context, Device) (iosConnector, error)
	// iosLibraryLogOnce opens go-ios's shared log the first time a phone connects.
	iosLibraryLogOnce sync.Once
	perfLog           perfLogger
	iosInfo           iosInfoCache // what each iPhone is, while usbmux keeps its id
	jarLocks          sync.Map     // serial -> *sync.Mutex: one scrcpy push per device at a time
	aspectMu          sync.Mutex
	aspects           map[string]float64 // screen height / width of each device, from its last window layout
	iosMu             sync.Mutex
	iosSessions       map[string]*iosDeviceSession
	androidMu         sync.Mutex
	androidSessions   map[string]*androidDeviceSession
	ExecuteStep       func(c context.Context, d Device, s Spec) (string, error)
	Lang              string
	Root              string
	Adb               string
	Scrcpy            string
	ToolDir           string
	Parallel          int
	Timeout           time.Duration
	Ctx               context.Context
	StartupCxt        bool
	usageLock         *os.File
	storeMu           sync.Mutex
	MasterSize        int
	MasterFPS         int
	FollowerSize      int
	FollowerFPS       int
	RetryPause        time.Duration // first pause between attempts of a step; it doubles up to 30s
}

func New() *Service {
	app := &Service{}
	app.ExecuteStep = app.Execute
	app.Lang = "es"
	app.RetryPause = time.Second
	return app
}
