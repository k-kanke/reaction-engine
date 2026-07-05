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

State is local (`terraform.tfstate`, gitignored) for now -- single operator,
single environment. Move to a `gcs` backend if that stops being true.

## Resources managed here

- `module.media_bucket` (Phase 14 Step 14-1): the Cloud Storage bucket
  `backend/internal/media.GCSMediaStore` uploads baseline frames to. No
  service account / IAM binding yet -- that's the next step once this
  bucket exists.

