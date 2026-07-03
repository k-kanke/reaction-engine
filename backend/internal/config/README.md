# config

Environment configuration for Go services.

Responsibilities:

- Load and validate Cloud Run environment variables.
- Centralize project id, Pub/Sub topic names, bucket names, Redis settings, and database settings.
- Fail fast on missing required configuration.
