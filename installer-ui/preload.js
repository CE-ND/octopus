const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('octopusSetup', {
  minimize: () => ipcRenderer.invoke('window:minimize'),
  close: () => ipcRenderer.invoke('window:close'),
  getDefaults: () => ipcRenderer.invoke('installer:getDefaults'),
  selectDirectory: (currentPath) => ipcRenderer.invoke('installer:selectDirectory', currentPath),
  install: (options) => ipcRenderer.invoke('installer:install', options),
  launch: (executable) => ipcRenderer.invoke('installer:launch', executable),
  onInstallLog: (callback) => {
    const listener = (_event, payload) => callback(payload);
    ipcRenderer.on('installer:install-log', listener);
    return () => ipcRenderer.removeListener('installer:install-log', listener);
  },
});
