variable "project_id" {
  type        = string
  description = "GCP project ID the job is created in."
}

variable "location" {
  type        = string
  description = "Cloud Run region, e.g. asia-northeast1."
}

variable "job_name" {
  type        = string
  description = "Cloud Run Job name, e.g. \"r-post-session-job\". Unique within the project + region."
}

variable "image" {
  type        = string
  description = "Full image reference to deploy, e.g. \"<repository_url>/post-session-job:<git-short-sha>\". Never \":latest\" -- a Cloud Run revision should always trace back to an exact commit."
}

variable "service_account_email" {
  type        = string
  description = "Runtime identity the container runs as. Determines what it can access via Application Default Credentials (Cloud SQL, GCS, etc.) with no key file involved."
}

variable "args" {
  type        = list(string)
  default     = []
  description = "Default container args baked into the job, e.g. [\"--session-id=placeholder\"]. post-session-job/pdf-renderer/gmail-sender all require --session-id at runtime, which is a real session ID unknown at deploy time -- callers (e.g. gateway on session_end, plan/post-session-report-implementation.md Step 5) override this per-execution via `gcloud run jobs execute --args` or the Admin API's RunJobRequest overrides, so this default only matters for a manual `gcloud run jobs execute` with no --args."
}

variable "env_vars" {
  type        = map(string)
  default     = {}
  description = "Plain (non-secret) environment variables, e.g. { JSONL_STORE_BACKEND = \"gcs\", GCS_JSONL_BUCKET = \"...\" }."
}

variable "cloudsql_connection_names" {
  type        = list(string)
  default     = []
  description = "Cloud SQL instance connection names (module.db.connection_name) to attach via Cloud Run's built-in Cloud SQL volume -- no VPC connector needed. Leave empty for jobs that don't talk to Postgres directly."
}

variable "cpu" {
  type        = string
  default     = "1"
  description = "vCPU limit per task, per google_cloud_run_v2_job's containers.resources.limits."
}

variable "memory" {
  type        = string
  default     = "512Mi"
  description = "Memory limit per task, per google_cloud_run_v2_job's containers.resources.limits."
}

variable "max_retries" {
  type        = number
  default     = 0
  description = "Retries allowed per task execution. Default 0 (no retry): post-session-job/pdf-renderer/gmail-sender each have side effects (report insert, PDF write, delivery insert) that aren't safely re-runnable from a partial failure without dedup logic, so a failed execution should surface as failed rather than silently retry."
}

variable "task_timeout" {
  type        = string
  default     = "600s"
  description = "Max duration a single task execution may run, as a duration string (e.g. \"600s\"). Cloud Run Jobs kills the task if it's still running past this."
}

variable "deletion_protection" {
  type        = bool
  default     = true
  description = "Block `terraform destroy` from deleting this job. Keep true outside of throwaway testing."
}

variable "invoker_members" {
  type        = list(string)
  default     = []
  description = "IAM members (e.g. \"serviceAccount:caller@project.iam.gserviceaccount.com\") granted roles/run.invoker on this job, i.e. allowed to call jobs.run. Needed for gateway's service account once Step 5 wires up session_end-triggered execution."
}
