# iPhone and iPad from Windows, without an Apple account

DeviceShell controls iPhones and iPads over USB, or over Wi-Fi after pairing them once by cable. It needs no Apple Developer membership and no Apple ID, does not sign or install any app on the phone, and has no 7-day expiry. It uses the development services Apple ships in iOS's own **developer disk image**, the same ones Xcode uses. Everything runs inside `deviceshell.exe`, in Go: the tunnel, touch, keyboard, buttons, apps, screenshots and video (`internal/ios/coredevice` and the `ios_*.go` files in `internal/core`). No Python, helper programs or tunnel drivers are required. It does not need administrator rights, except when you choose QuickTime as the video source over cable: then it asks once per PC, to install its USB driver (see [Video](#video)).

**Requirement: iOS 27 or later.** Live touch input requires an open DisplayService video session, and the iPhone only grants one starting with iOS 27. Earlier versions answer "Remote control requires iOS 27.0 or later", so DeviceShell does not support them.

Tested on physical hardware with iOS 27.2: a 60 fps window, taps, drags, text with accents and symbols, buttons, screenshots, opening and closing apps, opening URLs, remounting the disk image after a restart, and recovery after DeviceShell is force-closed.

## iPhone engine

- **Tunnel.** DeviceShell opens the CoreDevice tunnel over USB and runs its own IPv6 stack (gVisor) inside it, with TCP and UDP. Measured: tunnel and service list in 50–120 ms.
- **Services.** The services that go-ios (MIT) provides are used as a library: apps, screenshots (DVT), pasteboard, disk image mounting, Developer Mode, install, uninstall and reboot. The iOS 27 services that go-ios lacks (UniversalHID touch and keyboard, Indigo buttons, and the DisplayService session with its RTCP reports) are written from real traffic, described in [iPhone protocol](iphone-protocol.md). The tests in `internal/ios/coredevice` check that what is sent matches the captures.
- **Local port.** go-ios services reach the tunnel through a port on `127.0.0.1` that only accepts connections from `deviceshell.exe` itself (checked against the Windows TCP table).
- **Logs.** `output/logs/ios-control-<UDID>.log` (tunnel, touch session, reconnections; recreated on each connection) and `ios-goios.log` (go-ios warnings).

## Video

By default, the iPhone picture is the **DisplayService H.264 stream**, both over cable and over Wi-Fi. It is the same stream the iPhone already sends so that touch input works. DeviceShell decodes it with the FFmpeg that ships with scrcpy (`avcodec`/`avutil` from its folder, called without cgo) and hands each window the latest frame, already scaled to its size (`internal/core/ios_video.go`, `displaySource`). It needs no driver, does not change the status bar and does not reconnect the USB device.

In `/settings`, "iPhone video on cable" switches the cable source to **QuickTime** (config key `iphoneVideo`: `displayservice` or `quicktime`). Over Wi-Fi there is no alternative: it is always DisplayService. The change takes effect when the next screen opens. The connection stays alive for 1 minute after the last screen closes, so wait that minute or restart DeviceShell. `doctor` shows which source is in use.

Measured over cable on iOS 27.2 while swiping between home screen pages ([report](research/video-comparison/report.md)):

| | DisplayService (default) | QuickTime |
|---|---|---|
| Time to first picture | 0.35 s | 1.3 s |
| Frame rate while swiping | 48–50 fps | 56 fps |
| Touch → picture (median / p90) | 201 / 233 ms | 165 / 169 ms |
| Bandwidth while swiping | 4.9 Mbit/s | 32 Mbit/s |
| PC CPU while swiping | 0.19 cores | 0.43 cores |
| Sharpness | Native resolution; no visible difference at actual size | Native resolution |

HEVC is not usable with DisplayService (see [Over Wi-Fi](#over-wi-fi-no-cable)), so DeviceShell requests H.264.

### QuickTime (optional, cable only)

The same stream QuickTime Player records on a Mac: H.264 at native resolution (for example 1320×2868), up to 60 fps, using the USB bandwidth (10–44 Mbit/s measured). The protocol is ported to Go from [quicktime_video_hack](https://github.com/danielpaulus/quicktime_video_hack) (MIT) in `internal/ios/quicktime`. It only works on Windows.

- **Driver.** It is only installed if QuickTime is selected in `/settings`. The iPhone's USB interface 2 (the one used for USB tethering, which QuickTime turns into its capture interface) needs Windows' WinUSB driver. DeviceShell installs it once per PC, when it prepares the first iPhone with QuickTime selected, and asks for administrator permission (UAC). An elevated `deviceshell.exe` process does the work (`internal/ios/qtdriver`): it creates a catalog for `qt-driver.inf`, creates its own certificate, adds it to the machine's trusted stores and signs the catalog with it. The private key only exists in that process's memory and is never saved. This is the same approach Zadig uses. The driver registers with provider "DeviceShell" and device name "iPhone QuickTime capture (DeviceShell)". From then on, the iPhone no longer offers USB tethering to that PC. Without the driver there is no picture. The result of the last driver installation is kept in `tools/ios/setup/_quicktime-driver.json` and shown by `doctor`.
- **How it works.** DeviceShell enables QuickTime before opening the tunnel, because enabling it briefly reconnects the USB device. It reads the interface directly through `winusb.dll` without changing the USB configuration, so usbmux, the tunnel and touch input keep working. On close, it closes the tunnel first and then disables QuickTime, and the iPhone returns to its normal USB mode. If an earlier shutdown left QuickTime enabled, it turns it off and on again, because the iPhone does not start a new session on top of the previous one.
- **Status bar.** While the capture runs, the iPhone shows QuickTime's demo status bar: 9:41, full signal and full battery, without the charging bolt. The iPhone keeps charging. The normal status bar returns when the capture ends.
- **DisplayService alongside QuickTime.** Its session stays open because iOS drops touch input without it, but its video is discarded without being decoded; the RTCP reports sent every second keep the iPhone from closing it.
- **Errors.** Without a picture, the window keeps the last frame and says why in its title: `waiting for video` while starting, `no video: USB driver missing, reconnect the iPhone`, or `USB video error (details in output/logs/ios-video-*.log)`. A capture that drops is not retried within the same session, because re-enabling QuickTime reconnects the USB device and would interrupt touch input. It is retried on the next connection, which opens when a window is opened more than one minute after the last one closed.
- **Locked iPhone.** An iPhone or iPad locked with a passcode is marked `locked` from the first scan (lockdown refuses the service that checks Developer Mode) or as soon as it refuses a connection. It is no longer listed and DeviceShell stops connecting to it: it only reads its state every 30 s, and the device reappears on its own once it responds unlocked. To avoid this, set Auto-Lock to "Never".
- **Frozen picture.** If the capture is still open but the iPhone sends nothing for 5 s, a watchdog restarts it with a new connection (`watchdog: no QuickTime data` in the log). If it happens again right away, the stall threshold doubles with each restart (up to 5 min) so the iPhone is not pushed too hard; after 2 minutes of normal operation it goes back to 5 s.
- **Many iPhones.** The window appears immediately ("connecting") and the connection starts in the background. At most 4 iPhones switch USB mode (QuickTime) at the same time, so the USB controllers do not reset in a chain. The inventory only queries an iPhone (model, iOS version, Developer Mode) when usbmux sees it with a new identifier or after preparation; the switch to QuickTime that DeviceShell itself makes does not count as new. `doctor` shows how many iPhones are on each USB controller and how much video they can send.
- **Log.** In `output/logs/ios-video-<UDID>.log` (recreated for each session): `usbmux back in … ms` (how long the iPhone takes to reappear after QuickTime is enabled), `video source: quicktime`, or `no video:` with the reason, and `first frame after … ms` (full startup until the first picture). Every 5 s and per window: `video WxH (quicktime 1320x2868): decoded=… fps shown=… fps render=… ms usb=… Mbit/s`.
- **Removing the driver.**
  1. In Device Manager, uninstall "iPhone QuickTime capture (DeviceShell)" and check "Attempt to remove the driver for this device". Drivers installed by earlier versions appear under the old name "iPhone QuickTime capture (BOTFARM)" (provider "BOTFARM"); remove them the same way. They still work with current versions, so you only need to remove them if you want them gone.
  2. If the driver package is still in the driver store (for example because the iPhone was not connected), run `pnputil /enum-drivers` in an elevated prompt, find the `oem<N>.inf` entries whose provider is "DeviceShell" or "BOTFARM" and original name `qt-driver.inf`, and remove each one with `pnputil /delete-driver oem<N>.inf /uninstall`.
  3. Open `certlm.msc` and delete the "DeviceShell iPhone QuickTime driver" certificates from both "Trusted Root Certification Authorities" and "Trusted Publishers". Earlier versions created certificates named "BOTFARM iPhone QuickTime driver" in the same two stores; delete those too. Each installation creates a new certificate, so there may be more than one.

## Over Wi-Fi (no cable)

An iPhone paired once by cable can later be used with just its charger, without installing anything on it. This is what Xcode 27's Device Hub does: RemotePairing over the local network and the same CoreDevice tunnel as always. Measured on iOS 27.2: tunnel in 0.3 s, HOME in 0.2 s, picture at native resolution at 40–48 fps using 2–4 Mbit/s.

- **Pairing.** When automatic preparation finishes with an iPhone connected by cable, DeviceShell pairs it for Wi-Fi in the background, once. If the iPhone has never been paired this way, it asks "Trust This Computer?": accept it. If it is declined, it is not asked again until DeviceShell restarts. The PC's identity is stored in `tools/ios/remotepairing/selfIdentity.plist`, and the list of Wi-Fi iPhones (address, model, iOS version) in `tools/ios/config.json`, under `wifi`.
- **Discovery.** Every 10 s DeviceShell asks over mDNS who offers `_remotepairing._tcp`: by multicast and also directly to already known addresses, without needing Bonjour on Windows. Many networks do not deliver multicast to the iPhone (for example, with the PC on Ethernet and the iPhone on Wi-Fi). So, while a paired iPhone is missing (such as one just paired whose address is not known yet), DeviceShell also queries every address on the local network directly (private networks of up to 1024 addresses). Once it finds the iPhone, it saves its address and stops sweeping. You do not need to enter the IP address by hand. Each new address is checked once by opening its tunnel. Only an iPhone that trusts this PC opens it, which is how DeviceShell learns which one it is (its UDID). Other people's iPhones on the network are ignored for 10 min. When a pairing completes, that ignore list is cleared, because the newly paired iPhone is often on it. An iPhone that stops responding for 35 s is removed from the list.
- **In the device list.** It appears as `● Wi-Fi` (those connected by cable appear as `● USB`) and its window title shows `· iOS Wi-Fi ·` (by cable, `· iOS USB ·`). If it is also connected by cable, the cable connection takes precedence and uses the video source selected in `/settings`.
- **Picture.** It is the DisplayService H.264 stream, the same one that keeps touch input working, decoded with scrcpy's FFmpeg (`internal/core/ios_video.go`, `displaySource`). HEVC is not used: the iPhone offers it, but after a few page swipes its encoder references pictures that differ from the ones it sent, and the screen fills with blocks from other screens. This happens equally with FFmpeg and with Chrome's hardware decoder. The iPhone caps this stream at 6 Mbit/s and ignores requests for more bandwidth (REMB, TMMBR) and for a full frame (PLI). With H.264 the picture is sharp.
- **What works.** Windows, taps, drags, text, keys, buttons, URLs, opening and closing apps, screenshots, batches and replica. Listing, installing or uninstalling apps and rebooting do **not** work over Wi-Fi, because they need usbmux; DeviceShell tells you to connect the cable.
- **After the iPhone restarts.** The developer disk image is unmounted and cannot be mounted again over Wi-Fi: the iPhone is marked `needs cable` and is not listed until it is connected by cable once.
- **Network.** The PC and the iPhones must be on the same network, without client isolation (some guest networks enable it). Each iPhone uses 2–4 Mbit/s of Wi-Fi: with 20 iPhones, about 60 Mbit/s.
- **`doctor`.** Lists the Wi-Fi iPhones with their addresses, the paired ones that are not on the network, and declined pairings. Log: `output/logs/ios-wifi.log` (checks, addresses of iPhones that do not trust this PC, and pairings). In `tools/ios/config.json`, `seen` is the last time the iPhone responded over Wi-Fi.

## Devices that do not appear

`devices` and the interface only list available devices, and `@all`/`--all` only include those. Below the list, a line says how many are hidden ("4 unavailable · /doctor shows why"). The following are left out, whether iPhone or Android:
- disconnected, unauthorized or offline devices;
- devices locked with a passcode (see "Locked iPhone" under [QuickTime](#quicktime-optional-cable-only));
- devices on Wi-Fi that have restarted: they need the cable once (see [Over Wi-Fi](#over-wi-fi-no-cable));
- devices running a version earlier than iOS 27: update them in Settings > General > Software Update;
- iPhones on USB that do not trust the PC yet or do not have Developer Mode: DeviceShell enables it (see [USB activation](#usb-activation)).

Except for the ones being activated, DeviceShell does not interact with them: it does not prepare them, open anything on them or show warnings on screen. It only reads their state (every 30 s for locked devices, devices without Developer Mode and devices on old iOS versions), and they appear on their own as soon as they become available. Automatic preparation of available devices does not write to the screen either, so the menu stays visible; its result is in `/doctor`. If a group or target list includes an unavailable device, the interface skips it with a notice ("Unavailable, skipped: …") instead of launching an action that would fail; from the command line (`--devices DEV-018`), the result explains what is missing. `doctor` shows all of them, along with the latest preparation status of each iPhone.

The window title only shows the name, the connection (USB or Wi-Fi) and the frames per second received, for example `DEV-019 · iOS USB · 58 fps`; if there is a problem (reconnecting, disconnected…), the message replaces the fps. The shortcuts (Ctrl+H for HOME, Ctrl+V to paste) keep working even though they are not shown in the title. The window shows the DisplayService video, or the QuickTime video if it is selected for the cable. There is no screenshot mode. While connected, DeviceShell prevents the iPhone from auto-locking.

Starting and stopping the DisplayService session many times in a row (restarting DeviceShell, closing and opening windows, rapid retries) wedges that service on the iPhone. It stops responding (`touch stream unavailable … timeouts` in the log) and only recovers when the iPhone restarts. If the session fails, DeviceShell retries slowly: 5 s, 10 s, 20 s… up to 60 s. After 3 consecutive failed attempts, the title shows `touch blocked: restart the iPhone`. To avoid this, the connection stays alive for one minute after the last window closes, so a window that is reopened reuses the session instead of requesting a new one.

iOS treats that session like a call: if it is not stopped, it stays open on the iPhone even after the tunnel is gone, and eventually the iPhone rejects new ones. To prevent this:
- On close, DeviceShell stops the session and waits for the iPhone's confirmation before closing the tunnel.
- Before opening a session, it asks the iPhone which sessions are open and stops them (`stopping … stream(s) left on the phone` in the log). This cleans up sessions left behind when DeviceShell is force-closed.
- If the iPhone reports a call in progress, it does the same.

## USB activation

An iPhone or iPad connected over USB that is not ready yet is not registered: it gets no alias, does not appear in lists or in `@all`, and `/doctor` only says how many are waiting and what to do. Below the device list, a line summarizes it ("1 iPhone(s) to activate · …"). Meanwhile, DeviceShell activates it on its own:

1. **Trust.** If it has never trusted this PC, DeviceShell asks it "Trust This Computer?". Unlock it, tap "Trust" and enter the passcode. The prompt is shown again every 5 s while it is open. If you decline, it is not asked again until you disconnect and reconnect the device.
2. **Developer Mode.**
   - Without a passcode: DeviceShell enables it, the iPhone restarts and DeviceShell confirms the prompt it shows on startup. You do not need to touch anything.
   - With a passcode: iOS does not allow enabling it from the PC, so DeviceShell makes the option appear, and repeats this every 30 s while it is still off (the iPhone may have been locked the first time). iOS only shows the new option when Settings is opened from scratch: **fully close Settings** (swipe it away in the app switcher) and open it again. The option is at the bottom of Privacy & Security > Developer Mode. Turn it on, let the iPhone restart, unlock it and confirm "Turn On".

Once Developer Mode is on, the iPhone gets its alias and moves on to automatic preparation. `devices` waits for a restart in progress to finish before showing the list, and prints the pending steps to the console. If an already registered iPhone loses Developer Mode, it keeps its alias and is activated the same way.

Neither the Apple Devices app nor iTunes is needed for activation: Apple's usbmux service (Apple Mobile Device Support) is enough, and DeviceShell needs it anyway to see iPhones over USB. If Developer Mode does not turn on, `output/logs/ios-activation.log` records each step (trust, enabling, revealing the option) with the iPhone's response, and any failures reading whether the mode is on.

## Automatic preparation

There is no preparation command. With the interface open, DeviceShell prepares each iPhone or iPad as soon as it is activated (see [USB activation](#usb-activation)). The interface history shows the progress:
- if QuickTime is selected for the cable, it installs the USB video driver (WinUSB on the QuickTime interface) once per PC. This asks for administrator permission. If you decline, the window has no picture; it asks again the next time an iPhone is connected;
- it mounts the developer disk image. Apple signs it for your device through its public signing service, without an account, so this needs Internet access and the phone unlocked;
- it connects and reports what the device supports (`Ready (iOS …)`).

Each device is prepared once per connection. If it is locked, busy with a window, or preparation fails, it is retried automatically every 30 s. Disconnecting and reconnecting it prepares it again.

After the phone restarts, the image is unmounted. DeviceShell mounts it again automatically on the next connection (measured: 0.9 s), which also needs the phone unlocked and Internet access. The image is stored in `tools/ios/ddi/`; if it is missing, it is downloaded from the public [DeveloperDiskImage](https://github.com/doronz88/DeveloperDiskImage) repository, which publishes the files from Xcode. The device only accepts it with Apple's personalized signature, so a modified image cannot be mounted.

Checks: `doctor` (or `doctor --json`) shows the status without changing anything, including the latest preparation result for each device.

## Shared commands

Selections are always explicit: `--devices`, `--group`, `--all`; in the interface, `@alias`, `@group`, `@all`. iPhones keep `DEV` aliases and are identified internally as `ios:<UDID>`. Groups can mix platforms.

| Function | CLI | Interface |
|---|---|---|
| Tap | `tap 0.5 0.5` | `/motion tap 0.5 0.5` |
| Swipe | `swipe 0.5 0.8 0.5 0.2 300` | `/motion swipe 0.5 0.8 0.5 0.2 300` |
| Text / keys | `text "Hello"` / `key HOME` | `/motion text Hello` / `/motion key HOME` |
| Apps | `app list`, `app start ID`, `app stop ID` | `/apps`, `/start ID`, `/stop ID` |
| Install / uninstall | `app install FILE.ipa`, `app uninstall ID` | `/app install FILE`, `/app uninstall ID` |
| URL / screenshot / pause | `url URL`, `screenshot`, `wait 500` | `/url URL`, `/screenshot`, `/wait 500` |
| Normal reboot | `reboot system` | `/reboot system` |

iPhone keys: `HOME`, `ENTER`, `DEL`, `POWER`/`LOCK`, `VOLUME_UP`, `VOLUME_DOWN`, `MUTE`. Android keys with no equivalent (`BACK`, `RECENTS`…) are rejected.

Details:
- Coordinates from 0 to 1 refer to the full screen in portrait orientation.
- `text` types letters, digits and spaces; any other character (accented letters, `:`, `/`…) is pasted through the pasteboard, because iOS interprets keyboard input using the layout configured on the device. The phone's pasteboard is left holding that text.
- `url` opens Safari, creates a new tab and types the address.
- `app install` accepts already signed IPAs; DeviceShell does not sign apps.
- While DeviceShell has a keyboard connected (the "DeviceShell virtual keyboard"), iOS hides the on-screen keyboard.

## Per-platform routines

```json
{
  "platform": "both",
  "steps": [
    {"action":"key", "args":["HOME"]},
    {"action":"wait", "args":["500"]},
    {"action":"screenshot", "args":[]}
  ]
}
```

`platform` accepts `android`, `ios` or `both`; if it is missing, `android` is assumed. Before the first step, the platform and every step are checked for each device, including the capabilities that device published when it connected: a routine with a tap will not start on an iOS device without a touch service. `onError: continue` and retries do not skip this check. The whole routine uses a single connection to the iPhone.

## Screens and replica

`/view @group` opens DeviceShell's own windows for iPhone and Android devices in the same grid (Android windows use the scrcpy server; setting `"androidView": "scrcpy"` in `config.json` goes back to scrcpy.exe windows). On iPhone, the mouse acts as a live finger: pressing touches, dragging makes the phone follow the movement (and keep its momentum), and releasing lifts the finger, even if released outside the window. The scroll wheel performs a short drag at the pointer position. Ctrl+H sends HOME, Ctrl+V pastes text, Enter confirms and Backspace deletes. `--read-only` prevents sending any input. Windows that are closed reopen on their own.

The picture arrives already scaled to the window size, without JPEG compression, at up to 60 fps per window: every frame the iPhone emits is shown, and it sends more when the screen is moving. When the window is resized, the picture is requested again at the new size.

`/sync start MASTER @group` supports Android and iPhone masters and followers. It replicates one-finger gestures, text and compatible keys performed from the PC windows. Each follower has a queue of 32 actions; if it fills up, that follower's replica stops and the others continue.

## How it works and limits

- One connection per device, opened by DeviceShell and closed with it. Another DeviceShell instance cannot control the same phone at the same time.
- Apple's touch and video services are not publicly documented. An iOS update may change them; [iPhone protocol](iphone-protocol.md) explains how to capture the traffic again and compare.
- Trusting the computer is always confirmed on the device. Developer Mode can only be enabled without touching the iPhone when it has no passcode; with a passcode, iOS requires enabling it in Settings.

Sources: [go-ios v1.3.2](https://github.com/danielpaulus/go-ios/releases/tag/v1.3.2) (MIT, used as a library), [gVisor](https://gvisor.dev) (Apache-2.0, the tunnel's network stack), and the capture protocol from [quicktime_video_hack](https://github.com/danielpaulus/quicktime_video_hack) (MIT, ported to Go in `internal/ios/quicktime`). The iOS 27 services were implemented from captured traffic and public standards (USB HID, RFC 3550), without copying third-party code, so DeviceShell remains MIT-licensed.
