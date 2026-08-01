const { app, BrowserWindow, Menu, dialog, ipcMain, shell } = require("electron");
const { spawn } = require("node:child_process");
const { createHash } = require("node:crypto");
const { request } = require("node:http");
const https = require("node:https");
const { createWriteStream, existsSync, mkdirSync, readFileSync, rmSync } = require("node:fs");
const { basename, dirname, join, resolve } = require("node:path");
const packageInfo = require("../package.json");

let mainWindow;
let backend;
let quitting = false;
let updateState = null;

function dataRoot() {
  if (!app.isPackaged) return process.cwd();
  return app.getPath("userData");
}

function iconPath() {
  if (app.isPackaged) return join(process.resourcesPath, "assets", "icon.png");
  return join(process.cwd(), "assets", "icon.png");
}

function updateFeedURL() {
  return (process.env.TREX_UPDATE_FEED_URL || packageInfo.trex?.updateFeedUrl || "").trim();
}

function compareVersions(left, right) {
  const a = String(left || "0").split(".").map(value => Number.parseInt(value, 10) || 0);
  const b = String(right || "0").split(".").map(value => Number.parseInt(value, 10) || 0);
  for (let index = 0; index < Math.max(a.length, b.length); index++) {
    const difference = (a[index] || 0) - (b[index] || 0);
    if (difference !== 0) return difference;
  }
  return 0;
}

function parseLatestYML(text, feedURL) {
  const version = text.match(/^version:\s*["']?([^"'\r\n]+)["']?/m)?.[1]?.trim();
  const sha512 = text.match(/^\s*sha512:\s*["']?([^"'\r\n]+)["']?/m)?.[1]?.trim();
  const fileURL = text.match(/^\s*url:\s*["']?([^"'\r\n]+)["']?/m)?.[1]?.trim()
    || text.match(/^path:\s*["']?([^"'\r\n]+)["']?/m)?.[1]?.trim();
  if (!version || !fileURL) {
    throw new Error("Update metadata is missing version or installer URL.");
  }
  return {
    version,
    sha512: sha512 || "",
    installerURL: new URL(fileURL, feedURL).toString()
  };
}

function fetchText(url, redirects = 0) {
  return new Promise((resolveText, reject) => {
    const client = url.startsWith("https:") ? https : require("node:http");
    const req = client.get(url, response => {
      if ([301, 302, 303, 307, 308].includes(response.statusCode) && response.headers.location && redirects < 5) {
        response.resume();
        fetchText(new URL(response.headers.location, url).toString(), redirects + 1).then(resolveText, reject);
        return;
      }
      if (response.statusCode < 200 || response.statusCode >= 300) {
        response.resume();
        reject(new Error(`Update server returned HTTP ${response.statusCode}`));
        return;
      }
      let body = "";
      response.setEncoding("utf8");
      response.on("data", chunk => { body += chunk; });
      response.on("end", () => resolveText(body));
    });
    req.on("error", reject);
    req.setTimeout(15000, () => {
      req.destroy(new Error("Update check timed out."));
    });
  });
}

function sendUpdateEvent(payload) {
  mainWindow?.webContents.send("updater:event", payload);
}

async function checkForUpdates(manual = false) {
  const feedURL = updateFeedURL();
  if (!feedURL) {
    if (manual) return { available: false, message: "Update feed URL is not configured." };
    return { available: false };
  }
  if (!app.isPackaged && process.env.TREX_ALLOW_DEV_UPDATES !== "1") {
    if (manual) return { available: false, message: "Updater runs only in packaged builds unless TREX_ALLOW_DEV_UPDATES=1 is set." };
    return { available: false };
  }
  const metadata = parseLatestYML(await fetchText(feedURL), feedURL);
  const currentVersion = app.getVersion();
  const available = compareVersions(metadata.version, currentVersion) > 0;
  updateState = available ? { ...metadata, feedURL, currentVersion } : null;
  if (available) {
    sendUpdateEvent({ type: "available", currentVersion, newVersion: metadata.version });
  } else if (manual) {
    sendUpdateEvent({ type: "none", currentVersion });
  }
  return { available, currentVersion, newVersion: metadata.version };
}

function downloadFile(url, targetPath, onProgress, redirects = 0) {
  return new Promise((resolveDownload, reject) => {
    const client = url.startsWith("https:") ? https : require("node:http");
    const req = client.get(url, response => {
      if ([301, 302, 303, 307, 308].includes(response.statusCode) && response.headers.location && redirects < 5) {
        response.resume();
        downloadFile(new URL(response.headers.location, url).toString(), targetPath, onProgress, redirects + 1).then(resolveDownload, reject);
        return;
      }
      if (response.statusCode < 200 || response.statusCode >= 300) {
        response.resume();
        reject(new Error(`Installer download returned HTTP ${response.statusCode}`));
        return;
      }
      const total = Number(response.headers["content-length"]) || 0;
      let received = 0;
      mkdirSync(dirname(targetPath), { recursive: true });
      const output = createWriteStream(targetPath);
      response.on("data", chunk => {
        received += chunk.length;
        if (total > 0) onProgress(Math.round(received / total * 100));
      });
      response.pipe(output);
      output.on("finish", () => output.close(() => resolveDownload(targetPath)));
      output.on("error", reject);
    });
    req.on("error", reject);
  });
}

function verifySha512(path, expected) {
  if (!expected) return true;
  const digest = createHash("sha512").update(readFileSync(path)).digest("base64");
  return digest === expected;
}

async function downloadAndInstallUpdate() {
  if (!updateState) await checkForUpdates(true);
  if (!updateState) throw new Error("No update is currently available.");
  const updatesDir = join(app.getPath("userData"), "updates");
  mkdirSync(updatesDir, { recursive: true });
  const installerPath = resolve(updatesDir, basename(new URL(updateState.installerURL).pathname) || `T-REX-OSINT-${updateState.version}.exe`);
  try {
    rmSync(installerPath, { force: true });
  } catch {}
  sendUpdateEvent({ type: "download-started", newVersion: updateState.version });
  await downloadFile(updateState.installerURL, installerPath, progress => {
    sendUpdateEvent({ type: "download-progress", progress, newVersion: updateState.version });
  });
  if (!verifySha512(installerPath, updateState.sha512)) {
    throw new Error("Downloaded installer checksum did not match latest.yml.");
  }
  sendUpdateEvent({ type: "installing", progress: 100, newVersion: updateState.version });
  await requestBackendShutdown();
  await new Promise((resolveInstall, reject) => {
    const installer = spawn(installerPath, ["/S"], {
      detached: false,
      windowsHide: true,
      stdio: "ignore"
    });
    installer.on("error", reject);
    installer.on("exit", code => {
      if (code === 0) resolveInstall();
      else reject(new Error(`Installer exited with code ${code}.`));
    });
  });
  sendUpdateEvent({ type: "installed", newVersion: updateState.version });
  return { installed: true, version: updateState.version };
}

function backendCommand() {
  if (process.env.TREX_BACKEND_URL) return null;
  if (app.isPackaged) {
    return {
      command: join(process.resourcesPath, "backend", "trex-backend.exe"),
      args: []
    };
  }
  if (process.env.TREX_USE_COMPILED_BACKEND === "1") {
    const compiledBackend = join(process.cwd(), "bin", "trex-backend.exe");
    if (existsSync(compiledBackend)) {
      return { command: compiledBackend, args: [] };
    }
  }
  return { command: "go", args: ["run", "./backend/cmd/trex"] };
}

function searchWorkerEnvironment() {
  if (app.isPackaged) {
    return {
      TREX_SEARCH_WORKER_EXE: join(process.resourcesPath, "python_worker", "search.exe"),
      TREX_PYTHON_WORKER: ""
    };
  }

  const sourceWorker = join(process.cwd(), "python_worker", "search_timeline.py");
  if (existsSync(sourceWorker)) {
    return { TREX_PYTHON_WORKER: sourceWorker, TREX_SEARCH_WORKER_EXE: "" };
  }

  const executableWorker = join(process.cwd(), "python_worker", "search.exe");
  if (existsSync(executableWorker)) {
    return { TREX_SEARCH_WORKER_EXE: executableWorker, TREX_PYTHON_WORKER: "" };
  }

  return { TREX_PYTHON_WORKER: sourceWorker, TREX_SEARCH_WORKER_EXE: "" };
}

function startBackend() {
  const target = backendCommand();
  if (!target) return;
  const root = dataRoot();
  const workerEnvironment = searchWorkerEnvironment();
  mkdirSync(root, { recursive: true });
  backend = spawn(target.command, target.args, {
    cwd: app.isPackaged ? root : process.cwd(),
    windowsHide: true,
    env: { ...process.env, TREX_DATA_DIR: root, ...workerEnvironment },
    stdio: app.isPackaged ? "ignore" : "inherit"
  });
}

function requestBackendShutdown() {
  return new Promise(resolve => {
    if (!backend || backend.killed) {
      resolve();
      return;
    }
    const req = request({
      hostname: "127.0.0.1",
      port: 8787,
      path: "/api/shutdown",
      method: "POST",
      timeout: 1200
    }, res => {
      res.resume();
      res.on("end", resolve);
    });
    req.on("timeout", () => {
      req.destroy();
      resolve();
    });
    req.on("error", resolve);
    req.end();
  });
}

function killBackendTree() {
  if (!backend || backend.killed || !backend.pid) return;
  if (process.platform === "win32") {
    spawn("taskkill", ["/PID", String(backend.pid), "/T", "/F"], {
      windowsHide: true,
      stdio: "ignore"
    });
    return;
  }
  backend.kill();
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1320,
    height: 920,
    minWidth: 1120,
    minHeight: 780,
    backgroundColor: "#101214",
    show: false,
    title: "T-REX OSINT",
    icon: iconPath(),
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
  Menu.setApplicationMenu(null);
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
  packaged: app.isPackaged,
  version: app.getVersion(),
  updateFeedConfigured: Boolean(updateFeedURL())
}));

ipcMain.handle("updater:check", async (_event, manual = false) => {
  try {
    return await checkForUpdates(Boolean(manual));
  } catch (error) {
    sendUpdateEvent({ type: "error", message: error.message });
    return { available: false, error: error.message };
  }
});

ipcMain.handle("updater:download-install", async () => {
  try {
    return await downloadAndInstallUpdate();
  } catch (error) {
    sendUpdateEvent({ type: "error", message: error.message });
    throw error;
  }
});

app.whenReady().then(() => {
  startBackend();
  buildMenu();
  setTimeout(createWindow, process.env.TREX_BACKEND_URL ? 0 : 700);
  setTimeout(() => checkForUpdates(false).catch(error => {
    sendUpdateEvent({ type: "error", message: error.message });
  }), 2500);
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") app.quit();
});

app.on("before-quit", event => {
  if (quitting || !backend || backend.killed) return;
  event.preventDefault();
  quitting = true;
  requestBackendShutdown().finally(() => {
    const fallback = setTimeout(killBackendTree, 1800);
    backend.once("exit", () => {
      clearTimeout(fallback);
      app.quit();
    });
    setTimeout(() => app.quit(), 2300);
  });
});
