# GCP デプロイ手順(全サービス)

## この文書の位置づけ

`infra/`(Terraform)には、`r-media-api` を Cloud Run にデプロイするまでの作業が既に **Step A〜E** としてコード上のコメント・README に断片的に記録されている(`infra/environments/prod/main.tf` の各 `module` ブロック直上のコメント、`infra/environments/prod/README.md` を参照)。ただしこれらのステップを一つにまとめた文書は存在しなかった。この文書は Step A〜E を踏まえた上で、**残り全サービス**(gateway・writer・image-analysis-worker・post-session-job・pdf-renderer・gmail-sender)と周辺リソース(Memorystore・Pub/Sub・Secret Manager・Monitoring)を Cloud Run にデプロイする手順を Step F 以降としてまとめたもの。

関連ドキュメント:

- `architecture.md` — 論理コンポーネント / GCP サービス対応表(正)。この文書のデプロイ対象はこの表に従う。
- `plan/gcp-adapter-migration-phase14.md` — ローカルスタンドイン実装(fake STT/LLM、`local_events` テーブル等)を実 GCP アダプタに切り替える**コード側**の計画。Cloud Storage(Step 14-1、実装済み)・Cloud SQL(Step 14-3、実装済み)以外はまだ着手されていない(Pub/Sub・Memorystore・STT・Vertex AI)。この文書はその**インフラ側**(Terraform・Cloud Run へのデプロイ)を扱う。両者は補完関係にあり、片方だけでは動かない。
- `plan/mood-wave-contract-migration.md` — アプリケーションロジック(mood_wave_sample モデル)の移行計画。develop に全マージ済み。
- `docs/system-computation-flow.md` — 古い設計(`realtime_feature`/`system_signal`/`reaction_wave` モデル)を前提にしたメモで、現行の `architecture.md`(`mood_wave_sample` モデル)とは食い違っている。Terraform のコメントから参照はされているが、**この文書のデプロイ手順は `architecture.md` を正として書く**。`docs/system-computation-flow.md` 自体の更新はこの文書のスコープ外。

## 現状(Step A〜E、実施済み)

`infra/environments/prod/main.tf` に実装済み。`terraform apply` 済みで `r-media-api` は実際に Cloud Run 上で稼働している(`infra/environments/prod/README.md` にトラブルシュート記録あり)。

| Step | 内容 | Terraform |
| --- | --- | --- |
| A | Cloud SQL インスタンス(`reaction-engine-db`、Postgres 17、`db-f1-micro`、Public IP) | `module.db` |
| B | Artifact Registry リポジトリ(`reaction-engine-backend`) | `module.backend_images` |
| C | GCS 署名 URL 発行用に `media_service_account` へ `roles/iam.serviceAccountTokenCreator`(自己 impersonation)を付与。キーファイル無しで動く前提 | `google_service_account_iam_member.media_service_account_token_creator` |
| D | 汎用 `cloud-run-service` モジュール実装 | `infra/modules/cloud-run-service/` |
| E | `r-media-api` を Cloud Run にデプロイ(`roles/run.invoker` は `media_api_invoker_members` に限定、アプリ側認証が無いため) | `module.media_api_service` |

これに加えて `module.media_bucket`(Phase 14 Step 14-1)、`module.terraform_state_bucket`、`module.tester_service_account`(IAM 保護された Cloud Run を curl で検証するための impersonate 専用アカウント)も存在する。

## デプロイ対象サービス一覧

`backend/cmd/*` の実装(2026-07-08 時点、`develop` ブランチ)を実際に読んで確認した分類。

| サービス | 種別 | 常駐/バッチ | 依存 | 現状 |
| --- | --- | --- | --- | --- |
| gateway | Cloud Run **Service** | 常駐(WebSocket) | Cloud SQL, Redis | 未デプロイ |
| media-api | Cloud Run **Service** | 常駐(HTTP) | Cloud SQL, Cloud Storage | **デプロイ済み**(`r-media-api`) |
| writer | Cloud Run **Service** | 常駐(2秒ポーリング) | Cloud SQL, Cloud Storage(JSONL、Step F対応済み) | 未デプロイ、コード修正は完了。残るは Step G(`/healthz`) |
| image-analysis-worker | Cloud Run **Service** | 常駐(2秒ポーリング、debug HTTP あり) | Cloud SQL, Redis, Cloud Storage | 未デプロイ |
| post-session-job | Cloud Run **Job** | バッチ(`--session-id` 必須) | Cloud SQL, Cloud Storage(JSONL、Step F対応済み) | 未デプロイ、コード修正は完了 |
| pdf-renderer | Cloud Run **Job** | バッチ(`--session-id` 必須) | Cloud SQL, Cloud Storage(PDF、Step F対応済み) | 未デプロイ、コード修正は完了 |
| gmail-sender | Cloud Run **Job** | バッチ(`--session-id --to` 必須) | Cloud SQL | 未デプロイ(コード変更不要) |

`architecture.md` の対応表通り、post-session-job/pdf-renderer は Cloud Run Jobs、gateway/media-api/writer/image-analysis-worker は Cloud Run service として扱う。

## ブロッカー: JSONL永続化がローカルディスク前提になっている(Step F、実装済み)

**Step F は実装・実 GCS バケットでの検証まで完了した**(`feat/gcp-deploy-step-f-jsonl-gcs-store` ブランチ)。この節は元は「これから直す」という前提で書いていたが、実際に手を動かした結果に合わせて書き直してある。

発見した問題: `backend/internal/writer/jsonl.go` の `appendJSONLine` は `os.MkdirAll` + `os.OpenFile` で **常にローカルファイルシステム**に書いていた。Phase 14 Step 14-1 で `MediaStore`(`internal/media`)が `Local`/`GCS` を切り替えられるようになったのと違い、JSONL 書き込み層には local/GCS を切り替える interface が一切無かった。Cloud Run のコンテナインスタンスはインスタンス間でファイルシステムを共有しないため、`writer` が書いた JSONL を別インスタンスの `post-session-job`/`pdf-renderer` が読めず、`important_windows` が常に空になる ── というサイレントな壊れ方をする状態だった。`cmd/pdf-renderer/main.go` も同様に、GCS 対応済みの `internal/media.MediaStore` を使わず `os.WriteFile` で直接ローカルディスクに PDF を書いていた(Step 11 実装時の見落とし)。

### Step F-1: `internal/writer.JSONLStore` interface(実装済み)

`internal/media.MediaStore` のパターンを踏襲し、`internal/writer/store.go` に interface を追加した(名前は当初案の `Store` ではなく `JSONLStore` ── `internal/writer` には既に Postgres 永続化用の `Store` 構造体があり衝突するため):

```go
// internal/writer/store.go
type JSONLStore interface {
    Append(ctx context.Context, sessionID, eventID string, payload any, parts ...string) error
    ReadAll(ctx context.Context, sessionID string, parts ...string) ([][]byte, error)
}
```

`GCSJSONLStore`(`internal/writer/gcs_store.go`)の実装方針は、当初案の2択(read-modify-write / 1行1オブジェクト)のどちらでもなく、**Cloud Storage の Compose API** を使った第3の方式にした:

1. 新しい1行を短命な「ステージングオブジェクト」(`part-0001.jsonl.append-{event_id}`)としてアップロードする。
2. 対象の `part-0001.jsonl` が既に存在すれば `ComposerFrom(target, staging)` で「今の内容 + 新しい1行」を1回の Compose 呼び出しで合成し、`part-0001.jsonl` を上書きする。まだ存在しなければ(セッションの最初の1行)ステージングオブジェクトをそのままコピーするだけ。
3. ステージングオブジェクトは(ベストエフォートで)削除する。

これにより、architecture.md が明記している `sessions/{session_id}/mood-wave/part-0001.jsonl` という**単一ファイルのパス**をそのまま維持しつつ(1行1オブジェクトの方式だとこの命名と食い違う)、既存内容全体を読み直す必要もない(Compose の入力は常に2つ ── 今の `part-0001.jsonl` 自身 + 新しい1行 ── なので、行数が増えてもコストが線形に増えない)。

並行性については、generation precondition によるリトライは実装していない。`writer` を `max_instance_count=1` で運用する前提(後述 Step H)でこれを安全としている。

`LocalJSONLStore`(`internal/writer/local_store.go`)は既存の `appendJSONLine`/`readJSONLLines` のロジックをそのまま移植したもので、ローカル docker compose での挙動は変えていない。

環境変数は `MEDIA_STORE_BACKEND=local|gcs` に倣い `JSONL_STORE_BACKEND=local|gcs` + `GCS_JSONL_BUCKET` を追加した(`.env.example` 参照)。`cmd/writer/main.go`・`cmd/post-session-job/main.go` は新 interface 経由に書き換えた。

### Step F-2: `pdf-renderer` の PDF 書き込み(実装済み)

`internal/media.MediaStore` に新しい `MediaWriter` interface(`Write(ctx, sessionID string, parts []string, data []byte, contentType string) (mediaRef string, err error)`)を追加し、`LocalMediaStore`/`GCSMediaStore` 両方に実装した。`cmd/pdf-renderer/main.go` の `os.WriteFile` 直書きをこれに置き換えた。`MEDIA_STORE_BACKEND` は media-api/image-analysis-worker と共通の環境変数をそのまま使う(新しい環境変数は増やしていない)。

### Step F の完了条件(確認済み)

- `go build ./... && go vet ./... && go test ./...` が通ることを確認した(新規ユニットテスト: `internal/writer/local_store_test.go`、`internal/media/local_store_test.go`)。
- 実 GCS バケット(`reaction-engine-501316-sessions`、Terraform で作成済みのもの)に対して、ローカルの ADC(`gcloud auth application-default login`)から `GCSJSONLStore.Append`/`ReadAll` を実行し、3行 append → 3行読み出し(順序保持)を確認した。同じセッションに対して**プロセスを分けて**再実行し、前回の3行 + 今回の3行 = 6行になることを確認 ── Compose による追記が本当に永続化されていることの確認になる。
- `media.GCSMediaStore.Write` で PDF を書き込み、`Exists`/`Read` で読み戻せることを確認した(`GCSMediaStore` は署名用に実在のサービスアカウント身元を要求するため、ローカル検証時は `.secrets/reaction-engine-media-api-key.json` を `GOOGLE_APPLICATION_CREDENTIALS` 相当として使った。Cloud Run 上ではアタッチされたランタイム SA が自動的に使われるため、これは不要)。
- 検証で作成したオブジェクト(`sessions/sess_step_f_gcs_verify/...`)は `gsutil rm -r` で削除済み。ステージングオブジェクトが残っていないことも確認した(Compose 後の delete が正しく効いている)。

## Step G: `writer` に `/healthz` を追加する

`backend-local-docker-runbook.md` Phase 15 のチェックリスト(「全サービスで `GET /healthz` を実装する」)がまだ `writer` に対して未達。`cmd/writer/main.go` は現状 HTTP リスナーを一切持たない(`image-analysis-worker` は `/debug/healthz` を既に持っている ── `cmd/image-analysis-worker/main.go` を参考にする)。

Cloud Run **Service**(`google_cloud_run_v2_service`)はコンテナが `$PORT` で listen して応答することを健全性の条件にしている。`writer` を Cloud Run Job ではなく Service としてデプロイする(常駐ポーリングという設計上そうすべき、`architecture.md` の対応表とも一致)以上、これは必須。

```go
// cmd/writer/main.go に追加。image-analysis-worker と同じパターン。
mux := http.NewServeMux()
mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
    w.Write([]byte("ok"))
})
port := os.Getenv("WRITER_PORT")
if port == "" {
    port = "8080"
}
go func() {
    log.Printf("writer: healthz endpoint on :%s", port)
    log.Fatal(http.ListenAndServe(":"+port, mux))
}()
```

既存の `for range ticker.C` ループはそのまま `main()` の残りに置く(goroutine 化は不要、health server 側だけ goroutine にする)。

## Step H: `writer` を Cloud Run Service としてデプロイする

Step F・G 完了後。`infra/environments/prod/main.tf` に追加する(既存の `cloud-run-service` モジュールをそのまま再利用):

```hcl
module "writer_service_account" {
  source = "../../modules/service-account"

  project_id    = var.project_id
  account_id    = "reaction-engine-writer"
  display_name  = "Reaction Engine Writer"
  project_roles = ["roles/cloudsql.client"]
}

# Step F の JSONL バケット書き込み権限。media_bucket を流用するか、
# 専用バケットにするかは Step F 実装時に決める(専用バケットの方が
# IAM を絞れて安全 -- media_bucket は baseline frame という別種の
# データを持つため)。
resource "google_storage_bucket_iam_member" "writer_jsonl_bucket_object_admin" {
  bucket = module.media_bucket.name # または新規 module.jsonl_bucket
  role   = "roles/storage.objectAdmin"
  member = module.writer_service_account.member
}

module "writer_service" {
  source = "../../modules/cloud-run-service"

  project_id = var.project_id
  location   = var.region

  service_name              = "r-writer"
  image                     = "${module.backend_images.repository_url}/writer:${var.writer_image_tag}"
  service_account_email     = module.writer_service_account.email
  container_port            = 8080
  cloudsql_connection_names = [module.db.connection_name]

  env_vars = {
    WRITER_PORT       = "8080"
    JSONL_STORE_BACKEND = "gcs"
    GCS_JSONL_BUCKET  = module.media_bucket.name # Step F 参照
    DATABASE_URL      = "postgres://${module.db.database_user}:${module.db.database_password}@/${module.db.database_name}?host=/cloudsql/${module.db.connection_name}&sslmode=disable"
  }

  # writer は local_events テーブルを SELECT ... LIMIT (行ロック無し) で
  # ポーリングする。複数インスタンスが同時に同じ行を取り合っても
  # Ack は冪等・後続の INSERT は event_id で ON CONFLICT DO NOTHING に
  # なっているため二重処理で壊れはしないが、無駄な重複実行が起きる。
  # 行ロック(SELECT ... FOR UPDATE SKIP LOCKED)を足すまでは 1 に
  # 固定しておく。
  min_instance_count = 1
  max_instance_count = 1

  # 外部からの着信 HTTP は無い(WebSocket も受けない、/healthz だけ)。
  # run.invoker を絞る意味が薄いので allUsers で構わない。
  allow_unauthenticated = true
}
```

デプロイ確認は media-api の時と同じ手順(`gcloud builds submit` で Artifact Registry に push → `terraform apply -var="writer_image_tag=$(git rev-parse --short HEAD)"`)。

## Step I: Memorystore for Redis + Serverless VPC Access

gateway と image-analysis-worker は Redis(`REDIS_ADDR`)に依存する。Memorystore for Redis は VPC 内部 IP しか持たないため、Cloud Run から到達するには Serverless VPC Access コネクタが要る(`infra/modules/vpc/README.md` に明記済み)。`infra/modules/vpc/`・`infra/modules/memorystore/` は現状 README のみのプレースホルダーなので、ここで実装する。

```hcl
# infra/modules/vpc/main.tf (新規実装)
resource "google_compute_network" "this" {
  project                 = var.project_id
  name                     = var.network_name
  auto_create_subnetworks  = false
}

resource "google_compute_subnetwork" "this" {
  project       = var.project_id
  name          = "${var.network_name}-subnet"
  region        = var.region
  network       = google_compute_network.this.id
  ip_cidr_range = var.subnet_cidr # 例: "10.8.0.0/28" -- VPCコネクタ用に小さくてよい
}

resource "google_vpc_access_connector" "this" {
  project       = var.project_id
  name          = var.connector_name
  region        = var.region
  subnet {
    name = google_compute_subnetwork.this.name
  }
  min_instances = 2
  max_instances = 3
  machine_type  = "e2-micro"
}
```

```hcl
# infra/modules/memorystore/main.tf (新規実装)
resource "google_redis_instance" "this" {
  project        = var.project_id
  name           = var.instance_name
  region         = var.region
  tier           = "BASIC" # MVP: レプリカ無し。本番負荷が見えてから STANDARD_HA に上げる
  memory_size_gb = var.memory_size_gb # 例: 1
  redis_version  = "REDIS_7_2"
  authorized_network = var.network_id # Step Iのvpcモジュールのnetwork
}
```

`infra/environments/prod/main.tf` に追加:

```hcl
module "vpc" {
  source = "../../modules/vpc"

  project_id     = var.project_id
  region         = var.region
  network_name   = "reaction-engine-vpc"
  subnet_cidr    = "10.8.0.0/28"
  connector_name = "reaction-engine-connector"
}

module "redis" {
  source = "../../modules/memorystore"

  project_id     = var.project_id
  region         = var.region
  instance_name  = "reaction-engine-redis"
  memory_size_gb = 1
  network_id     = module.vpc.network_id
}
```

`cloud-run-service` モジュールに VPC コネクタ用の変数(`vpc_connector`、`vpc_egress`)を追加する必要がある(現状の `variables.tf` には無い ── Step D 実装時点では Cloud SQL 経由の接続しか考慮していなかったため):

```hcl
# infra/modules/cloud-run-service/variables.tf に追加
variable "vpc_connector" {
  type        = string
  default     = null
  description = "Serverless VPC Access connector name/ID. Set when the service needs to reach Memorystore or other VPC-internal resources."
}

variable "vpc_egress" {
  type        = string
  default     = "PRIVATE_RANGES_ONLY"
  description = "PRIVATE_RANGES_ONLY (default, only RFC1918 traffic goes through the connector) or ALL_TRAFFIC."
}
```

`main.tf` の `google_cloud_run_v2_service` リソースに `vpc_access` ブロックを条件付きで追加する(`dynamic "vpc_access"` ブロック、`var.vpc_connector != null` の時だけ生成)。

## Step J: gateway を Cloud Run Service としてデプロイする

Step I 完了後。gateway は WebSocket サーバーであり、Cloud Run の WebSocket サポート(HTTP/1.1 アップグレード)は最大 60 分のリクエストタイムアウト内でのみ有効 ── `architecture.md` の「Cloud Run WebSocket は timeout / reconnect を前提にする」という設計上の注意通り、これは Chrome 拡張側の再接続ロジックで吸収する前提とする(拡張側の対応状況はこの文書のスコープ外)。

```hcl
module "gateway_service_account" {
  source = "../../modules/service-account"

  project_id    = var.project_id
  account_id    = "reaction-engine-gateway"
  display_name  = "Reaction Engine Gateway"
  project_roles = ["roles/cloudsql.client"]
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
    GATEWAY_PORT   = "8080"
    REDIS_ADDR     = "${module.redis.host}:${module.redis.port}"
    ENABLE_REAL_LLM = "false" # Phase 14 Step 14-6 (Vertex AI) が終わるまでは false のまま
    DATABASE_URL   = "postgres://${module.db.database_user}:${module.db.database_password}@/${module.db.database_name}?host=/cloudsql/${module.db.connection_name}&sslmode=disable"
  }

  # Chrome拡張から直接繋ぐエンドポイントなので、アプリ側認証が無い今は
  # allUsers にせざるを得ない(media-apiと同じ理由でinvoker_membersに
  # 絞ることも技術的には可能だが、拡張ユーザー全員がGCP IDトークンを
  # 持てないため実質的にallUsers相当の公開が必要)。architecture.mdの
  # 「実装順」にアプリレベル認証は入っていないため、ここはこの文書の
  # スコープ外の既知の課題として明記するに留める。
  allow_unauthenticated = true

  min_instance_count = 1 # WebSocket接続を維持するため scale-to-zero は避ける
  max_instance_count = 3
}
```

## Step K: image-analysis-worker を Cloud Run Service としてデプロイする

Step I 完了後(Redis 依存)。既に `/debug/healthz` を持つため Step G 相当の追加作業は不要。

```hcl
module "image_worker_service_account" {
  source = "../../modules/service-account"

  project_id    = var.project_id
  account_id    = "reaction-engine-image-worker"
  display_name  = "Reaction Engine Image Analysis Worker"
  project_roles = ["roles/cloudsql.client"]
}

resource "google_storage_bucket_iam_member" "image_worker_media_bucket_reader" {
  bucket = module.media_bucket.name
  role   = "roles/storage.objectViewer" # baseline frameを読むだけ、書かない
  member = module.image_worker_service_account.member
}

module "image_analysis_worker_service" {
  source = "../../modules/cloud-run-service"

  project_id = var.project_id
  location   = var.region

  service_name              = "r-image-analysis-worker"
  image                     = "${module.backend_images.repository_url}/image-analysis-worker:${var.image_worker_image_tag}"
  service_account_email     = module.image_worker_service_account.email
  container_port            = 8080
  cloudsql_connection_names = [module.db.connection_name]
  vpc_connector             = module.vpc.connector_id

  env_vars = {
    IMAGE_ANALYSIS_WORKER_DEBUG_PORT = "8080"
    REDIS_ADDR          = "${module.redis.host}:${module.redis.port}"
    MEDIA_STORE_BACKEND = "gcs"
    GCS_MEDIA_BUCKET    = module.media_bucket.name
    DATABASE_URL        = "postgres://${module.db.database_user}:${module.db.database_password}@/${module.db.database_name}?host=/cloudsql/${module.db.connection_name}&sslmode=disable"
  }

  allow_unauthenticated = true # 着信は無い(pollループのみ)。invoker制限してもよい
  min_instance_count    = 1
  max_instance_count    = 1 # writerと同じ理由(local_eventsの行ロック無し)
}
```

## Step L: post-session-job / pdf-renderer / gmail-sender を Cloud Run Jobs としてデプロイする

Step F 完了後。3つとも `--session-id`(gmail-sender は追加で `--to`)を**必須の起動引数**として取る設計になっており、現状のコードには「セッション終了を検知して自動的にキューする」仕組みが無い(`architecture.md` の実装順ステップ 11「session end で Post-session Job を起動する」に対応するトリガー機構が未実装)。この文書はインフラのデプロイに閉じるため、**自動トリガーの実装はスコープ外**とし、まず手動 `gcloud run jobs execute` で実行できる状態を作ることを Step L のゴールとする。自動化(Cloud Scheduler や Eventarc、あるいはセッション終了APIの新設)は別途アプリ側の設計が要る。

`infra/modules/cloud-run-job/` は現状 README のみなので実装する:

```hcl
# infra/modules/cloud-run-job/main.tf (新規実装)
resource "google_cloud_run_v2_job" "this" {
  project  = var.project_id
  name     = var.job_name
  location = var.location

  template {
    template {
      service_account = var.service_account_email
      timeout          = var.timeout
      max_retries      = var.max_retries

      containers {
        image = var.image
        dynamic "env" {
          for_each = var.env_vars
          content {
            name  = env.key
            value = env.value
          }
        }
        dynamic "volume_mounts" {
          for_each = length(var.cloudsql_connection_names) > 0 ? [1] : []
          content {
            name       = "cloudsql"
            mount_path = "/cloudsql"
          }
        }
      }

      dynamic "volumes" {
        for_each = length(var.cloudsql_connection_names) > 0 ? [1] : []
        content {
          name = "cloudsql"
          cloud_sql_instance {
            instances = var.cloudsql_connection_names
          }
        }
      }
    }
  }

  lifecycle {
    ignore_changes = [
      template[0].template[0].containers[0].image, # gcloud run jobs execute --image で都度上書きする運用も許容
    ]
  }
}
```

```hcl
# infra/modules/cloud-run-job/variables.tf
variable "project_id" {}
variable "location" {}
variable "job_name" { description = "e.g. \"r-post-session-job\"" }
variable "image" {}
variable "service_account_email" {}
variable "env_vars" { type = map(string); default = {} }
variable "cloudsql_connection_names" { type = list(string); default = [] }
variable "timeout" { type = string; default = "600s" }
variable "max_retries" { type = number; default = 1 }
```

`infra/environments/prod/main.tf`:

```hcl
module "jobs_service_account" {
  source = "../../modules/service-account"

  project_id    = var.project_id
  account_id    = "reaction-engine-jobs"
  display_name  = "Reaction Engine Post-session Jobs"
  project_roles = ["roles/cloudsql.client"]
}

resource "google_storage_bucket_iam_member" "jobs_media_bucket_object_admin" {
  bucket = module.media_bucket.name
  role   = "roles/storage.objectAdmin" # pdf-rendererがreport.pdfを書く(Step F-2)
  member = module.jobs_service_account.member
}

locals {
  jobs_database_url = "postgres://${module.db.database_user}:${module.db.database_password}@/${module.db.database_name}?host=/cloudsql/${module.db.connection_name}&sslmode=disable"
}

module "post_session_job" {
  source = "../../modules/cloud-run-job"

  project_id             = var.project_id
  location               = var.region
  job_name               = "r-post-session-job"
  image                  = "${module.backend_images.repository_url}/post-session-job:${var.jobs_image_tag}"
  service_account_email  = module.jobs_service_account.email
  cloudsql_connection_names = [module.db.connection_name]
  env_vars = {
    DATABASE_URL        = local.jobs_database_url
    JSONL_STORE_BACKEND = "gcs"
    GCS_JSONL_BUCKET    = module.media_bucket.name
  }
}

module "pdf_renderer_job" {
  source = "../../modules/cloud-run-job"

  project_id             = var.project_id
  location               = var.region
  job_name               = "r-pdf-renderer"
  image                  = "${module.backend_images.repository_url}/pdf-renderer:${var.jobs_image_tag}"
  service_account_email  = module.jobs_service_account.email
  cloudsql_connection_names = [module.db.connection_name]
  env_vars = {
    DATABASE_URL     = local.jobs_database_url
    MEDIA_STORE_BACKEND = "gcs" # Step F-2
    GCS_MEDIA_BUCKET = module.media_bucket.name
  }
}

module "gmail_sender_job" {
  source = "../../modules/cloud-run-job"

  project_id             = var.project_id
  location               = var.region
  job_name               = "r-gmail-sender"
  image                  = "${module.backend_images.repository_url}/gmail-sender:${var.jobs_image_tag}"
  service_account_email  = module.jobs_service_account.email
  cloudsql_connection_names = [module.db.connection_name]
  env_vars = {
    DATABASE_URL = local.jobs_database_url
  }
}
```

実行例(手動トリガー、`--args` で `main()` の `flag` を上書きする):

```bash
gcloud run jobs execute r-post-session-job --region=asia-northeast1 \
  --args="--session-id=sess_123"
gcloud run jobs execute r-pdf-renderer --region=asia-northeast1 \
  --args="--session-id=sess_123"
gcloud run jobs execute r-gmail-sender --region=asia-northeast1 \
  --args="--session-id=sess_123,--to=presenter@example.com"
```

`gmail-sender` は現状ローカルスタブ(実際に Gmail API を呼ばない、`cmd/gmail-sender/README.md` に明記)なので、デプロイしても実メール送信にはならない。Phase 14 のスコープには Gmail API 実装が入っていないため、これも別途計画が必要(この文書では「ジョブとしてデプロイ可能な状態にする」までを扱う)。

## Step M: Secret Manager へ移行する

現状、`DATABASE_URL` はパスワードを含んだ平文文字列として Cloud Run の `env_vars`(≒ 通常の環境変数)に直接埋め込まれている(Step E の `media_api_service` も同様)。`architecture.md` の対応表は Secret Manager を明示的に要求している。`infra/modules/secret-manager/` を実装し、各サービスの `DATABASE_URL` を Secret Manager 経由に切り替える。

```hcl
# infra/modules/secret-manager/main.tf (新規実装)
resource "google_secret_manager_secret" "this" {
  project   = var.project_id
  secret_id = var.secret_id
  replication {
    auto {}
  }
}

resource "google_secret_manager_secret_version" "this" {
  secret      = google_secret_manager_secret.this.id
  secret_data = var.secret_value
}

resource "google_secret_manager_secret_iam_member" "accessors" {
  for_each  = toset(var.accessor_members)
  project   = var.project_id
  secret_id = google_secret_manager_secret.this.secret_id
  role      = "roles/secretmanager.secretAccessor"
  member    = each.value
}
```

`cloud-run-service`/`cloud-run-job` モジュールに `secret_env_vars`(`map(object({ secret_id = string, version = optional(string, "latest") }))` のような型)を追加し、`google_cloud_run_v2_service` の `containers.env` に `value_source.secret_key_ref` を使う分岐を足す。既存の平文 `env_vars.DATABASE_URL` の組み立てをやめ、`module.db_url_secret` のような形で各サービスに配る。

この Step は他の Step と違って**アプリの動作に必須ではない**(平文環境変数でも動く)ため、優先度は最後でよい。ただし本番運用に入る前には必ずやる。

## Step N: Cloud Logging / Cloud Monitoring

`infra/modules/monitoring/` を実装し、最低限:

- 各 Cloud Run サービスのエラー率・レイテンシに対するアラートポリシー(`google_monitoring_alert_policy`)
- Cloud Run Jobs の実行失敗通知(`google_monitoring_alert_policy` + ログベースメトリクス、`resource.type="cloud_run_job"` かつ最終ステータス失敗を検知)
- 通知チャネル(`google_monitoring_notification_channel`、まずはメールで十分)

`architecture.md` の「設計上の注意」にある「Redis/Cloud SQL/Pub/Sub/Cloud Storage 接続失敗時に分かりやすいログを出す」は Step F〜L のコード側で担保する話であり、この Step はそれを**外部から観測できるようにする**部分。

## Step O: この文書に含めなかったもの(別ドキュメント参照)

- **Pub/Sub への切り替え**(`plan/gcp-adapter-migration-phase14.md` Step 14-2): 現状 `writer`/`image-analysis-worker` は `local_events` という Postgres テーブルをポーリングしているだけで、Cloud SQL さえ動いていれば Pub/Sub が無くても Step H/K のデプロイ自体は成立する。Pub/Sub化はスケーラビリティ・疎結合のための改善であり、初回デプロイの前提条件ではない。
- **Speech-to-Text / Vertex AI 実装**(Phase 14 Step 14-5/14-6): `ENABLE_REAL_STT`/`ENABLE_REAL_LLM` を `true` にする前提のコードがまだ無い(スタブのまま)。この文書では両方とも `false` のままデプロイする。
- **Chrome 拡張の WebSocket 再接続・Cloud Run URL への向き先変更**: 拡張側の設定変更はこの文書のスコープ外。
- **セッション終了の自動検知 → Jobs 自動起動**(Step L で触れた通り): Cloud Scheduler や Eventarc を使うにしても、まず「セッションが終了した」というイベント自体をアプリ側で定義する必要があり、単純なインフラ追加では済まない。
- **アプリレベル認証**(gateway/media-api の `allow_unauthenticated` を絞る話): 認証の仕組み自体が未設計。

## 実施順序のまとめ

依存関係に基づく推奨順序。並行できるものは並行してよい。

1. **Step F**(JSONL/PDF の GCS 化、コード変更) ── 他の全 Step の前提。**実装・実GCS検証済み**(`feat/gcp-deploy-step-f-jsonl-gcs-store`)
2. **Step G**(writer に `/healthz`、コード変更) ── Step H の前提
3. **Step H**(writer デプロイ) ── Step F, G 完了後。Step I とは独立に進められる
4. **Step I**(Memorystore + VPC) ── Step J, K の前提
5. **Step J**(gateway デプロイ) ── Step I 完了後
6. **Step K**(image-analysis-worker デプロイ) ── Step I 完了後、Step J とは独立
7. **Step L**(post-session-job/pdf-renderer/gmail-sender デプロイ) ── Step F 完了後、Step H〜K とは独立
8. **Step M**(Secret Manager) ── いつでもよい、本番投入前必須
9. **Step N**(Monitoring) ── いつでもよい、本番投入前必須

各 Step は `plan/mood-wave-contract-migration.md` で確立した「1 Step = 1 ブランチ = 1 PR」の運用を踏襲する。Terraform 変更と対応する Go コード変更(Step F・G)は同じ PR にまとめてよい(密結合しているため)。

## 各 Step 共通のデプロイ手順

Step D/E(media-api)で確立済みの手順をそのまま使う。

```bash
# 1. イメージビルド(Apple SiliconでのローカルDockerビルドはlinux/amd64クロスビルドが
#    QEMU上でクラッシュすることがあるため、Cloud Buildで行う)
TAG=$(git rev-parse --short HEAD)
SERVICE=writer  # gateway / media-api / writer / image-analysis-worker / post-session-job / pdf-renderer / gmail-sender
IMAGE="$(cd infra/environments/prod && terraform output -raw backend_images_repository_url)/${SERVICE}:${TAG}"

cat > /tmp/cloudbuild-${SERVICE}.yaml <<EOF
steps:
  - name: 'gcr.io/cloud-builders/docker'
    args: ['build', '--build-arg', "SERVICE=${SERVICE}", '-t', '$IMAGE', '-f', 'backend/Dockerfile', 'backend']
images: ['$IMAGE']
EOF
gcloud builds submit --config=/tmp/cloudbuild-${SERVICE}.yaml .

# 2. Terraform適用
cd infra/environments/prod
terraform apply -var="${SERVICE}_image_tag=${TAG}"
# (Cloud Run Job の場合は上記に加えて、既に動いているJobへの反映は
#  Jobは"新しい実行"のたびに現在のtemplateを使うため、apply後は特別な
#  再起動操作は不要 -- Serviceと違ってwarm instanceの概念が無いため)

# 3. Cloud Run Serviceの場合、IAMロール新規付与直後は既存revisionに
#    反映されないことがある(README記載のmedia-apiでの実例)。反映され
#    ない場合は強制再デプロイ:
gcloud run services update r-${SERVICE} --region=asia-northeast1 \
  --update-labels=force-redeploy=$(date +%s)
```

## 動作確認チェックリスト(全 Step 完了後)

1. `curl` で `r-gateway` の `wss://.../ws` に接続し、`mood_wave_sample` を送って `feedback_event` が返ることを確認する(architecture.md のサンプルJSONを使う)。
2. `r-media-api` で baseline frame の upload-url 発行 → PUT → complete が通ることを確認する(既存の `tester_service_account` impersonation 手順を使う)。
3. Cloud SQL の `trigger_events`/`feedback_events`/`participant_baselines` にレコードが増えることを確認する。
4. GCS バケットに `sessions/{id}/mood-wave/`・`sessions/{id}/baseline/frames/` 以下にオブジェクトが増えることを確認する。
5. `gcloud run jobs execute r-post-session-job --args="--session-id=<実際のsession_id>"` を実行し、`reports` テーブルにレコードが増え、`important_windows` が空でないことを確認する。
6. `gcloud run jobs execute r-pdf-renderer --args="--session-id=<同じID>"` を実行し、GCS 上に `report.pdf` が生成され、`reports.pdf_path` が更新されることを確認する。
7. `gcloud run jobs execute r-gmail-sender --args="--session-id=<同じID>,--to=<自分のメール>"` を実行し、`report_deliveries` にレコードが増えることを確認する(実メールは届かない、スタブのため)。
8. 全サービスの Cloud Logging を見て、接続エラー・タイムアウトが出ていないことを確認する。

## コスト・運用上の注意

- `db-f1-micro`(Cloud SQL)・`BASIC` tier(Memorystore)・`e2-micro`(VPC connector)はいずれも MVP 向けの最安構成。実トラフィックが見えてから見直す。
- gateway は `min_instance_count = 1` を推奨(WebSocket 接続維持、コールドスタートで会議中の接続が切れるのを避けるため)。他の常駐サービス(writer/image-analysis-worker)は `min_instance_count = 1` かつ `max_instance_count = 1` に固定(前述の行ロック無し問題)。
- Cloud Run Jobs はスケジュール実行しない限り課金は実行時間分のみ。
- `deletion_protection = true`(Cloud SQL・Cloud Run Service のデフォルト)は本番投入後は外さないこと。
