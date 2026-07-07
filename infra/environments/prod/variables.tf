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

variable "backend_images_repository_id" {
  type        = string
  default     = "reaction-engine-backend"
  description = "Artifact Registry repository name for backend service Docker images. Unique within the project + region, not globally."
}

variable "media_api_image_tag" {
  type        = string
  description = "Short git commit hash of the media-api image to deploy, e.g. output of `git rev-parse --short HEAD`. No default -- pass explicitly on every apply (-var or TF_VAR_media_api_image_tag) so a deploy always traces back to one commit."
}

variable "media_api_invoker_members" {
  type        = list(string)
  description = "IAM members granted roles/run.invoker on r-media-api, e.g. [\"user:you@example.com\"]. media-api has no application-level auth yet, so keep this to trusted individuals for now -- see docs/system-computation-flow.md deploy plan Step E. Switch to allow_unauthenticated only once real auth is built, since Chrome extension clients can't hold GCP identity tokens."
}
