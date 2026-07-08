variable "project_id" {
  type        = string
  description = "GCP project ID the instance is created in."
}

variable "region" {
  type        = string
  description = "GCP region, e.g. asia-northeast1."
}

variable "instance_name" {
  type        = string
  description = "Cloud SQL instance name. Unique within the project (not globally, unlike GCS bucket names) -- but reusing a name within ~7 days of deleting an instance with that name can fail, so avoid churn."
}

variable "database_version" {
  type        = string
  default     = "POSTGRES_17"
  description = "Matches the postgres:17 image used in the local compose.yaml."
}

variable "tier" {
  type        = string
  default     = "db-f1-micro"
  description = "Cheapest shared-core tier, fine for MVP/dev traffic. Upgrade before any real load."
}

variable "database_name" {
  type        = string
  default     = "reaction"
  description = "Matches the local Postgres database name in compose.yaml."
}

variable "database_user" {
  type        = string
  default     = "reaction"
  description = "Matches the local Postgres user name in compose.yaml."
}

variable "deletion_protection" {
  type        = bool
  default     = true
  description = "Block `terraform destroy` from deleting this instance. Keep true outside of throwaway testing -- there is no bucket-style force_destroy escape hatch for a database."
}
