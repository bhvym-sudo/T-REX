# T-REX OSINT

T-REX is the Go + Electron successor to the original Python/PySide RTE OSINT prototype.

## Architecture

```text
Electron desktop UI
        |
        | REST commands + WebSocket events
        v
Go backend
  - X session bootstrap
  - direct GraphQL collection
  - cursor pagination
  - logging and exports
  - local application state
        |
        v
Future Python ML worker
```

The browser is used only to create or refresh an authenticated X session and capture the web authorization metadata. Collection is performed through direct GraphQL requests.

## Development

```powershell
npm.cmd install
go mod download
npm.cmd run dev
```

The Go API listens on `127.0.0.1:8787`. Electron starts it automatically unless `TREX_BACKEND_URL` points at an already-running backend.

## Build

Full ordered release build:

```powershell
.\scripts\build_setup.bat
```

This script runs the build in order and stops immediately if any step fails:

1. Go backend executable
2. Python SearchTimeline worker executable
3. Electron Windows setup

Create the Go backend binary:

```powershell
go build -o bin\trex-backend.exe .\backend\cmd\trex
```

Create the Python SearchTimeline worker executable with Nuitka:

```powershell
python -m nuitka `
  --standalone `
  --onefile `
  --windows-console-mode=disable `
  --output-dir=python_worker `
  --output-filename=search.exe `
  --remove-output `
  --include-package=playwright `
  python_worker/search_timeline.py
```

Build the Windows setup:

```powershell
npm.cmd run dist
```

The setup output is written under `dist`.

Production packages include the already-built binaries:

- `bin/trex-backend.exe`
- `python_worker/search.exe`
- `assets/icon.png`
- `assets/icon.ico`

In development, Electron runs `python_worker/search_timeline.py` when it exists. If the Python source file is absent, Electron falls back to `python_worker/search.exe`, matching the production layout.

## Auto updates

T-REX checks for updates on startup in packaged builds. Development builds do not auto-check unless you set:

```powershell
$env:TREX_ALLOW_DEV_UPDATES="1"
```

Configure the update feed before building a release:

1. Open `package.json`.
2. Set `trex.updateFeedUrl` to the full URL of your `latest.yml` file.

Example:

```json
"trex": {
  "updateFeedUrl": "https://github.com/bhvym-sudo/T-REX/releases/latest/download/latest.yml"
}
```

Also update the Electron Builder publish URL:

```json
"publish": [
  {
    "provider": "generic",
    "url": "https://github.com/bhvym-sudo/T-REX/releases/latest/download/"
  }
]
```

When you create a new release, upload these files from `dist` to the same release folder/server:

- `latest.yml`
- `T-REX-OSINT-Setup-<version>-x64.exe`

For GitHub Releases, upload both files as release assets. For example, a release can contain:

```text
latest.yml
T-REX-OSINT-Setup-0.5.0-x64.exe
```

The app checks:

```text
https://github.com/bhvym-sudo/T-REX/releases/latest/download/latest.yml
```

GitHub redirects that URL to the newest release marked as latest.

Typical flow:

1. Increase `version` in `package.json`, for example from `0.1.0` to `0.5.0`.
2. Run:

```powershell
.\scripts\build_setup.bat
```

3. Upload the new setup `.exe`.
4. Upload the matching `latest.yml`.

When an installed user opens T-REX:

1. The app checks `latest.yml`.
2. If the release version is newer, it shows an update dialog.
3. The user clicks `Download Update`.
4. T-REX downloads the setup and shows progress.
5. T-REX runs the installer silently.
6. When installation completes, the user is told to close and manually reopen the app.

## Workspace data

Runtime data stays beside the project/application executable, matching the original local-workspace behavior:

- `sessions/x_edge_profile`
- `logs`
- `exports`
- `config/runtime.json`

Do not commit session data.
