# Memorystore for Redis: architecture.md's Realtime state store (mood wave
# recent window, transcript recent window, trigger/cooldown). BASIC tier
# (no HA replica) is the cheapest option, fine for MVP traffic; STANDARD_HA
# adds a replica with automatic failover but note that tier changes require
# recreating the instance (no in-place upgrade), so switching later means
# planned downtime.

resource "google_redis_instance" "this" {
  project        = var.project_id
  name           = var.instance_name
  region         = var.region
  tier           = var.tier
  memory_size_gb = var.memory_size_gb
  redis_version  = var.redis_version

  authorized_network = var.network_id
  # DIRECT_PEERING (Memorystore's original connect mode) sets up its own
  # VPC peering automatically -- no extra resources needed. The newer
  # PRIVATE_SERVICE_ACCESS mode is what Google now recommends, but it
  # requires provisioning a reserved IP range
  # (google_compute_global_address) and a
  # google_service_networking_connection first; skipped for this MVP step
  # to keep modules/vpc self-contained. Revisit if/when this needs to
  # share Private Service Access with other managed services.
  connect_mode            = "DIRECT_PEERING"
  transit_encryption_mode = "DISABLED" # matches internal/redis.NewClient's plain (non-TLS) connection

  lifecycle {
    prevent_destroy = true
  }
}
