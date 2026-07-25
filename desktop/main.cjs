const { app, BrowserWindow, Menu, dialog, ipcMain, shell } = require("electron");
const { spawn } = require("node:child_process");
const { existsSync, mkdirSync } = require("node:fs");
const { dirname, join } = require("node:path");

let mainWindow;
let backend;

function dataRoot() {
  if (!app.isPackaged) return process.cwd();
  return dirname(process.execPath);
}

function backendCommand() {
  if (process.env.TREX_BACKEND_URL) return null;
  if (app.isPackaged) {
    return {
      command: join(process.resourcesPath, "backend", "trex-backend.exe"),
      args: []
    };
  }
  const compiledBackend = join(process.cwd(), "bin", "trex-backend.exe");
  if (existsSync(compiledBackend)) {
    return { command: compiledBackend, args: [] };
  }
  return { command: "go", args: ["run", "./backend/cmd/trex"] };
}

function startBackend() {
  const target = backendCommand();
  if (!target) return;
  const root = dataRoot();
  const pythonWorker = app.isPackaged
    ? join(process.resourcesPath, "python_worker", "search_timeline.py")
    : join(process.cwd(), "python_worker", "search_timeline.py");
  mkdirSync(root, { recursive: true });
  backend = spawn(target.command, target.args, {
    cwd: app.isPackaged ? root : process.cwd(),
    windowsHide: true,
    env: { ...process.env, TREX_DATA_DIR: root, TREX_PYTHON_WORKER: pythonWorker },
    stdio: app.isPackaged ? "ignore" : "inherit"
  });
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1320,
    height: 860,
    minWidth: 1120,
    minHeight: 720,
    backgroundColor: "#101214",
    show: false,
    title: "T-REX OSINT",
    webPreferences: {
      preload: join(__dirname, "preload.cjs"),
      contextIsolation: true,
      nodeIntegration: false
    }
  });
  mainWindow.loadFile(join(__dirname, "renderer", "index.html"));
  mainWindow.once("ready-to-show", () => mainWindow.show());
  mainWindow.webContents.setWindowOpenHandler(({ url }) => {
    if (/^https?:/.test(url)) shell.openExternal(url);
    return { action: "deny" };
  });
}

function emitMenu(action) {
  mainWindow?.webContents.send("menu:action", action);
}

function buildMenu() {
  const template = [
    {
      label: "File",
      submenu: [
        { label: "Export Data", accelerator: "CmdOrCtrl+Shift+E", click: () => emitMenu("export-data") },
        { label: "Export Tweet Analytics", click: () => emitMenu("export-analytics") },
        { label: "Export Authors Data", click: () => emitMenu("export-authors") },
        { type: "separator" },
        { label: "Generate Report", accelerator: "CmdOrCtrl+Shift+R", click: () => emitMenu("generate-report") },
        { type: "separator" },
        { role: "quit" }
      ]
    },
    {
      label: "Edit",
      submenu: [
        { role: "copy" },
        { role: "selectAll" }
      ]
    },
    {
      label: "View",
      submenu: [
        { label: "Logs", accelerator: "CmdOrCtrl+L", click: () => emitMenu("logs") },
        { type: "separator" },
        { role: "reload" },
        { role: "toggleDevTools" },
        { type: "separator" },
        { role: "resetZoom" },
        { role: "zoomIn" },
        { role: "zoomOut" }
      ]
    }
  ];
  Menu.setApplicationMenu(Menu.buildFromTemplate(template));
}

ipcMain.handle("dialog:save", async (_event, options) => {
  const result = await dialog.showSaveDialog(mainWindow, options);
  return result.canceled ? "" : result.filePath;
});

ipcMain.handle("dialog:folder", async () => {
  const result = await dialog.showOpenDialog(mainWindow, { properties: ["openDirectory", "createDirectory"] });
  return result.canceled ? "" : result.filePaths[0];
});

ipcMain.handle("report:pdf", async (_event, { html, defaultPath }) => {
  const choice = await dialog.showSaveDialog(mainWindow, {
    defaultPath,
    filters: [{ name: "PDF Files", extensions: ["pdf"] }]
  });
  if (choice.canceled || !choice.filePath) return "";
  const reportWindow = new BrowserWindow({ show: false, webPreferences: { sandbox: true } });
  await reportWindow.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(html)}`);
  const data = await reportWindow.webContents.printToPDF({
    printBackground: true,
    pageSize: "A4",
    margins: { top: 0.35, bottom: 0.35, left: 0.35, right: 0.35 }
  });
  require("node:fs").writeFileSync(choice.filePath, data);
  reportWindow.destroy();
  return choice.filePath;
});

ipcMain.handle("app:info", () => ({
  backendURL: process.env.TREX_BACKEND_URL || "http://127.0.0.1:8787",
  dataRoot: dataRoot(),
  packaged: app.isPackaged
}));

app.whenReady().then(() => {
  startBackend();
  buildMenu();
  setTimeout(createWindow, process.env.TREX_BACKEND_URL ? 0 : 700);
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});

app.on("before-quit", () => {
  if (backend && !backend.killed) backend.kill();
});
