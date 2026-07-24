const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("trexDesktop", {
  appInfo: () => ipcRenderer.invoke("app:info"),
  saveFile: options => ipcRenderer.invoke("dialog:save", options),
  chooseFolder: () => ipcRenderer.invoke("dialog:folder"),
  savePDF: payload => ipcRenderer.invoke("report:pdf", payload),
  onMenuAction: callback => {
    const listener = (_event, action) => callback(action);
    ipcRenderer.on("menu:action", listener);
    return () => ipcRenderer.removeListener("menu:action", listener);
  }
});
