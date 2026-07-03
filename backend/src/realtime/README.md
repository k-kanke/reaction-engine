# realtime

Realtime decision and short-term state logic.

Responsibilities:

- Read/write recent feature windows in Memorystore for Redis.
- Maintain latest session state and feedback cooldowns.
- Aggregate 10s/30s windows.
- Build realtime signal summaries such as avg, delta, slope, duration, and confidence.
- Generate low-risk feedback decisions from feature trends.
- Produce decision logs for later evaluation.
