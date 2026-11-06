const fs = require('node:fs');
const path = require('node:path');

function updateAvailability({ platform, packaged, executable, resources, exists = fs.existsSync }) {
  if (!packaged) return 'development';
  // macOS 当前采用 ad-hoc 签名，尚不满足 Squirrel 的签名升级要求。
  if (platform !== 'win32') return 'platform';
  if (!exists(path.join(path.dirname(executable), 'Uninstall Octopus.exe'))) return 'not-installed';
  if (!exists(path.join(resources, 'app-update.yml'))) return 'missing-config';
  return null;
}

function isTrustedUpdateSender(event, window, backendURL) {
  if (!window || window.isDestroyed() || event.sender !== window.webContents) return false;
  if (!event.senderFrame || event.senderFrame !== event.sender.mainFrame) return false;
  try {
    return new URL(event.senderFrame.url).origin === new URL(backendURL).origin;
  } catch {
    return false;
  }
}

function updateErrorCode(error, operation) {
  const code = String(error?.code || '');
  if (/SHA|CHECKSUM|SIGNATURE/.test(code)) return 'integrity';
  if (/CHANNEL_FILE_NOT_FOUND|LATEST_VERSION_NOT_FOUND|INVALID_UPDATE_INFO|NO_FILES_PROVIDED/.test(code)) return 'manifest';
  if (operation === 'install') return 'install';
  return operation === 'download' ? 'download' : 'check';
}

/** Renderer 只能操作更新状态；下载地址、安装程序和目录都由主进程管理。 */
function createDesktopUpdater({ updater, currentVersion, unavailable = null, installDirectory, publishState, beforeInstall, installFailed, logger = console }) {
  let state = {
    revision: 0,
    supported: unavailable === null,
    unavailable,
    currentVersion,
    phase: unavailable ? 'unsupported' : 'idle',
    version: null,
    progress: null,
    error: null,
    errorOperation: null,
    checkedAt: null,
  };
  let operation = null;
  let cancellationToken = null;
  let downloadAvailable = false;
  let downloaded = false;

  const snapshot = () => ({ ...state, progress: state.progress ? { ...state.progress } : null });
  function change(patch) {
    state = { ...state, ...patch, revision: state.revision + 1 };
    publishState(snapshot());
  }
  function fail(error, action = operation || 'check') {
    logger.error('Desktop update failed:', error);
    change({ phase: 'error', error: updateErrorCode(error, action), errorOperation: action });
  }

  if (state.supported) {
    updater.autoDownload = false;
    // 只有用户点击“重启并更新”才安装，普通退出不会中断用户工作。
    updater.autoInstallOnAppQuit = false;
    updater.allowPrerelease = false;
    updater.allowDowngrade = false;
    updater.disableWebInstaller = true;
    updater.installDirectory = installDirectory;
    updater.on('update-available', info => {
      downloadAvailable = true;
      change({ phase: 'available', version: info.version, checkedAt: new Date().toISOString() });
    });
    updater.on('update-not-available', () => {
      downloadAvailable = false;
      change({ phase: 'up-to-date', version: null, checkedAt: new Date().toISOString() });
    });
    updater.on('download-progress', progress => {
      if (state.phase !== 'downloading') return;
      const number = value => Number.isFinite(value) ? Math.max(0, value) : 0;
      change({ progress: {
        percent: Math.min(100, number(progress.percent)),
        transferred: number(progress.transferred),
        total: number(progress.total),
        bytesPerSecond: number(progress.bytesPerSecond),
      } });
    });
    // electron-updater 完成 SHA-512 校验后才发送此事件。
    updater.on('update-downloaded', info => {
      downloaded = true;
      change({ phase: 'downloaded', version: info.version, progress: null, error: null, errorOperation: null });
    });
    updater.on('error', error => {
      // check/download 的拒绝由调用处统一处理，安装错误还可能异步发生。
      if (operation === 'check' || operation === 'download') return;
      const installing = state.phase === 'installing';
      fail(error, installing ? 'install' : 'check');
      if (installing) Promise.resolve(installFailed?.()).catch(failure => logger.error('Unable to resume after update failure:', failure));
    });
  }

  async function check() {
    if (!state.supported || operation || downloaded || state.phase === 'installing') return snapshot();
    operation = 'check';
    downloadAvailable = false;
    change({ phase: 'checking', error: null, errorOperation: null, progress: null });
    try {
      const result = await updater.checkForUpdates();
      if (!result) throw new Error('Desktop updater is inactive');
      cancellationToken = result.cancellationToken;
    } catch (error) {
      fail(error, 'check');
    } finally {
      operation = null;
    }
    return snapshot();
  }

  async function download() {
    if (!state.supported || operation || downloaded || state.phase === 'installing') return snapshot();
    // 取消后的 token 不可复用；重新检查也避免重试时下载已被撤回的版本。
    if (!cancellationToken || cancellationToken.cancelled || !downloadAvailable) await check();
    if (operation || !downloadAvailable || !cancellationToken) return snapshot();
    operation = 'download';
    change({ phase: 'downloading', error: null, errorOperation: null, progress: null });
    try {
      await updater.downloadUpdate(cancellationToken);
    } catch (error) {
      if (cancellationToken.cancelled) {
        change({ phase: 'available', progress: null });
      } else {
        fail(error, 'download');
      }
    } finally {
      operation = null;
    }
    return snapshot();
  }

  async function install() {
    if (!state.supported || operation || !downloaded || state.phase === 'installing') return snapshot();
    operation = 'install';
    change({ phase: 'installing', error: null, errorOperation: null });
    try {
      await beforeInstall();
      // NSIS 的 --updated 保留安装信息；/D 指向当前运行程序的目录。
      updater.quitAndInstall(true, true);
    } catch (error) {
      fail(error, 'install');
      try { await installFailed?.(); } catch (failure) { logger.error('Unable to resume after update failure:', failure); }
    } finally {
      operation = null;
    }
    return snapshot();
  }

  function cancel() {
    if (operation === 'download' && state.phase === 'downloading') {
      change({ phase: 'cancelling' });
      cancellationToken.cancel();
    }
    return snapshot();
  }

  return { getState: snapshot, check, download, cancel, install };
}

function registerUpdateIPC(ipcMain, manager, trusted) {
  for (const [name, action] of Object.entries({ state: manager.getState, check: manager.check, download: manager.download, cancel: manager.cancel, install: manager.install })) {
    ipcMain.handle(`octopus-update:${name}`, event => {
      if (!trusted(event)) throw new Error('Untrusted desktop update request');
      return action();
    });
  }
}

module.exports = { createDesktopUpdater, updateAvailability, isTrustedUpdateSender, registerUpdateIPC };
