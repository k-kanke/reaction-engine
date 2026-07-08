# prod

Terraform environment for this project's only GCP environment. There is no
separate `dev` GCP project/environment; `infra/environments/dev/` stays an
empty placeholder until (if ever) a second project is actually needed. See
`infra/README.md`.

## Usage

```bash
cd infra/environments/prod
cp terraform.tfvars.example terraform.tfvars   # fill in your project_id
terraform init
terraform plan
terraform apply
```

`gateway_image_tag` / `media_api_image_tag` are intentionally required
Terraform variables so service revisions always point at explicit image
tags. For routine local plan/apply where you do not want Terraform to
prompt for them, use the wrapper from the repository root. It reads the
currently deployed Cloud Run image tags and passes them as `-var` values:

```bash
scripts/terraform-prod plan
scripts/terraform-prod apply
```

State lives in the `gcs` backend configured in `versions.tf`
(`gs://reaction-engine-501316-tfstate/terraform/state/prod`), a bucket
managed by `module.terraform_state_bucket` below. It was bootstrapped with
local state before the backend block existed, then migrated in with
`terraform init -migrate-state`.

## Resources managed here

- `module.media_bucket` (Phase 14 Step 14-1): the Cloud Storage bucket
  `backend/internal/media.GCSMediaStore` uploads baseline frames to.
- `module.media_service_account` (Phase 14 Step 14-1): the identity that
  same backend code uses -- scoped to `roles/storage.objectAdmin` on only
  `module.media_bucket`, nothing project-wide. Its key isn't created by
  Terraform (see `modules/service-account/README.md`); mint one after
  `apply` with:
  ```bash
  terraform output -raw media_service_account_email
  gcloud iam service-accounts keys create ./reaction-engine-media-api-key.json \
    --iam-account=$(terraform output -raw media_service_account_email)
  ```
- `module.terraform_state_bucket`: this environment's own Terraform state,
  versioned so a bad state push can be rolled back.
- `module.db` (Phase 14 Step 14-3): the Cloud SQL for PostgreSQL instance
  backing `backend/migrations`. Public IP only; connect locally through the
  [Cloud SQL Auth
  Proxy](https://cloud.google.com/sql/docs/postgres/sql-proxy):
  ```bash
  cloud-sql-proxy $(terraform output -raw db_connection_name)
  ```
  then point `DATABASE_URL` at `postgres://<db_database_user>:<db_database_password>@localhost:5432/<db_database_name>?sslmode=disable`
  (`terraform output -raw db_database_password` for the password).
- `module.backend_images` (Phase 14 Step 14-3 deploy plan): the Artifact
  Registry Docker repository backend service images are pushed to before
  Cloud Run deploys them. `module.media_service_account` has
  `roles/artifactregistry.reader` on it so it can pull images once
  attached to a Cloud Run service. Tag images with the short git commit
  hash they were built from -- never `:latest` -- so a Cloud Run revision
  always traces back to an exact source commit.
  Cloud Run needs a `linux/amd64` image. On Apple Silicon, cross-building
  that locally under QEMU (`docker buildx build --platform linux/amd64`)
  has been observed to crash mid-`go mod download` (a known Go/QEMU
  interaction, not a project bug) -- build on Cloud Build instead, which
  builds natively on `amd64` workers and pushes directly:
  ```bash
  TAG=$(git rev-parse --short HEAD)
  IMAGE=$(terraform output -raw backend_images_repository_url)/media-api:$TAG
  cat > /tmp/cloudbuild-media-api.yaml <<EOF
  steps:
    - name: 'gcr.io/cloud-builders/docker'
      args: ['build', '--build-arg', 'SERVICE=media-api', '-t', '$IMAGE', '-f', 'backend/Dockerfile', 'backend']
  images: ['$IMAGE']
  EOF
  gcloud builds submit --config=/tmp/cloudbuild-media-api.yaml --substitutions=_IMAGE="$IMAGE" ../../..
  ```
- `module.media_api_service` (Step E): the `r-media-api` Cloud Run
  service. Cloud SQL is reached through the built-in volume (no VPC
  connector), and the runtime identity is `module.media_service_account`
  -- no key file, no `GOOGLE_APPLICATION_CREDENTIALS`. There's no
  application-level auth in `backend/internal/media` yet, so
  `roles/run.invoker` is restricted to `media_api_invoker_members` (set in
  `terraform.tfvars`) rather than public; widen that only once real auth
  exists, since Chrome extension clients can't hold GCP identity tokens.
  ```bash
  terraform apply -var="media_api_image_tag=$(git rev-parse --short HEAD)"
  ```
- `module.tester_service_account`: exists only so humans in
  `media_api_invoker_members` can impersonate it to test IAM-protected
  Cloud Run services. `gcloud auth print-identity-token` for a *user*
  account carries gcloud's own OAuth client ID as its audience, not the
  Cloud Run URL -- Cloud Run silently rejects that (404, not 403, so as
  not to reveal the service exists) before the request reaches the
  container. Impersonation is the documented workaround, since
  `--audiences` only works for service accounts:
  ```bash
  TOKEN=$(gcloud auth print-identity-token \
    --impersonate-service-account=$(terraform output -raw tester_service_account_email) \
    --audiences="$(terraform output -raw media_api_url)")
  curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
    -d '{"purpose":"baseline","content_type":"image/webp","capture_id":"cap_1","t_ms":1000,"audience_id":"aud_1"}' \
    "$(terraform output -raw media_api_url)/sessions/sess_1/media/upload-url"
  ```
  Note: `/healthz` specifically gets intercepted before reaching the
  container (a generic Google 404 page, no server-side log entry at all,
  regardless of auth) -- verify against a real route like `upload-url`
  above instead, not `/healthz`.

### Troubleshooting Cloud Run + Cloud SQL first-time setup

Two gotchas hit during the first `r-media-api` deploy, in case they recur
for another service:

- **`sqladmin.googleapis.com` must be enabled**, separately from creating
  the Cloud SQL instance itself (which succeeds either way). Without it,
  Cloud Run's Cloud SQL connector fails with `dial unix
  .../.s.PGSQL.5432: connect: connection refused` -- an unhelpful error
  that looks like a networking problem but is actually `Cloud SQL Admin
  API has not been used in project ... or it is disabled` buried in the
  full log line. `gcloud services enable sqladmin.googleapis.com`.
- **A running instance won't pick up a newly-granted IAM role.** If
  `roles/cloudsql.client` (or any permission the container needs at
  startup) is granted *after* an instance is already warm, that instance
  keeps failing until it's replaced -- IAM propagation delay isn't the
  (whole) story. Force a fresh revision rather than waiting it out:
  ```bash
  gcloud run services update r-media-api --region=asia-northeast1 \
    --update-labels=force-redeploy=$(date +%s)
  ```
