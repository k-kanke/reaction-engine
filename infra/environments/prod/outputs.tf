output "media_bucket_name" {
  value = module.media_bucket.name
}

output "media_bucket_url" {
  value = module.media_bucket.url
}

output "media_service_account_email" {
  value       = module.media_service_account.email
  description = "Pass to `gcloud iam service-accounts keys create --iam-account=<this>` to mint a key for GOOGLE_APPLICATION_CREDENTIALS."
}

output "terraform_state_bucket_name" {
  value = module.terraform_state_bucket.name
}

output "db_connection_name" {
  value       = module.db.connection_name
  description = "Pass to cloud-sql-proxy for local connections, and to Cloud Run's Cloud SQL volume config once deployed."
}

output "db_public_ip_address" {
  value = module.db.public_ip_address
}

output "db_database_name" {
  value = module.db.database_name
}

output "db_database_user" {
  value = module.db.database_user
}

output "db_database_password" {
  value       = module.db.database_password
  sensitive   = true
  description = "Retrieve with `terraform output -raw db_database_password`."
}

output "backend_images_repository_url" {
  value       = module.backend_images.repository_url
  description = "Prefix for docker tag/push, e.g. `docker push <this>/media-api:<tag>`."
}

output "media_api_url" {
  value       = module.media_api_service.uri
  description = "r-media-api's Cloud Run URL. Only media_api_invoker_members can call it, via an identity token impersonating tester_service_account_email (see this file's README for the full command) -- a plain `gcloud auth print-identity-token` for your user account has the wrong audience and gets silently 404'd."
}

output "tester_service_account_email" {
  value       = module.tester_service_account.email
  description = "Impersonate this (if you're in media_api_invoker_members) to mint a correctly-audienced identity token for testing IAM-protected Cloud Run services. See README."
}

output "redis_host" {
  value       = module.redis.host
  description = "VPC-internal IP, only reachable from resources on module.vpc (e.g. r-gateway, r-image-worker via the Serverless VPC Access connector). Combine with redis_port as REDIS_ADDR."
}

output "redis_port" {
  value = module.redis.port
}

output "gateway_url" {
  value       = module.gateway_service.uri
  description = "r-gateway's Cloud Run URL (wss://<this-without-https>/ws for the WebSocket endpoint). Publicly reachable (allow_unauthenticated) since Chrome extension clients can't hold GCP identity tokens -- see plan/gcp-deployment-runbook.md Step J."
}

output "jsonl_bucket_name" {
  value = module.jsonl_bucket.name
}

output "writer_service_account_email" {
  value = module.writer_service_account.email
}

output "writer_url" {
  value       = module.writer_service.uri
  description = "r-writer's Cloud Run URL. Not meant to be called -- it's a poll-loop worker with only a /healthz endpoint -- but Cloud Run always assigns one."
}

output "image_analysis_worker_service_account_email" {
  value = module.image_analysis_worker_service_account.email
}

output "image_analysis_worker_url" {
  value       = module.image_analysis_worker_service.uri
  description = "r-image-analysis-worker's Cloud Run URL. Not meant to be called -- it's a poll-loop worker with only a /debug/healthz endpoint -- but Cloud Run always assigns one."
}

output "post_session_job_name" {
  value       = module.post_session_job.name
  description = "Trigger with `gcloud run jobs execute r-post-session-job --region=<region> --args=--session-id=<id>`."
}

output "pdf_renderer_job_name" {
  value       = module.pdf_renderer_job.name
  description = "Trigger with `gcloud run jobs execute r-pdf-renderer --region=<region> --args=--session-id=<id>`."
}

output "gmail_sender_job_name" {
  value       = module.gmail_sender_job.name
  description = "Trigger with `gcloud run jobs execute r-gmail-sender --region=<region> --args=--session-id=<id>,--to=<email>`."
}

output "pubsub_push_service_account_email" {
  value = module.pubsub_push_service_account.email
}

output "feature_events_topic_name" {
  value = module.feature_events_pubsub.topic_name
}

output "media_analysis_events_topic_name" {
  value = module.media_analysis_events_pubsub.topic_name
}
