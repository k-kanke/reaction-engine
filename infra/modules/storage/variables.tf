variable "project_id" {
  type        = string
  description = "GCP project ID the bucket is created in."
}

variable "name" {
  type        = string
  description = "Globally unique Cloud Storage bucket name (GCS bucket names are unique across all of GCP, not just this project)."
}

variable "location" {
  type        = string
  description = "Bucket location, e.g. asia-northeast1."
}

variable "uniform_bucket_level_access" {
  type        = bool
  default     = true
  description = "Use IAM-only access control instead of per-object ACLs."
}

variable "force_destroy" {
  type        = bool
  default     = false
  description = "Allow `terraform destroy` to delete the bucket even if it still has objects in it. Keep false outside of throwaway testing."
}

variable "versioning_enabled" {
  type        = bool
  default     = false
  description = "Keep old object versions on overwrite/delete. Turn on for buckets holding data you can't regenerate, e.g. Terraform state."
}
