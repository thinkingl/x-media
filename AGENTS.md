# AGENTS.md

## Project overview

X-Media: Go media streaming platform with Vue3 web UI. Supports MP4/RTSP inputs, RTMP/RTSP/HTTP-FLV outputs with pipe-based routing.

## Structure

- `server/` — Go backend
  - `cmd/main.go` — entrypoint (`-c config.yaml` for config, `-s static_path` for frontend)
  - `internal/api/` — Gin HTTP handlers (inputs, outputs, pipes, stats, logs)
  - `internal/service/` — business logic
  - `internal/repository/` — GORM data access (SQLite)
  - `internal/media/` — media engine: file/RTSP inputs, RTMP/RTSP/HTTP-FLV outputs
  - `internal/model/` — data models (Input, Output, Pipe, Stat)
  - `internal/config/` — YAML config loader
  - `pkg/` — shared utilities (logger with lumberjack rotation, errors, utils)
  - `test/fixtures/test.mp4` — test media file
- `web/` — Vue3 + TypeScript frontend (Vite, Element Plus, Pinia)
  - `src/views/` — Dashboard, Inputs, Outputs, Pipes, Logs
  - `src/stores/` — Pinia stores
  - `src/api/` — Axios API layer
- `docker/` — Dockerfile (multi-stage: Go backend + Node frontend) + config.yaml
- `docker-compose.yml` — single-service deployment
- `docs/design.md` — full architecture design doc

## Commands

### Backend (from `server/`)

```bash
make build         # CGO_ENABLED=1 required for SQLite
make test          # go test ./... -v -count=1
make test-coverage # generates coverage.out and coverage.html
make lint          # go vet ./...
make fmt           # go fmt ./...
make deps          # go mod tidy
make run           # build + start with -c config.yaml
```

### Frontend (from `web/`)

```bash
npm install
npm run dev        # dev server on :18091, proxies /api and /uploads to :18090
npm run build      # vue-tsc + vite build, outputs to dist/
```

### Docker (from project root)

```bash
docker-compose up -d
docker-compose down
```

## Key tech stack

- Go 1.21+, Gin, GORM + SQLite, zap + lumberjack (log rotation)
- Vue3, TypeScript, Vite, Element Plus, Pinia, Vue Router, Axios
- Testing: `github.com/stretchr/testify` (assert + mock)
- CGO required (mattn/go-sqlite3)

## Testing patterns

- Tests use testify mocks for repos and media engine
- Media tests: `TestMain` in `internal/media/engine_test.go` initializes logger before tests
- File input tests reference `../../test/fixtures/test.mp4` (relative to test file) via `testFixturePath` helper
- `integration_test.go` exists but uses `httptest.NewServer` — not a full E2E test requiring external services

## Architecture notes

- Layered: API handlers → Services → Repositories → GORM/SQLite
- Media engine is an interface (`internal/media/engine.go`) with `DefaultMediaEngine` implementation
- Services take repo + engine dependencies via constructor
- Config loaded from YAML via `-c` flag (default: `config.yaml`)
- Frontend served as static files via `-s` flag (SPA fallback via `NoRoute`)
- CORS middleware allows all origins (`*`)
- Unified JSON response: `{"code": N, "message": "...", "data": ...}`

## Environment quirks

- This machine has 10GB RAM — avoid running multiple heavy operations simultaneously
- Swap is often full; run `go build` and `npm install` separately, not in parallel
- `go vet` is the only linter (no golangci-lint configured)
- Frontend dev server port is 18091 (not 3000); proxy target is 18090 (not 8080) — see `web/vite.config.ts`
- Dev config: `server/config_dev.yaml` sets backend to `:18090` (matches frontend proxy); default config uses `:8080`
