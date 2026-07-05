variable "project_id" {
  type        = string
  description = "GCP project ID the service account is created in."
}

variable "account_id" {
  type        = string
  description = "Service account ID (the part before @project.iam.gserviceaccount.com). Lowercase letters, digits, hyphens; 6-30 chars."
}

variable "display_name" {
  type        = string
  description = "Human-readable name shown in the GCP Console."
}

variable "project_roles" {
  type        = list(string)
  default     = []
  description = "Project-wide IAM roles to grant this account, e.g. [\"roles/pubsub.publisher\"]. Prefer resource-scoped bindings (attached from that resource's own module) over project-wide roles when a role can be scoped to one resource."
}
