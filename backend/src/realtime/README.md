# realtime

Realtime decision and short-term state logic.

Responsibilities:

- Read/write recent feature windows in Memorystore for Redis.
- Maintain latest session state and feedback cooldowns.
- Aggregate 10s/30s windows.
- Generate low-risk feedback decisions from feature trends.
