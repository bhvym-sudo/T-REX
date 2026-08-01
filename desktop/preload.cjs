const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("trexDesktop", {
  appInfo: () => ipcRenderer.invoke("app:info"),
  saveFile: options => ipcRenderer.invoke("dialog:save", options),
  chooseFolder: () => ipcRenderer.invoke("dialog:folder"),
  savePDF: payload => ipcRenderer.invoke("report:pdf", payload),
  checkForUpdates: manual => ipcRenderer.invoke("updater:check", manual),
  downloadAndInstallUpdate: () => ipcRenderer.invoke("updater:download-install"),
  onUpdateEvent: callback => {
    const listener = (_event, payload) => callback(payload);
    ipcRenderer.on("updater:event", listener);
    return () => ipcRenderer.removeListener("updater:event", listener);
  },
  onMenuAction: callback => {
    const listener = (_event, action) => callback(action);
    ipcRenderer.on("menu:action", listener);
    return () => ipcRenderer.removeListener("menu:action", listener);
  }
});
