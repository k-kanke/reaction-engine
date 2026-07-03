# storage

Cloud Storage helpers.

Responsibilities:

- Write compact raw feature JSONL chunks.
- Write signal summary and decision log JSONL chunks.
- Manage paths for representative frames and short clips.
- Keep compact raw event storage append-like through chunk files.
- Support dedupe by `event_id` during post-session analysis.
