# iPhone protocol (iOS 27) used by DeviceShell

This document describes the traffic between a Windows host and an iPhone running iOS 27.2 that DeviceShell relies on for interoperability. It was obtained by tracing a pymobiledevice3-based client while it drove the phone. The Go code in `internal/ios/coredevice` is written from this document, from the captures (`internal/ios/coredevice/testdata/capture-*.jsonl`) and from public standards (USB HID, RFC 3550, protobuf, binary plist). No third-party code was translated (in particular, no code from pymobiledevice3, which is GPL-licensed), so DeviceShell remains MIT-licensed. The CoreDevice tunnel handshake is adapted from [go-ios](https://github.com/danielpaulus/go-ios) (MIT), which DeviceShell also uses as a library.

The UDID, serial number and ECID are replaced in the captures. Each line is one event: `xpc_out`/`xpc_in` (decoded XPC message), `h2_out`/`h2_in` (raw HTTP/2 frame), `udp_out`/`udp_in` (RTP/RTCP) and `mark` (scenario step and timings).

## Capturing again (new iOS version)

1. Temporarily install `pymobiledevice3` in a development Python environment (it is not part of DeviceShell).
2. Run a small tracing script that wraps `RemoteXPCConnection.send_request`, `receive_response`, `_send_frame`, `_receive_frame` and `UserspaceUdp.recv/sendto`, and writes each event as a JSONL line in the format above.
3. Scenarios: passive (open, status, screenshot, close) and active (HOME, volume, tap, drag, ASCII text and paste, DEL, ENTER, swipe, open and close Settings, URL). The iPhone should be freshly restarted: opening many video sessions in a row wedges its video service until it restarts.
4. Compare with the current captures and adjust `internal/ios/coredevice`. The tests in `internal/ios/coredevice` replay the captures.

## Tunnel and discovery

- **Tunnel:** lockdown → service `com.apple.internal.devicecompute.CoreDeviceProxy` → CDTunnel exchange (the `CDTunnel\0` magic, one length byte and a JSON body each way; the client sends `{type: "clientHandshakeRequest", mtu: 1280}` and the phone answers with `ServerAddress`, `ServerRSDPort` and `ClientParameters{Address, Netmask, Mtu}`) → IPv6 packets over that connection. In the captures the iPhone is `…::1` and the host `…::2`. go-ios provides usbmux and lockdown; DeviceShell runs the exchange (adapted from go-ios) and its own gVisor stack with TCP **and UDP** (the video session uses UDP).
- **RSD:** RemoteXPC on the iPhone's RSD port. In the captures, the client sends `{MessageType: Handshake, MessagingProtocolVersion: 7, UUID, Properties: {RemoteXPCVersionFlags, SensitivePropertiesVisible: true}, Services: {}}`. The phone's Handshake message carries `Services` (name → `Port` as a string) and `Properties` (including `UniqueDeviceID` and `ProductVersion`); DeviceShell reads that message to learn the service ports.
- Services used: `com.apple.coredevice.hid.universalhidservice` (touch and keyboard), `com.apple.coredevice.hid.indigo` (buttons), `com.apple.coredevice.displayservice`, `com.apple.coredevice.deviceinfo`, `com.apple.coredevice.appservice`, `com.apple.coredevice.pasteboardservice`, `com.apple.coredevice.screencaptureservice` and `com.apple.instruments.dtservicehub` (DVT). The CoreDevice services do not appear unless the developer disk image is mounted.

## CoreDevice invocation

Each RemoteXPC connection starts with an empty message, followed by the request:

```
CoreDevice.featureIdentifier   com.apple.coredevice.feature.<name>
CoreDevice.actionIdentifier    com.apple.coredevice.action.<name>   (only some)
CoreDevice.action              {}
CoreDevice.input               {…}
CoreDevice.invocationIdentifier  UUID as a string, new for each call
CoreDevice.deviceIdentifier      UUID as a string, new for each connection
CoreDevice.CoreDeviceDDIProtocolVersion  int64 2
CoreDevice.coreDeviceVersion   {components: [uint64 629, uint64 3], originalComponentsCount: int64 2, stringValue: "629.3"}
```

The response carries `CoreDevice.output` (or an error with `NSLocalizedDescription`).

| Feature | Action | Input | Output |
|---|---|---|---|
| `getdisplayinfo` (deviceinfo) | — | `{}` | `displays`, `orientation`… |
| `startmediastream` (displayservice) | `mediastreamstart` | see below | `connection`, `negotiatorAnswer` |
| `getmediastreamserverstatus` | `mediastreamstatus` | `{}` | `sessions: [{connection…}]` |
| `stopmediastream` | `mediastreamstop` | `{avcMediaStreamOptionClientSessionID: {uuid: <UUID>}, stopAll: true}` | `stoppedStreams: [ids]`, `serverInfo`. Without `stopAll` the phone answers "Expected to find key stopAll", and with `stopAll: false`, "Invalid request sent": only `true` works, and it closes every session on the iPhone, including any left running by an earlier shutdown. A client that sends this message without `stopAll`, or does not read the reply, never actually closes its sessions. The client must wait for the reply before closing the tunnel: a session that was not closed stays on the iPhone even after the tunnel is gone. |
| `launchapplication` (appservice) | — | `applicationSpecifier.bundleIdentifier._0`, `options{arguments: [], environmentVariables: {}, standardIOUsesPseudoterminals: true, startStopped: false, terminateExisting: false, user.shortName: "mobile", platformSpecificOptions: <XML plist of an empty dict>}`, `standardIOIdentifiers: {}` | — |

DeviceShell sends `launchapplication` through go-ios's `appservice` package.

## DisplayService session (required for touch)

iOS 27 drops touch input unless a DisplayService video session is open. DeviceShell opens one, receives its video over UDP and keeps it alive with RTCP reports. By default this video is the picture shown in DeviceShell's window (see [Video](iphone.md#video)); when QuickTime is selected as the cable video source, the stream is received but not decoded.

`startmediastream` input:

```
type            "video"
direction       "output"
timeout         uint64 20
senderIP        the iPhone's IPv6 address in the tunnel (…::1)
receiverIP      ours (…::2)
receiverPort    uint64, the local UDP port we listen on
clientSupportedFeatures  uint64 140
options         {AVCMediaStreamNegotiatorAccessNetworkType: {int: 1},
                 AVCMediaStreamNegotiatorTransportProtocolType: {int: 2},
                 CoreDeviceVideoDisplayMode: {string: "DisplayByID"},
                 VideoStreamForDisplayID: {int: 1},
                 avcMediaStreamOptionClientSessionID: {uuid: <new UUID>}}
negotiatorOffer data: binary plist (see below)
```

`negotiatorOffer` is a binary plist with:

- `avcMediaStreamNegotiatorMode`: 5.
- `avcMediaStreamOptionCallID`: an uppercase UUID, new for each session.
- `avcMediaStreamOptionRemoteEndpointInfo`: protobuf `{1: 0, 2: 1, 3: "<Mac model>", 4: "<version>", 5: "<build>"}` (in the capture and in DeviceShell, `Mac15,9`, `2205.3.1` and `25F80`).
- `avcMediaStreamNegotiatorMediaBlob`: zlib-compressed protobuf. Captured content:
  - `1: 1`, `2: 1`, `6: "Viceroy 1.7.0"`, `8: 0`, `14: 2`, `16: 0`, `18: 1`, and `13`: a 64-bit integer.
  - `5` = video description:
    - `1`: **our SSRC** (random, 32-bit) and `2: 0`;
    - two `3` entries (formats): payload type `123` (H.264) and `100` (HEVC), each with resolutions `2 = {1: 1, 2: 1|2, 3: 50115, 4: 0}`, a feature string (`FLS;SW:1;` and `FLS;VRAE:0;SW:1;`) and `4` (1 and 14);
    - `7: 0`, `8: 63`, `10: 1`, `12: 1`.
  - `9`, repeated: a table of bit rates and buffers (`{1, 2, 3}`).

When the picture is going to be shown, DeviceShell removes the HEVC format (payload type 100) from the offer, so the phone sends H.264: the HEVC stream drifts after a few page swipes (the encoder predicts from pictures that differ from the ones it sent). When the picture is not shown, it sends the offer exactly as captured.

Response (`CoreDevice.output.connection.streamConfig`), fields DeviceShell uses:

| Field | Captured value | Use |
|---|---|---|
| `SourcePort` / `SourceIP` | the iPhone's UDP port / `…::1` | destination of our RTCP |
| `DestPort` / `DestIp` | our `receiverPort` / `…::2` | — |
| `LocalSSRC` | the iPhone's SSRC | source of the reports |
| `RemoteSSRC` | our SSRC | sender of the reports |
| `RTCPSendInterval` | 1.0 s | how often to send RRs |
| `RTCPTimeoutInterval` / `RTCPTimeoutEnabled` | 20 s / true | the iPhone closes the session without RTCP |
| `TxPayloadType` | 100 (both formats offered) | payload type of the incoming RTP: 123 H.264, 100 HEVC |

The response also carries `RxPayloadType` (100 in the capture), which DeviceShell does not use.

`connection.options.avcMediaStreamOptionClientSessionID.uuid` is the identifier for `stopmediastream`. Before opening a session, DeviceShell calls `getmediastreamserverstatus` and, if any session is left, closes it with `stopAll`. If `startmediastream` answers "call is currently in progress", it does the same. Three consecutive starts that time out mean the iPhone's video service is wedged: the iPhone must be restarted.

### RTCP (RFC 3550)

Every second, a 44-byte compound packet is sent to `SourceIP:SourcePort`:

```
81 c9 00 07   RR, 1 block, length 7
<sender SSRC: RemoteSSRC>
<source SSRC: LocalSSRC>  00 00 00 00 (loss)  <extended highest sequence received, 32 bits>  00000000 (jitter)  <LSR>  <DLSR>
81 ca 00 02   SDES, 1 chunk, length 2
<SSRC: RemoteSSRC> 01 00 00 00   (empty CNAME + end)
```

LSR and DLSR are zero until the phone sends a sender report; after that, LSR is the middle 32 bits of the NTP timestamp of its last SR and DLSR the delay since it arrived, in units of 1/65536 s (RFC 3550 6.4.1), so the phone can measure the round trip.

The incoming RTP is always counted for the highest sequence number. When the picture is shown, it is also depacketized (H.264 per RFC 6184: single NAL units, STAP-A and FU-A; HEVC per RFC 7798) and decoded. After a packet loss DeviceShell sends an RTCP PLI (RFC 4585), although the phone does not respond to it with a new keyframe.

## Touch and keyboard: UniversalHID

Messages on `com.apple.coredevice.hid.universalhidservice` (after the initial empty message):

```
{featureIdentifier: "com.apple.coredevice.feature.remote.universalhidservice",
 messageType: "Request",
 payload: {send: {_0: <report, data>, _1: <serviceID, uint64>}}}
```

**Touchscreen** (`serviceID` 257). 58-byte report:

| Bytes | Content |
|---|---|
| 0–2 | `09 01 05` |
| 3 | `c2` finger in contact, `02` released |
| 4–5 | X, uint16 little-endian, 0–65535 (left to right) |
| 6–7 | Y, uint16 little-endian, 0–65535 (top to bottom) |
| 8–39 | zeros |
| 40–43 | `02 00 00 00` |
| 44–51 | 64-bit little-endian timestamp (host monotonic clock in ns) |
| 52–57 | zeros |

A tap is contact + release with 50 ms in between. A swipe is a series of interpolated contact samples (about 60 per second, between 8 and 120) followed by a final release.

**Keyboard.** First, a virtual keyboard service is created:

```
payload: {createService: {_0: {
  DeviceUsagePairs: [{DeviceUsage: int64 6, DeviceUsagePage: int64 1}],
  PrimaryUsage: uint64 6, PrimaryUsagePage: uint64 1,
  Product: "<name>", ProductID: int64 592, VendorID: int64 1452,
  _CoreDevice_codablePropertyStorage: { the same properties wrapped in {string|int|array|dictionary|data|bool|uint: …},
    Manufacturer, Transport: "USB", ReportDescriptor: <standard HID keyboard descriptor>,
    UniversalControlVirtualService: true, _ServiceID },
  _ServiceID: uint64 <proposed id>}}}
→ response {serviceID: uint64}
```

DeviceShell uses `Product: "DeviceShell virtual keyboard"` and `Manufacturer: "DeviceShell"`.

39-byte keyboard report: `01`, a 30-byte bitmap in bytes 1–30 (HID usage code *n* sets bit *n mod 8* of byte *1 + n/8*; the modifiers 0xE0–0xE7 fall in byte 29) and an 8-byte timestamp in bytes 31–38. A key press = a report with the key, then an empty report. A combination (Shift+letter, Cmd+V, Cmd+T, Cmd+L) is sent as four reports: modifier, modifier+key, modifier, empty.

Letters, digits and spaces are typed through HID. All other text is pasted: the pasteboard is set with `pasteboardservice` (`command: "SET"`, `pasteboardName: "general"`, `items: [{types: [public.utf8-plain-text, public.plain-text, public.text], data: {…: {data: <UTF-8>}}}]`) and Cmd+V is pressed. go-ios implements this (`pasteboard`).

## Buttons: Indigo

On `com.apple.coredevice.hid.indigo`:

```
{messageType: "IndigoButtonEvent",
 featureIdentifier: "com.apple.coredevice.feature.remote.hid.button",
 payload: {usagePage: uint64, usageCode: uint64, state: uint64 (1 pressed, 2 released)}}
```

| Key | Page | Code | Duration |
|---|---|---|---|
| HOME | 12 | 64 (0x40) | 50 ms |
| LOCK / POWER | 12 | 48 (0x30) | 500 ms |
| VOLUME_UP | 12 | 233 (0xE9) | 50 ms |
| VOLUME_DOWN | 12 | 234 (0xEA) | 50 ms |
| MUTE | 12 | 226 (0xE2) | 50 ms |

## Other actions (go-ios)

- **URL:** open Safari (`launchapplication` with `com.apple.mobilesafari`), wait 1.2 s, Cmd+T, wait 0.8 s, Cmd+L, wait 0.4 s, type the URL and press ENTER.
- **Close an app:** CoreDevice `appservice` through go-ios: list the processes, find the one whose path ends in the app's executable, and kill it by PID.
- **Screenshot:** DVT `screenshot`, over one connection that is reused (about 2.5 times faster than opening a new connection per screenshot); if it fails, DeviceShell reconnects once and retries.
- **Mounting the developer disk image:** go-ios `imagemounter` (personalized image signed by Apple's TSS). Measured: 0.9 s.
- **Keeping the screen awake:** shim service `com.apple.mobile.assertion_agent.shim.remote` (`RSDCheckin` greeting, handled by go-ios) and a plist `{CommandKey: CommandCreateAssertion, AssertionTypeKey: PreventUserIdleSystemSleep, AssertionNameKey, AssertionTimeoutKey: 300, AssertionDetailKey}`; the phone answers `{}`. It is renewed every 120 s.

## Reference measurements (iOS 27.2)

For comparison, the pymobiledevice3-based Python client that produced the captures:

- Process start until the tunnel and services are ready: 4.2 s, of which 1.1 s is tunnel + session and the rest is loading Python.
- Memory: 118 MB per iPhone. Idle CPU: 0.75% of one core.
- Sending one touch sample: 0.24 ms median and 0.34 ms at p95, plus ~0.5 ms of local HTTP between the host application and the Python client.

DeviceShell's Go engine (`internal/ios/coredevice`): mounting the image 0.9 s; tunnel + RSD 52 ms; opening the DisplayService session 216 ms; the session stays alive with our RTCP reports beyond the 20 s timeout.
