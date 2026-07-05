output "media_bucket_name" {
  value = module.media_bucket.name
}

output "media_bucket_url" {
  value = module.media_bucket.url
}

output "media_service_account_email" {
  value       = module.media_service_account.email
  description = "Pass to `gcloud iam service-accounts keys create --iam-account=<this>` to mint a key for GOOGLE_APPLICATION_CREDENTIALS."
}

output "terraform_state_bucket_name" {
  value = module.terraform_state_bucket.name
}

output "db_connection_name" {
  value       = module.db.connection_name
  description = "Pass to cloud-sql-proxy for local connections, and to Cloud Run's Cloud SQL volume config once deployed."
}

output "db_public_ip_address" {
  value = module.db.public_ip_address
}

output "db_database_name" {
  value = module.db.database_name
}

output "db_database_user" {
  value = module.db.database_user
}

output "db_database_password" {
  value       = module.db.database_password
  sensitive   = true
  description = "Retrieve with `terraform output -raw db_database_password`."
}
