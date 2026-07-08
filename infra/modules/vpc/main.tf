# A dedicated VPC + Serverless VPC Access connector so Cloud Run services
# can reach Memorystore for Redis (VPC-internal IP only). Serverless VPC
# Access requires this connector's subnet to be small and unshared with
# anything else -- google_vpc_access_connector documents /28 as the
# minimum practical size.

resource "google_compute_network" "this" {
  project                 = var.project_id
  name                    = var.network_name
  auto_create_subnetworks = false
}

resource "google_compute_subnetwork" "connector" {
  project       = var.project_id
  name          = "${var.network_name}-connector-subnet"
  region        = var.region
  network       = google_compute_network.this.id
  ip_cidr_range = var.connector_subnet_cidr
}

resource "google_vpc_access_connector" "this" {
  project = var.project_id
  name    = var.connector_name
  region  = var.region

  subnet {
    name = google_compute_subnetwork.connector.name
  }

  min_instances = var.connector_min_instances
  max_instances = var.connector_max_instances
  machine_type  = var.connector_machine_type
}
