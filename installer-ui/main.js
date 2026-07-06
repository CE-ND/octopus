const { app, BrowserWindow, dialog, ipcMain, shell } = require('electron');
const fs = require('fs');
const os = require('os');
const path = require('path');
const { spawn } = require('child_process');

let mainWindow;

function defaultInstallDir(scope) {
  if (scope === 'allUsers') {
    return path.join(process.env.ProgramFiles || 'C:\\Program Files', 'Octopus');
  }

  return path.join(process.env.LOCALAPPDATA || path.join(os.homedir(), 'AppData', 'Local'), 'Programs', 'Octopus');
}

function payloadInstallerPath() {
  if (process.env.OCTOPUS_INSTALLER_PAYLOAD) {
    return process.env.OCTOPUS_INSTALLER_PAYLOAD;
  }

  if (app.isPackaged) {
    return path.join(process.resourcesPath, 'payload', 'Octopus Setup.exe');
  }

  return path.join(app.getAppPath(), '..', 'build', 'installer-ui', 'payload', 'Octopus Setup.exe');
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 900,
    height: 590,
    minWidth: 860,
    minHeight: 560,
    resizable: false,
    frame: false,
    title: 'Octopus Setup',
    backgroundColor: '#10231d',
    show: false,
    webPreferences: {
      preload: path.join(__dirname, 'preload.js'),
      contextIsolation: true,
      nodeIntegration: false,
    },
  });

  mainWindow.once('ready-to-show', () => {
    mainWindow.show();
  });

  mainWindow.loadFile(path.join(__dirname, 'renderer', 'index.html'));
}

ipcMain.handle('window:minimize', () => {
  mainWindow?.minimize();
});

ipcMain.handle('window:close', () => {
  mainWindow?.close();
});

ipcMain.handle('installer:getDefaults', () => {
  const payload = payloadInstallerPath();

  return {
    userName: os.userInfo().username,
    dataDir: path.join(os.homedir(), '.octopus'),
    currentUserDir: defaultInstallDir('currentUser'),
    allUsersDir: defaultInstallDir('allUsers'),
    payloadExists: fs.existsSync(payload),
    payload,
  };
});

ipcMain.handle('installer:selectDirectory', async (_event, currentPath) => {
  const result = await dialog.showOpenDialog(mainWindow, {
    title: '选择 Octopus 安装位置',
    defaultPath: currentPath || defaultInstallDir('currentUser'),
    properties: ['openDirectory', 'createDirectory'],
  });

  if (result.canceled || result.filePaths.length === 0) {
    return null;
  }

  return result.filePaths[0];
});

ipcMain.handle('installer:install', async (event, options) => {
  const payload = payloadInstallerPath();
  if (!fs.existsSync(payload)) {
    throw new Error(`安装 payload 不存在：${payload}`);
  }

  const installDir = options.installDir || defaultInstallDir(options.scope);
  const args = ['/S', options.scope === 'allUsers' ? '/allusers' : '/currentuser'];

  if (options.desktopShortcut === false) {
    args.push('/no-desktop-shortcut');
  }

  args.push(`/D=${installDir}`);

  event.sender.send('installer:install-log', {
    state: 'running',
    message: '正在写入 Octopus Desktop 文件',
  });

  return new Promise((resolve, reject) => {
    const child = spawn(payload, args, {
      windowsHide: true,
    });

    child.on('error', reject);
    child.on('close', (code) => {
      if (code === 0) {
        event.sender.send('installer:install-log', {
          state: 'complete',
          message: '安装完成',
        });
        resolve({
          installDir,
          executable: path.join(installDir, 'Octopus.exe'),
        });
        return;
      }

      reject(new Error(`安装程序退出，代码 ${code}`));
    });
  });
});

ipcMain.handle('installer:launch', async (_event, executable) => {
  if (!executable || !fs.existsSync(executable)) {
    return { ok: false };
  }

  const error = await shell.openPath(executable);
  return { ok: error === '', error };
});

app.whenReady().then(createWindow);

app.on('window-all-closed', () => {
  app.quit();
});
