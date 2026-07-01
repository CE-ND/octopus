const { app, BrowserWindow, dialog, nativeImage } = require('electron');
const { spawn } = require('child_process');
const fs = require('fs');
const path = require('path');
const net = require('net');
const crypto = require('crypto');
const os = require('os');

let backendProcess = null;
let backendBaseUrl = null;
let backendLogStream = null;
let shutdownToken = null;
let shuttingDown = false;
let stopBackendPromise = null;

const isWindows = process.platform === 'win32';
const appId = 'com.hureru.octopus.desktop';
const backendBinaryName = isWindows ? 'octopus.exe' : 'octopus';

if (isWindows) {
  app.setAppUserModelId(appId);
}

function getBackendBinaryPath() {
  if (app.isPackaged) {
    return path.join(process.resourcesPath, 'backend', backendBinaryName);
  }
  return path.join(__dirname, '..', 'build', 'desktop', 'backend', backendBinaryName);
}

function getAppIcon() {
  const iconName = isWindows ? 'icon.ico' : 'icon.png';
  const icon = nativeImage.createFromPath(path.join(__dirname, 'assets', iconName));
  return icon.isEmpty() ? undefined : icon;
}

function isPortAvailable(host, port) {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', () => resolve(false));
    server.listen({ host, port }, () => {
      server.close((err) => {
        if (err) {
          reject(err);
        } else {
          resolve(true);
        }
      });
    });
  });
}

function getRandomAvailablePort(host) {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.once('error', reject);
    server.listen({ host, port: 0 }, () => {
      const address = server.address();
      const port = typeof address === 'object' && address ? address.port : null;
      server.close((err) => {
        if (err) {
          reject(err);
        } else if (port) {
          resolve(port);
        } else {
          reject(new Error('failed to allocate a local port'));
        }
      });
    });
  });
}

async function choosePort(host, preferredPort) {
  if (await isPortAvailable(host, preferredPort)) {
    return preferredPort;
  }
  return getRandomAvailablePort(host);
}

async function waitForBackendHealth(timeoutMs = 30000) {
  const start = Date.now();
  let lastError = null;

  while (Date.now() - start < timeoutMs) {
    if (backendProcess && backendProcess.exitCode !== null) {
      throw new Error(`backend exited early with code ${backendProcess.exitCode}`);
    }

    try {
      const res = await fetch(`${backendBaseUrl}/api/v1/desktop/health`, {
        headers: {
          'X-Octopus-Desktop-Token': shutdownToken,
        },
      });
      if (res.ok) return;
      lastError = new Error(`health check returned HTTP ${res.status}`);
    } catch (err) {
      lastError = err;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }

  throw lastError || new Error('backend health check timed out');
}

function readExistingPort(configPath) {
  try {
    if (!fs.existsSync(configPath)) return null;
    const parsed = JSON.parse(fs.readFileSync(configPath, 'utf8'));
    const port = parsed?.server?.port;
    if (Number.isInteger(port) && port > 0) return port;
  } catch (_) {
    // ignore broken config and fall back to the default
  }
  return null;
}

function directoryHasEntries(dir) {
  try {
    return fs.existsSync(dir) && fs.readdirSync(dir).length > 0;
  } catch (_) {
    return false;
  }
}

function migrateLegacyUserData(root) {
  const legacyRoot = path.join(app.getPath('userData'), 'octopus');
  if (path.resolve(legacyRoot) === path.resolve(root)) return;
  if (!directoryHasEntries(legacyRoot) || directoryHasEntries(root)) return;

  if (fs.existsSync(root)) {
    fs.rmSync(root, { recursive: true, force: true });
  }

  fs.mkdirSync(path.dirname(root), { recursive: true });
  try {
    fs.renameSync(legacyRoot, root);
  } catch (_) {
    fs.cpSync(legacyRoot, root, { recursive: true });
  }
}

function getUserDataPaths() {
  const root = process.env.OCTOPUS_DESKTOP_DATA_DIR || path.join(os.homedir(), '.octopus');
  migrateLegacyUserData(root);
  return {
    root,
    dataDir: path.join(root, 'data'),
    configPath: path.join(root, 'data', 'config.json'),
    logDir: path.join(root, 'logs'),
  };
}

async function startBackend() {
  const backendPath = getBackendBinaryPath();
  if (!fs.existsSync(backendPath)) {
    throw new Error(`Backend binary not found: ${backendPath}`);
  }

  const { root, dataDir, configPath, logDir } = getUserDataPaths();
  fs.mkdirSync(dataDir, { recursive: true });
  fs.mkdirSync(logDir, { recursive: true });
  backendLogStream = fs.createWriteStream(path.join(logDir, 'backend.log'), { flags: 'a' });

  const host = '127.0.0.1';
  const preferredPort =
    Number.parseInt(process.env.OCTOPUS_DESKTOP_PORT || process.env.OCTOPUS_SERVER_PORT || '', 10) ||
    readExistingPort(configPath) ||
    8080;
  const port = await choosePort(host, preferredPort);
  shutdownToken = crypto.randomBytes(24).toString('hex');
  backendBaseUrl = `http://${host}:${port}`;

  const env = {
    ...process.env,
    OCTOPUS_DESKTOP: '1',
    OCTOPUS_SERVER_HOST: host,
    OCTOPUS_SERVER_PORT: String(port),
    OCTOPUS_DESKTOP_SHUTDOWN_TOKEN: shutdownToken,
  };

  backendProcess = spawn(backendPath, ['start', '--config', configPath], {
    cwd: root,
    env,
    windowsHide: true,
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  backendProcess.stdout.on('data', (chunk) => backendLogStream.write(chunk));
  backendProcess.stderr.on('data', (chunk) => backendLogStream.write(chunk));

  backendProcess.on('exit', (code, signal) => {
    backendLogStream.end();
    if (!shuttingDown && mainWindow && !mainWindow.isDestroyed()) {
      dialog.showErrorBox(
        'Octopus backend stopped',
        `The local backend stopped unexpectedly (code=${code}, signal=${signal || 'none'}).`
      );
      app.quit();
    }
  });

  await waitForBackendHealth();
}

async function requestBackendShutdown() {
  if (!backendBaseUrl || !shutdownToken) return;
  try {
    await fetch(`${backendBaseUrl}/api/v1/desktop/shutdown`, {
      method: 'POST',
      headers: {
        'X-Octopus-Desktop-Token': shutdownToken,
      },
    });
  } catch (_) {
    // fallback to process termination below
  }
}

function waitForBackendExit(timeoutMs = 8000) {
  return new Promise((resolve) => {
    if (!backendProcess || backendProcess.exitCode !== null) {
      resolve(true);
      return;
    }

    const timer = setTimeout(() => {
      backendProcess.removeListener('exit', onExit);
      resolve(false);
    }, timeoutMs);

    const onExit = () => {
      clearTimeout(timer);
      resolve(true);
    };

    backendProcess.once('exit', onExit);
  });
}

async function stopBackend() {
  if (stopBackendPromise) return stopBackendPromise;

  stopBackendPromise = (async () => {
    shuttingDown = true;
    await requestBackendShutdown();
    const exited = await waitForBackendExit();
    if (!exited && backendProcess && backendProcess.exitCode === null && !backendProcess.killed) {
      backendProcess.kill();
      await waitForBackendExit(2000);
    }
  })();

  return stopBackendPromise;
}

let mainWindow = null;

async function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1440,
    height: 960,
    minWidth: 1200,
    minHeight: 780,
    show: false,
    title: 'Octopus',
    icon: getAppIcon(),
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });

  mainWindow.once('ready-to-show', () => {
    mainWindow.show();
  });

  await mainWindow.loadURL(backendBaseUrl);
}

app.commandLine.appendSwitch('disable-http-cache');

const hasSingleInstanceLock = app.requestSingleInstanceLock();

if (!hasSingleInstanceLock) {
  app.quit();
} else {
  app.on('second-instance', () => {
    if (!mainWindow) return;
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.focus();
  });

  app.whenReady().then(async () => {
    try {
      await startBackend();
      await createWindow();
    } catch (err) {
      dialog.showErrorBox('Octopus failed to start', err instanceof Error ? err.message : String(err));
      app.quit();
    }
  });

  app.on('window-all-closed', async () => {
    if (process.platform !== 'darwin') {
      await stopBackend();
      app.quit();
    }
  });

  app.on('before-quit', async (event) => {
    if (shuttingDown) return;
    event.preventDefault();
    await stopBackend();
    app.exit(0);
  });

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0 && backendBaseUrl) {
      createWindow().catch((err) => {
        dialog.showErrorBox('Octopus failed to reopen', err instanceof Error ? err.message : String(err));
      });
    }
  });
}
