# db

Cloud SQL PostgreSQL access.

Responsibilities:

- Provide Kysely + postgres.js database client.
- Own table definitions and repositories.
- Store session metadata, signal summaries, decision logs, feedback history, reports, and media refs.
- Do not store every compact raw high-frequency feature event in Cloud SQL.
