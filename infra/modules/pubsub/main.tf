resource "google_pubsub_topic" "this" {
  project = var.project_id
  name    = var.topic_name
}

# Poison messages (malformed payloads, or a handler that keeps 5xx-ing)
# land here after max_delivery_attempts instead of retrying forever --
# nothing consumes this topic automatically; it exists so a stuck message
# is inspectable (gcloud pubsub subscriptions pull) instead of silently
# discarded or retried at cost indefinitely.
resource "google_pubsub_topic" "dead_letter" {
  project = var.project_id
  name    = "${var.topic_name}-dlq"
}

resource "google_pubsub_subscription" "push" {
  project = var.project_id
  name    = "${var.topic_name}-push"
  topic   = google_pubsub_topic.this.id

  ack_deadline_seconds = var.ack_deadline_seconds

  push_config {
    push_endpoint = var.push_endpoint
    oidc_token {
      service_account_email = var.push_service_account_email
    }
  }

  retry_policy {
    minimum_backoff = "${var.minimum_backoff_seconds}s"
    maximum_backoff = "${var.maximum_backoff_seconds}s"
  }

  dead_letter_policy {
    dead_letter_topic     = google_pubsub_topic.dead_letter.id
    max_delivery_attempts = var.max_delivery_attempts
  }
}

# Dead-lettering itself is performed by Google's Pub/Sub service agent, not
# by our own service accounts -- it needs publish rights on the DLQ topic
# and subscribe (to pull-and-forward failed messages) on the main
# subscription. Without these two bindings dead_letter_policy above is
# silently ignored and messages just retry forever instead.
resource "google_pubsub_topic_iam_member" "dead_letter_publisher" {
  project = var.project_id
  topic   = google_pubsub_topic.dead_letter.name
  role    = "roles/pubsub.publisher"
  member  = "serviceAccount:service-${var.project_number}@gcp-sa-pubsub.iam.gserviceaccount.com"
}

resource "google_pubsub_subscription_iam_member" "dead_letter_subscriber" {
  project      = var.project_id
  subscription = google_pubsub_subscription.push.name
  role         = "roles/pubsub.subscriber"
  member       = "serviceAccount:service-${var.project_number}@gcp-sa-pubsub.iam.gserviceaccount.com"
}

resource "google_pubsub_topic_iam_member" "publishers" {
  for_each = toset(var.publisher_members)

  project = var.project_id
  topic   = google_pubsub_topic.this.name
  role    = "roles/pubsub.publisher"
  member  = each.value
}
