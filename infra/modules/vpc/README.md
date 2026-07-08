# vpc

Terraform module for a dedicated VPC network + Serverless VPC Access
connector, used so Cloud Run services can reach Memorystore for Redis
(`modules/memorystore`) — Memorystore only has a VPC-internal IP, and Cloud
Run services aren't in any VPC by default.

Step I of `plan/gcp-deployment-runbook.md`.

## Usage

```hcl
module "vpc" {
  source = "../../modules/vpc"

  project_id             = var.project_id
  region                 = var.region
  network_name           = "reaction-engine-vpc"
  connector_subnet_cidr  = "10.8.0.0/28"
  connector_name         = "reaction-engine-connector"
}
```

Pass `module.vpc.connector_id` to `modules/cloud-run-service`'s
`vpc_connector` variable for any service that needs to reach Memorystore
(gateway, image-analysis-worker). Pass `module.vpc.network_id` to
`modules/memorystore`'s `network_id` variable.

`connector_name` is limited to 25 characters by Google (a
`google_vpc_access_connector` constraint, not a limit this module adds).
