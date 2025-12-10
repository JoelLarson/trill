# Trill (Agent Manager)

Trill is a tiny agent manager: a Go HTTP service plus a single-page UI that keeps track of agent chat sessions. It exists to call your threads back together when you need them. Codex is the first working model (via the `codex` CLI); additional backends are planned.

## Quick start
- Prerequisites: Go 1.22+ and the `codex` CLI on your `PATH`.
- Run locally: `go run ./cmd/trill` (app on `:8080`, observability on `:9090`).
- Open the UI: http://localhost:8080/ to start or manage conversations.
- Try the API:
 ```sh
 curl -X POST http://localhost:8080/send \
 -H 'Content-Type: application/json' \
 -d '{"id":"","message":"hello"}'
 ```

## Installation
- Build from source:
  ```sh
  go build -o trill ./cmd/trill
  ./trill -port :8080
  ```
- Or install to `$GOBIN`:
  ```sh
  go install ./cmd/trill
  trill -port :8080
  ```
- Requirements: `codex` CLI must be available; other model backends will be added in future versions.

## Usage
- UI: embedded SPA served at `/` for starting, chatting, inspecting, and closing sessions.
- Observability UI: served at `/` on the observability port (default `:9090`) with a live event feed of prompts, plan steps, Codex inputs, and outputs.
- API (JSON):
  - `POST /start` → `{ "id": "" }` (placeholder; IDs appear after the first send)
  - `POST /send` with `{ "id": "<session|empty>", "message": "<text>" }` → reply + session metadata
  - `GET /list` → `["sess-1", "sess-2", ...]`
  - `GET /conversation?id=<session>` → full conversation payload
  - `POST /close` with `{ "id": "<session>" }` → 200 on success
 - `POST /run` with `{ "prompt": "<text>" }` → lightweight plan/execute loop, returns `{"result": "<text>" }`

## Configuration
- Port: `PORT` env var or `-port` flag (default `:8080`).
- Observability port: `OBS_PORT` env var or `-obs-port` flag (default `:9090`).
- Model: currently fixed to the local `codex` CLI; future releases will add model selection.
- Storage: in-memory only; restart clears sessions.

## Output and behavior
- API responses are JSON; UI fetches the same endpoints.
- Raw Codex output is preserved per call (viewable in the UI under each assistant reply).
- Exit codes: standard Go server; non-zero on fatal startup errors.

## Troubleshooting
- “codex error”: ensure the `codex` CLI is installed and on `PATH`; confirm it can run interactively.
- Port conflicts: set `PORT`/`-port` to a free address.
- Empty replies: check Codex CLI output (UI exposes raw output in a collapsible panel).

## Compatibility and versioning
- Developed and tested with Go 1.22 on Unix-like systems; Windows should work but is less exercised.
- No stability guarantees yet; expect breaking changes while backends and API evolve.

## Security and data handling
- No telemetry. No authentication is built in; run behind your own proxy or on trusted networks.
- Conversations live in memory only; restarting the process clears them.

## Contributing and support
- Issues and ideas: open a ticket or PR with repro steps and the command you ran.
- Tests: `go test ./...`.
