output "secret_id" {
  value       = google_secret_manager_secret.this.secret_id
  description = "Secret ID, for cloud-run-job/cloud-run-service's secret_env_vars (secret_id field)."
  # secret_env_vars defaults to version = "latest" -- without this,
  # nothing downstream references google_secret_manager_secret_version, so
  # Terraform has no reason to order a consumer (e.g. a Cloud Run Job) after
  # the version actually exists. That race produced "Secret ...
  # versions/latest was not found" when the job update ran concurrently
  # with (or before) the version's creation.
  depends_on = [google_secret_manager_secret_version.this]
}
