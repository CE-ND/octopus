const { spawnSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const rootDir = path.resolve(__dirname, '..');
const desktopPackage = JSON.parse(fs.readFileSync(path.join(rootDir, 'package.json'), 'utf8'));
const webDir = path.join(rootDir, 'web');
const staticOutDir = path.join(rootDir, 'static', 'out');
const webOutDir = path.join(webDir, 'out');
const backendDir = path.join(rootDir, 'build', 'desktop', 'backend');
const isWindows = process.platform === 'win32';
const goosByPlatform = {
  win32: 'windows',
  darwin: 'darwin',
  linux: 'linux',
};
const goarchByNodeArch = {
  x64: 'amd64',
  arm64: 'arm64',
  ia32: '386',
};
const targetGoos = process.env.OCTOPUS_DESKTOP_GOOS || goosByPlatform[process.platform];
const targetGoarch = process.env.OCTOPUS_DESKTOP_GOARCH || goarchByNodeArch[process.arch];
const backendName = targetGoos === 'windows' ? 'octopus.exe' : 'octopus';
const backendPath = path.join(backendDir, backendName);

const args = new Set(process.argv.slice(2));
const skipFrontend = args.has('--skip-frontend') || process.env.OCTOPUS_DESKTOP_SKIP_FRONTEND === '1';
const skipInstall = args.has('--skip-install') || process.env.OCTOPUS_DESKTOP_SKIP_INSTALL === '1';

function resolveCommand(command) {
  if (!isWindows || command !== 'go') {
    return command;
  }

  const candidates = [
    process.env.GOROOT ? path.join(process.env.GOROOT, 'bin', 'go.exe') : null,
    'D:\\GO\\bin\\go.exe',
    path.join(process.env.ProgramFiles || 'C:\\Program Files', 'Go', 'bin', 'go.exe'),
    path.join(process.env['ProgramFiles(x86)'] || 'C:\\Program Files (x86)', 'Go', 'bin', 'go.exe'),
    'C:\\Go\\bin\\go.exe',
  ].filter(Boolean);

  return candidates.find((candidate) => fs.existsSync(candidate)) || command;
}

function run(command, commandArgs, options = {}) {
  const display = [command, ...commandArgs].join(' ');
  const useShell = isWindows && command === 'pnpm';
  const executable = resolveCommand(command);
  const env = { ...process.env, ...(options.env || {}) };

  if (command === 'go' && executable !== command) {
    const goBin = path.dirname(executable);
    env.Path = `${goBin};${env.Path || env.PATH || ''}`;
  }

  console.log(`> ${display}`);
  const result = spawnSync(executable, commandArgs, {
    cwd: options.cwd || rootDir,
    env,
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
  if (!targetGoos || !targetGoarch) {
    throw new Error(`unsupported desktop target: platform=${process.platform}, arch=${process.arch}`);
  }

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

  console.log(`Building desktop backend for ${targetGoos}/${targetGoarch}`);
  run('go', ['build', '-o', backendPath, '-ldflags', ldflags, '-tags=jsoniter', './'], {
    env: {
      CGO_ENABLED: '0',
      GOOS: targetGoos,
      GOARCH: targetGoarch,
    },
  });

  if (targetGoos !== 'windows') {
    fs.chmodSync(backendPath, 0o755);
  }
}

function main() {
  ensureCommand('node', ['--version']);
  ensureCommand('pnpm', ['--version']);
  ensureCommand('go', ['version']);

  const gitVersion = process.env.OCTOPUS_DESKTOP_VERSION || `v${desktopPackage.version}`;
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
