output "host" {
  value       = google_redis_instance.this.host
  description = "Redis instance IP, VPC-internal only -- only reachable from services on the same network (via a Serverless VPC Access connector for Cloud Run). Combine with the port output as REDIS_ADDR, e.g. \"host:port\"."
}

output "port" {
  value = google_redis_instance.this.port
}

output "id" {
  value = google_redis_instance.this.id
}
