resource "google_service_account" "this" {
  project      = var.project_id
  account_id   = var.account_id
  display_name = var.display_name
}

# Project-level roles (e.g. roles/pubsub.publisher), for when a role can't
# be scoped to one resource. Resource-scoped bindings (e.g. this bucket
# only) are attached from the resource's own module instead -- see
# infra/environments/prod/main.tf wiring this account's email into
# module.media_bucket's iam_bindings.
resource "google_project_iam_member" "roles" {
  for_each = toset(var.project_roles)

  project = var.project_id
  role    = each.value
  member  = "serviceAccount:${google_service_account.this.email}"
}
