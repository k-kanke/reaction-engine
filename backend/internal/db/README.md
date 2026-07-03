# db

Cloud SQL PostgreSQL access.

Responsibilities:

- Provide `pgxpool` database access.
- Own repositories for sessions, capture snapshots, media refs, baselines, visual summaries, summaries, decisions, transcripts, feedback, and reports.
- Do not store every compact raw high-frequency feature event in Cloud SQL.
