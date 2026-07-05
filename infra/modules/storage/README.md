# storage

Terraform module for a single Cloud Storage bucket.

Used for raw feature JSONL, representative frames, short clips, and analysis artifacts.

Creates the bucket (`google_storage_bucket`) and, optionally, bucket-scoped
IAM bindings (`google_storage_bucket_iam_member`). The service account
itself comes from `modules/service-account`; wire its email into this
module's `iam_bindings` from the environment (see
`plan/gcp-adapter-migration-phase14.md` Step 14-1).

## Usage

```hcl
module "media_service_account" {
  source       = "../../modules/service-account"
  project_id   = var.project_id
  account_id   = "reaction-engine-media-api"
  display_name = "Reaction Engine Media API"
}

module "media_bucket" {
  source     = "../../modules/storage"
  project_id = var.project_id
  name       = "${var.project_id}-reaction-engine-sessions"
  location   = var.region

  iam_bindings = [
    {
      role    = "roles/storage.objectAdmin"
      members = [module.media_service_account.member]
    }
  ]
}
```

