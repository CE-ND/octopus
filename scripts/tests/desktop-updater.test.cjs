const { test } = require('node:test');
const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const path = require('node:path');
const { createDesktopUpdater, updateAvailability, isTrustedUpdateSender, registerUpdateIPC } = require('../../desktop/updater');

function fixture(options = {}) {
  const updater = new EventEmitter();
  const calls = [];
  const states = [];
  updater.checkForUpdates = async () => {
    calls.push('check');
    updater.emit('update-available', { version: '0.2.0' });
    return { cancellationToken: { cancelled: false, cancel() { this.cancelled = true; } } };
  };
  updater.downloadUpdate = async () => {
    calls.push('download');
    updater.emit('download-progress', { percent: 50, transferred: 5, total: 10, bytesPerSecond: 1 });
    updater.emit('update-downloaded', { version: '0.2.0' });
    return ['verified-installer.exe'];
  };
  updater.quitAndInstall = (silent, restart) => { calls.push(['install', silent, restart]); };
  const manager = createDesktopUpdater({
    updater, currentVersion: '0.1.9', installDirectory: 'D:\\中文 空格\\Octopus',
    publishState: state => states.push(state), beforeInstall: async () => { calls.push('stop-backend'); },
    logger: { error() {} }, ...options,
  });
  return { manager, updater, calls, states };
}

test('download is explicit, verified updates wait for the backend, and installation uses the current directory', async () => {
  const { manager, updater, calls, states } = fixture();
  assert.equal(updater.autoDownload, false);
  assert.equal(updater.autoInstallOnAppQuit, false);
  assert.equal(updater.allowPrerelease, false);
  assert.equal(updater.allowDowngrade, false);
  assert.equal(updater.installDirectory, 'D:\\中文 空格\\Octopus');
  await manager.check();
  assert.deepEqual(calls, ['check']);
  assert.equal(manager.getState().phase, 'available');
  await manager.install();
  assert.deepEqual(calls, ['check'], 'An unverified installer must never start');
  await manager.download();
  assert.equal(manager.getState().phase, 'downloaded');
  assert(states.some(state => state.progress?.percent === 50));
  assert(!calls.includes('stop-backend'), 'Downloading must leave the backend running');
  await manager.install();
  assert.deepEqual(calls, ['check', 'download', 'stop-backend', ['install', true, true]]);
  await manager.install();
  assert.equal(calls.filter(call => Array.isArray(call)).length, 1);
});

test('installation waits for graceful shutdown and blocks replacement if it fails', async () => {
  let release;
  const stopped = new Promise(resolve => { release = resolve; });
  const { manager, calls } = fixture({ beforeInstall: () => stopped });
  await manager.download();
  const installing = manager.install();
  assert.equal(manager.getState().phase, 'installing');
  assert(!calls.some(call => Array.isArray(call)));
  release();
  await installing;
  assert.deepEqual(calls.at(-1), ['install', true, true]);

  let resumed = false;
  const failed = fixture({ beforeInstall: async () => { throw new Error('backend is busy'); }, installFailed: async () => { resumed = true; } });
  await failed.manager.download();
  await failed.manager.install();
  assert.equal(failed.manager.getState().error, 'install');
  assert(!failed.calls.some(call => Array.isArray(call)));
  assert.equal(resumed, true);
});

test('a checksum failure cannot reach installation and can be retried', async () => {
  const { manager, updater, calls } = fixture();
  updater.downloadUpdate = async () => {
    const error = Object.assign(new Error('bad checksum'), { code: 'ERR_CHECKSUM_MISMATCH' });
    updater.emit('error', error);
    throw error;
  };
  await manager.download();
  assert.equal(manager.getState().error, 'integrity');
  await manager.install();
  assert(!calls.includes('stop-backend'));
  updater.downloadUpdate = async () => updater.emit('update-downloaded', { version: '0.2.0' });
  await manager.download();
  assert.equal(manager.getState().phase, 'downloaded');
});

test('cancel and concurrent clicks never start another download or stop the running app', async () => {
  const { manager, updater, calls } = fixture();
  let finish;
  updater.downloadUpdate = token => new Promise((_resolve, reject) => {
    calls.push('download');
    finish = () => { assert(token.cancelled); reject(new Error('cancelled')); };
  });
  await manager.check();
  const first = manager.download();
  await manager.download();
  await manager.check();
  manager.cancel();
  assert.equal(manager.getState().phase, 'cancelling');
  finish();
  await first;
  assert.equal(manager.getState().phase, 'available');
  assert.deepEqual(calls, ['check', 'download']);
  updater.downloadUpdate = async token => {
    assert.equal(token.cancelled, false, 'Retries need a fresh cancellation token');
    updater.emit('update-downloaded', { version: '0.2.0' });
  };
  await manager.download();
  assert.deepEqual(calls, ['check', 'download', 'check']);
  assert.equal(manager.getState().phase, 'downloaded');
});

test('missing release metadata is an actionable failure and checking can recover', async () => {
  const { manager, updater } = fixture();
  updater.checkForUpdates = async () => { throw Object.assign(new Error('missing latest.yml'), { code: 'ERR_UPDATER_CHANNEL_FILE_NOT_FOUND' }); };
  await manager.check();
  assert.equal(manager.getState().error, 'manifest');
  assert.equal(manager.getState().errorOperation, 'check');
  updater.checkForUpdates = async () => {
    updater.emit('update-not-available');
    return {};
  };
  await manager.check();
  assert.equal(manager.getState().phase, 'up-to-date');
  assert.equal(manager.getState().error, null);
});

test('development, unpacked apps, unsupported platforms and missing configuration cannot install updates', async () => {
  const base = { packaged: true, platform: 'win32', executable: path.resolve('app', 'Octopus.exe'), resources: path.resolve('app', 'resources'), exists: () => true };
  assert.equal(updateAvailability(base), null);
  assert.equal(updateAvailability({ ...base, packaged: false }), 'development');
  assert.equal(updateAvailability({ ...base, platform: 'darwin' }), 'platform');
  assert.equal(updateAvailability({ ...base, exists: () => false }), 'not-installed');
  assert.equal(updateAvailability({ ...base, exists: file => !file.endsWith('app-update.yml') }), 'missing-config');
  const { manager, calls } = fixture({ unavailable: 'development', updater: null });
  await manager.check();
  await manager.download();
  await manager.install();
  assert.deepEqual(calls, []);
});

test('only the local main frame can control updates; renderer arguments cannot supply paths or URLs', async () => {
  const frame = { url: 'http://127.0.0.1:18777/' };
  const sender = { mainFrame: frame };
  const window = { webContents: sender, isDestroyed: () => false };
  const event = { sender, senderFrame: frame };
  const trusted = candidate => isTrustedUpdateSender(candidate, window, 'http://127.0.0.1:18777');
  assert(trusted(event));
  assert(!trusted({ ...event, sender: { mainFrame: frame } }));
  assert(!trusted({ ...event, senderFrame: { url: frame.url } }));
  frame.url = 'https://example.com/';
  assert(!trusted(event));
  frame.url = 'http://127.0.0.1:18777/';
  const handlers = new Map();
  const { manager, calls } = fixture();
  registerUpdateIPC({ handle: (name, action) => handlers.set(name, action) }, manager, trusted);
  assert.throws(() => handlers.get('octopus-update:install')({ ...event, senderFrame: null }), /Untrusted/);
  await handlers.get('octopus-update:download')(event, { url: 'https://example.com/malware.exe', path: 'C:\\Windows' });
  assert.deepEqual(calls, ['check', 'download']);
});
