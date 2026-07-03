# pubsub

Pub/Sub boundary.

Responsibilities:

- Publish `feature-events` and `media-analysis-events`.
- Provide subscriber helpers for Durable Writer and Image Analysis Worker.
- Keep event payloads versioned with `schema_version`.
