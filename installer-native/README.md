# Native Windows installer

The installer is C++20 using Win32 controls, Direct2D, DirectWrite and WIC. It
requires Windows 10 or later, matching the desktop application's platform range.
The compiler and its SDK live in `build/toolchains`; only Windows system DLLs
are required by the resulting executable.

`scripts/desktop-custom-installer.js` builds the desktop app and NSIS engine,
then `scripts/build-native-installer.cjs` embeds that engine, its SHA-256 and the
generated background into a native EXE. The embedded engine is accessed only
after the user starts installation. There is no startup self-extraction.

The window has a 16-DIP outside radius and 10-DIP form/button corners. Text and
controls are rendered independently from the background. The layout follows
monitor DPI and is capped to fit the monitor's work area. Native edit, button,
checkbox and radio controls provide keyboard navigation and accessibility roles.

Current-user installation is the default. All-user installation requests Windows
elevation only when starting the NSIS engine. Each attempt uses a private session
directory restricted to the current user, SYSTEM and administrators. Setup verifies
the embedded engine and the installed application, and requires NSIS's explicit
completion marker before reporting success. It removes only its own temporary
payload and stage log, and keeps the diagnostic log for the user.

The patch in `patches/app-builder-lib@25.1.8.patch` fixes an out-of-bounds read of
the Shell-allocated default installation directory. `nsis/installer.nsh` adds
stage logging while preserving NSIS registers and its pre-existing error flag.
The original Octopus NSIS GUID is retained for upgrades and uninstallation.

## Development and verification

```powershell
pnpm installer:preview
pnpm installer:test
pnpm desktop:dist:custom
node scripts/verify-native-installer.cjs
```

The preview executable never installs or starts the desktop app. Preview-only
options include `--preview-state advanced`, `--preview-state progress`,
`--preview-state success`, `--preview-state error`, and `--preview-scale 1.5`.
The complete installer can also be opened safely with `--preview`.

`--self-test <report-file>` exercises argument handling, directory validation,
default-folder lookup and embedded resource metadata without installing anything.
`--smoke-test <report-file>` implies preview mode and closes after recording the
first paint. In-process timings exclude Windows image loading and antivirus work;
the verification script also measures process launch to report creation.

Integration tests use the production installation worker and patched NSIS engine
with a unique test application GUID and a tiny test executable. They verify fresh
installation, Unicode and spaces, upgrade, uninstallation and reinstall; they do
not replace the installed Octopus application. Local tests use current-user scope;
all-user elevation should additionally be checked in a disposable Windows VM.

The package version comes from the root `package.json`. Generated metadata, build
objects, test apps and compiler downloads are all stored under ignored `build/`.
No installation credentials or image-service configuration are packaged.

## Background provenance

`assets/background.png` was generated with the user-selected `centos-image-gen`
skill, model `gpt-image-2.5-sunburst`, through `https://ai.centos.hk/v1/images/edits`
on 2026-11-06, using the approved installer concept as its reference. Generation
took 34 seconds. It contains no UI text, controls or logo. The source prompt is
retained as `assets/background-prompt.txt`. API keys are not stored in this folder.
