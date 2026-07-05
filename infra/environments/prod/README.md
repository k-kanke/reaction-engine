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
  ```bash
  gcloud auth configure-docker asia-northeast1-docker.pkg.dev
  TAG=$(git rev-parse --short HEAD)
  docker build --build-arg SERVICE=media-api -t $(terraform output -raw backend_images_repository_url)/media-api:$TAG backend/
  docker push $(terraform output -raw backend_images_repository_url)/media-api:$TAG
  ```

