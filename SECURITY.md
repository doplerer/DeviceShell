# Security policy

## Supported versions

Only the latest release receives security fixes.

## Reporting a vulnerability

Please do not report security problems in public issues. Use GitHub's private
vulnerability reporting instead: open the repository's **Security** tab and choose
**Report a vulnerability**.

Include the affected version, the steps to reproduce and the impact you expect. You will
get an acknowledgement within a week, and we will keep you informed until a fix is
released. Please give us a reasonable time to ship a fix before disclosing the issue.

## Scope

DeviceShell runs locally and controls devices connected to the same PC. Areas of
particular interest are:

- the local synchronization and peer-control channels (`internal/core/sync.go`,
  `internal/core/peer_control.go`);
- the handling of pairing records and ADB keys under `tools/`;
- the Windows driver and certificate installation (`internal/ios/qtdriver`).
