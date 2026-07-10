resource "google_cloud_run_v2_job" "this" {
  project             = var.project_id
  name                = var.job_name
  location            = var.location
  deletion_protection = var.deletion_protection

  template {
    template {
      service_account = var.service_account_email
      max_retries     = var.max_retries
      timeout         = var.task_timeout

      containers {
        image = var.image
        args  = var.args

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

        dynamic "env" {
          for_each = var.secret_env_vars
          content {
            name = env.key
            value_source {
              secret_key_ref {
                secret  = env.value.secret_id
                version = env.value.version
              }
            }
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
}

resource "google_cloud_run_v2_job_iam_member" "invoker" {
  for_each = toset(var.invoker_members)

  project  = var.project_id
  location = google_cloud_run_v2_job.this.location
  name     = google_cloud_run_v2_job.this.name
  # Not roles/run.invoker: every real caller of a Cloud Run Job in this
  # codebase runs it via RunJobRequest.Overrides (--session-id, --to --
  # see the `args` variable's doc comment), which needs the
  # run.jobs.runWithOverrides permission. run.invoker only grants
  # run.jobs.run (no-overrides), so postsessiontrigger.CloudRunTrigger's
  # calls were failing with PermissionDenied on run.jobs.runWithOverrides
  # until this was roles/run.jobsExecutorWithOverrides instead.
  role   = "roles/run.jobsExecutorWithOverrides"
  member = each.value
}

# CloudRunTrigger.runJob (backend/internal/postsessiontrigger) blocks on
# RunJob's long-running operation via op.Wait(ctx), which polls
# run.operations.get -- not covered by run.jobsExecutorWithOverrides
# either. roles/run.viewer is the smallest predefined role that has it
# (plus the harmless run.operations.list/run.*.get-style read permissions
# viewing this job's own executions needs), so it's granted alongside
# rather than widening to roles/run.developer just for one permission.
resource "google_cloud_run_v2_job_iam_member" "operations_viewer" {
  for_each = toset(var.invoker_members)

  project  = var.project_id
  location = google_cloud_run_v2_job.this.location
  name     = google_cloud_run_v2_job.this.name
  role     = "roles/run.viewer"
  member   = each.value
}
