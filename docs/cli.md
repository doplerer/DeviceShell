# DeviceShell CLI

Open the DeviceShell folder in PowerShell and run `./deviceshell.exe`. You do not need Go to run the compiled program. The code in `cmd/` and `internal/` is MIT-licensed; ADB and scrcpy keep their own licenses.

```powershell
./deviceshell.exe devices
./deviceshell.exe doctor --json
./deviceshell.exe group create tests --devices DEV-001,DEV-002
./deviceshell.exe group list
./deviceshell.exe key HOME --group tests
./deviceshell.exe apk install 'C:\path with spaces\app.apk' --group tests
./deviceshell.exe app list --devices DEV-001
./deviceshell.exe app start com.android.settings --group tests
./deviceshell.exe app stop com.android.settings --group tests
./deviceshell.exe url 'https://example.com/?a=1&b=2' --group tests
./deviceshell.exe push 'C:\path\file.txt' /sdcard/Download/file.txt --group tests
./deviceshell.exe text 'Hello, tomorrow 😊' --group tests
./deviceshell.exe tap 0.5 0.5 --group tests
./deviceshell.exe swipe 0.5 0.8 0.5 0.2 400 --group tests
./deviceshell.exe screenshot --all
./deviceshell.exe shell 'getprop ro.product.model' --all
./deviceshell.exe routine run setup --group tests
./deviceshell.exe job show ID
./deviceshell.exe job retry ID
```

Maintenance and system actions:

```powershell
./deviceshell.exe apk install 'C:\apks\split-app\' --group tests    # folder with several .apk files
./deviceshell.exe apk uninstall com.example.app --group tests
./deviceshell.exe app clear com.android.chrome --group tests
./deviceshell.exe pull /sdcard/Download/ --devices DEV-001
./deviceshell.exe perm grant com.android.chrome android.permission.CAMERA --group tests
./deviceshell.exe setting global stay_on_while_plugged_in 7 --all
./deviceshell.exe wifi off --group tests
./deviceshell.exe rotation 90 --all
./deviceshell.exe resolution 1080x1920 --all
./deviceshell.exe reboot recovery --devices DEV-003
./deviceshell.exe logcat 500 --devices DEV-001
./deviceshell.exe screenrecord 10 --devices DEV-001
```

Every action needs exactly one selector: `--devices` (comma-separated aliases or serials), `--group` or `--all`. Groups store serial numbers, not USB positions. `--all` takes only the authorized devices that are connected when the command starts. There is no maximum number of devices.

By default four workers run in parallel (up to 64), and each step has a timeout of 180 seconds. A failure on one device does not stop the others. `Ctrl+C` cancels the pending steps; whatever has already run stays done. Reports are written to `output/logs/jobs/`. A batch with any failure exits with code 1.

`--json` gives structured results that a script or agent can consume. Diagnostics may update the local inventory. `doctor` includes `usb`: how many phones hang off each USB controller of the PC, the bandwidth that QuickTime video from their iPhones may use (10–44 Mbit/s each), and a warning from 12 phones on the same controller (xHCI controllers have limited resources; if a phone disappears or loses video, spread the phones across controllers). `notOnUsb` lists the phones that are not on a USB port. For each connected device it also includes `health` (battery level and whether it is charging, temperature, Android version, free storage and memory in MB, Wi‑Fi IP address, and whether the screen is on) and `alerts`, with whatever exceeds the limits set in the settings. A battery value of `-1` means the device does not report it. Reports keep the step arguments so that steps can be retried: do not pass confidential text if you do not want it stored locally.

## Concepts

- **Device**: each Android phone connected over USB. It is identified by its serial number and gets an alias (`DEV-001`…) that is kept in `data/inventory.json`. Existing `BOT-NNN` aliases from older versions are renamed to `DEV-NNN` automatically on first start. States: *connected*, *unauthorized* (the device has not accepted this PC's ADB key), *offline* (not responding) and *disconnected*.
- **Group**: a named set of devices (`data/groups.json`). It stores serial numbers, so it does not depend on the USB port. Create one with `/group create NAME @devices`, change its devices with `/group set`, rename it with `/group rename NAME NEW_NAME` (or `deviceshell group rename NAME NEW_NAME`) and delete it with `/group delete`.
- **Target**: the devices each command goes to: all connected devices, a group, or a list. It is always explicit.
- **Action**: what is done on each device: screenshot or screen recording, key press, tap or swipe (0..1 coordinates), text, URL, installing or uninstalling an APK, starting, stopping or clearing the data of apps, permissions, copying files in both directions, Android settings, Wi‑Fi, orientation, reboot, system log and shell commands.
- **Health**: battery, temperature and state of each connected device. It is read when you run `/doctor` or `/devices`, and every minute while the interface is open; it is never stored in the inventory.
- **Batch**: a command run on several devices in parallel. If one fails, the others carry on; a report is left in `output/logs/jobs/`, and during the interactive session you can retry only the devices that failed.
- **Routine**: several steps saved in `routines/*.json`, with `${name}` parameters.
- **Live replica**: you control a master device from the PC and the others repeat the same input in real time.

## Interactive interface

Type `/help` for the list of commands and `/help COMMAND` for the usage and an example of each command. Typing `/` shows the full command list, which you can browse with ↑↓.

`./deviceshell.exe` with no arguments (or `./deviceshell.exe tui`, or double-clicking the executable) opens the interactive interface. Windows Terminal is recommended. Without an interactive terminal (for example, when output is redirected), it prints the help instead.

- **No need to know the syntax**: type `/`, pick a command with ↑↓ and press Enter. If something is missing, a **wizard** asks for each value in turn, with an explanation and suggestions (coordinates, keys, files, routines…), checks each answer immediately, and finishes by asking which devices to use (all, a group, or "choose several…" to tick them in the list; numbers pick a group). At the end it shows the complete command, which is a good way to learn the short form. Backspace on an empty box goes back one step and Esc cancels. When you type a command by hand, the line above the input box shows its form with the current argument highlighted and what it means, and once the arguments are complete the menu offers the devices without having to type `@`. For numbers, coordinates and text, what you type wins (a decimal comma is accepted). For integers and durations, here and when editing in `/settings`, ↑↓ add or subtract 1 (1 s for durations) and ←→ subtract or add 10, never going below 0 or outside the value's limits; with an empty box they start from the default value or the first suggestion. For coordinates and text, suggestions are used only if you select them with the arrow keys.
- **`/` menu with ready-made actions**: `/` alone shows only the commands; typing one (`/view`, `/motion key`, or a search such as `/capture`) expands its ready-made commands below it, each with an explanation (`/view @all`, `/view @all --read-only`, `/group create`, `/motion key HOME`, `/motion key POWER` power, `/motion swipe 0.5 0.8 0.5 0.2` swipe up, one per routine, `!dumpsys battery`…). You can search by what you want to do, ignoring accents and case: `/capture`, `/install`, `/drag`, `/replicate`, `/quit`… This also works in arguments: `chrome` in `/start` finds `com.android.chrome`; after the arguments, the command's `--…` options are offered and, as a target, "the ones that failed in the last batch".
- **Contextual recommendation**: when the input is empty, a single command appears in gray with "Tab or → to accept", based on the current state (diagnostics, retrying failed devices, screens, replica or settings). Tab or the right arrow copies it into the box so you can edit it; Enter then sends it. Enter on an empty box does not accept it. It disappears as you type; Esc hides it until the recommendation changes or you send another command. It is hidden during tasks, forms, panels and confirmations. It is not added to the history until you send it.
- **Commands**: type `/` to see the list with autocompletion (Tab or Enter). The slash is optional, and `/help COMMAND` shows the usage. There are commands for the inventory (`/devices`, `/doctor`, `/rename`, `/group`), actions (`/motion`, `/apk`, `/app`, `/apps`, `/start`, `/stop`, `/url`, `/push`, `/shell`, `/screenshot`, `/wait`, `/uninstall`, `/wipe`, `/pull`), system (`/reboot`, `/wifi`, `/rotation`, `/resolution`, `/android-setting`, `/perm`, `/logcat`, `/record`), routines and batches (`/routine`, `/jobs [ID]`, `/retry`), screens and replica (`/view`, `/sync`) and the session (`/settings`, `/copy`, `/clear`, `/reset`, `/exit`). `/app install FILE` and `/app uninstall ID` install and uninstall apps on Android or iPhone (APK or signed IPA).
- **Interacting with the phone**: `/motion` lets you pick `tap`, `swipe`, `key` or `text` and fill in the values with the wizard. You can also type `/motion tap 0.5 0.5 @all`, `/motion swipe 0.5 0.8 0.5 0.2 300 @all`, `/motion key HOME @all` or `/motion text Hello @DEV-001`. `/help motion` lists the actions and `/help motion tap` explains one. The old `/tap`, `/swipe`, `/key` and `/text` commands have been retired: they show the new form without running the command. This change only affects the interactive interface; the external CLI and the JSON steps of routines keep their names.
- **Maintenance and system**: `/uninstall` and `/wipe` (clears an app's data, session and cache) cannot be undone and carry ⚠ in their help, as does `/reboot`. `/pull` saves what it fetches from each device in `output/downloads/ALIAS/`; `/logcat [LINES]` (500 by default) saves to `output/logcat/`, and `/record SECONDS` (1–180) saves the video to `output/screenshots/`: the step timeout must be longer than the recording. `/perm grant|revoke PACKAGE PERMISSION` changes permissions without touching the screen; `/android-setting system|secure|global KEY VALUE` is equivalent to `settings put` and suggests common settings (stay awake while charging, brightness, animations); `/wifi on|off` and `/rotation auto|0|90|180|270` set connectivity and orientation. `/resolution` shows the resolution and density of each device; `/resolution WIDTHxHEIGHT [DPI]` sets it permanently (for example, `/resolution 1080x1920 @all` to make them all the same) and `/resolution reset` returns to the physical resolution without changing the density. Keys also accept `POWER`, `VOLUME_UP`, `VOLUME_DOWN`, `MENU`, `TAB`, `DEL` and `CAMERA`.
- **Health and alerts**: `/devices` shows the battery (`⚡` when charging) and temperature of each connected device; a value outside its limit is marked with `!`. The status bar counts the devices with alerts (`! 2 alerts`), `/doctor` lists them in detail, and the contextual recommendation suggests it. The limits are in `/settings` › Alerts.
- **Target**: `@tests`, `@DEV-001,DEV-002` or `@all` on the command line itself (with autocompletion and the state of each device). `/settings` sets a target for the whole session, shown in the status bar. As in the CLI, there is never an implicit target.
- **Quick shell**: `!getprop ro.product.model` is the same as `/shell getprop ro.product.model` on the current target.
- **Execution**: commands run when you press Enter, without asking for confirmation; if any device in the target is offline, you are warned before it starts. The interface uses a fixed black background with white and gray text; states are told apart by symbols and labels.
- **Batches**: while a batch runs you see the progress of each device (`↻1` if it is repeating a step); `Esc` cancels the pending steps, also while waiting between retries. When it finishes, a summary remains with ✓/✗, the duration, `↻N` retries, `!N` failed steps that were skipped, and the batch ID; `/retry` retries the failed devices of the last batch. `/jobs` opens the recent batches (Enter shows the details, `r` retries the failed devices) and `/jobs ID` shows the details of one directly. `/job` (and the Spanish `/lotes`) are aliases of `/jobs` and accept the same optional ID; `/help job` shows the help for `/jobs [ID]`. Long output (`/apps`, `/shell`) is truncated: `Ctrl+O` shows the full output of the last result.
- **Device picker** (`/devices`): ↑↓ to move, Space to mark, `a` to mark all connected devices, Enter to set them as the target, `g` to create a group with them, `r` to rename, `s` to take screenshots, `v` to view their screens and `p` to start a replica.
- **Viewing screens**: `/view @all` (or `@group`, or `v` from `/devices`) opens one window per device, all the same size and tiled edge to edge in a grid that fills the monitor. The windows open together and form a block from the moment they appear: dragging one moves them all, minimizing one minimizes them all, and bringing one to the front raises them together. Each window controls its device (click, swipe, mouse wheel, keyboard); `/view @all --read-only` opens them for watching only. If a window fails, hangs before appearing, or is closed (including with its X button), it reopens by itself in its slot of the block, waiting 1 to 15 s if it fails again; a disconnected phone reappears when you reconnect it. `/view close` closes them all. DeviceShell draws the Android windows itself, just like the iPhone ones: up to 60 fps (30 with `--read-only`), with the picture sent at twice the window size and scaled down on the PC. Click and drag act as your finger, the wheel scrolls where the pointer is, right-click or Esc is BACK, `Ctrl+H` is HOME and `Ctrl+V` pastes the clipboard; the title shows only the name, the connection and the received fps (for example, `DEV-001 · Android USB · 60 fps`). To go back to scrcpy windows, add `"androidView": "scrcpy"` to `config.json`. Each window writes its log to `output/logs/android-video-<serial>.log` (startup, `first frame after … ms`, and statistics every 5 s). All windows run inside the DeviceShell process itself, without one process per window. A minimized window costs nothing: if no window is showing an Android phone (all minimized or closed), its video stops being decoded after 2 s and comes back with a fresh picture when you restore it. If an Android phone stops sending video for 3 s, its connection restarts by itself (`watchdog` in the log). At most 16 video servers start at the same time, and the scrcpy server is copied to the phone only once (`/data/local/tmp/deviceshell-scrcpy-5.0.1.jar`). While windows are open, `output/logs/performance.log` records CPU, RAM, windows and fps every 30 s.
- **Replica**: `/sync start DEV-001 DEV-002 DEV-003` (the first one is the master) or `/sync start DEV-001 @tests`, or from `/devices` by marking the followers and pressing `p` on the master. The windows are placed in the same grid and block as `/view`, with the master at the top left and its title marked `master` (for example, `DEV-001 · Android USB · master`); the followers have the same short title as in `/view`. The control connections to the followers open in parallel and reconnect by themselves if they drop; crashed or closed windows, including the master's, reopen without stopping the replica. `/sync` opens its panel (`p` pauses or resumes, `s` stops), and `/sync pause|resume|stop|status` also controls, by ID, sessions opened from another terminal. The replica ends with `/sync stop` (or `s` in its panel).
- **Screen**: the interface fills the whole window; the input box and the status bar always stay on the bottom rows, and suggestions open upward. Results scroll with the mouse wheel, `PgUp/PgDn` and `Home/End` (with an empty line; `Ctrl+Home/End` always). If results arrive while you are reading further up, the status bar shows `↓ new`. Because the interface uses the mouse wheel, select text in Windows Terminal with **Shift + drag**. When you exit, the session history is printed to the terminal.
- **Editing, copying and pasting** on the command line works as in any editor: `Shift+←/→` selects, `Ctrl+←/→` jumps by words (with `Shift` it selects words), `Shift+Home/End` selects to the beginning or end, `Ctrl+A` selects everything, `Ctrl+C` copies the selection (with no selection it keeps its usual role of clearing the line and exiting), `Ctrl+X` cuts, `Ctrl+V` pastes (a multi-line paste is joined into one line), and typing or deleting replaces the selection. `Ctrl+W` deletes the previous word, `Ctrl+U` deletes to the beginning and `Ctrl+K` to the end. To copy results, `/copy` copies the last one as text, or select on screen with **Shift + drag** and copy with `Ctrl+C`.
- **Shortcuts**: `?` on an empty line shows them. ↑↓ browse the command history (saved in `data/tui-history.txt`), `Ctrl+L` clears the results from the screen, and pressing `Ctrl+C` twice exits; if a batch or a replica is running, it closes them first.

The interface polls `adb devices` every few seconds for the status bar, without writing the inventory. Group names cannot contain spaces, commas, `@` or slashes.

## Settings

`/settings` opens a panel with the General, Execution, Screens, Connection and Alerts sections. Use the arrow keys to move, Enter to edit and confirm, and Esc to cancel editing or close the panel. Each confirmed value is saved immediately.

The saved settings are the language, Concurrency (1–64 devices per batch), the timeout per step and device (for example, 90s), the window area, the vendor ADB key (CXT key), the cable video mode for iPhone, and the alert limits: maximum temperature (`alertTemp`, 42 °C by default, up to 90) and minimum battery (`alertBattery`, 15% by default). Setting a limit to 0 turns its alert off. The target lasts only for the session: every start begins with no target set.

The custom window area asks for X, Y, width and height; the origin can be negative and the dimensions must be positive. It applies to windows opened afterward. Changes to the CXT key take effect after restarting DeviceShell and do not restart the ADB server. Batches that have already started keep their parameters.

The CLI uses the preferences saved in `config.json` (`parallel`: 4 and `timeout`: "180s" by default). There are no separate configuration commands and no `language`, `--parallel` or `--timeout` options: change these values in `/settings` or in `config.json`. New installations start in English (`"language": "en"`); Spanish (`es`) is also available.

Two environment variables are read at startup: `DEVICESHELL_LANG` takes priority over the saved language, and `DEVICESHELL_ROOT` points to the installation folder (the one with `config.json`) when you run from source. The older `BOTFARM_LANG` and `BOTFARM_ROOT` names are still accepted.

Older `config.json` keys are migrated automatically: `areaVentanas` becomes `windowArea`, and `"monitores": "todos"` becomes `"monitors": "all"`.

### Headless boards with a vendor ADB key

Some headless phone boards (for example, CXT boxes) ship with a vendor ADB key instead of asking for authorization on screen. To use them, set `"useCxtKey": true` in `config.json` (or enable the CXT key in `/settings`) and put the vendor key at `tools/cxt-auth/adbkey`. DeviceShell adds it to the ADB server through the `ADB_VENDOR_KEYS` environment variable.

## Routines

Save files in `routines/` with the structure below. Actions use the same internal names: `apk-install`, `apk-uninstall`, `app-list`, `app-start`, `app-stop`, `app-clear`, `url`, `text`, `key`, `push`, `pull`, `shell`, `screenshot`, `screenrecord`, `logcat`, `wait`, `tap`, `swipe`, `reboot`, `perm-grant`, `perm-revoke`, `setting`, `wifi`, `rotation`, `resolution` (args: `WIDTHxHEIGHT|reset|show` and `DPI|keep|reset`). A `setup` routine is included (`routines/setup.json`).

```json
{"steps":[
  {"action":"app-start","args":["com.android.settings"]},
  {"action":"wait","args":["500"]},
  {"action":"tap","X":0.5,"Y":0.9,"retry":2,"onError":"continue"},
  {"action":"push","args":["C:\\data\\${alias}.txt","/sdcard/Download/${alias}.txt"],"timeout":"30s"},
  {"action":"screenshot","args":[]}
]}
```

`wait` takes milliseconds. `tap` uses `X` and `Y` between 0 and 1. `swipe` adds `X2`, `Y2` and `duration` in milliseconds. Arguments accept `${name}` parameters: `routine run example 'name=value with spaces'`. Missing parameters, unknown actions, and invalid coordinates or values are rejected before the batch starts.

Each step also accepts an error policy:

- `retry`: number of retries if it fails (0–10). Between attempts it waits 1 s, 2 s, 4 s… up to 30 s.
- `onError`: `abort` (the default) stops that device; `continue` records the failure as a warning (`! step N (action): error` in the output) and moves on to the next step, and the device counts as successful.
- `timeout`: the step's own timeout (`30s`, `2m`), instead of the one in `/settings`.

Built-in variables: `${alias}`, `${serial}`, `${model}` and `${index}` (the device's position in the batch, starting at 1) are filled in by each device when the step runs, also in one-off commands (`/motion text Hello ${alias} @all`). They are not asked for as parameters and cannot be used as a parameter name. A `push` or `apk-install` whose local path contains one of them is checked on each device when it runs.

## Live replica

```powershell
./deviceshell.exe sync start --master DEV-001 --group tests --views all
# From another terminal, using the ID shown:
./deviceshell.exe sync status ID
./deviceshell.exe sync pause ID
./deviceshell.exe sync resume ID
./deviceshell.exe sync stop ID
```

**How it works, in plain terms**: you choose a master device and several followers. One window opens per device; whatever you do with the mouse and keyboard in the master's window is sent at the same instant to all the followers, at the equivalent position on their screens. So that each tap lands on the same button, Android followers with a different resolution or density take the master's values for as long as the replica lasts (if their screen has a different shape you will see black bars) and go back to their own when it stops; if the program closes abruptly, they are restored the next time it opens or when another replica starts. Gestures are repeated, not app content: put all devices on the same starting screen and in the same orientation before you begin.

All windows are the same size and are tiled together in a grid in the work area of the primary monitor (the master first). With `"monitors": "all"` in `config.json`, the grid is spread across all monitors: the primary monitor fills first and then the others, from left to right, with the largest windows that fit; the windows on each monitor form their own block. To use a different area or leave room for the terminal, add `"windowArea": [x, y, width, height]` in pixels to `config.json`; it takes priority over `monitors` and also applies to `view`. The windows move **as a block**, both in the replica and in `/view`: if you drag one by its title bar they all move together; they cannot be separated or resized (they snap back into place), minimizing or restoring one minimizes or restores them all, and bringing one to the front raises them all. If one closes or fails, it reopens by itself in its slot of the block.

Without replicating: `./deviceshell.exe view --all` (or `--group`, `--devices`) opens the screens in a grid, each one controllable, until you press Ctrl+C; with `--read-only` they are for watching only.

Work in the master's window. Followers are view-only; if a follower's window is closed, it reopens and stays in the replica. Closing the master window also reopens it: the session ends with Ctrl+C or `sync stop`. `--views master` shows only the master and reduces resource use. Master: a scrcpy window at 1024 px / 60 fps; Android followers: DeviceShell windows at 30 fps (with `"androidView": "scrcpy"`, scrcpy windows at 480 px / 30 fps). `sync start` also accepts `--master-size`, `--master-fps`, `--follower-size` and `--follower-fps` to change those values. There is no audio. Explicitly pasted text is replicated; automatic clipboard synchronization is disabled.

The session takes a fixed selection: it does not add new devices or replay missed input when a device reconnects. The devices must show equivalent interfaces: coordinates are adjusted, but app content is not interpreted. Different rotations or screens can produce different results. Pause before changing orientation or rearranging the screens.

The implementation uses the scrcpy **5.0.1** protocol. Do not upgrade its binaries separately without adapting and testing the relay. Each session's control port listens only on 127.0.0.1, uses a temporary local credential, and is removed when the session ends. Closing the master, `sync stop` or `Ctrl+C` ends the session and its processes. Temporary metadata is kept in `output/logs/sessions/` and the final report in `output/logs/sync/ID.json`.

The ADB server is not restarted for each action. If `unauthorized` devices reappear because another application started the ADB server without the vendor key, stop that server (`adb kill-server` with the bundled `adb.exe` under `tools/`) and run DeviceShell again, so that the server it starts picks up the key.

## Data and reset

`./deviceshell.exe reset` and `/reset` (also `/borrar-datos`) show what will be deleted and ask you to type `BORRAR` (the confirmation word is the same in both languages). Without a terminal, use `./deviceshell.exe reset --yes --json` to confirm explicitly and get a structured result. `files` and `bytes` give the expected scope; `failures` reports what could not be deleted, and the process exits with code 1 if anything fails.

`data/` and `output/` are deleted directly, including screenshots and recordings, together with the legacy `docs/historial/` archive folder if it exists. Nothing goes to the Recycle Bin and no backups are made. `config.json`, `routines/`, the tools and the vendor (CXT) keys are kept. The command does not erase anything on the Android devices, `.android`, or external copies, and it does not guarantee forensic unrecoverability. Connected devices will reappear the next time they are listed, with a new inventory.

Close other instances, maintenance tasks, batches, screens and replicas before deleting. The interface closes when it finishes and discards its transcript. If some files are locked, the failure is reported; you can run the command again once they are released. Links and reparse points are rejected before anything is deleted.

Usual retention is as follows:

- Inventory, aliases, groups, configuration and routines: until you change or explicitly delete them. `reset` keeps the configuration and routines.
- Screenshots, recordings, files fetched with `pull` (`output/downloads/`) and `logcat` logs (`output/logcat/`): until explicitly deleted; `reset` includes them.
- Persistent history: the last 100 safe commands without arguments. Text, URLs, shell commands, routine parameters and unknown entries are not saved. The full recent history is kept only for the session.
- Reports: summaries from the last 7 days, at most 100 per category. They include actions, devices, status, duration and the number of retries and warnings; not arguments, output or original error messages.
- Process diagnostics: a tail of up to 64 KiB per process, in memory only. Replica credentials are kept while the session is active and removed when it ends or when an abandoned session is detected.

The details and steps of the last 100 batches of the interactive session stay in memory for `/jobs ID` and `/retry`. After a restart, `/jobs`, `/jobs ID` and `job show` show summaries; to repeat an action you have to enter the original command again. `job retry` cannot recover steps from a closed session. Cleanup runs at startup when no other instance is running, and after reports are saved.

When upgrading from an older version, the first start sanitizes the existing history, converts old reports into summaries and deletes the detailed logs. Unreadable reports are deleted. This migration makes no copies and leaves the inventory, groups, settings, routines and screenshots untouched; the legacy `docs/historial/` folder is only removed by `reset`.

## Building and testing

Run `scripts/build.ps1` with PowerShell and `-ExecutionPolicy Bypass` for that process. On the first run it downloads the Go toolchain pinned in `scripts/toolchain.json` into `.toolchain/` and verifies its SHA-256; regenerable caches go in `.cache/`. Dependencies are vendored in `vendor/`, so no Go modules are downloaded during the build. The automated tests do not run real actions on the phones.

If an abrupt shutdown leaves `data/.inventory.lock` behind, make sure no diagnostics are running and delete that empty directory before trying again.

The architecture and how to install the executable are described in [Development](development.md).

## iPhone support and per-platform routines

See [the iPhone guide](iphone.md) for iPhone and iPad without an Apple account: they are prepared automatically when connected with Developer Mode turned on. The `platform` field of routines accepts `android`, `ios` and `both`; if it is missing, it means `android`. The whole routine is checked before the first step runs on each device.
