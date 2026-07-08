output "network_id" {
  value       = google_compute_network.this.id
  description = "Pass to modules/memorystore's network_id so Memorystore attaches to this VPC."
}

output "network_self_link" {
  value = google_compute_network.this.self_link
}

output "connector_id" {
  value       = google_vpc_access_connector.this.id
  description = "Pass to modules/cloud-run-service's vpc_connector so that service can reach VPC-internal resources (e.g. Memorystore)."
}
