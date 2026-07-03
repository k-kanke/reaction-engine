# writer

Durable Writer entrypoint.

Responsibilities:

- Consume `feature-events` Pub/Sub messages.
- Write compact raw features, signal summaries, decision logs, and transcripts to Cloud Storage JSONL.
- Upsert summaries, transcripts, decision logs, and feedback history into Cloud SQL.
- Ack only after durable writes complete.
