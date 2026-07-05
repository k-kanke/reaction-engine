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

