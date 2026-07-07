# pdf-renderer

PDF Renderer job entrypoint (architecture.md's 全体FBレポートフロー step 9:
"PDF renderer が PDF を生成し Cloud Storage に保存する").

Responsibilities:

- Load the latest post-session report (`reports.report`, jsonb) for a
  session_id.
- Render it as a PDF via `internal/pdf` (Step 11 of
  `plan/mood-wave-contract-migration.md` — a minimal, dependency-free
  text-only PDF, not an HTML/headless-Chromium renderer).
- Save the PDF under the local media store
  (`sessions/{session_id}/reports/report.pdf`, matching the Cloud Storage
  layout architecture.md describes) and record its `media_ref` on the
  report row (`reports.pdf_path`).

Separate from `post-session-job` and `gmail-sender`, per architecture.md's
"PDF 生成と Gmail 送信は post-session report 生成から分離し、delivery
retry を別管理する".
