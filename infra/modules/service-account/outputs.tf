output "email" {
  value       = google_service_account.this.email
  description = "Full service account email, e.g. name@project.iam.gserviceaccount.com."
}

output "member" {
  value       = "serviceAccount:${google_service_account.this.email}"
  description = "IAM member string, ready to drop into another resource's iam_bindings/members list."
}

output "name" {
  value       = google_service_account.this.name
  description = "Fully-qualified resource name (projects/{project}/serviceAccounts/{email}), for google_service_account_iam_member.service_account_id."
}
