# writer

Durable writer internals.

Responsibilities:

- Consume feature event Pub/Sub messages.
- Persist JSONL chunks and Cloud SQL summaries.
- Handle retries, dead letters, and idempotency.
