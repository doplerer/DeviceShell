# Changelog

All notable changes to DeviceShell are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

**Adding an entry:** write each change under **[Unreleased]**, in the matching subsection (Added, Changed, Deprecated, Removed, Fixed, or Security), as one sentence about what users or developers of DeviceShell will notice. When a version is released, move those entries into a new section with the version number and date. Never include serial numbers, UDIDs, IP addresses, or keys.

## [Unreleased]

## [1.0.0] - 2026-11-17

First public release. DeviceShell is a portable Windows application for managing Android devices and iPhones over USB and Wi-Fi from a CLI or an interactive terminal interface. It runs batch actions and routines across many devices at once, shows their screens in a grid of native windows, mirrors input from one device to others, and controls iPhones and iPads without an Apple Developer account or signed apps.

### Changed

- **The project is renamed from BOTFARM to DeviceShell.**
  - The executable is now `deviceshell.exe` (built from `cmd/deviceshell`), and the Go module is `github.com/doplerer/deviceshell`. The core package `internal/farm` is now `internal/core`.
  - Environment variables use the `DEVICESHELL_` prefix (`DEVICESHELL_ROOT`, `DEVICESHELL_LANG`). The old `BOTFARM_ROOT` and `BOTFARM_LANG` are still accepted when the new names are not set.
  - Generated device aliases are `DEV-001`, `DEV-002`, and so on, instead of `BOT-001`.
  - `config.json` keys are in English: `areaVentanas` is now `windowArea`, and `"monitores": "todos"` is now `"monitors": "all"`.
  - New installations start in English.
- **Installations from BOTFARM are migrated automatically on start:** old lock files are removed, Spanish folder and file names are renamed, `BOT-NNN` aliases become `DEV-NNN`, and Spanish `config.json` keys are renamed. `scripts/build.ps1` removes a leftover `botfarm.exe` from `dist/` when it installs the new executable.

### Added

- `scripts/fetch-tools.ps1` downloads the pinned scrcpy v5.0.1 release (ADB, scrcpy-server, and the FFmpeg libraries), checks its SHA-256, and installs it into `dist/tools`.
- `scripts/build.ps1` downloads the pinned Go toolchain into `.toolchain/` on first run and creates a new installation in `dist/` from `configs/` when there is none.
- Release packaging: pushing a `v*` tag builds a portable `deviceshell-<version>-windows-amd64.zip` (executable, default configuration and routines, tools, license, and notices) with its SHA-256 checksum and publishes it as a GitHub release.
- Community files: contributing guide (`CONTRIBUTING.md`), security policy (`SECURITY.md`), code of conduct (`CODE_OF_CONDUCT.md`), and issue and pull request templates.
- `THIRD_PARTY_NOTICES.md`, listing third-party code and components with their licenses.

## Pre-release history

Changes made before the first public release, when the project was named BOTFARM. Names below follow the current ones unless the old name is the point of the entry.

### After 2026-11-02

#### Fixed

- **A newly paired iPhone is found over Wi-Fi even if its IP address was never saved.** While any paired iPhone is missing, DeviceShell queries every address on the local network directly (private networks of up to 1,024 addresses only). This also finds it on networks that block mDNS multicast. Once it is found, its address is saved and the sweep stops.
- **No more 10-minute block after pairing.** If the iPhone was tried over Wi-Fi before tapping "Trust", its IP address was blocked for that long. The block is now cleared when pairing finishes, and the phone is looked up right away.
- **Direct queries go out through the interface of that network**, not always through the first one (which could be a VPN).
- **The Developer Mode option appears again on iPhones with a passcode.** Previously it was shown only once per connection: if the iPhone was locked or Settings was already open, it did not appear, and you had to fall back to the Apple Devices app. It is now shown again every 30 s while Developer Mode is off, and the message asks you to close Settings completely before opening it.
- **An iPhone whose Developer Mode status could not be read is no longer treated as enabled until it is reconnected.** If the image service does not answer, the status is read from lockdown; if that fails too, it is checked again on the next scan. Previously it was saved as enabled and activation never started.

#### Changed

- `ios-wifi.log` records the addresses of iPhones that do not trust this PC.
- New `output/logs/ios-activation.log`: each step of USB activation (trust, enabling Developer Mode, showing the option) with the iPhone's response.
- In `tools/ios/config.json`, `seen` stores the last time the iPhone answered over Wi-Fi.

### 2026-11-02

#### Added

- **iPhone activation over USB:**
  - An iPhone that has never trusted the PC gets the "Trust This Computer?" prompt.
  - One without Developer Mode gets it enabled. Without a passcode, it restarts and confirms on its own; with a passcode, the option appears in Settings.
  - Until it is ready, it is neither registered nor given an alias. The interface and `doctor` only show how many are waiting and what to do, and `devices` waits for a restart in progress to finish.
- **Continuous integration** on GitHub Actions: formatting, `go vet` (Windows and Linux), tests, and build.
- **Automatic migration of older installations** to the English names on start.

#### Changed

- **Standard Go layout:**
  - The Go module lives at the repository root, with `cmd/` and `internal/`.
  - Packages extracted from the core: `android/adb`, `android/scrcpy`, `ios/quicktime`, `ios/qtdriver`, `filelock`, `procstat`, and `usb`.
  - `iosdev` and `iosrp` become `ios/coredevice` and `ios/remotepairing`.
- **The portable installation lives in `dist/`**, separate from the source code. The toolchain is in `.toolchain/` and the caches in `.cache/`. `desarrollo/compilar.ps1` becomes `scripts/build.ps1`, which also checks `gofmt`.
- **Installation folders in English:** `data/`, `routines/`, `output/` (`logs/`, `screenshots/`, `downloads/`, `logcat/`), and `tools/`. Every path comes from `internal/core/paths.go`.
- **Clearer file names:**
  - In `tui`, without the `tui_` prefix.
  - The FFmpeg decoder moves to `video_decoder_*.go`.
- **Documentation files get lowercase English names:** `cli.md`, `development.md`, `iphone.md`, and `iphone-protocol.md`. Measurements are in `docs/research/`.

### 2026-11-01

#### Added

- **iPhone over Wi-Fi, with no app on the phone:**
  - Uses RemotePairing, the path behind Xcode's "Connect via network": a one-time pairing over USB, mDNS discovery of `_remotepairing._tcp`, and the same CoreDevice tunnel over the network.
  - It appears in the list as `● Wi‑Fi`.
  - After a restart it needs the cable once (`needs cable`).
  - Anything that requires usbmux (listing or installing apps, restarting) asks for the cable.
- **Configurable iPhone video in `/settings`,** in the new "iPhone" section: DisplayService (the default, also over USB) or QuickTime. Administrator rights for the QuickTime driver are requested only if QuickTime is chosen.
- **Renaming groups:** `/group rename` and `deviceshell group rename`.
- **Multiple monitors:** `"monitors": "all"` in `config.json` spreads the wall across every monitor.
- **Performance benchmark:** `--internal-bench` opens real windows with fake connectors and writes to `output/logs/bench-*.log`. In addition, `performance.log` records CPU, RAM, and fps every 30 s.

#### Changed

- **Windows inside the DeviceShell process:**
  - Each screen is a Win32 window of DeviceShell, double-buffered and painted directly.
  - Scaling is optimized.
  - The Go garbage collector is tuned when the first window opens.
  - The window block is moved by window events.
  - The iPhone inventory is cached while usbmux keeps its ID.
  - Concurrent starts are limited, and watchdogs detect frozen video.
  - At 60 fps per window:

    | Windows | Before | After |
    |---|---|---|
    | 200 Android + 20 iPhone | 17.6 cores and 5.0 GB | 5.3–5.5 cores and 250–290 MB |
    | 220 windows | — | open in 1.8 s |
- **iPhone video over DisplayService is H.264 instead of HEVC,** because the iPhone's HEVC encoder left artifacts when changing pages. Comparison over USB: DisplayService delivers the first frame in 0.35 s and uses half the CPU and one sixth of the bandwidth; QuickTime runs at 56 fps and responds about 36 ms sooner.
- **Only available devices are shown** in the list, in `@all`, and in suggestions:
  - Devices that are disconnected, unauthorized, offline, locked, without Developer Mode, or on an old iOS version are left out. A gray line shows how many are hidden, and `/doctor` explains why.
  - A group containing unavailable devices skips them with a warning.
- **An iPhone or iPad locked with a passcode is marked as `locked`** from the first listing. It is not launched and is checked again every 30 s.
- **Automatic iPhone setup no longer writes to the screen**, so it does not cover the main menu.
- **Short, consistent window titles,** for example `DEV-001 · Android USB · 60 fps`, `name · iOS Wi‑Fi · 43 fps`, and `· master` in `/sync`. This applies to DeviceShell's own windows, scrcpy windows, and sync windows.
- **`/devices` shows `● USB` or `● Wi‑Fi`** instead of "connected".

#### Fixed

- **Technical messages no longer leak into the interface.** FFmpeg is loaded silently, and anything that would reach the terminal while the interface is open goes to `output/logs/tui.log`.
- **Android devices showed up as unauthorized** when the ADB server was started without the CXT key. The server is now always started with that key.

### 2026-10-31

#### Added

- **Native iPhone engine in Go**, without Python:
  - CoreDevice tunnel over a gVisor stack.
  - RemoteXPC, taps and keyboard over UniversalHID, and buttons.
  - DisplayService session with RTCP.
  - Apps, URLs, screenshots, and developer disk image mounting.
  - Written from captures of real traffic, documented in `docs/iphone-protocol.md`, and released under the MIT license.
  - The tunnel and services open in about 50–90 ms, down from 4.2 s.
- **go-ios is used as a library** instead of `ios.exe`.
- **QuickTime capture and its WinUSB installer, in Go.** The driver is signed with a local certificate whose private key is not stored.
- **Native ADB client:** frequent commands talk directly to the ADB server instead of starting `adb.exe` each time.
- **Built-in Android viewer:**
  - Android windows use the same window and FFmpeg decoder as the iPhone ones, with scrcpy-server 5.0.1.
  - Up to 60 fps.
  - Mouse wheel with real scrolling, right-click or Esc for BACK, Ctrl+H for HOME, and paste with Ctrl+V.
  - `"androidView": "scrcpy"` switches back to scrcpy windows.

#### Changed

- **The iPhone sets itself up** when connected with Developer Mode on. Setup runs once per connection, is retried every 30 s if the phone is locked or busy, and its progress shows in the history.
- **iPhone video is decoded in Go with scrcpy's FFmpeg,** without cgo. Every frame reaches the window, about 55 fps, compared with about 42 with Python.
- **The iPhone starts faster:**
  - Closing DeviceShell with the window's close button shuts down QuickTime capture.
  - The fixed 2 s wait was removed.
  - The USB mode switch is checked every 100 ms.
- **Android in windows at 60 fps** (30 fps in view-only mode), with sharper video.

#### Fixed

- **The iPhone window took up a different size than the Android ones in the grid.** The picture area now fits its cell exactly.
- **iPhone video sessions are closed with `stopAll`.** Previously they piled up and blocked the service until the phone was restarted.

#### Removed

- **The `/ios` and `ios setup` commands**: setup is automatic.
- **The Python iPhone bridge** (`bridge.py`, pymobiledevice3 and its 257–291 MB environment), `ios.exe`, and the iPhone PowerShell scripts.

### 2026-10-30

#### Added

- **iPhone control without an Apple Developer account,** using the developer disk image that Apple signs for free for each device. The first version used a Python bridge, which was replaced by Go on October 31.
- **QuickTime USB capture** for iPhone video: about 58 fps at 1320×2868, without DisplayService's resolution drops. It requires the WinUSB driver on the capture interface.
- **`/resolution`**: shows the resolution and density of each device.
- **The → arrow key accepts the gray suggestion**, like Tab.

#### Changed

- **Only iPhones and iPads with iOS 27 or later are supported,** the first version that delivers live video and taps. Older ones are hidden, and when named explicitly they are rejected with a clear message.
- **An iPhone without Developer Mode does not appear** in `devices` or in `@all`. When named explicitly, the action explains what is missing.
- **`/view` and `/sync` launch all windows at once:**
  - Video quality depends on each window's size: about 1 Mbit/s for small ones and up to 8 Mbit/s for the master.
  - The screen's aspect ratio is saved for later launches.
- **The window block works from the start,** and a window that closes or fails reopens in its slot. Previously windows moved independently while loading and then stayed fixed.
- **`/sync` matches the resolution and density** of Android followers to the master while mirroring, so taps land on the same button, and restores them afterward.

#### Removed

- **The WebDriverAgent path:** Apple Developer signing, Sideloadly, and the `/fleet` command with its coordinator and agents were added on October 29 and removed when native control without an account arrived.

#### Fixed

- **iPhone video got stuck after many restarts in a row.** Retries are now spaced out (5 s, 10 s, 20 s… up to 60 s), and video is stopped before DeviceShell exits.

### 2026-10-29

#### Added

- **Initial portable installation,** with ADB and scrcpy verified by SHA-256:
  - Supports any number of devices.
  - Headless devices from the CXT enclosure are authorized with the factory key (`ADB_VENDOR_KEYS`), without touching the PC's own keys.
- **Go CLI built with Cobra** (`deviceshell.exe`):
  - Inventory with fixed aliases per serial number, and groups.
  - Batch actions with concurrency, continuing past failures, and `--json` output.
  - JSON routines with parameters.
  - Live mirroring (`sync`) from a master device to its followers.
  - Screens in a grid (`view`).
  - The portable toolchain is kept inside the project and dependencies are vendored with `go mod vendor`.
- **Interactive terminal interface (TUI) built with Bubble Tea**, opened by running `deviceshell.exe` without arguments:
  - `/` commands with autocompletion and a menu of ready-made commands.
  - Targets with `@`, shell commands with `!`, and batches with progress.
  - `/devices`, `/jobs`, and `/sync` panels.
  - Fixed input line and status bar, text selection, copy and paste, and `/copy`.
- **Step-by-step wizard:** after you pick a command, it asks for whatever is missing, with suggestions, validation, and the target last.
- **Screens in a grid (`/view`):**
  - All windows have the same size, calculated to make the most of the screen, and move as a block.
  - Each one is controlled individually; with `--read-only` they are view-only.
  - The area is configurable (`windowArea`).
- **Languages:** Spanish and English. Commands are in English, and the old Spanish words still work.
- **`/settings`:** language, execution (concurrent devices, timeout, session target), windows, CXT key, and alerts, in a single panel.
- **`/reset` and `deviceshell reset`:** permanently delete usage data after you type `BORRAR`, keeping settings, routines, and keys.
- **Data retention policy:** a history of 100 commands, and summarized reports kept for 7 days.
- **Device health:**
  - Battery, temperature, storage, memory, IP address, and screen.
  - Configurable temperature and battery alerts, in `/devices`, in the status bar, and in `doctor`.
- **New actions:**
  - `/uninstall`, `/wipe`, `/pull`, `/reboot`, `/wifi`, `/rotation`, `/android-setting`, `/perm`, `/logcat`, and `/record`.
  - Split APKs.
  - More keys.
- **More robust routines:**
  - `retry` with increasing backoff, `onError: "continue"`, and a per-step `timeout`.
  - Per-device variables: `${alias}`, `${serial}`, `${model}`, and `${index}`.
  - Platform restriction (`android`, `ios`, or both), checked before the first step.
- **Mixed Android/iPhone groups** with the same commands for both platforms.
- **Home screen:**
  - ASCII block logo with a gradient and a flash on click.
  - Summary of devices, groups, and routines.
  - "To get started" with `/devices`, `/view`, `/sync`, and `/help`.
- **Contextual gray suggestion** when the input is empty, accepted with Tab.
- **The mouse wheel scrolls lists** one item per step, counting events according to the Windows "lines per notch" setting.
- **Numeric fields are edited with the arrow keys:** ↑↓ ±1 and ←→ ±10.

#### Changed

- **Professional monochrome look,** with color only for states: green for connected, amber for unauthorized or locked, red for offline.
- **Device lists put connected devices first** and disconnected ones last.
- **No device limit:** the summary counts connected devices.
- **The prompt is `>`.**
- **`/jobs [ID]`** combines the list and the details (`/job` remains as an alias).
- **Code split** into `internal/{core,cli,tui}`, with translations kept separately.
- **Project reorganized** into `app/`, `datos/`, `rutinas/`, `herramientas/`, `desarrollo/`, `salidas/`, and `docs/`, freeing 368 MB.
- **Commands run as soon as you press Enter.** Only `/reset` asks you to type `BORRAR`.

#### Fixed

- **`/url` adds `https://`** when it is missing. Previously, without a scheme, Android found no app to open it.
- **Text pasted through the terminal** (for example, with a middle click in Warp) no longer runs each line as a command.
- **`/clear` keeps the home screen,** even with screens or mirroring open.
- **The first character typed is no longer hidden** after a long command.
- **Three devices** with incomplete USB registration on Windows are now recognized by ADB.

#### Removed

- **Simulation** (the mode and `--dry-run`).
- **CONFIRM/AUTO modes.**
- **`/guide`.**
- **Expected device count** (`expectedDevices`).
- **`/language`, `/mode`, `/parallel`, `/timeout`, and `/target`,** replaced by `/settings`.
- **The initial `.cmd` launchers and PowerShell program.**

[Unreleased]: https://github.com/doplerer/DeviceShell/compare/v1.0.0...HEAD
[1.0.0]: https://github.com/doplerer/DeviceShell/releases/tag/v1.0.0
