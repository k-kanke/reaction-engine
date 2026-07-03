# jobs

Cloud Run Jobs entrypoints.

Responsibilities:

- Run post-session analysis.
- Load compact raw feature JSONL and signal summary JSONL from Cloud Storage.
- Align feature timeline with transcript chunks.
- Detect reaction change points.
- Call Speech-to-Text and Vertex AI / Gemini when enabled.
- Persist final reports to Cloud SQL.
