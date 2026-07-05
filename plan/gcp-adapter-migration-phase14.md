# GCP Adapter Migration (Phase 14 詳細手順)

`plan/backend-local-docker-runbook.md` の Phase 14「real GCP adapters」を、実際に手を動かせる粒度まで分解したもの。Phase 0〜13 でローカル Docker Compose 上の通り道（Gateway/Media API/Writer/Image Analysis Worker/Post-session Job）が一通り検証できた状態を前提にする。

## 目的

ローカルスタンドイン実装（`local_events` テーブル、`./tmp/media` ファイルシステム、fake STT/LLM スタブ）を、実際の Google Cloud サービスに **1リソースずつ** 差し替える。一度に全部切り替えず、1つ差し替えるごとに実際に動かして確認してから次に進む。ローカル実装は削除せず、env で local/real を切り替えられる状態を保つ（Phase 15 で Cloud Run 前提が固まった段階で local 経路の扱いを再検討する）。

## 前提: 現状の実装は「interface を切っていない」

`architecture.md` の Directory Map は `internal/pubsub/`, `internal/storage/` を将来のパッケージとして挙げているが、Phase 0〜13 時点ではまだ存在しない。現状の実装は次の通り、具体的な local 実装に直接依存している。

- イベントバス: `internal/db.LocalEventStore`（Postgres の `local_events` テーブル）を gateway / writer / image-analysis-worker が直接呼んでいる。`EventPublisher` / `EventStore` のような interface は無い。
- メディア保存: `internal/media/signed_url.go` の `localUploadURL` / `mediaRef` / `localFilePath` はローカルファイルパスを直接組み立てる関数で、`MediaStore` interface は無い。
- STT: `internal/gateway/gateway.go` の `buildFakeTranscriptChunk` が固定文字列を返すだけ。
- Realtime LLM: `internal/gateway/gateway.go` の `generateLLMStubCandidate` が固定テンプレートを返すだけ（外部呼び出しなし）。
- Post-session report: `internal/postsession/report.go` の `BuildReport` は LLM を呼ばない decision的な集計のみ（`source: "post_session_stub"`）。

したがって Phase 14 の各ステップは「env を切り替えるだけ」では済まず、まず該当箇所を interface 化してから real 実装を追加する作業を含む。Cloud SQL / Memorystore は例外で、`pgx` / `go-redis` が既に接続先非依存なので、基本は接続情報の差し替えとネットワーク経路の確保が中心になる。

## 進め方の方針

1. **1リソース = 1ブランチ = 1PR。** `backend-runbook-phase` skill と同じ運用で、develop から分岐し、develop に戻す PR を作る。
2. **local 実装を消さない。** 各 interface に local 実装と real 実装の両方を用意し、env フラグで切り替える。`go test ./...` は real GCP 認証情報が無くても通る状態を維持する（local 実装 or フェイクを使うユニットテストのまま）。
3. **フラグはリソース単位で分ける。** `.env.example` の `ENABLE_REAL_GCP` は 1 個の bool では「1つずつ切り替える」を表現できないため、リソースごとのフラグに分割する（下記）。
4. **各ステップの最後に、Phase 0〜13 で使ったのと同じ手作業検証（curl / redis-cli / psql）を real リソース向けに実行し、ローカルスタンドインと同じ結果になることを確認する。**

### 追加する env フラグ

`backend/.env.example` の `ENABLE_REAL_GCP=false` を次のように分割する（既存の `ENABLE_REAL_LLM` / `ENABLE_REAL_STT` はそのまま流用）。

```env
# Phase 14 stepごとに true にしていく。全部 false のままなら Phase 0-13 のローカル動作と同じ。
MEDIA_STORE_BACKEND=local        # local | gcs
EVENT_BUS_BACKEND=local          # local | pubsub
ENABLE_REAL_STT=false            # false | true
ENABLE_REAL_LLM=false            # false | true (realtime + post-session 両方に使う)

GCS_MEDIA_BUCKET=reaction-engine-sessions
PUBSUB_FEATURE_EVENTS_TOPIC=feature-events
PUBSUB_FEATURE_EVENTS_SUBSCRIPTION=feature-events-durable-writer
PUBSUB_MEDIA_ANALYSIS_TOPIC=media-analysis-events
PUBSUB_MEDIA_ANALYSIS_SUBSCRIPTION=media-analysis-events-image-worker

VERTEX_PROJECT=local-reaction-engine
VERTEX_LOCATION=asia-northeast1
VERTEX_REALTIME_MODEL=gemini-flash
VERTEX_REPORT_MODEL=gemini-flash
```

Cloud SQL / Memorystore は専用フラグを設けず、`DATABASE_URL` / `REDIS_ADDR` を real インスタンスの接続情報に書き換えるだけにする（interface 変更が無いため）。

## 全体順序

| Step | リソース | 主な変更 | 前提 |
| --- | --- | --- | --- |
| 14-1 | Cloud Storage | Media API の `MediaStore` interface化 + GCS 実装 | GCP プロジェクト作成済み |
| 14-2 | Pub/Sub | イベントバスの `EventPublisher`/`EventSubscriber` interface化 + Pub/Sub 実装 | 14-1 完了 |
| 14-3 | Cloud SQL | `DATABASE_URL` を Cloud SQL に向ける（Cloud SQL Auth Proxy 経由） | 14-2 完了（依存はないが順序どおり進める） |
| 14-4 | Memorystore | `REDIS_ADDR` を Memorystore に向ける（VPC connector 経由） | 14-3 完了 |
| 14-5 | Speech-to-Text streaming | `buildFakeTranscriptChunk` を実 streaming 呼び出しに置き換え | 14-4 完了 |
| 14-6 | Vertex AI / Gemini | realtime LLM stub と post-session report stub を実 Gemini 呼び出しに置き換え | 14-5 完了 |

依存関係は緩い（本質的には並行可能）が、runbook の優先順位（データの通り道 → 保存 → cache → 推論）に合わせて上から順に進める。

---

## Step 14-1: Cloud Storage（Media API baseline frame）

### 14-1.1 GCP リソース準備

```bash
gcloud storage buckets create gs://reaction-engine-sessions \
  --project=<PROJECT_ID> \
  --location=asia-northeast1 \
  --uniform-bucket-level-access

# アップロード用 signed URL を発行する service account
gcloud iam service-accounts create reaction-engine-media-api
gcloud storage buckets add-iam-policy-binding gs://reaction-engine-sessions \
  --member="serviceAccount:reaction-engine-media-api@<PROJECT_ID>.iam.gserviceaccount.com" \
  --role="roles/storage.objectAdmin"
```

### 14-1.2 コード変更

- `backend/internal/media/` に `MediaStore` interface を追加する。

  ```go
  type MediaStore interface {
      SignedUploadURL(ctx context.Context, object string, contentType string, ttl time.Duration) (uploadURL string, err error)
      Exists(ctx context.Context, object string) (bool, error)
      Read(ctx context.Context, object string) ([]byte, error)
  }
  ```

- 既存の `localUploadURL` / `localFilePath` ベースの実装を `LocalMediaStore` としてこの interface に適合させる。
- `cloud.google.com/go/storage` を使う `GCSMediaStore` を追加し、`GenerateSignedPostPolicyV4` 相当で signed URL を発行する。`media_ref` は `local://...` ではなく `gs://reaction-engine-sessions/sessions/{session_id}/baseline/frames/{capture_id}.{ext}`（`architecture.md` の形式）にする。
- `backend/cmd/media-api/main.go` で `MEDIA_STORE_BACKEND` を見て `LocalMediaStore` / `GCSMediaStore` を選択する。
- Image Analysis Worker（`internal/imageanalysis`）も `MediaStore.Read` 経由で画像を取得するように変更する（現状は local ファイルパスを直接開いている想定なので、ここも interface に寄せる）。
- `/local-upload` エンドポイントは `MEDIA_STORE_BACKEND=local` のときのみ有効にする（real の場合 Chrome 拡張は GCS に直接 PUT するので media-api を経由しない）。

### 14-1.3 env

```env
MEDIA_STORE_BACKEND=gcs
GCS_MEDIA_BUCKET=reaction-engine-sessions
GOOGLE_APPLICATION_CREDENTIALS=/path/to/reaction-engine-media-api-key.json   # ローカル検証時のみ
```

### 14-1.4 検証手順（Phase 0-13 の手作業検証と同じ形）

1. `docker compose up -d postgres redis media-api`（`MEDIA_STORE_BACKEND=gcs` で起動）
2. `curl -X POST http://localhost:8081/sessions/sess_gcs/media/upload-url ...` で upload-url を取得し、`upload_url` が `https://storage.googleapis.com/...` になっていることを確認する
3. 取得した signed URL に実際に画像バイトを `PUT` する（`local-upload` は経由しない）
4. `gcloud storage ls gs://reaction-engine-sessions/sessions/sess_gcs/baseline/frames/` でファイルが実在することを確認する
5. `/complete` を叩き、`capture_snapshots.upload_status = uploaded` になることを Postgres で確認する
6. Image Analysis Worker のログで `media_uploaded` イベント処理後、GCS から画像を読めていること（エラーが出ていないこと）を確認する

### 14-1.5 完了条件

- `MEDIA_STORE_BACKEND=local` に戻すと Phase 0-13 のローカル検証がそのまま通る（回帰なし）。
- `MEDIA_STORE_BACKEND=gcs` で upload-url〜complete〜Image Analysis Worker まで実 GCS 上で一気通貫する。

---

## Step 14-2: Pub/Sub（feature-events / media-analysis-events）

### 14-2.1 GCP リソース準備

```bash
gcloud pubsub topics create feature-events
gcloud pubsub topics create media-analysis-events
gcloud pubsub subscriptions create feature-events-durable-writer --topic=feature-events
gcloud pubsub subscriptions create media-analysis-events-image-worker --topic=media-analysis-events
```

### 14-2.2 コード変更

- `backend/internal/db/local_events.go` の `LocalEventStore` が果たしている役割を `EventPublisher` / `EventSubscriber` interface に分離する。

  ```go
  type EventPublisher interface {
      Publish(ctx context.Context, topic string, eventID string, payload any) error
  }

  type EventSubscriber interface {
      // Pull 1件処理し、ハンドラが成功したら ack する
      Pull(ctx context.Context, topic string, handler func(ctx context.Context, payload json.RawMessage) error) error
  }
  ```

- 既存の `LocalEventStore` をこの2つの interface に適合させたラッパーにする（`Enqueue`→`Publish`、`FetchUnacked`+`Ack`のポーリングループ→`Pull`）。
- `cloud.google.com/go/pubsub` を使う `PubSubPublisher` / `PubSubSubscriber` を追加する。
- Gateway（publisher 側: `feature-events` への enqueue 箇所）、Writer（`feature-events` の subscriber）、Media API（`media-analysis-events` への publish 箇所）、Image Analysis Worker（`media-analysis-events` の subscriber）の呼び出し箇所を interface 経由に差し替える。
- `EVENT_BUS_BACKEND` で `LocalEventStore` ベース実装 / Pub/Sub 実装を選択する。

### 14-2.3 env

```env
EVENT_BUS_BACKEND=pubsub
PUBSUB_FEATURE_EVENTS_TOPIC=feature-events
PUBSUB_FEATURE_EVENTS_SUBSCRIPTION=feature-events-durable-writer
PUBSUB_MEDIA_ANALYSIS_TOPIC=media-analysis-events
PUBSUB_MEDIA_ANALYSIS_SUBSCRIPTION=media-analysis-events-image-worker
```

### 14-2.4 検証手順

1. `EVENT_BUS_BACKEND=pubsub` で gateway / writer / media-api / image-analysis-worker を起動
2. Phase 4 と同じ `realtime_feature` を websocat で送信
3. `gcloud pubsub subscriptions pull feature-events-durable-writer --auto-ack --limit=1` で message が届いていることを直接確認（writer 起動前に一度確認するとデバッグしやすい）
4. writer 経由で `tmp/jsonl/...` に JSONL が書かれることを Phase 6 と同じ手順で確認
5. Media API の upload-complete → `media-analysis-events` publish → Image Analysis Worker の処理を Phase 7-8 と同じ手順で確認

### 14-2.5 完了条件

- `EVENT_BUS_BACKEND=local` に戻すとローカル検証が回帰しない。
- `EVENT_BUS_BACKEND=pubsub` で Gateway→Writer、Media API→Image Analysis Worker の両経路が実 Pub/Sub 経由で動く。
- Pub/Sub は at-least-once 前提のため、同じ message が2回配信されても `event_id` で副作用が重複しないこと（JSONL 重複行は許容、DB upsert は重複しても結果が変わらないこと）を確認する。

---

## Step 14-3: Cloud SQL for PostgreSQL

### 14-3.1 GCP リソース準備

```bash
gcloud sql instances create reaction-engine-db \
  --database-version=POSTGRES_17 \
  --region=asia-northeast1 \
  --tier=db-f1-micro
gcloud sql databases create reaction --instance=reaction-engine-db
gcloud sql users create reaction --instance=reaction-engine-db --password=<PASSWORD>
```

ローカル検証には Cloud SQL Auth Proxy を使う。

```bash
cloud-sql-proxy <PROJECT_ID>:asia-northeast1:reaction-engine-db --port 5433
```

### 14-3.2 コード変更

なし（`pgx`/`pgxpool` は接続文字列非依存）。migration 運用（`make migrate-up`）はそのまま Cloud SQL 向け `DATABASE_URL` で使う。

### 14-3.3 env

```env
DATABASE_URL=postgres://reaction:<PASSWORD>@127.0.0.1:5433/reaction?sslmode=disable
```

### 14-3.4 検証手順

1. Auth Proxy 経由で `make migrate-up` を実行し、Cloud SQL 側にテーブルが作られることを確認
2. gateway / writer / media-api / image-analysis-worker / post-session-job の `DATABASE_URL` を上記に向けて起動
3. Phase 0-13 の検証コマンド一式（`local_events`、`capture_snapshots`、`participant_baselines`、`visual_summaries`、`reports` の psql 確認）を Cloud SQL 上でそのまま再実行する

### 14-3.5 完了条件

- Phase 0-13 の検証がすべて Cloud SQL 上で再現する。
- ローカル Postgres に戻すだけで local 検証にも戻れる（`DATABASE_URL` の差し替えのみ）。

---

## Step 14-4: Memorystore for Redis

### 14-4.1 GCP リソース準備

Memorystore は VPC 内からしかアクセスできないため、Cloud Run から使う前提なら Serverless VPC Access connector が必要。ローカル検証では SSH tunnel か bastion 経由でアクセスする。

```bash
gcloud redis instances create reaction-engine-redis \
  --region=asia-northeast1 \
  --tier=basic \
  --size=1
```

### 14-4.2 コード変更

なし（`go-redis` は接続先非依存）。

### 14-4.3 env

```env
REDIS_ADDR=<MEMORYSTORE_IP>:6379
```

### 14-4.4 検証手順

Phase 4/9 の redis-cli 確認（`features:recent:*`、`session:baseline_status:*` など）を Memorystore 上でそのまま再実行する。

### 14-4.5 完了条件

- Phase 0-13 の Redis 依存の検証がすべて Memorystore 上で再現する。

---

## Step 14-5: Speech-to-Text streaming

### 14-5.1 GCP リソース準備

```bash
gcloud services enable speech.googleapis.com
```

専用の service account に `roles/speech.client` を付与する。

### 14-5.2 コード変更

- `internal/gateway/gateway.go` の `buildFakeTranscriptChunk` 呼び出し箇所を、`speaker` ごとの streaming セッション実装に差し替える。`ENABLE_REAL_STT=true` のときだけ `cloud.google.com/go/speech/apiv1` の streaming client を使い、`false` のときは既存のスタブ関数を使う（両方を選べる小さな interface でラップする）。
- `audio_chunk` の `pcm` フィールドを実際に streaming API へ転送する処理を追加する（現状は「PCM は一切パースしない」ため、ここで初めて実データが必要になる）。
- 1ストリームの継続時間上限に対応する再接続処理を追加する（`architecture.md` の記載どおり）。

### 14-5.3 env

```env
ENABLE_REAL_STT=true
```

### 14-5.4 検証手順

1. 実際の音声（wav→PCM16変換したサンプルなど）を `audio_chunk` として送る
2. `transcript:recent:{session_id}:{speaker}` に stub ではなく実際の書き起こしテキストが入ることを確認
3. Phase 12 と同じ手順で `realtime_feature` を送り、`decision_log.evidence_quote` が実際の transcript になっていることを確認

### 14-5.5 完了条件

- `ENABLE_REAL_STT=false` のとき Phase 11 のスタブ動作に回帰しない。
- `ENABLE_REAL_STT=true` のとき実音声から実際の transcript が生成される。

---

## Step 14-6: Vertex AI / Gemini（realtime LLM + post-session report）

### 14-6.1 GCP リソース準備

```bash
gcloud services enable aiplatform.googleapis.com
```

### 14-6.2 コード変更

- `internal/gateway/gateway.go` の `generateLLMStubCandidate` を、`ENABLE_REAL_LLM=true` のときは Vertex AI Gemini Flash 呼び出しに、`false` のときは既存スタブに分岐させる。`realtimeLLMTimeout`（`REALTIME_LLM_TIMEOUT_MS`）はそのまま流用し、timeout/エラー時は `source: "rule"` へのフォールバックを維持する（Phase 12 の完了条件を壊さない）。
- `internal/postsession/report.go` の `BuildReport` に、`source: "post_session_stub"` の集計結果を入力として Gemini に投げて `coaching_suggestion` 等を追記する経路を追加する（`ENABLE_REAL_LLM=true` のときのみ）。false のときは既存の `post_session_stub` のまま返す。

### 14-6.3 env

```env
ENABLE_REAL_LLM=true
VERTEX_PROJECT=<PROJECT_ID>
VERTEX_LOCATION=asia-northeast1
VERTEX_REALTIME_MODEL=gemini-flash
VERTEX_REPORT_MODEL=gemini-flash
```

### 14-6.4 検証手順

1. Phase 12 と同じ手順（audio_chunk でtranscript蓄積→realtime_feature）を実行し、`source: "llm_stub"` ではなく `source: "llm"` で実際の Gemini 生成文が返ることを確認する
2. わざと `VERTEX_PROJECT` を無効値にするなどして timeout/エラーを起こし、`source: "rule"` にフォールバックすることを確認する（Phase 12 の完了条件がここでも壊れていないことの回帰確認）
3. `go run ./cmd/post-session-job --session-id=...` を実行し、`reports.report` の `source` が実 LLM 生成の値になっていること、`participants` 等の集計フィールドは Phase 13 と同じ形を保っていることを確認する

### 14-6.5 完了条件

- `ENABLE_REAL_LLM=false` で Phase 12/13 のスタブ動作に回帰しない。
- `ENABLE_REAL_LLM=true` で実 Gemini 呼び出し・timeout フォールバック・post-session report 生成がすべて動く。

---

## Phase 14 全体の完了条件

```text
MEDIA_STORE_BACKEND=local / EVENT_BUS_BACKEND=local / ENABLE_REAL_STT=false / ENABLE_REAL_LLM=false
  -> Phase 0-13 のローカル検証がそのまま通る（回帰なし）

MEDIA_STORE_BACKEND=gcs / EVENT_BUS_BACKEND=pubsub / ENABLE_REAL_STT=true / ENABLE_REAL_LLM=true
  -> 同じ一連の検証が実 GCP リソース上で通る
```

すべてのフラグを true/real にした状態が、Phase 15 の Cloud Run 移行チェックリストに進む前提になる。

## 次: Phase 15 への接続

Phase 14 の各アダプタが実リソースで検証できたら、`plan/backend-local-docker-runbook.md` Phase 15（Cloud Run readiness checklist）に進む。Phase 15 で local 実装をいつまで残すか（開発・CI用に残す／Cloud Run 移行後に削除する）を判断する。
