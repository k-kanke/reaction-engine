# imageanalysis

Image analysis worker internals.

Responsibilities:

- Read uploaded image and `capture_snapshots.feature_snapshot`.
- Call Gemini Vision / vision model.
- Produce visual summaries.
- Update participant baselines and Redis cache.
