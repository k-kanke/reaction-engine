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
