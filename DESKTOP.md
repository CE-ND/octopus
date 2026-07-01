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

Create an unpacked app directory:

```bash
pnpm desktop:pack
```

Create an installer/package with Electron Builder:

```bash
pnpm desktop:dist
```

Artifacts are written to `dist/desktop`.

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
