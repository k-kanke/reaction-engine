variable "project_id" {
  type        = string
  description = "GCP project ID the repository is created in."
}

variable "location" {
  type        = string
  description = "Repository location, e.g. asia-northeast1. Determines the registry hostname (<location>-docker.pkg.dev)."
}

variable "repository_id" {
  type        = string
  description = "Repository name. Unique within the project + location, not globally."
}

variable "description" {
  type        = string
  default     = ""
  description = "Human-readable description shown in the GCP console."
}

variable "iam_bindings" {
  type = list(object({
    role    = string
    members = list(string)
  }))
  default     = []
  description = "Repository-scoped IAM bindings, e.g. [{ role = \"roles/artifactregistry.reader\", members = [\"serviceAccount:foo@project.iam.gserviceaccount.com\"] }] so a Cloud Run service's runtime account can pull images without a project-wide grant."
}
