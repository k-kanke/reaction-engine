# storage

Terraform module for a single Cloud Storage bucket.

Used for raw feature JSONL, representative frames, short clips, and analysis artifacts.

Minimal on purpose: creates only the bucket (`google_storage_bucket`). IAM
bindings (e.g. granting a service account `roles/storage.objectAdmin`) and
the service account itself are out of scope here and will be wired in from
the environment once that's needed (see
`plan/gcp-adapter-migration-phase14.md` Step 14-1).

## Usage

```hcl
module "media_bucket" {
  source     = "../../modules/storage"
  project_id = var.project_id
  name       = "${var.project_id}-reaction-engine-sessions"
  location   = var.region
}
```

