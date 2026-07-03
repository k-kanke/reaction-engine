# image-analysis-worker

Image Analysis Worker entrypoint.

Responsibilities:

- Consume `media-analysis-events`.
- Read image objects from Cloud Storage.
- Read `capture_snapshots.feature_snapshot` from Cloud SQL.
- Generate `visual_summary` and update `participant_baseline`.
- Cache baseline / visual summary / baseline status in Redis.
