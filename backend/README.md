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
  migrations/       Cloud SQL migrations
  deploy/           Cloud Run and Google Cloud deployment files
  scripts/          Local backend utility scripts
  tests/            Backend tests
```

## Initial Service Split

- `reaction-gateway`: WebSocket Gateway, Memorystore writes, signal summary / decision log, Pub/Sub publish, feedback return.
- `reaction-media-api`: signed upload URL, media_ref, capture_snapshots, media_uploaded publish.
- `reaction-writer`: Pub/Sub consumer, Cloud Storage JSONL writer, Cloud SQL signal summary / decision log writer.
- `reaction-image-analysis-worker`: image + capture_snapshots.feature_snapshot -> participant_baseline / visual_summary.
- `reaction-analysis-job`: Cloud Run Job for post-session analysis.

Keep high-frequency compact raw feature events out of Cloud SQL. Store compact raw features as JSONL chunks in Cloud Storage and put metadata, signal summaries, decision logs, feedback history, and reports in Cloud SQL.
