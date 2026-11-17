# Contributing to DeviceShell

Thanks for your interest in improving DeviceShell. Bug reports, fixes, documentation
and well-scoped features are all welcome.

## Before you start

- For anything larger than a small fix, open an issue first so we can agree on the
  approach before you invest time in it.
- Security problems must not go in public issues: see [SECURITY.md](SECURITY.md).
- By contributing you agree that your work is released under the [MIT License](LICENSE)
  and that you follow the [Code of Conduct](CODE_OF_CONDUCT.md).

## Development setup

DeviceShell targets Windows 10/11 x64. Everything builds offline from the repository:
the Go toolchain version is pinned in `scripts/toolchain.json` and the dependencies are
vendored in `vendor/`.

```powershell
git clone https://github.com/doplerer/DeviceShell.git
cd DeviceShell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\build.ps1 -ValidateOnly
```

The first run downloads the pinned Go toolchain into `.toolchain/` and checks its SHA-256.
`-ValidateOnly` runs `gofmt`, `go vet` (for Windows and Linux) and `go test`. Without it,
the script also builds `deviceshell.exe` and installs it into `dist/`. To try the
application, add the runtime tools once:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\fetch-tools.ps1
.\dist\deviceshell.exe doctor
```

See [docs/development.md](docs/development.md) for the architecture, the tests that need
real devices and how to measure performance.

## Pull requests

1. Branch from `develop` and target `develop` with your pull request; `master` only
   receives releases.
2. Keep each pull request focused on one change, with tests for new behavior.
3. Make sure `scripts/build.ps1 -ValidateOnly` passes. CI runs the same checks.
4. Write commit messages in the [Conventional Commits](https://www.conventionalcommits.org/)
   style (`feat:`, `fix:`, `docs:`, `refactor:`, `build:`, `test:`, `chore:`).
5. Add a line under **Unreleased** in [CHANGELOG.md](CHANGELOG.md) for user-visible changes.

## Code style

- Format with `gofmt`; keep code comments in English.
- User-facing text is bilingual: write both versions with `app.L("español", "English")`.
- Error values from low-level packages (`internal/ios/*`, `internal/android/*`) are plain
  English, lowercase, without final punctuation.
- Never commit device identifiers, pairing records, ADB keys or personal data. Test
  fixtures recorded from real devices must have identifiers replaced by placeholders.
