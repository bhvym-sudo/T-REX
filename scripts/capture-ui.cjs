const { app, BrowserWindow, ipcMain } = require("electron");
const { spawn } = require("node:child_process");
const { join } = require("node:path");

app.whenReady().then(async () => {
  ipcMain.handle("app:info", () => ({
    backendURL: "http://127.0.0.1:8787",
    dataRoot: process.cwd(),
    packaged: false
  }));
  const backend = spawn(join(process.cwd(), "bin", "trex-backend.exe"), [], {
    cwd: process.cwd(),
    windowsHide: true,
    env: { ...process.env, TREX_DATA_DIR: process.cwd() }
  });
  const window = new BrowserWindow({
    width: 1320,
    height: 860,
    show: false,
    webPreferences: {
      preload: join(process.cwd(), "desktop", "preload.cjs"),
      contextIsolation: true
    }
  });
  await window.loadFile(join(process.cwd(), "desktop", "renderer", "index.html"));
  await new Promise(resolve => setTimeout(resolve, 1500));
  await window.webContents.executeJavaScript("openWorkspace();");
  await new Promise(resolve => setTimeout(resolve, 400));
  const image = await window.webContents.capturePage();
  require("node:fs").writeFileSync(join(process.cwd(), "ui-preview.png"), image.toPNG());
  backend.kill();
  window.destroy();
  app.quit();
});
