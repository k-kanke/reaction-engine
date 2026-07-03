# Backend 実装ステップ

このドキュメントは、Reaction Engine のサーバーサイドを Google Cloud 前提で Go で実装するための step-by-step plan。

対象スタック:

- Language: Go
- Runtime: Go 1.26
- HTTP: standard `net/http` + `chi` or standard router
- WebSocket: `nhooyr.io/websocket` or `gorilla/websocket`
- Deploy: Cloud Run
- Validation: Go struct validation + JSON Schema / OpenAPI contract
- DB: Cloud SQL for PostgreSQL + `pgx`
- Redis: `go-redis` against Memorystore for Redis
- GCP SDK:
  - `cloud.google.com/go/pubsub`
  - `cloud.google.com/go/storage`
  - `cloud.google.com/go/speech/apiv1`
  - Vertex AI / Gemini SDK or REST client

対象ディレクトリ:

- `backend/`
- `infra/`
- `architecture.md`

## Goal

Chrome 拡張から送られる `realtime_feature` / `audio_chunk` を Cloud Run WebSocket Gateway で受け取り、以下を実現する。

1. Memorystore for Redis に参加者ごとの直近 window / latest state / cooldown を保存する。
2. Gateway 内のシステム演算層で signal summary / decision log を計算する。
3. Realtime LLM を timeout 付きで呼び、失敗時は rule fallback で `feedback_event` を返す。
4. Speech-to-Text streaming に self/other 音声チャンクを中継し、final transcript を保存経路へ流す。
5. Pub/Sub topic `feature-events` に compact raw feature + signal summary + transcript_chunk + decision log を publish する。
6. Durable Writer が Pub/Sub event を読み、JSONL を Cloud Storage、summary を Cloud SQL に保存する。
7. Media API が signed upload URL、`media_ref`、`capture_snapshots.feature_snapshot` を管理する。
8. Image Analysis Worker が画像 + `capture_snapshots.feature_snapshot` から participant baseline / visual summary を作る。
9. Post-session Job が compact raw JSONL、signal summary、transcript、participant baseline、visual summary からレポートを作る。

## Phase 0: 前提整理

### 0.1 決定事項を固定する

- Google Cloud project id
- default region
- Cloud Run service / job 名
  - `reaction-gateway`
  - `reaction-media-api`
  - `reaction-writer`
  - `reaction-image-analysis-worker`
  - `reaction-post-session-job`
- Pub/Sub topic / subscription 名
  - topic: `feature-events`
  - topic: `media-analysis-events`
  - dead-letter topic: `feature-events-dead-letter`
- Cloud Storage bucket 名
- Cloud SQL instance / database 名
- Memorystore instance 名
- dev / prod の分け方

受け入れ条件:

- `architecture.md` と矛盾しない。
- backend / infra の担当者が同じ名前を使える。

### 0.2 ローカル開発方針を決める

MVP では Google Cloud 実リソースなしでも最低限動く fallback を用意する。

- Redis 未設定時: in-memory recent window
- Pub/Sub 未設定時: local log publisher
- Cloud Storage 未設定時: local JSONL writer
- Cloud SQL 未設定時: local Postgres or DB write skip

受け入れ条件:

- ローカルで Chrome 拡張から WebSocket 接続し、`feedback_event` を返せる。
- GCP 接続がないことを理由に gateway 開発が止まらない。

## Phase 1: Go project skeleton

### 1.1 Go module を作る

追加するファイル:

- `backend/go.mod`
- `backend/go.sum`
- `backend/Makefile`
- `backend/cmd/gateway/main.go`
- `backend/cmd/media-api/main.go`
- `backend/cmd/writer/main.go`
- `backend/cmd/image-analysis-worker/main.go`
- `backend/cmd/post-session-job/main.go`

標準コマンド:

```text
make test
make lint
make build
make run-gateway
make run-media-api
make run-writer
```

受け入れ条件:

- `cd backend && go test ./...` が通る。
- 各 `cmd/*` が placeholder として build できる。

### 1.2 パッケージ構成を作る

推奨構成:

```text
backend/
  cmd/
    gateway/
    media-api/
    writer/
    image-analysis-worker/
    post-session-job/
  internal/
    config/
    contract/
    db/
    redis/
    pubsub/
    storage/
    gateway/
    realtime/
    media/
    imageanalysis/
    writer/
    analysis/
    observability/
  migrations/
  deploy/
  tests/
```

方針:

- `cmd/*` は起動だけを担当する。
- 業務ロジックは `internal/*` に閉じ込める。
- Chrome 拡張と共有する payload contract は `internal/contract` と `docs/openapi` に集約する。

### 1.3 env config を作る

場所:

- `backend/internal/config/config.go`

必要な env:

- `PORT`
- `GOOGLE_CLOUD_PROJECT`
- `GCP_REGION`
- `REDIS_ADDR`
- `DATABASE_URL`
- `FEATURE_EVENTS_TOPIC`
- `MEDIA_ANALYSIS_EVENTS_TOPIC`
- `FEATURE_EVENTS_SUBSCRIPTION`
- `MEDIA_BUCKET`
- `SIGNED_URL_TTL_SECONDS`
- `REALTIME_LLM_TIMEOUT_MS`

受け入れ条件:

- 起動時に config を validate する。
- 不足 env は明確な error で落ちる。

## Phase 2: データ契約

### 2.1 Go structs を定義する

場所:

- `backend/internal/contract/realtime_feature.go`
- `backend/internal/contract/audio_chunk.go`
- `backend/internal/contract/transcript_chunk.go`
- `backend/internal/contract/feedback_event.go`
- `backend/internal/contract/capture_snapshot.go`
- `backend/internal/contract/pubsub_event.go`

方針:

- `json` tag を必ず付ける。
- schema version を持つ。
- `event_id` / `server_received_at_ms` は Gateway 側で付与する。
- `capture_snapshot` は画像処理フロー専用で、`realtime_feature` には含めない。

### 2.2 schema 生成方針を決める

Chrome 拡張は TypeScript なので、Go struct を直接共有しない。

推奨:

- API contract は OpenAPI / JSON Schema を source of truth にする。
- Go 側は struct + validation を実装する。
- Extension 側は JSON Schema / OpenAPI から TypeScript 型を生成する。

受け入れ条件:

- `realtime_feature`、`capture_snapshot`、`feedback_event` の schema が docs に残る。

## Phase 3: WebSocket Gateway

### 3.1 Gateway server を作る

場所:

- `backend/cmd/gateway/main.go`
- `backend/internal/gateway/server.go`
- `backend/internal/gateway/websocket.go`

要件:

- `GET /healthz`
- `GET /ws`
- Cloud Run の `PORT` を使う。
- `0.0.0.0` で listen する。
- graceful shutdown する。

受け入れ条件:

- Chrome 拡張から WebSocket 接続できる。
- ping / close / reconnect を扱える。

### 3.2 realtime_feature handler

処理:

1. payload validate
2. `event_id` 採番
3. `server_received_at_ms` 付与
4. `session_id + audience_id` ごとに Redis recent window 更新
5. signal summary / decision log 計算
6. Pub/Sub publish
7. `feedback_event` 返却

Redis key:

```text
features:recent:{session_id}:{audience_id}
session:computed:{session_id}:{audience_id}
feedback:cooldown:{session_id}:{audience_id}
session:baseline:{session_id}:{audience_id}
session:visual_summary:{session_id}:{audience_id}
session:baseline_status:{session_id}:{audience_id}
```

## Phase 4: Speech-to-Text streaming

### 4.1 audio_chunk relay

場所:

- `backend/internal/gateway/audio_relay.go`
- `backend/internal/realtime/transcription.go`

処理:

- `audio_chunk` を既存 WebSocket で受ける。
- session_id ごとに self/other 各1本の Speech-to-Text streaming session を持つ。
- final result だけ `transcript_chunk` として Redis / Pub/Sub に流す。
- 生 PCM は保存しない。

受け入れ条件:

- streaming session の再接続に対応する。
- interim result は永続化しない。

## Phase 5: システム演算層

場所:

- `backend/internal/realtime/window.go`
- `backend/internal/realtime/signal_summary.go`
- `backend/internal/realtime/decision_log.go`
- `backend/internal/realtime/feedback_policy.go`

要件:

- 5s / 10s / 30s window の平均・変化率・継続時間を計算する。
- `participant_baseline` / `visual_summary` が Redis にある場合だけ補正に使う。
- baseline 未準備なら `baseline_status = warming_up` として default threshold を使う。
- feedback は cooldown を通す。

## Phase 6: Realtime LLM

場所:

- `backend/internal/realtime/llm.go`

要件:

- 約10秒ごとに signal summary + transcript window を Gemini Flash に渡す。
- timeout / quota / error 時は rule fallback に戻す。
- LLM の出力も decision log に残す。
- 因果を断定しない文言にする。

## Phase 7: Pub/Sub publisher

場所:

- `backend/internal/pubsub/publisher.go`

topic:

- `feature-events`

payload:

- compact raw feature
- signal summary
- transcript_chunk optional
- decision log

受け入れ条件:

- Pub/Sub 未設定時は local log publisher に fallback する。
- publish 失敗時の retry / log がある。

## Phase 8: Media API

場所:

- `backend/cmd/media-api/main.go`
- `backend/internal/media/server.go`
- `backend/internal/media/signed_url.go`
- `backend/internal/media/capture_snapshot.go`

API:

```text
POST /sessions/{session_id}/media/upload-url
POST /sessions/{session_id}/media
POST /sessions/{session_id}/media/{capture_id}/complete
```

処理:

1. Chrome 拡張から `capture_id` / `t_ms` / `audience_id` / `tile_id` / compact `feature_snapshot` を受け取る。
2. Cloud Storage signed upload URL と `media_ref` を発行する。
3. Cloud SQL `capture_snapshots` に `feature_snapshot` を保存する。
4. upload complete 時に Cloud Storage object の存在を確認する。
5. `capture_snapshots.upload_status = uploaded` に更新する。
6. `media-analysis-events` に `media_uploaded` を publish する。

注意:

- `capture_snapshot` は画像処理フロー専用。
- `media_ref` / `feature_snapshot` は `realtime_feature` に混ぜない。

## Phase 9: Image Analysis Worker

場所:

- `backend/cmd/image-analysis-worker/main.go`
- `backend/internal/imageanalysis/worker.go`
- `backend/internal/imageanalysis/vision.go`
- `backend/internal/imageanalysis/baseline.go`

処理:

1. Pub/Sub topic `media-analysis-events` を subscribe する。
2. `capture_id` で Cloud SQL `capture_snapshots` を読む。
3. `media_ref` の画像を Cloud Storage から読む。
4. `feature_snapshot` が無ければ `baseline_status = insufficient_snapshot` として終了する。
5. 画像 + `feature_snapshot` を Gemini Vision / Vision model に渡す。
6. `visual_summary` を作る。
7. 複数 sample から `participant_baseline` を更新する。
8. Redis に baseline / visual_summary / baseline_status を cache する。
9. Cloud SQL に participant_baselines / visual_summaries を保存する。

## Phase 10: Durable Writer

場所:

- `backend/cmd/writer/main.go`
- `backend/internal/writer/worker.go`
- `backend/internal/storage/jsonl_writer.go`

処理:

1. Pub/Sub subscription `feature-events-durable-writer` を読む。
2. `event_id` で冪等性を確保する。
3. compact raw feature / signal summary / decision log / transcript_chunk を Cloud Storage JSONL に保存する。
4. signal summary / decision log / feedback history / transcript を Cloud SQL に保存する。
5. 保存成功後に ack する。

注意:

- `capture_snapshots` と `media_refs` は Media API が作る。Durable Writer は作らない。

## Phase 11: Cloud SQL schema / migrations

場所:

- `backend/migrations/`

必要テーブル:

- `sessions`
- `participants`
- `capture_snapshots`
- `media_refs`
- `participant_baselines`
- `visual_summaries`
- `signal_summaries`
- `decision_logs`
- `transcripts`
- `feedback_events`
- `reports`

Go 実装:

- migration tool は `golang-migrate/migrate` などを使う。
- DB client は `pgxpool`。

## Phase 12: Post-session Job

場所:

- `backend/cmd/post-session-job/main.go`
- `backend/internal/analysis/job.go`

入力:

- Cloud Storage compact raw feature JSONL
- Cloud SQL signal summaries
- Cloud SQL transcripts
- Cloud SQL participant_baselines
- Cloud SQL visual_summaries
- feedback history

処理:

1. timeline を align する。
2. 変化点を検出する。
3. visual summary / baseline を補正情報として使う。
4. Gemini で report / coaching suggestion を生成する。
5. Cloud SQL に report を保存する。

## Phase 13: Docker / Cloud Run

### 13.1 Dockerfile

追加:

- `backend/Dockerfile`

方針:

- multi-stage build
- static binary
- distroless or scratch runtime
- non-root user
- `PORT` env を使う

### 13.2 Cloud Run service split

- `reaction-gateway`
- `reaction-media-api`
- `reaction-writer`
- `reaction-image-analysis-worker`

Cloud Run Jobs:

- `reaction-post-session-job`

## Phase 14: Observability

場所:

- `backend/internal/observability/`

必要なログ:

- session_id
- audience_id
- capture_id
- event_id
- latency
- Redis latency
- Pub/Sub publish latency
- LLM latency / timeout / fallback
- Image Analysis Worker success / failure

## Done Criteria

MVP backend が done と言える状態:

- Go services が build / test できる。
- Cloud Run Gateway が WebSocket で feature / audio を受ける。
- Redis に participant ごとの recent window と baseline cache を保存できる。
- Pub/Sub `feature-events` に realtime analysis event を publish できる。
- Durable Writer が Cloud Storage / Cloud SQL に保存できる。
- Media API が signed upload URL と capture snapshot を保存できる。
- Image Analysis Worker が画像 + feature_snapshot から baseline / visual_summary を作れる。
- Post-session Job が保存済みデータから report を作れる。
