output "topic_name" {
  value = google_pubsub_topic.this.name
}

output "topic_id" {
  value = google_pubsub_topic.this.id
}

output "dead_letter_topic_name" {
  value = google_pubsub_topic.dead_letter.name
}

output "subscription_name" {
  value = google_pubsub_subscription.push.name
}
