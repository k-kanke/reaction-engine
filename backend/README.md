# Backend

Reaction Engine backend workspace.

This backend is planned as:

- Language: TypeScript
- Runtime: Node.js 22
- Framework: Fastify
- Deploy: Cloud Run
- Validation: TypeBox
- DB: Kysely + postgres.js
- Redis: ioredis against Memorystore for Redis
- Google Cloud SDKs: `@google-cloud/pubsub`, `@google-cloud/storage`

## Directory Map

```text
backend/
  src/
    gateway/        Cloud Run WebSocket Gateway
    realtime/       Memorystore recent window, cooldown, feedback rules
    events/         Pub/Sub publisher/subscriber adapters and event envelopes
    writer/         Pub/Sub -> Cloud Storage / Cloud SQL durable writer
    storage/        Cloud Storage raw JSONL / frames / clips helpers
    db/             Cloud SQL PostgreSQL client, schema, repositories
    jobs/           Cloud Run Jobs for post-session analysis
    schemas/        Shared TypeBox schemas for realtime_feature / feedback_event
    config/         Environment config loading and validation
    observability/  Logging, metrics, tracing helpers
  migrations/       Cloud SQL migrations
  deploy/           Cloud Run and Google Cloud deployment files
  scripts/          Local backend utility scripts
  tests/            Backend tests
```

## Initial Service Split

- `reaction-gateway`: WebSocket Gateway, Memorystore writes, Pub/Sub publish, feedback return.
- `reaction-writer`: Pub/Sub consumer, Cloud Storage JSONL writer, Cloud SQL summary writer.
- `reaction-analysis-job`: Cloud Run Job for post-session analysis.

Keep raw high-frequency feature events out of Cloud SQL. Store raw events as JSONL chunks in Cloud Storage and put only metadata, summaries, feedback history, and reports in Cloud SQL.
