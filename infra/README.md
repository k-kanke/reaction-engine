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
