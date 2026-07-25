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

## Workspace data

Runtime data stays beside the project/application executable, matching the original local-workspace behavior:

- `sessions/x_edge_profile`
- `logs`
- `exports`
- `config/runtime.json`

Do not commit session data.
