// Package remotepairing is RemotePairing for iPhones over Wi-Fi without any app on the phone:
// pairing once over the USB tunnel, finding paired phones on the LAN (mDNS) and
// opening their TLS-PSK tunnel, which carries the same CDTunnel as CoreDeviceProxy.
//
// The pairing code (codec, framing, tlv, srp, pairrecord, untrusted) comes from
// go-ios (github.com/danielpaulus/go-ios, ios/tunnel), MIT License,
// Copyright (c) 2019 danielpaulus. The LAN transport (wifi.go) and discovery
// (mdns.go) are DeviceShell's, written from captures of the phone's traffic.
package remotepairing

const logModule = "remotepairing"
