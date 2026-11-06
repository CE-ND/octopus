# Octopus Desktop

This repository can be packaged as a desktop app with Electron. The Electron
main process starts the bundled Octopus Go backend, waits for a local health
check, and then opens the embedded web UI.

## Requirements

- Go
- Node.js
- pnpm

## Development

Install the desktop dependencies from the repository root:

```bash
pnpm install
```

Start the desktop app:

```bash
pnpm desktop:dev
```

The script builds `web/out`, copies it to `static/out`, builds the Go backend
into `build/desktop/backend`, and launches Electron.

## Packaging

Run packaging commands from the repository root.

Prepare desktop assets only. This builds the frontend, copies `web/out` to
`static/out`, and compiles the Go backend into `build/desktop/backend`:

```bash
pnpm desktop:prepare
```

Create an unpacked app directory:

```bash
pnpm desktop:pack
```

Create an installer/package with Electron Builder:

```bash
pnpm desktop:dist
```

Artifacts are written to `dist/desktop`. On Windows, `pnpm desktop:dist`
creates an NSIS installer by default. The installer uses a guided setup flow and
allows the user to choose the installation directory.

### Ad-hoc signed macOS packages

The release workflow builds separate ad-hoc signed DMG files for Apple Silicon
(`arm64`) and Intel (`x64`) Macs. Ad-hoc signing protects the integrity of the
application bundle and does not require an Apple Developer ID or notarization.
Gatekeeper does not treat it as an identified-developer signature, so macOS may
still block the first launch because the app was downloaded from the internet.

To open it for the first time:

1. Try to open Octopus once and dismiss the warning.
2. Open **System Settings > Privacy & Security**.
3. Find the message about Octopus and click **Open Anyway**.
4. Confirm the action with the macOS login password or Touch ID.

Build an ad-hoc signed package locally on macOS:

```bash
# Apple Silicon
OCTOPUS_DESKTOP_GOOS=darwin OCTOPUS_DESKTOP_GOARCH=arm64 pnpm desktop:prepare
CSC_IDENTITY_AUTO_DISCOVERY=false pnpm exec electron-builder --mac dmg --arm64 --publish never

# Intel
OCTOPUS_DESKTOP_GOOS=darwin OCTOPUS_DESKTOP_GOARCH=amd64 pnpm desktop:prepare
CSC_IDENTITY_AUTO_DISCOVERY=false pnpm exec electron-builder --mac dmg --x64 --publish never
```

Create the custom Octopus setup UI:

```bash
pnpm desktop:dist:custom
```

This builds a native C++ / Win32 setup window with Direct2D / DirectWrite and an
embedded NSIS installation engine. The window loads its background and controls
before extracting the installation payload. It does not package another Electron
runtime for setup. The offline, single-file installer is written to
`dist/installer-ui/Octopus Custom Setup <version>.exe`.

The pinned LLVM-MinGW compiler is downloaded into `build/toolchains` and checked
against its SHA-256 on first use. It does not require a system-wide compiler
installation. Set `OCTOPUS_LLVM_MINGW` to use an existing LLVM-MinGW installation.
Install desktop dependencies with **pnpm** so the checked-in NSIS directory
reading fix is applied; the custom packaging script verifies this patch.

Build a preview which cannot install or launch Octopus:

```powershell
pnpm installer:preview
& "build/native-installer/preview/Octopus Setup Preview.exe"
```

Run the installation tests using a separate application ID, executable name and
directory, including paths containing Chinese characters and spaces:

```powershell
pnpm installer:test
```

After a complete build, verify its payload metadata, native self-tests and first
paint timings:

```powershell
node scripts/verify-native-installer.cjs
```

To rebuild setup using already prepared desktop assets, use
`node scripts/desktop-custom-installer.js --skip-prepare`. The cached backend
must match the package version. Setup logs are stored under
`%LOCALAPPDATA%/Octopus/Setup/Logs`; user data remains under `%USERPROFILE%/.octopus`.

The background is a checked-in generated PNG. Builds do not call an image service
or require image API credentials. See `installer-native/README.md` for the native
architecture and preview options.

### Windows symlink permission error

During packaging, Electron Builder may download and extract `winCodeSign`. If
Windows reports `Cannot create symbolic link` for files such as
`libcrypto.dylib` or `libssl.dylib`, the current user does not have permission
to create symbolic links.

Fix it by enabling Windows Developer Mode or by running the packaging command
from an administrator PowerShell window. After changing the permission, remove
the failed cache and retry:

```powershell
Remove-Item "$env:LOCALAPPDATA\electron-builder\Cache\winCodeSign" -Recurse -Force
pnpm desktop:dist
```

### Windows in-app updates

Installed Windows builds use `electron-updater` to check the GitHub release feed
configured in `package.json` under `build.publish`. Settings shows download
progress, cancellation, retry, and **Restart and update**. Downloads are checked
against the release manifest's SHA-512 before installation becomes available.
Downloading does not stop the app, and an ordinary exit does not install an update.

When the user chooses to update, Electron stops the Go backend first and starts
the NSIS engine with `--updated /S --force-run`. The installation directory comes
from the running executable. The stable NSIS GUID preserves the existing install
registration, and NSIS removes obsolete program files as part of the upgrade.
Accounts, settings and plugin data stay in `%USERPROFILE%/.octopus`, outside the
installation directory. Administrator approval may still be required for an
all-users installation.

Windows releases must contain the native setup, the standard NSIS installer,
its `.blockmap`, and the channel manifest (`latest.yml` for stable releases).
`scripts/collect-release-assets.cjs` checks the manifest's version, file name,
size and SHA-512 before staging an explicit list of publishable artifacts.
The release workflow repeats this validation after collecting the Windows and
macOS builds. Build logs and effective configuration files are not published.

Versions released before this updater was added need one manual installation of
a new build. Development and unpacked builds do not install updates. macOS
currently uses ad-hoc signing and manual DMG updates; reliable Squirrel updates
require a suitable signing and release setup. Changing `OCTOPUS_GITHUB_REPO` for
the backend does not redirect executable updates; forks must set `build.publish`.

Run the desktop update tests with `pnpm desktop:test`. `pnpm installer:test`
also verifies silent cross-version updates in a separate test installation,
including a path containing Chinese characters and spaces.
`pnpm desktop:test:bridge` checks the real sandboxed Electron preload and IPC
without installing anything. Custom installer builds also run
`scripts/verify-desktop-update.cjs` to verify the packaged update feed, load the
updater from the built ASAR, and validate the NSIS manifest and checksum.

## Useful Environment Variables

- `OCTOPUS_DESKTOP_PORT=18777`: preferred local backend port. If the port is in
  use, Electron shows an error and stops startup.
- `OCTOPUS_SERVER_PORT=18777`: also works as the preferred desktop port, matching
  the normal backend configuration name.
- `OCTOPUS_DESKTOP_SKIP_FRONTEND=1`: reuse an existing `web/out` build.
- `OCTOPUS_DESKTOP_SKIP_INSTALL=1`: skip `pnpm install` inside `web`.
- `OCTOPUS_DESKTOP_GOOS=darwin`: override the Go backend target OS during
  desktop packaging.
- `OCTOPUS_DESKTOP_GOARCH=arm64`: override the Go backend target architecture
  during desktop packaging. Use `amd64` for an Intel Mac package.
- `OCTOPUS_DESKTOP_VERSION=v0.1.4`: override the version embedded into the
  desktop frontend and backend. By default it uses the root package version.
- `OCTOPUS_DESKTOP_DATA_DIR=C:\\path\\to\\data`: override the desktop data
  directory.

Desktop data is stored under the user's home directory, not in the installation
directory:

```text
%USERPROFILE%\.octopus\data\config.json
%USERPROFILE%\.octopus\data\data.db
%USERPROFILE%\.octopus\logs\backend.log
```
