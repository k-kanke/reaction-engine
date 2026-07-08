output "name" {
  value       = google_cloud_run_v2_job.this.name
  description = "Job name as passed in."
}

output "id" {
  value       = google_cloud_run_v2_job.this.id
  description = "Full resource ID, e.g. for `gcloud run jobs execute` or the Admin API's RunJobRequest."
}
