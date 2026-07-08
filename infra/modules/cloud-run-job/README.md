# cloud-run-job

Terraform module for a single Cloud Run v2 Job, generic enough to reuse for
`r-post-session-job`, `r-pdf-renderer`, and `r-gmail-sender`
(plan/post-session-report-implementation.md Step 4).

Wraps `google_cloud_run_v2_job` plus its `roles/run.invoker` IAM binding.
Notable choices baked in, mirroring `modules/cloud-run-service`:

- **Cloud SQL without a VPC connector.** Pass `cloudsql_connection_names`
  (from `module.db.connection_name`) and the module attaches Cloud Run's
  built-in Cloud SQL volume, mounted at `/cloudsql`. Point `DATABASE_URL` at
  `postgres://<user>:<password>@/<db>?host=/cloudsql/<connection_name>&sslmode=disable`
  (Unix socket, not `localhost:5432`).
- **No key files.** Set `service_account_email` to the identity the
  container should run as and it authenticates via Application Default
  Credentials.
- **`--session-id` is a runtime override, not a deploy-time value.**
  `post-session-job`/`pdf-renderer`/`gmail-sender` are CLIs that require
  `--session-id` (and `gmail-sender` also `--to`), and the actual session
  ID is only known when a real session ends -- not at `terraform apply`
  time. `args` here only sets the *default* baked into the job; the caller
  that starts an execution (gateway, on `session_end` -- Step 5) passes the
  real value via `gcloud run jobs execute <job> --args=--session-id=<id>`
  or the Admin API's `RunJobRequest` overrides, which replace `args` for
  that one execution only.
- **No automatic retries.** `max_retries` defaults to 0: these jobs each
  have side effects (report insert, PDF write, delivery insert) without
  dedup logic, so a failed execution should surface as failed rather than
  silently re-run and risk double-processing.
- **Invoker access is explicit.** Default is nobody can call `jobs.run`;
  set `invoker_members` to the callers that should be able to trigger an
  execution (e.g. `module.gateway_service_account.member` once Step 5 wires
  up session-end-triggered execution).

## Usage

```hcl
module "post_session_job" {
  source     = "../../modules/cloud-run-job"
  project_id = var.project_id
  location   = var.region

  job_name               = "r-post-session-job"
  image                  = "${module.backend_images.repository_url}/post-session-job:${var.post_session_job_image_tag}"
  service_account_email  = module.writer_service_account.email
  cloudsql_connection_names = [module.db.connection_name]

  env_vars = {
    JSONL_STORE_BACKEND = "gcs"
    GCS_JSONL_BUCKET    = module.jsonl_bucket.name
    DATABASE_URL        = "postgres://${module.db.database_user}:${module.db.database_password}@/${module.db.database_name}?host=/cloudsql/${module.db.connection_name}&sslmode=disable"
  }

  invoker_members = [module.gateway_service_account.member]
}
```
