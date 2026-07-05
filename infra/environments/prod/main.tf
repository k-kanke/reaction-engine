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

# Step C (deploy plan): lets media_service_account sign Cloud Storage V4
# URLs via the IAM Credentials SignBlob RPC (backend/internal/media.GCSMediaStore)
# instead of a downloaded key file -- needed once it's attached as a Cloud
# Run service's runtime identity, since Cloud Run has no key file to read.
# Self-scoped: it can only impersonate itself, not any other account.
resource "google_service_account_iam_member" "media_service_account_token_creator" {
  service_account_id = module.media_service_account.name
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = module.media_service_account.member
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

# Step B (deploy plan): where backend Docker images (built via
# backend/Dockerfile's SERVICE build arg) live before Cloud Run deploys
# them. media_service_account gets pull access here since Step D/E attach
# it as r-media-api's Cloud Run runtime identity.
module "backend_images" {
  source = "../../modules/artifact-registry"

  project_id    = var.project_id
  location      = var.region
  repository_id = var.backend_images_repository_id
  description   = "Backend service images (gateway, media-api, writer, image-analysis-worker, post-session-job)"

  iam_bindings = [
    {
      role    = "roles/artifactregistry.reader"
      members = [module.media_service_account.member]
    }
  ]
}
