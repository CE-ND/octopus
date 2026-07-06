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

Create the custom Octopus setup UI:

```bash
pnpm desktop:dist:custom
```

This first builds the normal NSIS installer as a silent payload, then packages a
branded Electron setup shell around it. The user-facing artifact is written to
`dist/installer-ui/Octopus Custom Setup <version>.exe`. It presents Octopus-specific
pages for install scope, install path, pre-install checks, and progress while the
payload handles the actual Windows installation in the background.

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

## Useful Environment Variables

- `OCTOPUS_DESKTOP_PORT=8080`: preferred local backend port. If the port is in
  use, Electron falls back to a random free port.
- `OCTOPUS_SERVER_PORT=8080`: also works as the preferred desktop port, matching
  the normal backend configuration name.
- `OCTOPUS_DESKTOP_SKIP_FRONTEND=1`: reuse an existing `web/out` build.
- `OCTOPUS_DESKTOP_SKIP_INSTALL=1`: skip `pnpm install` inside `web`.
- `OCTOPUS_DESKTOP_DATA_DIR=C:\\path\\to\\data`: override the desktop data
  directory.

Desktop data is stored under the user's home directory, not in the installation
directory:

```text
%USERPROFILE%\.octopus\data\config.json
%USERPROFILE%\.octopus\data\data.db
%USERPROFILE%\.octopus\logs\backend.log
```
