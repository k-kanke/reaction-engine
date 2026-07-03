# events

Pub/Sub event boundary.

Responsibilities:

- Define Pub/Sub message envelope conventions.
- Publish full `realtime_feature` events to `feature-events`.
- Provide subscriber helpers for writer and future workers.
- Keep event payloads versioned with `schema_version`.
