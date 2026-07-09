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

variable "gateway_image_tag" {
  type        = string
  description = "Short git commit hash of the gateway image to deploy, e.g. output of `git rev-parse --short HEAD`. No default -- pass explicitly on every apply (-var or TF_VAR_gateway_image_tag) so a deploy always traces back to one commit."
}

variable "jsonl_bucket_name" {
  type        = string
  description = "Globally unique Cloud Storage bucket name for mood_wave_sample/trigger/feedback/transcript JSONL (plan/post-session-report-implementation.md Step 1). Separate from media_bucket_name so IAM stays scoped independently -- set it in terraform.tfvars, e.g. \"<project_id>-reaction-engine-jsonl\"."
}

variable "writer_image_tag" {
  type        = string
  description = "Short git commit hash of the writer image to deploy, e.g. output of `git rev-parse --short HEAD`. No default -- pass explicitly on every apply (-var or TF_VAR_writer_image_tag) so a deploy always traces back to one commit."
}

variable "image_analysis_worker_image_tag" {
  type        = string
  description = "Short git commit hash of the image-analysis-worker image to deploy, e.g. output of `git rev-parse --short HEAD`. No default -- pass explicitly on every apply (-var or TF_VAR_image_analysis_worker_image_tag) so a deploy always traces back to one commit."
}

variable "post_session_job_image_tag" {
  type        = string
  description = "Short git commit hash of the post-session-job image to deploy, e.g. output of `git rev-parse --short HEAD`. No default -- pass explicitly on every apply (-var or TF_VAR_post_session_job_image_tag) so a deploy always traces back to one commit."
}

variable "pdf_renderer_image_tag" {
  type        = string
  description = "Short git commit hash of the pdf-renderer image to deploy, e.g. output of `git rev-parse --short HEAD`. No default -- pass explicitly on every apply (-var or TF_VAR_pdf_renderer_image_tag) so a deploy always traces back to one commit."
}

variable "gmail_sender_image_tag" {
  type        = string
  description = "Short git commit hash of the gmail-sender image to deploy, e.g. output of `git rev-parse --short HEAD`. No default -- pass explicitly on every apply (-var or TF_VAR_gmail_sender_image_tag) so a deploy always traces back to one commit."
}

variable "gmail_sender_from" {
  type        = string
  description = "Gmail address that owns the OAuth refresh token below (gmail_oauth_refresh_token) -- the account gmail-sender sends report emails as. Set in terraform.tfvars, e.g. \"you@gmail.com\"."
}

variable "gmail_oauth_client_id" {
  type        = string
  sensitive   = true
  description = "OAuth client ID for the Desktop app credential used by cmd/gmail-oauth-setup to mint gmail_oauth_refresh_token below (see cmd/gmail-sender/README.md). No default -- set in terraform.tfvars (not committed) or pass via TF_VAR_gmail_oauth_client_id."
}

variable "gmail_oauth_client_secret" {
  type        = string
  sensitive   = true
  description = "OAuth client secret paired with gmail_oauth_client_id. No default -- set in terraform.tfvars (not committed) or pass via TF_VAR_gmail_oauth_client_secret."
}

variable "gmail_oauth_refresh_token" {
  type        = string
  sensitive   = true
  description = "Refresh token minted by cmd/gmail-oauth-setup, authorizing gmail-sender to send as gmail_sender_from via the Gmail API. Testing-status OAuth consent screens expire this after 7 days -- re-run cmd/gmail-oauth-setup and re-apply when gmail-sender starts failing with invalid_grant. No default -- set in terraform.tfvars (not committed) or pass via TF_VAR_gmail_oauth_refresh_token."
}
