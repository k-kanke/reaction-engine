variable "project_id" {
  type        = string
  description = "GCP project ID the service is created in."
}

variable "location" {
  type        = string
  description = "Cloud Run region, e.g. asia-northeast1."
}

variable "service_name" {
  type        = string
  description = "Cloud Run service name, e.g. \"r-media-api\". Unique within the project + region."
}

variable "image" {
  type        = string
  description = "Full image reference to deploy, e.g. \"<repository_url>/media-api:<git-short-sha>\". Never \":latest\" -- a Cloud Run revision should always trace back to an exact commit."
}

variable "service_account_email" {
  type        = string
  description = "Runtime identity the container runs as. Determines what it can access via Application Default Credentials (GCS, IAM SignBlob, etc.) with no key file involved."
}

variable "container_port" {
  type        = number
  default     = 8080
  description = "Port the container listens on. Cloud Run injects a PORT env var matching this value -- the service's own *_PORT env var (e.g. MEDIA_API_PORT) must be set to the same number in env_vars."
}

variable "env_vars" {
  type        = map(string)
  default     = {}
  description = "Plain (non-secret) environment variables, e.g. { MEDIA_STORE_BACKEND = \"gcs\", MEDIA_API_PORT = \"8080\" }."
}

variable "cloudsql_connection_names" {
  type        = list(string)
  default     = []
  description = "Cloud SQL instance connection names (module.db.connection_name) to attach via Cloud Run's built-in Cloud SQL volume -- no VPC connector needed. Leave empty for services that don't talk to Postgres directly."
}

variable "min_instance_count" {
  type        = number
  default     = 0
  description = "Minimum instances kept warm. 0 lets the service scale to zero (cheapest, cold-start on first request)."
}

variable "max_instance_count" {
  type        = number
  default     = 2
  description = "Maximum instances. Keep low for MVP/dev traffic to bound cost; raise once real load is understood."
}

variable "cpu" {
  type        = string
  default     = "1"
  description = "vCPU limit per instance, per google_cloud_run_v2_service's containers.resources.limits."
}

variable "memory" {
  type        = string
  default     = "512Mi"
  description = "Memory limit per instance, per google_cloud_run_v2_service's containers.resources.limits."
}

variable "allow_unauthenticated" {
  type        = bool
  default     = false
  description = "Grant roles/run.invoker to allUsers. Only turn on for a service that must be reachable without an identity token (e.g. a public webhook); prefer invoker_members otherwise."
}

variable "invoker_members" {
  type        = list(string)
  default     = []
  description = "IAM members (e.g. \"user:you@example.com\", \"serviceAccount:caller@project.iam.gserviceaccount.com\") granted roles/run.invoker. Ignored when allow_unauthenticated is true."
}

variable "deletion_protection" {
  type        = bool
  default     = true
  description = "Block `terraform destroy` from deleting this service. Keep true outside of throwaway testing."
}
