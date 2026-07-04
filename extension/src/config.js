// Local backend endpoints (plan/backend-local-docker-runbook.md Phase 10.1).
// Loaded as a plain classic script before sidebar.js so the values are
// available as a global, independent of the ES module graph — this makes it
// easy to override per-environment (e.g. a build step swapping this file for
// Cloud Run URLs) without touching sidebar.js.
window.REACTION_ENGINE_CONFIG = window.REACTION_ENGINE_CONFIG || {
  gatewayWsUrl: "ws://localhost:8080/ws",
  mediaApiBaseUrl: "http://localhost:8081"
};
