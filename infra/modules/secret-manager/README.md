# secret-manager

Creates one Secret Manager secret + its initial version + resource-scoped
`roles/secretmanager.secretAccessor` bindings, per `accessor_members`. One
module instance = one secret (same convention as `modules/pubsub`'s one
instance = one topic).

Pair with `modules/cloud-run-job`'s `secret_env_vars` to mount the secret's
latest version as an env var in a job, e.g.:

```hcl
module "gmail_oauth_refresh_token" {
  source = "../../modules/secret-manager"

  project_id       = var.project_id
  secret_id        = "gmail-oauth-refresh-token"
  secret_value     = var.gmail_oauth_refresh_token
  accessor_members = [module.gmail_sender_service_account.member]
}

module "gmail_sender_job" {
  source = "../../modules/cloud-run-job"
  # ...
  secret_env_vars = {
    GMAIL_OAUTH_REFRESH_TOKEN = { secret_id = module.gmail_oauth_refresh_token.secret_id }
  }
}
```
