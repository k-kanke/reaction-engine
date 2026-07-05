output "connection_name" {
  value       = google_sql_database_instance.this.connection_name
  description = "Pass to cloud-sql-proxy / cloud_sql_proxy as <this>:5432 for local connections."
}

output "public_ip_address" {
  value       = google_sql_database_instance.this.public_ip_address
  description = "Only reachable through the Cloud SQL Auth Proxy or an authorized network -- not open to the internet by default."
}

output "database_name" {
  value = google_sql_database.this.name
}

output "database_user" {
  value = google_sql_user.this.name
}

output "database_password" {
  value       = random_password.db_user.result
  sensitive   = true
  description = "Retrieve with `terraform output -raw database_password`."
}
