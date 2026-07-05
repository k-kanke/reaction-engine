output "name" {
  value       = google_storage_bucket.this.name
  description = "Bucket name."
}

output "url" {
  value       = "gs://${google_storage_bucket.this.name}"
  description = "gs:// URL, matching the media_ref scheme in architecture.md."
}
