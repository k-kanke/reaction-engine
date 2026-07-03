# storage

Cloud Storage helpers.

Responsibilities:

- Generate signed upload URLs.
- Verify uploaded objects.
- Write JSONL chunks for compact raw features, signal summaries, decision logs, transcripts, and optional capture snapshot archives.
- Support dedupe by `event_id` during post-session analysis.
