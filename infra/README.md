# Infrastructure

Terraform workspace for Google Cloud infrastructure.

Use this directory for managed resources such as:

- Cloud Run services and jobs
- Pub/Sub topics, subscriptions, and dead-letter topics
- Memorystore for Redis
- Cloud SQL for PostgreSQL
- Cloud Storage buckets
- Secret Manager secrets
- service accounts and IAM bindings
- VPC / Serverless VPC Access
- Cloud Logging / Monitoring alerts

Start small for MVP. Provision Pub/Sub, Cloud Storage, service accounts, IAM, and placeholders first. Add Cloud Run, Memorystore, Cloud SQL, and VPC once the backend service contracts are stable.

## Layout

```text
infra/
  environments/
    dev/
    prod/
  modules/
    cloud-run-service/
    cloud-run-job/
    pubsub/
    memorystore/
    cloud-sql/
    storage/
    service-account/
    secret-manager/
    vpc/
    monitoring/
```

Each environment should own its `main.tf`, `variables.tf`, `outputs.tf`, and `terraform.tfvars.example`.

Only `environments/prod` is actually implemented. There is a single real GCP
project behind this app, so a separate `dev` Terraform environment would
just be a second piece of state to keep in sync for no benefit;
`environments/dev/` stays an empty placeholder until (if ever) a second
project is needed.

## Getting started (first time on this machine)

1. Install the gcloud CLI (needed for auth even though we drive everything
   through Terraform, not `gcloud` commands directly):
   ```bash
   brew install --cask google-cloud-sdk
   ```
2. Authenticate Application Default Credentials -- this is what the
   `google` Terraform provider actually reads. It uses *your own* Google
   account, not a service account key, as long as you already have
   Owner/Editor on the GCP project:
   ```bash
   gcloud auth application-default login
   ```
   (opens a browser; log in with the account that has access to the project)
3. Set up your local vars (never commit `terraform.tfvars`, only the
   `.example` file):
   ```bash
   cd infra/environments/prod
   cp terraform.tfvars.example terraform.tfvars
   # edit terraform.tfvars: set project_id to your actual GCP project ID
   ```
4. Standard Terraform flow:
   ```bash
   terraform init
   terraform plan
   terraform apply
   ```

State is local (`terraform.tfstate`, gitignored) -- there's one operator
right now. Move to a `gcs` backend if/when more than one person needs to
run `terraform apply` here.
