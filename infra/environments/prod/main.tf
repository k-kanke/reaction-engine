# Phase 14 Step 14-1 (plan/gcp-adapter-migration-phase14.md): the Cloud
# Storage bucket media-api / image-analysis-worker use for baseline frames
# (backend/internal/media.GCSMediaStore), plus the dedicated service
# account that backend uses to sign upload URLs and read/write objects --
# scoped to only this bucket, not the whole project. The account's key
# (a secret) is intentionally not created here; see
# modules/service-account/README.md.

module "media_service_account" {
  source = "../../modules/service-account"

  project_id   = var.project_id
  account_id   = "reaction-engine-media-api"
  display_name = "Reaction Engine Media API"
}

module "media_bucket" {
  source = "../../modules/storage"

  project_id = var.project_id
  name       = var.media_bucket_name
  location   = var.region

  iam_bindings = [
    {
      role    = "roles/storage.objectAdmin"
      members = [module.media_service_account.member]
    }
  ]
}

# Bootstrap bucket for this environment's own Terraform state. Created with
# the local backend still active (see versions.tf); once it exists, switch
# the backend block to `gcs` and run `terraform init -migrate-state` to
# move the state file into it. Versioned so a corrupted/bad state push can
# be rolled back.
module "terraform_state_bucket" {
  source = "../../modules/storage"

  project_id         = var.project_id
  name               = var.terraform_state_bucket_name
  location           = var.region
  versioning_enabled = true
}

# Step A (docs/system-computation-flow.md deploy plan / plan/gcp-adapter-migration-phase14.md
# Step 14-3): the Cloud SQL instance media-api (and later other services)
# use instead of the local Docker Compose Postgres. Public IP only, reached
# via the Cloud SQL Auth Proxy locally and via Cloud Run's built-in Cloud
# SQL connector once deployed -- no VPC needed for this piece.
module "db" {
  source = "../../modules/cloud-sql"

  project_id    = var.project_id
  region        = var.region
  instance_name = var.db_instance_name
}
