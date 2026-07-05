# Phase 14 Step 14-1 (plan/gcp-adapter-migration-phase14.md): minimal first
# apply is just the Cloud Storage bucket media-api / image-analysis-worker
# use for baseline frames (backend/internal/media.GCSMediaStore). Service
# account + IAM binding come next, once this bucket exists.

module "media_bucket" {
  source = "../../modules/storage"

  project_id = var.project_id
  name       = var.media_bucket_name
  location   = var.region
}
