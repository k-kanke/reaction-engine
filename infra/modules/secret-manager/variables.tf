variable "project_id" {
  type        = string
  description = "GCP project ID the secret is created in."
}

variable "secret_id" {
  type        = string
  description = "Secret Manager secret ID, e.g. \"gmail-oauth-refresh-token\". Unique within the project."
}

variable "secret_value" {
  type        = string
  sensitive   = true
  description = "Plaintext secret payload, stored as the secret's initial (and only, unless applied again with a new value) version. Passed in from a sensitive Terraform variable in the caller -- never hardcode this at the call site."
}

variable "accessor_members" {
  type        = list(string)
  default     = []
  description = "IAM members (e.g. module.gmail_sender_service_account.member) granted roles/secretmanager.secretAccessor on this one secret, scoped to just this secret rather than a project-wide role."
}
