output "repository_id" {
  value       = google_artifact_registry_repository.this.repository_id
  description = "Repository name as passed in."
}

output "repository_url" {
  value       = "${var.location}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.this.repository_id}"
  description = "Prefix for docker tag/push, e.g. `docker push <this>/media-api:<tag>`."
}
