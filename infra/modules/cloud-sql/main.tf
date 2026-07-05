resource "google_sql_database_instance" "this" {
  project             = var.project_id
  name                = var.instance_name
  region              = var.region
  database_version    = var.database_version
  deletion_protection = var.deletion_protection

  settings {
    tier = var.tier

    ip_configuration {
      ipv4_enabled = true
    }
  }
}

resource "google_sql_database" "this" {
  project  = var.project_id
  instance = google_sql_database_instance.this.name
  name     = var.database_name
}

# Generated rather than passed in as a variable, so the password never has
# to be typed into terraform.tfvars (which would then need protecting like
# a secret in its own right). Retrieve it with `terraform output -raw
# database_password`.
resource "random_password" "db_user" {
  length  = 24
  special = false
}

resource "google_sql_user" "this" {
  project  = var.project_id
  instance = google_sql_database_instance.this.name
  name     = var.database_user
  password = random_password.db_user.result
}
