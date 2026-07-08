variable "project_id" {
  type        = string
  description = "GCP project ID the network/connector are created in."
}

variable "region" {
  type        = string
  description = "GCP region, e.g. asia-northeast1. The connector and its subnet must be in the same region as the Cloud Run services that use it."
}

variable "network_name" {
  type        = string
  description = "VPC network name. Unique within the project."
}

variable "connector_subnet_cidr" {
  type        = string
  description = "CIDR range reserved for the Serverless VPC Access connector's own subnet -- must not overlap anything else on this network. /28 is Google's documented minimum practical size."
}

variable "connector_name" {
  type        = string
  description = "Serverless VPC Access connector name. Unique within the project + region. Max 25 characters (a Google-imposed limit on this resource type)."
}

variable "connector_min_instances" {
  type        = number
  default     = 2
  description = "Minimum connector instances. 2 is the platform minimum."
}

variable "connector_max_instances" {
  type        = number
  default     = 3
  description = "Maximum connector instances. Keep low for MVP traffic; each instance adds cost even when idle."
}

variable "connector_machine_type" {
  type        = string
  default     = "e2-micro"
  description = "Cheapest supported machine type, fine for MVP traffic."
}
