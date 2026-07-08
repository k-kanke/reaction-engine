# artifact-registry

Terraform module for a single Artifact Registry Docker repository, used to
hold the backend service images (`r-gateway`, `r-media-api`, `r-writer`,
`r-image-worker`, ...) built from `backend/Dockerfile` before Cloud Run
deploys them (`plan/gcp-adapter-migration-phase14.md` / Cloud Run
deployment plan in `docs/system-computation-flow.md`).

## Usage

```hcl
module "backend_images" {
  source        = "../../modules/artifact-registry"
  project_id    = var.project_id
  location      = var.region
  repository_id = "reaction-engine-backend"
}
```

Authenticate `docker` to push, once per machine:

```bash
gcloud auth configure-docker asia-northeast1-docker.pkg.dev
```

Build and push one service (see `backend/Dockerfile`'s `SERVICE` build arg).
Tag with the short git commit hash the image was built from -- never
`:latest` -- so a Cloud Run revision always traces back to an exact source
commit:

```bash
TAG=$(git rev-parse --short HEAD)
docker build --build-arg SERVICE=media-api -t <repository_url>/media-api:$TAG backend/
docker push <repository_url>/media-api:$TAG
```

Grant a Cloud Run service's runtime service account pull access via
`iam_bindings` rather than a project-wide `roles/artifactregistry.reader`:

```hcl
iam_bindings = [
  {
    role    = "roles/artifactregistry.reader"
    members = [module.media_service_account.member]
  }
]
```
