# contract

Payload contracts shared by backend services.

Responsibilities:

- Define Go structs for `realtime_feature`, `audio_chunk`, `transcript_chunk`, `capture_snapshot`, `feedback_event`, and Pub/Sub envelopes.
- Keep JSON tags and schema versions explicit.
- Keep contracts aligned with JSON Schema / OpenAPI used by the Chrome extension.
