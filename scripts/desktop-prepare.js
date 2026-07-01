const { spawnSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const rootDir = path.resolve(__dirname, '..');
const webDir = path.join(rootDir, 'web');
const staticOutDir = path.join(rootDir, 'static', 'out');
const webOutDir = path.join(webDir, 'out');
const backendDir = path.join(rootDir, 'build', 'desktop', 'backend');
const isWindows = process.platform === 'win32';
const backendName = isWindows ? 'octopus.exe' : 'octopus';
const backendPath = path.join(backendDir, backendName);

const args = new Set(process.argv.slice(2));
const skipFrontend = args.has('--skip-frontend') || process.env.OCTOPUS_DESKTOP_SKIP_FRONTEND === '1';
const skipInstall = args.has('--skip-install') || process.env.OCTOPUS_DESKTOP_SKIP_INSTALL === '1';

function run(command, commandArgs, options = {}) {
  const display = [command, ...commandArgs].join(' ');
  const useShell = isWindows && command === 'pnpm';
  console.log(`> ${display}`);
  const result = spawnSync(command, commandArgs, {
    cwd: options.cwd || rootDir,
    env: { ...process.env, ...(options.env || {}) },
    stdio: options.capture ? 'pipe' : 'inherit',
    shell: useShell,
    encoding: 'utf8',
  });

  if (result.error) {
    throw new Error(`${display} failed: ${result.error.message}`);
  }

  if (result.status !== 0) {
    const output = [result.stdout, result.stderr].filter(Boolean).join('\n').trim();
    const suffix = output ? `\n${output}` : '';
    throw new Error(`${display} failed with exit code ${result.status ?? 'unknown'}${suffix}`);
  }

  if (options.capture) {
    return (result.stdout || '').trim();
  }

  return '';
}

function tryRun(command, commandArgs, fallback) {
  try {
    return run(command, commandArgs, { capture: true });
  } catch (_) {
    return fallback;
  }
}

function ensureCommand(command, versionArgs) {
  try {
    const version = run(command, versionArgs, { capture: true });
    console.log(`${command}: ${version.split('\n')[0]}`);
  } catch (err) {
    throw new Error(`${command} is required for desktop packaging. ${err.message}`);
  }
}

function copyFrontendOutput() {
  if (!fs.existsSync(webOutDir)) {
    throw new Error(`frontend output not found: ${webOutDir}`);
  }

  fs.rmSync(staticOutDir, { recursive: true, force: true });
  fs.cpSync(webOutDir, staticOutDir, { recursive: true });
}

function buildFrontend(gitVersion) {
  if (skipFrontend) {
    console.log('Skipping frontend build because OCTOPUS_DESKTOP_SKIP_FRONTEND=1 or --skip-frontend was provided.');
    copyFrontendOutput();
    return;
  }

  if (!skipInstall) {
    run('pnpm', ['install'], { cwd: webDir });
  }

  run('pnpm', ['run', 'build'], {
    cwd: webDir,
    env: {
      NEXT_PUBLIC_APP_VERSION: gitVersion,
    },
  });
  copyFrontendOutput();
}

function buildBackend(gitVersion) {
  fs.mkdirSync(backendDir, { recursive: true });

  const buildTime = new Date().toISOString();
  const commit = tryRun('git', ['rev-parse', '--short', 'HEAD'], 'unknown');
  const ldflags = [
    `-X github.com/bestruirui/octopus/internal/conf.Version=${gitVersion}`,
    `-X github.com/bestruirui/octopus/internal/conf.BuildTime=${buildTime}`,
    '-X github.com/bestruirui/octopus/internal/conf.Author=hureru',
    `-X github.com/bestruirui/octopus/internal/conf.Commit=${commit}`,
    '-s',
    '-w',
  ].join(' ');

  run('go', ['build', '-o', backendPath, '-ldflags', ldflags, '-tags=jsoniter', './']);

  if (!isWindows) {
    fs.chmodSync(backendPath, 0o755);
  }
}

function main() {
  ensureCommand('node', ['--version']);
  ensureCommand('pnpm', ['--version']);
  ensureCommand('go', ['version']);

  const gitVersion = tryRun('git', ['describe', '--tags', '--abbrev=0'], 'dev');
  console.log(`Preparing Octopus desktop assets (${gitVersion})`);

  buildFrontend(gitVersion);
  buildBackend(gitVersion);

  console.log(`Desktop backend ready: ${backendPath}`);
}

try {
  main();
} catch (err) {
  console.error(err instanceof Error ? err.message : err);
  process.exit(1);
}
