# storage

Cloud Storage helpers.

Responsibilities:

- Write raw feature JSONL chunks.
- Manage paths for representative frames and short clips.
- Keep raw event storage append-like through chunk files.
- Support dedupe by `event_id` during post-session analysis.
