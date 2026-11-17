# DeviceShell development

## Architecture

The Go module (`github.com/doplerer/deviceshell`) lives at the repository root. The entry point, `cmd/deviceshell`, builds one service and explicitly shares it with the CLI and the interactive interface.

- `internal/core`: the core. Inventory, groups, actions, batches, routines, settings, persistence, the installation layout (`paths.go`, with the migration of older names in `layout_migration.go`), and the screen and sync processes. For Android, DeviceShell's own windows: H.264 video and control through scrcpy 5.0.1 (`android_video.go`), the shared scrcpy server (`scrcpy_server.go`), and the session behind each window (`android_view.go`). For the iPhone, the engine (`ios_native.go`), video (`ios_video.go`), and automatic setup (`ios_auto.go`, `ios_driver.go`). Both platforms share the FFmpeg decoder (`video_decoder_windows.go`), frame distribution (`frame_hub.go`), the link between a window and its device (`view_link.go`), and the native window (`window_windows.go`), which runs inside the DeviceShell process itself. Large-scale performance: `perf.go` (CPU, RAM, and `output/logs/performance.log`), `bench*.go` (benchmark), `startup.go` (concurrent start limit and watchdogs), `ios_inventory.go` (iPhone inventory cache), `monitors.go` (multiple monitors), and `usb.go` (USB controllers in `doctor`).
- `internal/android/adb`: the ADB server's smart-socket protocol, so frequent commands do not start `adb.exe`. When the server cannot serve a request it returns `ErrUnserved`, and `core` falls back to `adb.exe`.
- `internal/android/scrcpy`: the scrcpy-server 5.0.1 wire format: video socket headers and control messages (taps, scrolling, keys, text).
- `internal/ios/coredevice`: the iPhone protocol, with no dependency on `core`: the CoreDevice tunnel over a gVisor stack (TCP and UDP), RemoteXPC, taps, keyboard, buttons, the DisplayService session with RTCP, developer disk image mounting, and go-ios services. It is written from [iPhone protocol](iphone-protocol.md); its tests compare what it sends against the captures in `testdata/`.
- `internal/ios/remotepairing`: Wi-Fi pairing and tunneling (SRP, TLV, mDNS) with no app on the phone.
- `internal/ios/quicktime`: USB screen capture over WinUSB using the QuickTime protocol.
- `internal/ios/qtdriver`: installs the WinUSB driver for the capture interface (a catalog signed with a local certificate; the part that needs administrator rights runs as `deviceshell.exe --internal-install-qt-driver`).
- `internal/filelock`, `internal/procstat`, `internal/usb`: cross-process file locks, per-process CPU and memory, and USB controller enumeration, each with a per-OS implementation.
- `internal/cli`: Cobra commands, target selection from arguments, and result output.
- `internal/tui`: the Bubble Tea model, editor, forms, panels, commands, and suggestions. The screens adapter lets tests exercise its lifecycle without real processes.
- `internal/i18n`: texts and language normalization. The persisted language setting belongs to the service.

The CLI and the interface depend on the service; the service imports neither Cobra nor Bubble Tea. The packages under `internal/android`, `internal/ios`, and the system utilities do not depend on `core`. Video in DeviceShell's own windows (decoder, `frameHub`, native window) stays in `core` because it is tied to its types (`Spec`, `viewLink`). Each service keeps its own root, runtime settings, and dependencies. Each interface keeps its own command registry and presentation state.

Windows-specific implementations use the `_windows.go` suffix and have an `_other.go` counterpart. JSON types keep their existing fields and tags.

## Building and validating

From the repository root:

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\build.ps1
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\build.ps1 -ValidateOnly
```

`scripts/build.ps1` uses the Go toolchain pinned in `scripts/toolchain.json`. On the first run it downloads that archive from go.dev, checks its SHA-256, and extracts it into `.toolchain/`; later runs use `.toolchain/go/bin/go.exe` directly. It builds offline: `GOFLAGS=-mod=vendor` with the dependencies in `vendor/`, `GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`, and `CGO_ENABLED=0`, with the build and module caches in `.cache/`. It restores every environment variable it changes.

Before building, the script validates the code: `gofmt -l` on `cmd` and `internal`, `go vet ./...` for Windows and again with `GOOS=linux`, and `go test ./...`. With `-ValidateOnly` it stops there. Otherwise it builds `./cmd/deviceshell` with `-trimpath` into `.cache/build/deviceshell.exe` and installs it as `dist/deviceshell.exe`. Nothing is replaced if formatting, vet, tests, or the build fail.

- If `dist/` does not exist yet, the script creates a new installation there from `configs/config.example.json` and `configs/routines`, and reminds you to run `scripts/fetch-tools.ps1`.
- If DeviceShell (or an older `botfarm.exe`) is running from `dist/`, the script leaves the validated build in `.cache/build/deviceshell.exe` and warns you. Close DeviceShell and run the script again to install it.
- After installing, it removes a leftover `dist/botfarm.exe` from installations made before the rename.

`scripts/fetch-tools.ps1` downloads the external tools DeviceShell runs with: the official scrcpy v5.0.1 release for Windows (`scrcpy-win64-v5.0.1.zip`), which contains ADB, scrcpy-server, and the FFmpeg DLLs. It checks the archive against its pinned SHA-256, extracts it into `dist/tools/scrcpy-win64-v5.0.1/`, and writes `tools/install.json` (version, source, asset, SHA-256, and installation time), which DeviceShell reads to find the tools. Use `-Root <dir>` to install into another installation. If `adb.exe` is already in place, the script does nothing.

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\fetch-tools.ps1
```

With Go installed on the system, `go build -o .cache/build/deviceshell.exe ./cmd/deviceshell`, `go vet ./...`, and `go test ./...` also work; with `vendor/` present, Go builds without network access. Do not run `go build` without `-o` at the root: it would leave an executable next to the source code. Continuous integration (`.github/workflows/ci.yml`) runs the same checks on Windows. Record every change in [CHANGELOG.md](../CHANGELOG.md) under `[Unreleased]`.

## Tests and local data

Tests use temporary directories, simulated local connections, and replaceable runtime dependencies. They never act on real devices. They cover control protocols, cancellation, selection, inventory, settings, the interface, data retention, layout migration, and cross-process locks. They also check that two services and two interfaces keep their state separate.

`DEVICESHELL_ROOT` points DeviceShell at another root, for example to run a candidate build from source. That root must contain its own `config.json` and `tools/install.json`. For isolated tests, do not copy real keys or data. `DEVICESHELL_LANG` (`en` or `es`) sets the startup language. The older names `BOTFARM_ROOT` and `BOTFARM_LANG` are still accepted when the new ones are not set.

`data/`, `output/`, keys, settings, and lock files belong to day-to-day use and live in the installation (`dist/`). The empty screenshot and session folders are part of the layout and are recreated as needed.

### Opt-in tests with real devices

Some tests only run when you set their environment variables. Those that need ADB, scrcpy, or FFmpeg read them from the installation given by `DEVICESHELL_ROOT` (typically `dist/`), and most keep their output in `.cache/` (`.cache/bench`, `.cache/wifi-probe`); the table notes the exceptions. These variables have no `BOTFARM_` fallback.

| Variables | Test | What it does |
|---|---|---|
| `DEVICESHELL_ANDROID_SERIAL`, `DEVICESHELL_ROOT` | `TestAndroidVideoLive` (`internal/core`) | Streams a real Android device through the scrcpy server and FFmpeg, without input. |
| `DEVICESHELL_RECORD_ANDROID=<serial>`, `DEVICESHELL_ROOT` | `TestRecordAndroidStream` (`internal/core`) | Records about 20 s of real Android video into `.cache/bench/android.dsrec` for the benchmark. |
| `DEVICESHELL_RECORD_IPHONE=<UDID>`, `DEVICESHELL_ROOT` | `TestRecordIPhoneStream` (`internal/core`) | Records about 20 s of real iPhone QuickTime video into `.cache/bench/iphone.dsrec`. |
| `DEVICESHELL_COMPARE_UDID=<UDID>`, `DEVICESHELL_ROOT` | `TestCompareCableVideo` (`internal/core`) | With the phone on a cable, unlocked on its home screen, and DeviceShell closed, compares QuickTime and DisplayService video over USB and writes `docs/research/video-comparison/report.md`, plus PNG frames in the same folder. Do not commit those frames: they show the phone's screen. |
| `DEVICESHELL_RP_UDID=<UDID>`, optional `DEVICESHELL_RP_HOST=<ip>` | `TestRealPhone` (`internal/ios/remotepairing`) | Over USB, verifies (or creates) the pairing and reads the identifier; then finds the phone on the local network and opens its Wi-Fi tunnel. Saves the raw mDNS answer to `testdata/advert.bin`. |
| `DEVICESHELL_FRAMES=1`, optional `DEVICESHELL_CODEC=h264-`, `DEVICESHELL_THREADS=<n>` | `TestDisplayServiceFramesToPNG` (`internal/core`) | Decodes the DisplayService recordings `motion.rtp` and `still.rtp` from `.cache/wifi-probe` and saves large PNG frames there. |

`DEVICESHELL_TEST_SLEEP` and `DEVICESHELL_LOCK_TEST` are set by the tests themselves when they start helper processes; you never need to set them.

## Upgrading an existing installation

Each time DeviceShell starts (after it has found `config.json`), `MigrateLayout` in `internal/core/layout_migration.go` brings an older installation up to date. It is safe to run on every start: each step only acts when something old is still there.

1. **Old lock files.** It deletes `.botfarm.lock` and `.botfarm-store.lock` from the installation root. Windows refuses to delete a lock file another process holds open, so if this fails an older BOTFARM or DeviceShell instance is still running on that installation; DeviceShell stops and asks you to close it.
2. **Spanish folder and file names.** It renames them to the current layout, parents before children:

   | Old | Current |
   |---|---|
   | `datos/` | `data/` |
   | `data/inventario.json` | `data/inventory.json` |
   | `data/grupos.json` | `data/groups.json` |
   | `data/historial-tui.txt` | `data/tui-history.txt` |
   | `rutinas/` | `routines/` |
   | `salidas/` | `output/` |
   | `output/registros/` | `output/logs/` |
   | `output/logs/sesiones/` | `output/logs/sessions/` |
   | `output/capturas/` | `output/screenshots/` |
   | `output/descargas/` | `output/downloads/` |
   | `herramientas/` | `tools/` |
   | `tools/instalacion.json` | `tools/install.json` |
   | `tools/cxt-auth/autorizar.ps1` | `tools/cxt-auth/authorize.ps1` |
   | `tools/ios/setup/_controlador-quicktime.json` | `tools/ios/setup/_quicktime-driver.json` |

   A path is renamed only when the old name exists and the new one does not. If a rename fails (usually because another instance has files open), DeviceShell stops and asks you to close the other instances.
3. **Aliases.** In `data/inventory.json`, generated aliases `BOT-NNN` become `DEV-NNN` with the same number. Aliases you chose are kept, and so is a `BOT-NNN` whose `DEV-NNN` is already taken. Groups store serial numbers, so they need no change.
4. **Settings keys.** In `config.json`, `areaVentanas` becomes `windowArea` and `monitores` becomes `monitors`, with `"todos"` turned into `"all"`. If the English key is already present, its value wins and the Spanish key is removed.

**Recovery.** When both the old and the new name of a path exist, DeviceShell does not merge them, because that could lose data. It leaves both in place, prints a warning listing the old paths it kept, and carries on with the new ones. To resolve it, compare the two, move anything you need from the old path into the new one, and delete the old path; the warning disappears on the next start. An inventory or `config.json` that cannot be read is skipped by the migration and reported by the normal loading code instead.

## Performance and benchmark

Each window is a Win32 window inside the DeviceShell process, with its own thread (a goroutine locked in `GetMessage`, which costs nothing while it waits) and a render goroutine. The render goroutine takes the latest frame from the `frameHub`, scales it straight into the back buffer of the window's two DIBs, swaps them, and paints onto the window's own DC (`CS_OWNDC`). It does not go through `InvalidateRect`/`WM_PAINT`: with hundreds of windows at 60 fps, those window manager calls were the bottleneck (beyond roughly 9,600 paints per second everything stalled). `WM_PAINT` only repaints uncovered areas. A minimized window neither scales nor paints.

- **Zero copy.** The decoder lends its `AVFrame` (`av_frame_move_ref` into a pool of `AVFrame`s); whoever releases it last returns it with `av_frame_unref`. Only functions exported by avutil are used.
- **Scaler.** Each window caches its tables per source and destination size. When the picture is almost twice the window size (the usual case on Android), it takes exactly half and averages 2×2 blocks, producing the same bytes as the general path. Work is split across several goroutines only above 250,000 output pixels.
- **Go runtime.** When the first window opens, `GOGC` is set to 400 and the soft memory limit to 2 GiB; the `GOGC` and `GOMEMLIMIT` environment variables take precedence.
- **Window block.** It is moved by window events (`SetWinEventHook`: move, minimize, foreground) plus a safety pass every second, and the whole block moves at once with `DeferWindowPos`. scrcpy windows are still separate processes and are found by PID.
- **Logging.** While windows are open, `output/logs/performance.log` receives a line every 30 s: CPU (in cores and %), RAM (working set and private), windows, devices, decoded and painted fps, and milliseconds spent decoding, scaling, and painting per frame.

The hidden benchmark opens real windows on fake connectors, with no devices attached:

```powershell
.\dist\deviceshell.exe --internal-bench '{"android":200,"iphone":20,"seconds":30}'
```

JSON options:

| Key | Default | Meaning |
|---|---|---|
| `android`, `iphone` | 0 | Number of Android and iPhone windows (at least one in total). |
| `seconds` | 10 (minimum) | Measured duration. |
| `fps` | 60 (maximum 120) | Frame rate published to each window. |
| `readOnly` | `false` | View-only windows. |
| `allMonitors` | `false` | Spread the wall across every monitor. |
| `recordings` | none | Folder with `android.dsrec` and `iphone.dsrec`. |
| `profile` | none | File for a pprof CPU profile of the measured part. |
| `baseline` | `false` | Skip the Go runtime tuning, for comparison. |

It writes a line every 5 s and a final average to the console and to `output/logs/bench-*.log`; the first interval is discarded as warm-up. Without recordings it publishes synthetic pictures at each device's size (so decoding is not measured). To record 20 s of real video while you swipe on the device:

```powershell
$env:DEVICESHELL_ROOT = (Resolve-Path .\dist).Path
$env:DEVICESHELL_RECORD_ANDROID = '<serial>'     # or DEVICESHELL_RECORD_IPHONE = '<UDID>'
.toolchain/go/bin/go.exe test -mod=vendor ./internal/core -run 'TestRecord(Android|IPhone)Stream' -v
```

Recordings are stored in `.cache/bench/` (they are not part of the executable); pass `"recordings": ".cache/bench"` to use them. With synthetic pictures at 60 fps per window:

| Windows | Before (processes and HTTP) | Now |
|---|---|---|
| 50 Android + 5 iPhone | 3.7 cores, 1.4 GB (3.2 GB private) | 1.6 cores, 160 MB |
| 100 Android + 10 iPhone | 7.4 cores, 2.7 GB (6.3 GB private) | 3.2 cores, 170 MB |
| 200 Android + 20 iPhone | 17.6 cores, 5.0 GB (11.9 GB private) | 5.3 cores, 290 MB |

In the old design, fps counted the frames sent to each window. With 220 windows in the same process, painting through `WM_PAINT` topped out at 31 fps; painting directly reaches 60. With 20 iPhone windows at a small size, almost all of the remaining CPU goes to scaling 1320×2868 frames (every pixel is averaged so no sharpness is lost).

`.cache/go-build/` and `.cache/go-mod/` are caches and can be deleted at any time. `vendor/` is part of the repository; `.toolchain/` and `dist/tools/` are downloaded again by `scripts/build.ps1` and `scripts/fetch-tools.ps1`. Keep each of these distributions complete rather than removing individual files.
