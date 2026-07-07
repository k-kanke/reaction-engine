# realtime

Realtime decision and short-term state logic, per architecture.md's
WebSocket Gateway / Realtime Worker section and
plan/mood-wave-contract-migration.md Step 5.

Responsibilities:

- Build the session-level 30s mood_wave_window + summary
  (slope/volatility/min/max) from the Redis mood wave time series
  (`BuildWindow`).
- Gate on the session's feedback cooldown before deciding anything
  (`HandleTrigger`).
- Assemble the realtime LLM evidence pack: mood_wave window, merged
  self/other transcript_window, every participant's ready baseline frame
  media_ref, and the triggering sample's own evidence_frame if uploaded
  (`buildEvidencePack`).
- Run the rule fallback or (stubbed) Realtime LLM decision flow and return
  the resulting feedback_event.

Out of scope (elsewhere in plan/mood-wave-contract-migration.md):

- Durable persistence of the resulting trigger_event/feedback_event
  (Step 6).
- Uploading/serving evidence_frame images (Step 7/8).
- A real Gemini Flash call (Phase 14 of
  plan/backend-local-docker-runbook.md) — generateLLMStubCandidate is
  deterministic and makes no network call.
