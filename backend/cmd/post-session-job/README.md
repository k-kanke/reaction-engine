# post-session-job

Post-session analysis Cloud Run Job entrypoint.

Responsibilities:

- Load durable feature, transcript, decision log, participant baseline, and visual summary data.
- Detect reaction change points.
- Generate final reports and coaching suggestions.
- Persist reports to Cloud SQL.
