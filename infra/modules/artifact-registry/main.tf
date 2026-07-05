resource "google_artifact_registry_repository" "this" {
  project       = var.project_id
  location      = var.location
  repository_id = var.repository_id
  description   = var.description
  format        = "DOCKER"
}

# Flattened for the same reason as modules/storage's iam_bindings_flat:
# for_each needs a map/set, not the nested list(object) shape this variable
# takes as input.
locals {
  iam_bindings_flat = { for pair in flatten([
    for binding in var.iam_bindings : [
      for member in binding.members : {
        key    = "${binding.role}:${member}"
        role   = binding.role
        member = member
      }
    ]
  ]) : pair.key => pair }
}

resource "google_artifact_registry_repository_iam_member" "this" {
  for_each = local.iam_bindings_flat

  project    = var.project_id
  location   = google_artifact_registry_repository.this.location
  repository = google_artifact_registry_repository.this.repository_id
  role       = each.value.role
  member     = each.value.member
}
