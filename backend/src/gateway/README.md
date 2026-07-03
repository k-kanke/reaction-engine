# gateway

Cloud Run WebSocket Gateway.

Responsibilities:

- Accept `realtime_feature` events from the Chrome extension.
- Assign `event_id` and `server_received_at_ms`.
- Write compact recent state to Memorystore for Redis.
- Publish full feature events to Pub/Sub topic `feature-events`.
- Return `feedback_event` messages to the connected extension.
