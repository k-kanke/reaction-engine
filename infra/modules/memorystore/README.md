# memorystore

Terraform module for a single Memorystore for Redis instance —
architecture.md's Realtime state store (mood_wave recent window,
transcript recent window, trigger/feedback cooldown; see
`internal/redis/client.go`).

Step I of `plan/gcp-deployment-runbook.md`.

## Usage

```hcl
module "redis" {
  source = "../../modules/memorystore"

  project_id    = var.project_id
  region        = var.region
  instance_name = "reaction-engine-redis"
  network_id    = module.vpc.network_id
}
```

Only reachable from resources on the same VPC (`network_id`, from
`modules/vpc`) — Cloud Run services need a Serverless VPC Access connector
(`modules/vpc`'s `connector_id`, passed to `modules/cloud-run-service`'s
`vpc_connector`) to reach it.

`tier = "BASIC"` (default) has no replica and is the cheapest option, fine
for MVP traffic. Switching to `STANDARD_HA` later requires recreating the
instance (no in-place tier change) — plan for a maintenance window if that
becomes necessary.
