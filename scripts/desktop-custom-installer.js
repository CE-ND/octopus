const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { buildNativeInstaller } = require('./build-native-installer.cjs');
const { packageRequire } = require('./installer-toolchain.cjs');

const root = path.resolve(__dirname, '..');

function run(script, args = []) {
  const result = spawnSync(process.execPath, [script, ...args], {
    cwd: root, env: process.env, stdio: 'inherit', windowsHide: true,
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${path.basename(script)} exited with code ${result.status}`);
}

async function main() {
  if (process.platform !== 'win32') throw new Error('Build the custom Windows installer on Windows.');
  const args = new Set(process.argv.slice(2));
  for (const arg of args) if (arg !== '--skip-prepare') throw new Error(`Unknown option: ${arg}`);
  const packageJson = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'));
  const builderRoot = path.dirname(packageRequire().resolve('app-builder-lib/package.json'));
  const multiUser = fs.readFileSync(path.join(builderRoot, 'templates', 'nsis', 'multiUser.nsh'), 'utf8');
  if (!multiUser.includes('kernel32::lstrcpynW(w .s, p r2, i ${NSIS_MAX_STRLEN}) p')) {
    throw new Error('The NSIS directory fix is missing. Run pnpm install --frozen-lockfile before packaging.');
  }
  if (!args.has('--skip-prepare')) run(path.join(root, 'scripts', 'desktop-prepare.js'));
  const backend = path.join(root, 'build', 'desktop', 'backend', 'octopus.exe');
  const version = spawnSync(backend, ['version'], { encoding: 'utf8', windowsHide: true });
  if (version.error || version.status !== 0 || !(version.stdout || '').includes(`Version: v${packageJson.version}`)) {
    throw new Error('The prepared backend does not match package.json. Run pnpm desktop:prepare before packaging.');
  }
  run(require.resolve('electron-builder/out/cli/cli.js'), ['--win', 'nsis', '--x64', '--publish', 'never']);
  run(path.join(root, 'scripts', 'verify-desktop-update.cjs'));
  // Match this exact version and architecture; do not pick an old package by mtime.
  const appDir = path.join(root, 'dist', 'desktop', 'win-unpacked');
  const payload = path.join(root, 'dist', 'desktop', `${packageJson.build.productName}-${packageJson.version}-windows-x64-setup.exe`);
  await buildNativeInstaller({ payload, 'app-dir': appDir });
}

main().catch(error => { console.error(error.message); process.exitCode = 1; });
