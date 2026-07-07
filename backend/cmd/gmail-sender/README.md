# gmail-sender

Gmail Sender job entrypoint (architecture.md's 全体FBレポートフロー step 10:
"Gmail sender が PDF を添付して送信する").

Responsibilities:

- Look up the latest report's PDF (`reports.pdf_path`, written by
  `pdf-renderer`) for a session_id.
- "Send" it to the given recipient and record delivery status
  (`report_deliveries`).

This is a **local stub** (Step 11 of
`plan/mood-wave-contract-migration.md`): it never calls the real Gmail API.
It logs what it would send and records a successful delivery — Phase 14 of
`plan/backend-local-docker-runbook.md` wires in the real Gmail API /
Workspace API call behind the same shape. Kept separate from
`pdf-renderer`, per architecture.md's "PDF 生成と Gmail 送信は
post-session report 生成から分離し、delivery retry を別管理する" — a real
implementation can retry sending without re-rendering the PDF.
