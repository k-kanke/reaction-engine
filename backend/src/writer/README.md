# writer

Durable writer for the asynchronous path.

Responsibilities:

- Consume Pub/Sub events from `feature-events-durable-writer`.
- Write raw feature events as Cloud Storage JSONL chunks.
- Upsert session summaries and feedback history into Cloud SQL.
- Ack only after durable writes complete.
- Handle retry/dead-letter behavior and `event_id` idempotency.
