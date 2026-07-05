terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
  }

  # Local state for now (single-operator, single-environment setup). Move
  # this to a `gcs` backend once more than one person needs to run
  # terraform apply against this environment.
}

provider "google" {
  project = var.project_id
  region  = var.region
}
