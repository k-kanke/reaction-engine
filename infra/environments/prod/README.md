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
  `backend/internal/media.GCSMediaStore` uploads baseline frames to. No
  service account / IAM binding yet -- that's the next step once this
  bucket exists.
- `module.terraform_state_bucket`: this environment's own Terraform state,
  versioned so a bad state push can be rolled back.

