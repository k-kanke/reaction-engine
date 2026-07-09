variable "project_id" {
  type        = string
  description = "GCP project ID the topic/subscription are created in."
}

variable "project_number" {
  type        = string
  description = "GCP project number (not ID) -- needed to reference the Pub/Sub service agent (service-<number>@gcp-sa-pubsub.iam.gserviceaccount.com) for dead-letter IAM bindings. Get it from data.google_project.current.number."
}

variable "topic_name" {
  type        = string
  description = "Pub/Sub topic name, e.g. \"feature-events\". Unique within the project."
}

variable "push_endpoint" {
  type        = string
  description = "Full HTTPS URL the push subscription calls for each message, e.g. \"<writer_service.uri>/pubsub/push\"."
}

variable "push_service_account_email" {
  type        = string
  description = "Identity the push subscription authenticates as (OIDC token in the Authorization header). The target Cloud Run service must separately grant this account roles/run.invoker (via that service's own invoker_members) or Cloud Run rejects the push before it reaches the container."
}

variable "publisher_members" {
  type        = list(string)
  default     = []
  description = "IAM members (e.g. module.gateway_service_account.member) granted roles/pubsub.publisher on this topic, scoped to just this topic rather than a project-wide role."
}

variable "ack_deadline_seconds" {
  type        = number
  default     = 60
  description = "How long Pub/Sub waits for the push endpoint to ack before considering the message unacked and eligible for retry."
}

variable "minimum_backoff_seconds" {
  type        = number
  default     = 10
  description = "Minimum retry backoff for a failed push delivery."
}

variable "maximum_backoff_seconds" {
  type        = number
  default     = 600
  description = "Maximum retry backoff for a failed push delivery."
}

variable "max_delivery_attempts" {
  type        = number
  default     = 5
  description = "Deliveries attempted before Pub/Sub gives up and moves the message to the dead-letter topic instead of retrying forever."
}
