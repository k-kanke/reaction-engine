# cloud-run-service

Terraform module for a single Cloud Run v2 service, generic enough to
reuse for `r-gateway`, `r-media-api`, `r-writer`, and `r-image-worker`.

Wraps `google_cloud_run_v2_service` plus its `roles/run.invoker` IAM
binding. Notable choices baked in:

- **Cloud SQL without a VPC connector.** Pass `cloudsql_connection_names`
  (from `module.db.connection_name`) and the module attaches Cloud Run's
  built-in Cloud SQL volume, mounted at `/cloudsql`. Point `DATABASE_URL` at
  `postgres://<user>:<password>@/<db>?host=/cloudsql/<connection_name>&sslmode=disable`
  (Unix socket, not `localhost:5432`).
- **No key files.** Set `service_account_email` to the identity the
  container should run as (e.g. `module.media_service_account.email`) and
  it authenticates via Application Default Credentials -- no
  `GOOGLE_APPLICATION_CREDENTIALS` needed. See
  `backend/internal/media/gcs_store.go`'s IAM SignBlob path.
- **PORT alignment.** Cloud Run always injects a `PORT` env var equal to
  `container_port` (default 8080) and expects the container to listen on
  it. This backend's services each read their own `*_PORT` var (e.g.
  `MEDIA_API_PORT`) instead of the generic `PORT`, so set that explicitly
  in `env_vars` to the same value as `container_port`.
- **Invoker access is explicit.** Default is nobody can invoke the
  service; set `invoker_members` to specific principals, or
  `allow_unauthenticated = true` only if the service must be reachable
  without an identity token.

## Usage

```hcl
module "media_api_service" {
  source     = "../../modules/cloud-run-service"
  project_id = var.project_id
  location   = var.region

  service_name           = "r-media-api"
  image                  = "${module.backend_images.repository_url}/media-api:${var.media_api_image_tag}"
  service_account_email  = module.media_service_account.email
  container_port         = 8080
  cloudsql_connection_names = [module.db.connection_name]

  env_vars = {
    MEDIA_STORE_BACKEND = "gcs"
    GCS_MEDIA_BUCKET    = var.media_bucket_name
    MEDIA_API_PORT      = "8080"
    DATABASE_URL        = "postgres://${module.db.database_user}:${module.db.database_password}@/${module.db.database_name}?host=/cloudsql/${module.db.connection_name}&sslmode=disable"
  }

  invoker_members = ["user:you@example.com"]
}
```
