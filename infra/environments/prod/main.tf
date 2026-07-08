# Phase 14 Step 14-1 (plan/gcp-adapter-migration-phase14.md): the Cloud
# Storage bucket media-api / image-analysis-worker use for baseline frames
# (backend/internal/media.GCSMediaStore), plus the dedicated service
# account that backend uses to sign upload URLs and read/write objects --
# scoped to only this bucket, not the whole project. The account's key
# (a secret) is intentionally not created here; see
# modules/service-account/README.md.

data "google_project" "current" {
  project_id = var.project_id
}

module "media_service_account" {
  source = "../../modules/service-account"

  project_id   = var.project_id
  account_id   = "reaction-engine-media-api"
  display_name = "Reaction Engine Media API"

  # roles/cloudsql.client can't be scoped to one instance (Cloud SQL has no
  # per-instance IAM binding like buckets do) -- it's what lets Cloud Run's
  # built-in Cloud SQL volume actually authenticate to the Cloud SQL Admin
  # API on this account's behalf. Without it the /cloudsql socket exists
  # but nothing answers on it (dial: connection refused).
  project_roles = ["roles/cloudsql.client"]
}

# Step C (deploy plan): lets media_service_account sign Cloud Storage V4
# URLs via the IAM Credentials SignBlob RPC (backend/internal/media.GCSMediaStore)
# instead of a downloaded key file -- needed once it's attached as a Cloud
# Run service's runtime identity, since Cloud Run has no key file to read.
# Self-scoped: it can only impersonate itself, not any other account.
resource "google_service_account_iam_member" "media_service_account_token_creator" {
  service_account_id = module.media_service_account.name
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = module.media_service_account.member
}

module "media_bucket" {
  source = "../../modules/storage"

  project_id = var.project_id
  name       = var.media_bucket_name
  location   = var.region

  cors = [
    {
      origins          = ["*"]
      methods          = ["GET", "HEAD", "PUT", "POST", "OPTIONS"]
      response_headers = ["Content-Type", "x-goog-resumable"]
      max_age_seconds  = 3600
    }
  ]

  iam_bindings = [
    {
      role    = "roles/storage.objectAdmin"
      members = [module.media_service_account.member]
    },
    {
      role = "roles/storage.objectViewer"
      members = [
        "serviceAccount:service-${data.google_project.current.number}@gcp-sa-aiplatform.iam.gserviceaccount.com",
      ]
    }
  ]
}

# Bootstrap bucket for this environment's own Terraform state. Created with
# the local backend still active (see versions.tf); once it exists, switch
# the backend block to `gcs` and run `terraform init -migrate-state` to
# move the state file into it. Versioned so a corrupted/bad state push can
# be rolled back.
module "terraform_state_bucket" {
  source = "../../modules/storage"

  project_id         = var.project_id
  name               = var.terraform_state_bucket_name
  location           = var.region
  versioning_enabled = true
}

# Step A (docs/system-computation-flow.md deploy plan / plan/gcp-adapter-migration-phase14.md
# Step 14-3): the Cloud SQL instance media-api (and later other services)
# use instead of the local Docker Compose Postgres. Public IP only, reached
# via the Cloud SQL Auth Proxy locally and via Cloud Run's built-in Cloud
# SQL connector once deployed -- no VPC needed for this piece.
module "db" {
  source = "../../modules/cloud-sql"

  project_id    = var.project_id
  region        = var.region
  instance_name = var.db_instance_name
}

# Step B (deploy plan): where backend Docker images (built via
# backend/Dockerfile's SERVICE build arg) live before Cloud Run deploys
# them. media_service_account gets pull access here since Step D/E attach
# it as r-media-api's Cloud Run runtime identity.
module "backend_images" {
  source = "../../modules/artifact-registry"

  project_id    = var.project_id
  location      = var.region
  repository_id = var.backend_images_repository_id
  description   = "Backend service images (gateway, media-api, writer, image-analysis-worker, post-session-job)"

  iam_bindings = [
    {
      role    = "roles/artifactregistry.reader"
      members = [module.media_service_account.member, module.gateway_service_account.member]
    }
  ]
}

# Step E (deploy plan): the media-api Cloud Run service itself. Originally
# restricted to media_api_invoker_members since no application-level auth
# existed yet -- but that also meant the Chrome extension itself (which
# can't hold a GCP identity token) got rejected before reaching the
# container, so upload-url/complete calls from real sessions silently never
# happened and no baseline/evidence frame ever reached GCS. Now public, same
# tradeoff module.gateway_service already accepts below.
module "media_api_service" {
  source = "../../modules/cloud-run-service"

  project_id = var.project_id
  location   = var.region

  service_name              = "r-media-api"
  image                     = "${module.backend_images.repository_url}/media-api:${var.media_api_image_tag}"
  service_account_email     = module.media_service_account.email
  container_port            = 8080
  cloudsql_connection_names = [module.db.connection_name]

  env_vars = {
    MEDIA_STORE_BACKEND = "gcs"
    GCS_MEDIA_BUCKET    = module.media_bucket.name
    MEDIA_API_PORT      = "8080"
    DATABASE_URL        = "postgres://${module.db.database_user}:${module.db.database_password}@/${module.db.database_name}?host=/cloudsql/${module.db.connection_name}&sslmode=disable"
  }

  invoker_members       = concat(var.media_api_invoker_members, [module.tester_service_account.member])
  allow_unauthenticated = true
}

# gcloud auth print-identity-token for a *user* account carries gcloud's
# own OAuth client ID as its audience, not the Cloud Run service URL --
# Cloud Run then rejects it (as a generic 404, not 403, to avoid leaking
# whether the service exists) before the request ever reaches the
# container. The documented workaround is impersonating a service account,
# which supports minting an ID token with the right --audiences. This SA
# exists only for that: humans in media_api_invoker_members can
# impersonate it to test IAM-protected Cloud Run services with curl.
module "tester_service_account" {
  source = "../../modules/service-account"

  project_id   = var.project_id
  account_id   = "reaction-engine-tester"
  display_name = "Reaction Engine Local Tester (impersonate-only, for curl verification)"
}

resource "google_service_account_iam_member" "tester_service_account_impersonators" {
  for_each = toset(var.media_api_invoker_members)

  service_account_id = module.tester_service_account.name
  role               = "roles/iam.serviceAccountTokenCreator"
  member             = each.value
}

# Step I (plan/gcp-deployment-runbook.md): dedicated VPC + Serverless VPC
# Access connector so Cloud Run services can reach Memorystore for Redis
# (VPC-internal IP only -- Cloud Run isn't in any VPC by default).
module "vpc" {
  source = "../../modules/vpc"

  project_id            = var.project_id
  region                = var.region
  network_name          = "reaction-engine-vpc"
  connector_subnet_cidr = "10.8.0.0/28"
  connector_name        = "reaction-engine-connector"
}

# Step I: Memorystore for Redis, architecture.md's Realtime state store
# (mood_wave/transcript recent windows, trigger/feedback cooldown --
# internal/redis/client.go). BASIC tier (no replica) for MVP traffic; see
# modules/memorystore/README.md for the tradeoffs of upgrading later.
module "redis" {
  source = "../../modules/memorystore"

  project_id     = var.project_id
  region         = var.region
  instance_name  = "reaction-engine-redis"
  network_id     = module.vpc.network_id
  memory_size_gb = 1
}

# Step J (plan/gcp-deployment-runbook.md): the realtime WebSocket entry
# point. Needs both Cloud SQL (local_events polling, participant baselines)
# and Redis (mood wave / transcript recent windows, feedback cooldown) --
# the latter via module.vpc's connector, unlike media-api which only needs
# the Cloud SQL built-in volume.
module "gateway_service_account" {
  source = "../../modules/service-account"

  project_id   = var.project_id
  account_id   = "reaction-engine-gateway"
  display_name = "Reaction Engine Gateway"
  # roles/speech.client: Step 4 of plan/realtime-llm-context-next-steps.md
  # (real Speech-to-Text streaming). Requires speech.googleapis.com to be
  # enabled on the project first -- not managed by this Terraform config,
  # same as aiplatform/storage/sql (see internal/speech/recognizer.go).
  project_roles = ["roles/cloudsql.client", "roles/aiplatform.user", "roles/speech.client"]
}

module "gateway_service" {
  source = "../../modules/cloud-run-service"

  project_id = var.project_id
  location   = var.region

  service_name              = "r-gateway"
  image                     = "${module.backend_images.repository_url}/gateway:${var.gateway_image_tag}"
  service_account_email     = module.gateway_service_account.email
  container_port            = 8080
  cloudsql_connection_names = [module.db.connection_name]
  vpc_connector             = module.vpc.connector_id
  vpc_egress                = "PRIVATE_RANGES_ONLY"

  env_vars = {
    GATEWAY_PORT          = "8080"
    REDIS_ADDR            = "${module.redis.host}:${module.redis.port}"
    ENABLE_REAL_LLM       = "true"
    VERTEX_PROJECT        = var.project_id
    VERTEX_LOCATION       = var.region
    VERTEX_REALTIME_MODEL = "gemini-2.5-flash"
    ENABLE_REAL_STT       = "true"
    STT_LANGUAGE_CODE     = "ja-JP"
    DATABASE_URL          = "postgres://${module.db.database_user}:${module.db.database_password}@/${module.db.database_name}?host=/cloudsql/${module.db.connection_name}&sslmode=disable"
  }

  # Chrome extension clients connect directly and can't hold GCP identity
  # tokens, so unlike media-api's invoker_members restriction this has to
  # be public -- there's no application-level auth to fall back on yet
  # (same known gap noted in plan/gcp-deployment-runbook.md Step O).
  allow_unauthenticated = true

  # Keep at least one warm instance so an established WebSocket connection
  # doesn't get cut by a cold start, and cap low for MVP traffic.
  min_instance_count = 1
  max_instance_count = 3
}
