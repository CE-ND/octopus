const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const assert = require('node:assert/strict');

if (!process.versions.electron) {
  const { spawnSync } = require('node:child_process');
  const temporary = fs.realpathSync(os.tmpdir());
  const directory = fs.mkdtempSync(path.join(temporary, 'octopus-update-bridge-'));
  try {
    const environment = { ...process.env };
    delete environment.ELECTRON_RUN_AS_NODE;
    const result = spawnSync(require('electron'), [__filename, directory], {
      env: environment,
      stdio: 'inherit', windowsHide: true, timeout: 45000,
    });
    if (result.error) throw result.error;
    assert.equal(result.status, 0, 'The real Electron preload/IPC smoke test must pass');
  } finally {
    const resolved = fs.realpathSync(directory);
    assert(resolved.startsWith(temporary + path.sep) && path.basename(resolved).startsWith('octopus-update-bridge-'));
    assert(!fs.lstatSync(directory).isSymbolicLink());
    fs.rmSync(resolved, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
  }
} else {
  const { app, BrowserWindow, ipcMain } = require('electron');
  const { EventEmitter } = require('node:events');
  const http = require('node:http');
  const { createDesktopUpdater, isTrustedUpdateSender, registerUpdateIPC } = require('../desktop/updater');
  app.setPath('userData', process.argv[2]);
  app.disableHardwareAcceleration();
  app.whenReady().then(async () => {
    const server = http.createServer((_request, response) => {
      response.setHeader('Content-Type', 'text/html');
      response.end('<!doctype html><html><head><title>Octopus updater test</title></head><body>Isolated updater smoke test</body></html>');
    });
    await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
    const origin = `http://127.0.0.1:${server.address().port}`;
    const window = new BrowserWindow({ show: false, webPreferences: {
      preload: path.resolve(__dirname, '..', 'desktop', 'preload.js'),
      contextIsolation: true, nodeIntegration: false, sandbox: true,
    } });
    const calls = [];
    const updater = new EventEmitter();
    updater.checkForUpdates = async () => {
      updater.emit('update-available', { version: '0.2.0' });
      return { cancellationToken: { cancelled: false, cancel() {} } };
    };
    updater.downloadUpdate = async () => {
      updater.emit('download-progress', { percent: 40, transferred: 4, total: 10, bytesPerSecond: 1 });
      updater.emit('update-downloaded', { version: '0.2.0' });
    };
    // 此测试只验证通信和顺序，不运行安装器或用户的后台进程。
    updater.quitAndInstall = () => calls.push('install');
    const manager = createDesktopUpdater({ updater, currentVersion: '0.1.9',
      installDirectory: path.dirname(process.execPath),
      publishState: state => window.webContents.send('octopus-update:state', state),
      beforeInstall: async () => calls.push('stop-backend'),
    });
    registerUpdateIPC(ipcMain, manager, event => isTrustedUpdateSender(event, window, origin));
    await window.loadURL(origin);
    const result = await window.webContents.executeJavaScript(`(async () => {
      const bridge = window.octopusDesktop.updater;
      const phases = [];
      const unsubscribe = bridge.onState(state => phases.push(state.phase));
      const initial = await bridge.getState();
      await bridge.check();
      const downloaded = await bridge.download();
      const installing = await bridge.install();
      unsubscribe();
      return { initial: initial.phase, downloaded: downloaded.phase, installing: installing.phase, phases, nodeAvailable: typeof require !== 'undefined' };
    })()`);
    assert.equal(result.initial, 'idle');
    assert.equal(result.downloaded, 'downloaded');
    assert.equal(result.installing, 'installing');
    assert.equal(result.nodeAvailable, false);
    assert(result.phases.includes('downloading') && result.phases.includes('downloaded'));
    assert.deepEqual(calls, ['stop-backend', 'install']);
    window.destroy();
    await new Promise(resolve => server.close(resolve));
    console.log('Electron sandboxed preload, trusted IPC, update events and shutdown ordering passed.');
    app.exit(0);
  }).catch(error => { console.error(error); app.exit(1); });
}
