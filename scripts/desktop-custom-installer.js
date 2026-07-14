const { spawnSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const rootDir = path.resolve(__dirname, '..');
const desktopDistDir = path.join(rootDir, 'dist', 'desktop');
const payloadDir = path.join(rootDir, 'build', 'installer-ui', 'payload');
const payloadPath = path.join(payloadDir, 'Octopus Setup.exe');

function run(command, args, options = {}) {
  const display = [command, ...args].join(' ');
  console.log(`> ${display}`);

  const result = spawnSync(command, args, {
    cwd: options.cwd || rootDir,
    env: { ...process.env, ...(options.env || {}) },
    stdio: 'inherit',
    shell: process.platform === 'win32',
  });

  if (result.error) {
    throw new Error(`${display} failed: ${result.error.message}`);
  }

  if (result.status !== 0) {
    throw new Error(`${display} failed with exit code ${result.status ?? 'unknown'}`);
  }
}

function findPayloadInstaller() {
  const entries = fs.existsSync(desktopDistDir) ? fs.readdirSync(desktopDistDir, { withFileTypes: true }) : [];
  const installers = entries
    .filter((entry) => entry.isFile() && /^Octopus-.+-windows-.+-setup\.exe$/i.test(entry.name))
    .map((entry) => {
      const file = path.join(desktopDistDir, entry.name);
      return { file, mtimeMs: fs.statSync(file).mtimeMs };
    })
    .sort((a, b) => b.mtimeMs - a.mtimeMs);

  if (installers.length === 0) {
    throw new Error(`payload installer not found in ${desktopDistDir}`);
  }

  return installers[0].file;
}

function stagePayloadInstaller() {
  fs.mkdirSync(payloadDir, { recursive: true });
  const source = findPayloadInstaller();
  fs.copyFileSync(source, payloadPath);
  console.log(`Payload staged: ${payloadPath}`);
}

function main() {
  run('pnpm', ['desktop:prepare'], {
    env: {
      OCTOPUS_DESKTOP_SKIP_FRONTEND: process.env.OCTOPUS_DESKTOP_SKIP_FRONTEND,
      OCTOPUS_DESKTOP_SKIP_INSTALL: process.env.OCTOPUS_DESKTOP_SKIP_INSTALL,
    },
  });
  run('pnpm', ['exec', 'electron-builder', '--win', 'nsis', '--publish', 'never']);
  stagePayloadInstaller();
  run('pnpm', ['exec', 'electron-builder', '--config', 'installer-ui/electron-builder.json', '--win', 'portable', '--publish', 'never']);
}

try {
  main();
} catch (err) {
  console.error(err instanceof Error ? err.message : err);
  process.exit(1);
}
