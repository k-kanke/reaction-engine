output "email" {
  value       = google_service_account.this.email
  description = "Full service account email, e.g. name@project.iam.gserviceaccount.com."
}

output "member" {
  value       = "serviceAccount:${google_service_account.this.email}"
  description = "IAM member string, ready to drop into another resource's iam_bindings/members list."
}
