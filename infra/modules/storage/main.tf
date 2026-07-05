resource "google_storage_bucket" "this" {
  project                     = var.project_id
  name                        = var.name
  location                    = var.location
  uniform_bucket_level_access = var.uniform_bucket_level_access
  force_destroy               = var.force_destroy

  versioning {
    enabled = var.versioning_enabled
  }
}

# Flattened so each (role, member) pair gets its own resource instance --
# needed because for_each requires a map/set of strings, not the nested
# list(object) shape iam_bindings takes as input.
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

resource "google_storage_bucket_iam_member" "this" {
  for_each = local.iam_bindings_flat

  bucket = google_storage_bucket.this.name
  role   = each.value.role
  member = each.value.member
}
