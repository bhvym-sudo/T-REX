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

```powershell
npm.cmd run build:backend
npm.cmd run build:desktop
```

Standalone desktop output is written under `release`.

## Workspace data

Runtime data stays beside the project/application executable, matching the original local-workspace behavior:

- `sessions/x_edge_profile`
- `logs`
- `exports`
- `config/runtime.json`

Do not commit session data.
