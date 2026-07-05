# service-account

Terraform module for a single service account, optionally with project-wide
IAM roles attached.

Deliberately doesn't create service account keys: keys are secrets, and
creating them via `google_service_account_key` would leave the private key
sitting in Terraform state in plaintext. Create keys out-of-band instead:

```bash
gcloud iam service-accounts keys create ./key.json \
  --iam-account=<email from this module's output>
```

Resource-scoped bindings (e.g. "this account can write to this one
bucket") aren't handled here -- they're attached from that resource's own
module (see `modules/storage`'s `iam_bindings`, wired together in
`environments/prod/main.tf`). Only use `project_roles` when a role can't be
scoped to a single resource.

Expected service accounts as more of the app moves to Cloud Run:

- Cloud Run Gateway
- Cloud Run Durable Writer
- Cloud Run analysis jobs
- `reaction-engine-media-api` (Phase 14 Step 14-1, implemented)

