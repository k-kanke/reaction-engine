# gmail-sender

Gmail Sender job entrypoint (architecture.md's 全体FBレポートフロー step 10:
"Gmail sender が PDF を添付して送信する").

Responsibilities:

- Look up the latest report's PDF (`reports.pdf_path`, written by
  `pdf-renderer`) for a session_id.
- Send it to the given recipient (`--to`) and record delivery status
  (`report_deliveries`).

Kept separate from `pdf-renderer`, per architecture.md's "PDF 生成と Gmail
送信は post-session report 生成から分離し、delivery retry を別管理する" — a
real implementation can retry sending without re-rendering the PDF.

## GMAIL_SEND_BACKEND

- `stub` (default): logs what it would send and records a `sent` delivery.
  No Google credentials needed — the mode local dev / docker-compose use.
- `real`: sends through the real Gmail API via OAuth2 user credentials
  (`internal/gmail.GmailSender`). A personal `@gmail.com` account has no
  domain-wide delegation to grant a service account, so this authenticates
  as one specific Gmail account (`GMAIL_SENDER_FROM`) using a refresh token
  minted once, ahead of time, by `cmd/gmail-oauth-setup`.

## One-time setup for GMAIL_SEND_BACKEND=real

1. Enable the API: `gcloud services enable gmail.googleapis.com --project=<project_id>`
2. In the GCP Console (APIs & Services > OAuth consent screen), create an
   **External** consent screen in **Testing** status, and add the sending
   Gmail account as a test user. (Testing status keeps this out of Google's
   verification process, at the cost of refresh tokens expiring after 7
   days — see below.)
3. In APIs & Services > Credentials, create an **OAuth client ID** of type
   **Desktop app**. Note the client ID and client secret.
4. Add `http://localhost:8085/callback` (or whatever `--port` you pass in
   the next step) as an authorized redirect URI on that client.
5. Run the one-shot local tool, signed into the sending Gmail account in
   your browser when it opens the consent screen:

   ```
   cd backend
   go run ./cmd/gmail-oauth-setup --client-id=<client_id> --client-secret=<client_secret>
   ```

   It prints a `refresh_token`. This tool doesn't store it anywhere —
   copy it from the terminal.
6. Store the three values (`gmail_oauth_client_id`, `gmail_oauth_client_secret`,
   `gmail_oauth_refresh_token`) plus `gmail_sender_from` (the sending
   address) in `infra/environments/prod/terraform.tfvars` (gitignored —
   see `terraform.tfvars.example`) and apply. Terraform stores them in
   Secret Manager and wires them into `r-gmail-sender` as env vars
   (`infra/modules/secret-manager`, `infra/modules/cloud-run-job`'s
   `secret_env_vars`).

**Refresh token expiry:** while the OAuth consent screen stays in Testing
status, Google expires refresh tokens after 7 days. If `gmail-sender`
starts failing with `invalid_grant`, re-run step 5 and re-apply with the
new `gmail_oauth_refresh_token`. Moving the consent screen to In
Production status avoids this but requires Google's app verification —
not done here, since `gmail-sender` is invoked manually per session either
way.

## Local testing (docker-compose)

```
GMAIL_SEND_BACKEND=real \
GMAIL_SENDER_FROM=you@gmail.com \
GMAIL_OAUTH_CLIENT_ID=... \
GMAIL_OAUTH_CLIENT_SECRET=... \
GMAIL_OAUTH_REFRESH_TOKEN=... \
go run ./cmd/gmail-sender --session-id=<id> --to=you@gmail.com
```

`MEDIA_STORE_BACKEND`/`LOCAL_MEDIA_DIR`/`GCS_MEDIA_BUCKET` control where the
report PDF is read from, same as `cmd/pdf-renderer`.
