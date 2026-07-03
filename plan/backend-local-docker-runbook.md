# Backend Local Docker Runbook

Go backend をローカル Docker 環境で段階的に構築し、動作検証できてから Google Cloud / Cloud Run へ移行するための実行手順。

この runbook は「何をどの順番で作り、何を確認できたら次へ進むか」を明確にする。実装の詳細設計は `architecture.md` と `plan/backend-implementation-steps.md` を正とする。

## 目的

ローカルで以下を再現できる状態にする。

```text
Chrome Extension
  -> WebSocket Gateway
  -> Redis recent state
  -> feedback_event

Chrome Extension
  -> Media API
  -> local media storage
  -> capture_snapshots
  -> media-analysis-events
  -> Image Analysis Worker
  -> participant_baselines / visual_summaries
  -> Redis cache

Gateway / Writer / Post-session Job
  -> local JSONL
  -> local Postgres
```

最初から Google Cloud 実リソースに接続しない。まず Docker Compose 上でサービス間の契約と処理順を固める。

## 前提

ローカルに必要なもの:

- Docker Desktop
- Docker Compose v2
- Go 1.26
- Make
- psql client optional
- redis-cli optional
- Chrome 拡張をローカル読み込みできる環境

Go 1.26 は最終的に Docker image 内でも固定する。

## ローカル構成

最初の Docker Compose は以下の構成にする。

```text
compose.yaml
  postgres
  redis
  gateway
  media-api
  writer
  image-analysis-worker
  post-session-job optional
```

MVP では Pub/Sub emulator / Cloud Storage emulator は必須にしない。

- Pub/Sub はまず local in-memory queue または local HTTP queue で代替する。
- Cloud Storage はまず local filesystem `./tmp/media` / `./tmp/jsonl` で代替する。
- 後で GCP 接続に差し替える。

## Phase 0: 方針固定

### 0.1 サービス名を固定する

ローカルでも Cloud Run でも同じ論理名を使う。

```text
r-gateway
r-media-api
r-writer
r-image-worker
r-post-session-job
```

### 0.2 ローカル port を固定する

推奨:

```text
gateway: http://localhost:8080
media-api: http://localhost:8081
writer: internal only
image-analysis-worker: internal only or http://localhost:8082/debug
postgres: localhost:5432
redis: localhost:6379
```

### 0.3 ローカル env を固定する

作成するファイル:

```text
backend/.env.local
```

例:

```env
APP_ENV=local
GOOGLE_CLOUD_PROJECT=local-reaction-engine
GCP_REGION=asia-northeast1

GATEWAY_PORT=8080
MEDIA_API_PORT=8081

DATABASE_URL=postgres://reaction:reaction@postgres:5432/reaction?sslmode=disable
REDIS_ADDR=redis:6379

FEATURE_EVENTS_TOPIC=feature-events
MEDIA_ANALYSIS_EVENTS_TOPIC=media-analysis-events

LOCAL_MEDIA_DIR=/var/reaction/media
LOCAL_JSONL_DIR=/var/reaction/jsonl

SIGNED_URL_TTL_SECONDS=900
REALTIME_LLM_TIMEOUT_MS=1500

ENABLE_REAL_GCP=false
ENABLE_REAL_LLM=false
ENABLE_REAL_STT=false
```

完了条件:

- env 名が `architecture.md` と矛盾しない。
- local と Cloud Run の差分が env で切り替えられる。

## Phase 1: Go skeleton

### 1.1 Go module 作成

作成するファイル:

```text
backend/go.mod
backend/go.sum
backend/Makefile
```

module 名の例:

```text
module github.com/<org>/reaction-engine/backend
```

最初に入れる依存候補:

```text
github.com/go-chi/chi/v5
nhooyr.io/websocket
github.com/redis/go-redis/v9
github.com/jackc/pgx/v5/pgxpool
github.com/jackc/pgx/v5
github.com/google/uuid
github.com/caarlos0/env/v11
github.com/rs/zerolog
```

GCP SDK は実接続フェーズで追加してもよい。

Makefile の初期 target:

```makefile
.PHONY: test build run-gateway run-media-api run-writer run-image-worker

test:
	go test ./...

build:
	go build ./cmd/gateway
	go build ./cmd/media-api
	go build ./cmd/writer
	go build ./cmd/image-analysis-worker
	go build ./cmd/post-session-job

run-gateway:
	go run ./cmd/gateway

run-media-api:
	go run ./cmd/media-api

run-writer:
	go run ./cmd/writer

run-image-worker:
	go run ./cmd/image-analysis-worker
```

完了条件:

```text
cd backend
go test ./...
make build
```

が通る。

### 1.2 entrypoint を作る

作成するファイル:

```text
backend/cmd/gateway/main.go
backend/cmd/media-api/main.go
backend/cmd/writer/main.go
backend/cmd/image-analysis-worker/main.go
backend/cmd/post-session-job/main.go
```

最初は全 service で `GET /healthz` または起動ログだけでよい。

完了条件:

```text
go run ./cmd/gateway
curl http://localhost:8080/healthz
```

で `ok` が返る。

## Phase 2: Docker / Compose

### 2.1 backend Dockerfile

作成するファイル:

```text
backend/Dockerfile
backend/.dockerignore
```

方針:

- Go 1.26 builder image
- multi-stage build
- runtime は distroless or alpine
- service は build arg で切り替えられるようにする

例:

```dockerfile
ARG GO_VERSION=1.26
ARG SERVICE=gateway

FROM golang:${GO_VERSION} AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/service ./cmd/${SERVICE}

FROM gcr.io/distroless/static-debian12
WORKDIR /
COPY --from=builder /out/service /service
USER nonroot:nonroot
ENTRYPOINT ["/service"]
```

完了条件:

```text
docker build -f backend/Dockerfile --build-arg SERVICE=gateway backend
```

が成功する。

### 2.2 compose.yaml

作成するファイル:

```text
compose.yaml
```

構成:

```yaml
services:
  postgres:
    image: postgres:17
    environment:
      POSTGRES_USER: reaction
      POSTGRES_PASSWORD: reaction
      POSTGRES_DB: reaction
    ports:
      - "5432:5432"
    volumes:
      - postgres-data:/var/lib/postgresql/data

  redis:
    image: redis:7
    ports:
      - "6379:6379"

  gateway:
    build:
      context: ./backend
      dockerfile: Dockerfile
      args:
        SERVICE: gateway
    env_file:
      - ./backend/.env.local
    environment:
      PORT: 8080
    ports:
      - "8080:8080"
    depends_on:
      - postgres
      - redis

  media-api:
    build:
      context: ./backend
      dockerfile: Dockerfile
      args:
        SERVICE: media-api
    env_file:
      - ./backend/.env.local
    environment:
      PORT: 8081
    ports:
      - "8081:8081"
    volumes:
      - ./tmp/media:/var/reaction/media
    depends_on:
      - postgres
      - redis

  writer:
    build:
      context: ./backend
      dockerfile: Dockerfile
      args:
        SERVICE: writer
    env_file:
      - ./backend/.env.local
    volumes:
      - ./tmp/jsonl:/var/reaction/jsonl
    depends_on:
      - postgres

  image-analysis-worker:
    build:
      context: ./backend
      dockerfile: Dockerfile
      args:
        SERVICE: image-analysis-worker
    env_file:
      - ./backend/.env.local
    volumes:
      - ./tmp/media:/var/reaction/media
    depends_on:
      - postgres
      - redis

volumes:
  postgres-data:
```

完了条件:

```text
docker compose up --build
curl http://localhost:8080/healthz
curl http://localhost:8081/healthz
```

が通る。

## Phase 3: DB migration

### 3.1 migration tool を決める

推奨:

```text
golang-migrate/migrate
```

作成するディレクトリ:

```text
backend/migrations/
```

採用するファイル形式は `golang-migrate/migrate` 標準にする。

```text
backend/migrations/
  000001_initial_schema.up.sql
  000001_initial_schema.down.sql
  000002_add_capture_snapshots.up.sql
  000002_add_capture_snapshots.down.sql
```

命名ルール:

- 6桁連番を使う。
- description は snake_case にする。
- 日付はファイル名に入れない。
- `up.sql` と `down.sql` を必ずペアで作る。
- migration の作成日時は Git 履歴で追う。

最初の migration:

```text
backend/migrations/
  000001_initial_schema.up.sql
  000001_initial_schema.down.sql
```

### 3.2 初期テーブル

最低限のテーブル:

```sql
sessions
participants
capture_snapshots
media_refs
participant_baselines
visual_summaries
signal_summaries
decision_logs
transcripts
feedback_events
reports
```

`capture_snapshots` の必須カラム:

```sql
capture_id text primary key
session_id text not null
audience_id text not null
tile_id text
t_ms bigint not null
media_ref text not null
upload_status text not null
feature_snapshot jsonb not null
created_at timestamptz not null
uploaded_at timestamptz
```

`participant_baselines` の必須カラム:

```sql
id uuid primary key
session_id text not null
audience_id text not null
baseline jsonb not null
sample_count int not null
confidence double precision not null
updated_at timestamptz not null
```

`visual_summaries` の必須カラム:

```sql
id uuid primary key
session_id text not null
audience_id text not null
capture_id text
media_ref text
visual_summary jsonb not null
confidence double precision
created_at timestamptz not null
```

完了条件:

```text
docker compose up -d postgres
make migrate-up
```

または migrate CLI で DB に初期テーブルが作成される。

## Phase 4: Gateway MVP

### 4.1 WebSocket endpoint

実装対象:

```text
GET /ws
```

最初に対応する message:

```json
{
  "type": "realtime_feature",
  "session_id": "sess_local",
  "t_ms": 1000,
  "features": {
    "face_tracks": [
      {
        "audience_id": "aud_1",
        "attention_score": 0.55
      }
    ]
  }
}
```

Gateway の処理:

1. JSON parse
2. type validate
3. `event_id` 採番
4. `server_received_at_ms` 付与
5. Redis に recent window 保存
6. 仮の `feedback_event` を返す

Redis key:

```text
features:recent:{session_id}:{audience_id}
session:computed:{session_id}:{audience_id}
feedback:cooldown:{session_id}:{audience_id}
```

完了条件:

- WebSocket client から `realtime_feature` を送れる。
- Redis に key ができる。
- `feedback_event` が返る。

検証例:

```text
docker compose up --build gateway redis
```

任意の WebSocket client で:

```json
{"type":"realtime_feature","session_id":"sess_local","t_ms":1000,"features":{"face_tracks":[{"audience_id":"aud_1","attention_score":0.55}]}}
```

期待:

```json
{"type":"feedback_event","session_id":"sess_local", ...}
```

## Phase 5: local event bus

GCP Pub/Sub に行く前に local event bus を作る。

選択肢:

- in-memory channel
- Postgres table queue
- Redis stream
- local HTTP endpoint

MVP では in-memory channel だと service 間で共有できないため、Docker Compose では Redis stream か Postgres table queue が扱いやすい。

推奨:

```text
local_events table
```

カラム:

```sql
id bigserial primary key
topic text not null
event_id text not null
payload jsonb not null
available_at timestamptz not null default now()
created_at timestamptz not null default now()
acked_at timestamptz
```

完了条件:

- Gateway が `feature-events` 相当の event を `local_events` に書く。
- Writer が unacked event を read できる。

## Phase 6: Durable Writer MVP

Writer の処理:

1. local event bus から `feature-events` を読む。
2. compact raw feature を `./tmp/jsonl/features/compact-raw/*.jsonl` に書く。
3. signal summary / decision log を `./tmp/jsonl/features/*` に書く。
4. Cloud SQL 代替の local Postgres に summary を保存する。
5. 成功した event を ack する。

ローカル保存先:

```text
tmp/jsonl/sessions/{session_id}/features/compact-raw/part-0001.jsonl
tmp/jsonl/sessions/{session_id}/features/signal-summary/part-0001.jsonl
tmp/jsonl/sessions/{session_id}/features/decision-log/part-0001.jsonl
```

完了条件:

- Gateway -> local event bus -> Writer -> JSONL まで流れる。
- Writer を再起動しても未 ack event を再処理できる。
- `event_id` で二重保存を避けられる。

## Phase 7: Media API MVP

### 7.1 upload-url endpoint

実装対象:

```text
POST /sessions/{session_id}/media/upload-url
```

ローカルでは signed URL を本物にしない。

レスポンス例:

```json
{
  "upload_url": "http://localhost:8081/local-upload/sess_local/cap_123.webp",
  "media_ref": "local://sessions/sess_local/baseline/frames/cap_123.webp",
  "capture_id": "cap_123",
  "expires_at": "2026-07-04T12:00:00Z"
}
```

request body:

```json
{
  "purpose": "baseline_frame",
  "content_type": "image/webp",
  "capture_id": "cap_123",
  "t_ms": 12345,
  "audience_id": "aud_1",
  "tile_id": "tile_1",
  "feature_snapshot": {
    "attention_score": 0.55,
    "motion_score": 0.02,
    "gaze_estimate": "screen",
    "head_pose_estimate": { "yaw": 0.01, "pitch": -0.1, "roll": -0.2 },
    "mouth_openness": 0.01,
    "eye_openness": { "left": 0.4, "right": 0.5 },
    "face_bbox": { "x": 0.28, "y": 0.45, "w": 0.138, "h": 0.244 }
  }
}
```

処理:

1. request validate
2. `capture_snapshots` に `upload_status = pending` で保存
3. `media_refs` に metadata 保存
4. local upload URL を返す

完了条件:

- `capture_snapshots` に `feature_snapshot` が保存される。
- `media_ref` が返る。

### 7.2 local upload endpoint

ローカル専用:

```text
PUT /local-upload/{session_id}/{filename}
```

処理:

1. body を `./tmp/media/...` に保存
2. content-type / size を確認

本番 Cloud Run ではこの endpoint は使わない。本番は Cloud Storage signed URL に直接 upload する。

### 7.3 upload complete endpoint

実装対象:

```text
POST /sessions/{session_id}/media/{capture_id}/complete
```

処理:

1. local file の存在確認
2. `capture_snapshots.upload_status = uploaded`
3. `media_refs.upload_status = uploaded`
4. `media-analysis-events` を local event bus に publish

完了条件:

- upload complete 後に `media_uploaded` event が作られる。

## Phase 8: Image Analysis Worker MVP

最初は LLM を呼ばない。fake / deterministic な `visual_summary` を作る。

処理:

1. local event bus から `media-analysis-events` を読む。
2. `capture_id` で `capture_snapshots` を読む。
3. `media_ref` の local file を読む。
4. `feature_snapshot` を読む。
5. fake `visual_summary` を作る。
6. `participant_baselines` を upsert する。
7. `visual_summaries` を insert する。
8. Redis に cache する。

fake visual summary 例:

```json
{
  "face_quality": "usable",
  "lighting": "unknown",
  "camera_angle": "unknown",
  "baseline_expression": "unknown",
  "source": "local_stub"
}
```

Redis key:

```text
session:baseline:{session_id}:{audience_id}
session:visual_summary:{session_id}:{audience_id}
session:baseline_status:{session_id}:{audience_id}
```

完了条件:

- `media_uploaded` event から `visual_summaries` が作られる。
- Redis に `baseline_status = ready` が入る。

## Phase 9: Gateway baseline 参照

Gateway の signal summary / feedback 処理に baseline 参照を追加する。

処理:

1. `session:baseline:{session_id}:{audience_id}` を読む。
2. `session:visual_summary:{session_id}:{audience_id}` を読む。
3. `session:baseline_status:{session_id}:{audience_id}` を読む。
4. `ready` の場合だけ feedback に補正を入れる。
5. 未準備なら `warming_up` として default threshold を使う。

完了条件:

- image worker 実行前は default feedback。
- image worker 実行後は baseline / visual_summary を参照した feedback になる。

## Phase 10: Chrome 拡張との local integration

### 10.1 Gateway 接続

Chrome 拡張 config:

```js
window.REACTION_ENGINE_CONFIG = {
  gatewayWsUrl: "ws://localhost:8080/ws",
  mediaApiBaseUrl: "http://localhost:8081"
};
```

確認:

- side panel から capture start
- `realtime_feature` が Gateway に届く
- `feedback_event` が sidebar に返る

### 10.2 Media API 接続

確認:

1. baseline capture timing で screenshot を作る。
2. 同じ瞬間の compact `feature_snapshot` を作る。
3. Media API に upload URL request を送る。
4. local upload URL に WebP を PUT する。
5. complete endpoint を呼ぶ。
6. Image Analysis Worker が visual summary を作る。

完了条件:

```text
Chrome Extension
  -> Gateway feedback
  -> Media API capture snapshot
  -> Image Analysis Worker visual summary
  -> Gateway baseline-aware feedback
```

がローカルで一通り動く。

## Phase 11: audio_chunk / STT stub

最初は Google Cloud Speech-to-Text に接続しない。

stub 方針:

- `audio_chunk` を受け取る。
- 一定間隔で fake `transcript_chunk` を作る。
- Redis transcript window に保存する。
- `feature-events` に含める。

完了条件:

- `transcript_chunk` が Durable Writer 経由で JSONL / Postgres に保存される。
- 生 PCM は保存されない。

## Phase 12: Realtime LLM stub

最初は Gemini を呼ばない。

stub 方針:

- signal summary + transcript window から deterministic な feedback candidate を作る。
- timeout / error fallback のコードパスだけ先に作る。

完了条件:

- `source = rule` と `source = llm_stub` の両方を decision log に残せる。
- LLM disabled でも feedback が返る。

## Phase 13: Post-session Job local

処理:

1. local JSONL を読む。
2. Postgres の signal summaries / transcripts / participant_baselines / visual_summaries を読む。
3. 変化点を検出する。
4. fake report を作る。
5. `reports` に保存する。

完了条件:

- `go run ./cmd/post-session-job --session-id sess_local` で report が生成される。

## Phase 14: real GCP adapters

ローカル処理が通ってから差し替える。

差し替え順:

1. Cloud Storage signed URL
2. Pub/Sub publisher / subscriber
3. Cloud SQL 接続
4. Memorystore 接続
5. Speech-to-Text streaming
6. Vertex AI / Gemini

各 adapter は interface を切る。

例:

```go
type EventPublisher interface {
    Publish(ctx context.Context, topic string, payload any) error
}

type MediaStore interface {
    SignedUploadURL(ctx context.Context, object string, contentType string) (string, error)
    Exists(ctx context.Context, object string) (bool, error)
    Read(ctx context.Context, object string) ([]byte, error)
}
```

完了条件:

- local adapter と GCP adapter を env で切り替えられる。
- local test が GCP SDK なしでも通る。

## Phase 15: Cloud Run readiness

Cloud Run 移行前 checklist:

- 全 service が `PORT` を使う。
- `GET /healthz` がある。
- graceful shutdown がある。
- Docker image が build できる。
- env は Secret Manager / Cloud Run env に分離できる。
- instance memory に session state を置いていない。
- Redis / Cloud SQL / Pub/Sub / Cloud Storage 接続失敗時の log が明確。
- LLM / STT は timeout 付き。
- worker は idempotent。

## 最初のローカル完了条件

以下ができたら、インフラ移行前のローカル MVP として十分。

```text
docker compose up --build

1. gateway / media-api / writer / image-analysis-worker / postgres / redis が起動する。
2. Chrome 拡張から realtime_feature を送る。
3. feedback_event が返る。
4. Media API に capture_snapshot を保存する。
5. WebP 画像を local media storage に upload する。
6. upload complete で media_uploaded event が発行される。
7. Image Analysis Worker が visual_summary を作る。
8. Redis に baseline / visual_summary / baseline_status が入る。
9. 次回以降の feedback が baseline / visual_summary を参照する。
10. Writer が feature-events を JSONL / Postgres に保存する。
```

## 実装の優先順位

優先度順:

1. Go skeleton + Docker Compose
2. Postgres / Redis 起動
3. Gateway WebSocket MVP
4. Redis recent window
5. local event bus
6. Durable Writer local JSONL
7. Media API + capture_snapshots
8. Image Analysis Worker stub
9. Gateway baseline-aware feedback
10. Chrome 拡張 integration
11. STT stub
12. Realtime LLM stub
13. Post-session Job stub
14. GCP adapters
15. Cloud Run deploy

LLM / STT / Terraform は最初にやらない。まずデータの通り道、保存、Redis cache、feedback loop を固める。
