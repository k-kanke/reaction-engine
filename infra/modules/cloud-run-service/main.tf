resource "google_cloud_run_v2_service" "this" {
  project             = var.project_id
  name                = var.service_name
  location            = var.location
  deletion_protection = var.deletion_protection

  template {
    service_account = var.service_account_email

    scaling {
      min_instance_count = var.min_instance_count
      max_instance_count = var.max_instance_count
    }

    containers {
      image = var.image

      ports {
        container_port = var.container_port
      }

      resources {
        limits = {
          cpu    = var.cpu
          memory = var.memory
        }
      }

      dynamic "env" {
        for_each = var.env_vars
        content {
          name  = env.key
          value = env.value
        }
      }

      dynamic "volume_mounts" {
        for_each = length(var.cloudsql_connection_names) > 0 ? [1] : []
        content {
          name       = "cloudsql"
          mount_path = "/cloudsql"
        }
      }
    }

    dynamic "volumes" {
      for_each = length(var.cloudsql_connection_names) > 0 ? [1] : []
      content {
        name = "cloudsql"
        cloud_sql_instance {
          instances = var.cloudsql_connection_names
        }
      }
    }
  }
}

# Flattened for the same reason as modules/storage's iam_bindings_flat --
# for_each needs a map/set, not a nested list(object).
locals {
  invoker_members = var.allow_unauthenticated ? ["allUsers"] : var.invoker_members
}

resource "google_cloud_run_v2_service_iam_member" "invoker" {
  for_each = toset(local.invoker_members)

  project  = var.project_id
  location = google_cloud_run_v2_service.this.location
  name     = google_cloud_run_v2_service.this.name
  role     = "roles/run.invoker"
  member   = each.value
}
