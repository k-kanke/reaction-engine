# Backend

Reaction Engine backend workspace.

This backend is planned as:

- Language: Go
- Runtime: Go 1.26
- HTTP: standard `net/http` + lightweight router
- WebSocket: `nhooyr.io/websocket` or `gorilla/websocket`
- Deploy: Cloud Run
- Validation: Go structs + JSON Schema / OpenAPI contract
- DB: `pgx` against Cloud SQL for PostgreSQL
- Redis: `go-redis` against Memorystore for Redis
- Google Cloud SDKs: `cloud.google.com/go/pubsub`, `cloud.google.com/go/storage`, Speech-to-Text, Vertex AI / Gemini client

## Directory Map

```text
backend/
  cmd/
    gateway/               Cloud Run WebSocket Gateway entrypoint
    media-api/             Media API entrypoint
    writer/                Durable Writer entrypoint
    image-analysis-worker/ Image Analysis Worker entrypoint
    post-session-job/      Post-session analysis job entrypoint
  internal/
    gateway/        Cloud Run WebSocket Gateway
    realtime/       システム演算層: Memorystore windows, signal summaries, cooldown, feedback rules
    pubsub/         Pub/Sub publisher/subscriber adapters and event envelopes
    writer/         Pub/Sub -> Cloud Storage / Cloud SQL durable writer
    storage/        Cloud Storage raw JSONL / frames / clips helpers
    db/             Cloud SQL PostgreSQL client, schema, repositories
    media/          signed upload URL, media_ref, capture snapshot handling
    imageanalysis/  image + feature_snapshot -> baseline / visual summary
    analysis/       post-session analysis logic
    contract/       Shared Go structs and JSON Schema / OpenAPI contracts
    config/         Environment config loading and validation
    observability/  Logging, metrics, tracing helpers
  migrations/       Cloud SQL migrations in golang-migrate format
  deploy/           Cloud Run and Google Cloud deployment files
  scripts/          Local backend utility scripts
  tests/            Backend tests
```

## Initial Service Split

- `r-gateway`: WebSocket Gateway, Memorystore writes, signal summary / decision log, Pub/Sub publish, feedback return.
- `r-media-api`: signed upload URL, media_ref, capture_snapshots, media_uploaded publish.
- `r-writer`: Pub/Sub consumer, Cloud Storage JSONL writer, Cloud SQL signal summary / decision log writer.
- `r-image-worker`: image + capture_snapshots.feature_snapshot -> participant_baseline / visual_summary.
- `r-post-session-job`: Cloud Run Job for post-session analysis.

Keep high-frequency compact raw feature events out of Cloud SQL. Store compact raw features as JSONL chunks in Cloud Storage and put metadata, signal summaries, decision logs, feedback history, and reports in Cloud SQL.

These logical service names are fixed and used identically in local Docker Compose and in Cloud Run.

## Local Ports

Fixed for local Docker Compose. Cloud Run uses `PORT` injected by the platform instead.

| service                 | local address                       |
| ----------------------- | ------------------------------------ |
| gateway                 | http://localhost:8080                |
| media-api               | http://localhost:8081                |
| writer                  | internal only (no exposed port)      |
| image-analysis-worker   | internal only, or http://localhost:8082/debug |
| postgres                | localhost:5432                       |
| redis                   | localhost:6379                       |

## Local Environment

Local env values are fixed in `backend/.env.example` (tracked) and instantiated as `backend/.env.local` (untracked, gitignored) for actual local runs:

```bash
cp backend/.env.example backend/.env.local
```

`backend/.env.local` is the file Docker Compose and locally-run services read. Its variable names must stay consistent with `architecture.md`; only the values differ between local and Cloud Run so that env is the sole switch between environments.

## Migration Naming

Use `golang-migrate/migrate` standard SQL file names.

```text
backend/migrations/
  000001_initial_schema.up.sql
  000001_initial_schema.down.sql
  000002_add_capture_snapshots.up.sql
  000002_add_capture_snapshots.down.sql
```

Rules:

- Use a 6-digit sequence number.
- Use snake_case for the description.
- Do not include dates in migration file names.
- Always create `up.sql` and `down.sql` as a pair.
- Track migration creation timing through Git history.

## Running Migrations

Migrations run via the `migrate/migrate` Docker image (`migrate` service in
`compose.yaml`, kept out of `docker compose up` by the `tools` profile):

```bash
docker compose up -d postgres
cd backend
make migrate-up
make migrate-down   # rolls back the most recent migration
```

## Getting Started

```bash
cd backend
go test ./...
make build
```

Run a single service locally (each exposes `GET /healthz`, except `writer` and `post-session-job`):

```bash
make run-gateway       # http://localhost:8080/healthz
make run-media-api     # http://localhost:8081/healthz
make run-writer
make run-image-worker  # http://localhost:8082/debug/healthz
make run-post-session-job
```

## Docker Compose

Run the full local stack (postgres, redis, gateway, media-api, writer, image-analysis-worker) with Docker Compose:

```bash
cp backend/.env.example backend/.env.local
docker compose up --build
```

```bash
curl http://localhost:8080/healthz
curl http://localhost:8081/healthz
```

`docker compose build --build-arg SERVICE=<name>` is not needed directly; `compose.yaml` sets each service's `SERVICE` build arg already. To build a single service image manually:

```bash
docker build -f backend/Dockerfile --build-arg SERVICE=gateway backend
```

`post-session-job` is a one-shot job and is intentionally not part of `compose.yaml`; run it with `make run-post-session-job` or `docker run` on demand.
