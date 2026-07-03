# gateway

Cloud Run WebSocket Gateway entrypoint.

Responsibilities:

- Accept `realtime_feature` and `audio_chunk` messages from the Chrome extension.
- Maintain Redis recent windows and cooldown state.
- Run realtime signal summary / decision log logic.
- Publish realtime analysis events to Pub/Sub.
- Return `feedback_event` messages to the connected extension.
