# Third-party notices

DeviceShell is released under the [MIT License](LICENSE). It includes or depends on
third-party work, listed here with its license.

## Code derived from other projects

Parts of `internal/ios/remotepairing` (codec, framing, TLV, SRP, pair records and the
untrusted tunnel) and of `internal/ios/coredevice` come from
[go-ios](https://github.com/danielpaulus/go-ios). `internal/ios/quicktime` is based on
[quicktime_video_hack](https://github.com/danielpaulus/quicktime_video_hack). Both are
distributed under the following license:

```text
MIT License

Copyright (c) 2019 danielpaulus

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Go modules

The Go dependencies are vendored in `vendor/`, each with its own license file.

| Module | Version | License file |
|---|---|---|
| `github.com/Masterminds/semver` | v1.5.0 | `vendor/github.com/Masterminds/semver/LICENSE.txt` |
| `github.com/atotto/clipboard` | v0.1.4 | `vendor/github.com/atotto/clipboard/LICENSE` |
| `github.com/aymanbagabas/go-osc52/v2` | v2.0.1 | `vendor/github.com/aymanbagabas/go-osc52/v2/LICENSE` |
| `github.com/cenkalti/backoff` | v2.2.1+incompatible | `vendor/github.com/cenkalti/backoff/LICENSE` |
| `github.com/charmbracelet/bubbles` | v1.0.0 | `vendor/github.com/charmbracelet/bubbles/LICENSE` |
| `github.com/charmbracelet/bubbletea` | v1.3.10 | `vendor/github.com/charmbracelet/bubbletea/LICENSE` |
| `github.com/charmbracelet/colorprofile` | v0.4.1 | `vendor/github.com/charmbracelet/colorprofile/LICENSE` |
| `github.com/charmbracelet/lipgloss` | v1.1.0 | `vendor/github.com/charmbracelet/lipgloss/LICENSE` |
| `github.com/charmbracelet/x/ansi` | v0.11.6 | `vendor/github.com/charmbracelet/x/ansi/LICENSE` |
| `github.com/charmbracelet/x/cellbuf` | v0.0.15 | `vendor/github.com/charmbracelet/x/cellbuf/LICENSE` |
| `github.com/charmbracelet/x/term` | v0.2.2 | `vendor/github.com/charmbracelet/x/term/LICENSE` |
| `github.com/clipperhouse/displaywidth` | v0.9.0 | `vendor/github.com/clipperhouse/displaywidth/LICENSE` |
| `github.com/clipperhouse/stringish` | v0.1.1 | `vendor/github.com/clipperhouse/stringish/LICENSE` |
| `github.com/clipperhouse/uax29/v2` | v2.5.0 | `vendor/github.com/clipperhouse/uax29/v2/LICENSE` |
| `github.com/danielpaulus/go-ios` | v1.3.2 | `vendor/github.com/danielpaulus/go-ios/LICENSE` |
| `github.com/erikgeiser/coninput` | v0.0.0-20211004153227-1c3628e74d0f | `vendor/github.com/erikgeiser/coninput/LICENSE` |
| `github.com/google/btree` | v1.1.2 | `vendor/github.com/google/btree/LICENSE` |
| `github.com/google/uuid` | v1.1.2 | `vendor/github.com/google/uuid/LICENSE` |
| `github.com/grandcat/zeroconf` | v1.0.0 | `vendor/github.com/grandcat/zeroconf/LICENSE` |
| `github.com/inconshreveable/mousetrap` | v1.1.0 | `vendor/github.com/inconshreveable/mousetrap/LICENSE` |
| `github.com/kr/text` | v0.2.0 | required by go.mod, no package is built |
| `github.com/lucasb-eyer/go-colorful` | v1.3.0 | `vendor/github.com/lucasb-eyer/go-colorful/LICENSE` |
| `github.com/mattn/go-isatty` | v0.0.20 | `vendor/github.com/mattn/go-isatty/LICENSE` |
| `github.com/mattn/go-localereader` | v0.0.1 | no license file upstream |
| `github.com/mattn/go-runewidth` | v0.0.19 | `vendor/github.com/mattn/go-runewidth/LICENSE` |
| `github.com/miekg/dns` | v1.1.57 | `vendor/github.com/miekg/dns/LICENSE` |
| `github.com/muesli/ansi` | v0.0.0-20230316100256-276c6243b2f6 | `vendor/github.com/muesli/ansi/LICENSE` |
| `github.com/muesli/cancelreader` | v0.2.2 | `vendor/github.com/muesli/cancelreader/LICENSE` |
| `github.com/muesli/termenv` | v0.16.0 | `vendor/github.com/muesli/termenv/LICENSE` |
| `github.com/pierrec/lz4` | v2.6.1+incompatible | `vendor/github.com/pierrec/lz4/LICENSE` |
| `github.com/quic-go/quic-go` | v0.59.1 | `vendor/github.com/quic-go/quic-go/LICENSE` |
| `github.com/rivo/uniseg` | v0.4.7 | `vendor/github.com/rivo/uniseg/LICENSE.txt` |
| `github.com/songgao/water` | v0.0.0-20200317203138-2b4b6d7c09d8 | `vendor/github.com/songgao/water/LICENSE` |
| `github.com/spf13/cobra` | v1.9.1 | `vendor/github.com/spf13/cobra/LICENSE.txt` |
| `github.com/spf13/pflag` | v1.0.6 | `vendor/github.com/spf13/pflag/LICENSE` |
| `github.com/tadglines/go-pkgs` | v0.0.0-20210623144937-b983b20f54f9 | `vendor/github.com/tadglines/go-pkgs/LICENSE` |
| `github.com/vishvananda/netlink` | v1.3.1 | `vendor/github.com/vishvananda/netlink/LICENSE` |
| `github.com/vishvananda/netns` | v0.0.5 | `vendor/github.com/vishvananda/netns/LICENSE` |
| `github.com/xo/terminfo` | v0.0.0-20220910002029-abceb7e1c41e | `vendor/github.com/xo/terminfo/LICENSE` |
| `go.mozilla.org/pkcs7` | v0.9.0 | `vendor/go.mozilla.org/pkcs7/LICENSE` |
| `golang.org/x/crypto` | v0.52.0 | `vendor/golang.org/x/crypto/LICENSE` |
| `golang.org/x/exp` | v0.0.0-20231006140011-7918f672742d | `vendor/golang.org/x/exp/LICENSE` |
| `golang.org/x/mod` | v0.35.0 | `vendor/golang.org/x/mod/LICENSE` |
| `golang.org/x/net` | v0.55.0 | `vendor/golang.org/x/net/LICENSE` |
| `golang.org/x/sync` | v0.20.0 | `vendor/golang.org/x/sync/LICENSE` |
| `golang.org/x/sys` | v0.45.0 | `vendor/golang.org/x/sys/LICENSE` |
| `golang.org/x/text` | v0.37.0 | `vendor/golang.org/x/text/LICENSE` |
| `golang.org/x/time` | v0.5.0 | `vendor/golang.org/x/time/LICENSE` |
| `golang.org/x/tools` | v0.44.0 | `vendor/golang.org/x/tools/LICENSE` |
| `golang.zx2c4.com/wintun` | v0.0.0-20230126152724-0fa3db229ce2 | `vendor/golang.zx2c4.com/wintun/LICENSE` |
| `gvisor.dev/gvisor` | v0.0.0-20240405191320-0878b34101b5 | `vendor/gvisor.dev/gvisor/LICENSE` |
| `howett.net/plist` | v1.0.1 | `vendor/howett.net/plist/LICENSE` |
| `software.sslmate.com/src/go-pkcs12` | v0.7.2 | `vendor/software.sslmate.com/src/go-pkcs12/LICENSE` |

## Tools used at run time

These are not part of this repository. `scripts/fetch-tools.ps1` downloads them, and the
portable release archive includes them unchanged with their license files.

| Tool | Source | License |
|---|---|---|
| scrcpy and scrcpy-server v5.0.1 | [Genymobile/scrcpy](https://github.com/Genymobile/scrcpy) | Apache License 2.0 |
| ADB (Android SDK Platform-Tools), shipped in the scrcpy release | [Android Open Source Project](https://source.android.com/) | Apache License 2.0 |
| FFmpeg libraries, shipped in the scrcpy release | [FFmpeg](https://ffmpeg.org/) | LGPL 2.1 or later |

The iOS Developer Disk Image is Apple software. DeviceShell does not redistribute it:
it downloads the image at run time, on the user's machine, the first time an iPhone
needs it.

## Trademarks

iPhone, iPad, iOS, QuickTime and Xcode are trademarks of Apple Inc. Android is a
trademark of Google LLC. DeviceShell is not affiliated with, endorsed by or sponsored by
Apple or Google.
