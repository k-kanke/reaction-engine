# cloud-sql

Terraform module for a single Cloud SQL for PostgreSQL instance +
database + user.

Used for sessions, summaries, feedback history, reports, and metadata --
the same schema `backend/migrations` applies locally.

Unlike Cloud Storage, this is reachable from a laptop without any extra
networking: run the [Cloud SQL Auth
Proxy](https://cloud.google.com/sql/docs/postgres/sql-proxy) against
`connection_name` and connect to `localhost:<port>` as if it were local
Postgres. (Memorystore for Redis, by contrast, only has a VPC-internal
address -- deliberately not provisioned yet; see
`plan/gcp-adapter-migration-phase14.md` Step 14-4.)

## Usage

```hcl
module "db" {
  source        = "../../modules/cloud-sql"
  project_id    = var.project_id
  region        = var.region
  instance_name = "reaction-engine-db"
}
```

The generated password is in Terraform state and `sensitive` output only --
retrieve it with `terraform output -raw database_password`, don't print it
in a plan/apply log.

