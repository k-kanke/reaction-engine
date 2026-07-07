output "uri" {
  value       = google_cloud_run_v2_service.this.uri
  description = "HTTPS URL Cloud Run assigns the service, e.g. https://r-media-api-xxxxx-an.a.run.app."
}

output "name" {
  value       = google_cloud_run_v2_service.this.name
  description = "Service name as passed in."
}
