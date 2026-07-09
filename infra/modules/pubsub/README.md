# pubsub

Terraform module for one Pub/Sub topic + push subscription + dead-letter
topic, generic enough to reuse for both `feature-events` (gateway ->
r-writer) and `media-analysis-events` (media-api -> r-image-analysis-worker)
-- plan/post-session-report-implementation.md's Pub/Sub migration off of
`internal/db.LocalEventStore`'s Postgres-polling stand-in.

Notable choices baked in:

- **Push, not pull.** The subscription calls `push_endpoint` directly over
  HTTPS with an OIDC token from `push_service_account_email`. This is what
  lets the target Cloud Run service scale to zero when idle -- a pull
  subscriber has to stay running to poll, which is exactly the
  always-billed problem this migration replaces.
- **The target Cloud Run service must grant `push_service_account_email`
  `roles/run.invoker` itself** (via that service's own `invoker_members` in
  `modules/cloud-run-service`) -- this module only creates the Pub/Sub side.
  Without it, Cloud Run rejects the push before the container ever sees it.
- **Dead-letter topic included.** After `max_delivery_attempts` failed
  deliveries, Pub/Sub moves the message to `<topic_name>-dlq` instead of
  retrying forever. Nothing consumes that topic automatically -- it exists
  so a stuck/poison message is inspectable rather than silently retried at
  cost indefinitely.
- **`publisher_members` grants `roles/pubsub.publisher` scoped to just this
  topic**, not project-wide, matching this repo's general pattern of
  resource-scoped IAM over project roles.

## Usage

```hcl
module "feature_events_pubsub" {
  source = "../../modules/pubsub"

  project_id     = var.project_id
  project_number = data.google_project.current.number

  topic_name                 = "feature-events"
  push_endpoint               = "${module.writer_service.uri}/pubsub/push"
  push_service_account_email  = module.pubsub_push_service_account.email
  publisher_members           = [module.gateway_service_account.member]
}
```
