variable "project_id" {
  type        = string
  description = "GCP project ID. There is intentionally only one environment (prod); see infra/README.md."
}

variable "region" {
  type        = string
  default     = "asia-northeast1"
  description = "Default GCP region for resources in this environment."
}

variable "media_bucket_name" {
  type        = string
  description = "Globally unique Cloud Storage bucket name for baseline frame uploads (Phase 14 Step 14-1). GCS bucket names are unique across all of GCP, not just this project, so this has no repo-wide default -- set it in terraform.tfvars, e.g. \"<project_id>-reaction-engine-sessions\"."
}

variable "terraform_state_bucket_name" {
  type        = string
  description = "Globally unique Cloud Storage bucket name for this environment's own Terraform state. Set it in terraform.tfvars, e.g. \"<project_id>-tfstate\"."
}

variable "db_instance_name" {
  type        = string
  default     = "reaction-engine-db"
  description = "Cloud SQL instance name (Phase 14 Step 14-3). Unique within the project, not globally -- see modules/cloud-sql/variables.tf."
}
