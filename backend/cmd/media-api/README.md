# media-api

Cloud Run Media API entrypoint.

Responsibilities:

- Issue signed upload URLs for baseline frames.
- Store `media_ref` metadata.
- Store `capture_snapshots.feature_snapshot` in Cloud SQL.
- Verify Cloud Storage object existence on upload complete.
- Publish `media_uploaded` events to `media-analysis-events`.
