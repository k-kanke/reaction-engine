# writer

Durable writer for the asynchronous path.

Responsibilities:

- Consume Pub/Sub events from `feature-events-durable-writer`.
- Write compact raw features, signal summaries, and decision logs as Cloud Storage JSONL chunks.
- Upsert signal summaries, decision logs, session summaries, and feedback history into Cloud SQL.
- Ack only after durable writes complete.
- Handle retry/dead-letter behavior and `event_id` idempotency.
