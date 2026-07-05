terraform {
  required_version = ">= 1.5"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
  }

  # Backend config can't reference variables, so the bucket name is
  # hardcoded here. That's fine: there's exactly one prod environment, and
  # this bucket is itself managed by module.terraform_state_bucket in
  # main.tf (bootstrapped with local state before this block existed).
  backend "gcs" {
    bucket = "reaction-engine-501316-tfstate"
    prefix = "terraform/state/prod"
  }
}

provider "google" {
  project = var.project_id
  region  = var.region
}
