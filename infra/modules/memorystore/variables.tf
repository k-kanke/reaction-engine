variable "project_id" {
  type        = string
  description = "GCP project ID the instance is created in."
}

variable "region" {
  type        = string
  description = "GCP region, e.g. asia-northeast1."
}

variable "instance_name" {
  type        = string
  description = "Memorystore instance name. Unique within the project."
}

variable "network_id" {
  type        = string
  description = "modules/vpc's network_id output. DIRECT_PEERING connect_mode peers with this network automatically."
}

variable "tier" {
  type        = string
  default     = "BASIC"
  description = "BASIC (no replica, cheapest, fine for MVP) or STANDARD_HA (adds a replica with automatic failover). Changing tier requires recreating the instance -- there's no in-place upgrade."
}

variable "memory_size_gb" {
  type        = number
  default     = 1
  description = "Redis memory size in GB. 1 is the smallest practical size; raise once real session-count load is understood (see plan/gcp-deployment-runbook.md's monitoring notes)."
}

variable "redis_version" {
  type        = string
  default     = "REDIS_7_2"
  description = "Matches the redis:7 image used in the local compose.yaml."
}
