# Backend 実装ステップ

このドキュメントは、Reaction Engine のサーバーサイドを Google Cloud 前提で実装するための step-by-step plan。

対象スタック:

- Language: TypeScript
- Runtime: Node.js 22
- Framework: Fastify
- Deploy: Cloud Run
- Validation: TypeBox
- DB: Kysely + postgres.js
- Redis: ioredis against Memorystore for Redis
- GCP SDK:
  - `@google-cloud/pubsub`
  - `@google-cloud/storage`

対象ディレクトリ:

- `backend/`
- `infra/`
- `architecture.md`

## Goal

Chrome 拡張から送られる `realtime_feature` を Cloud Run WebSocket Gateway で受け取り、以下を実現する。

1. Memorystore for Redis に直近 window / latest state / cooldown を保存する。
2. Gateway 内のシステム演算層で 5s / 10s / 30s window の signal summary を計算する。
3. Gateway 内のシステム演算層で decision log を作り、簡単な `feedback_event` を返す。
4. Pub/Sub topic `feature-events` に compact raw feature + signal summary + decision log を publish する。
5. Cloud Run Durable Writer が Pub/Sub event を読み、JSONL を Cloud Storage に保存する。
6. session metadata / signal summary / decision log / feedback history を Cloud SQL for PostgreSQL に保存する。
7. 後続の Cloud Run Jobs が compact raw JSONL、signal summary、transcript からセッション後レポートを作れる状態にする。

## Phase 0: 前提整理

### 0.1 決定事項を固定する

実装前に以下を README または issue に明記する。

- Google Cloud project id
- default region
- Cloud Run service 名
  - `reaction-gateway`
  - `reaction-writer`
  - `reaction-analysis-job`
- Pub/Sub topic / subscription 名
  - topic: `feature-events`
  - subscription: `feature-events-durable-writer`
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
- Cloud SQL 未設定時: DB 書き込み skip または local Postgres

受け入れ条件:

- ローカルで Chrome 拡張から WebSocket 接続し、`feedback_event` を返せる。
- GCP 接続がないことを理由に gateway 開発が止まらない。

## Phase 1: Backend project skeleton

### 1.1 `backend/package.json` を作る

追加する scripts:

- `dev:gateway`
- `dev:writer`
- `build`
- `check`
- `test`
- `lint` optional
- `typecheck`

依存候補:

- `fastify`
- `@fastify/websocket`
- `@sinclair/typebox`
- `kysely`
- `postgres`
- `ioredis`
- `@google-cloud/pubsub`
- `@google-cloud/storage`
- `tsx`
- `typescript`
- `vitest`

受け入れ条件:

- `cd backend && npm run check` が実行できる。
- Node.js 22 前提が README または `engines` にある。

### 1.2 TypeScript 設定を作る

追加候補:

- `backend/tsconfig.json`
- `backend/src/index.ts` placeholder

方針:

- ESM で統一する。
- `strict: true`
- source は `src/`
- build output は `dist/`

受け入れ条件:

- `npm run typecheck` が通る。

### 1.3 backend env schema を作る

場所:

- `backend/src/config/env.ts`

必要な env:

- `NODE_ENV`
- `PORT`
- `GOOGLE_CLOUD_PROJECT`
- `GCP_REGION`
- `REDIS_HOST`
- `REDIS_PORT`
- `PUBSUB_FEATURE_EVENTS_TOPIC`
- `PUBSUB_FEATURE_EVENTS_SUBSCRIPTION`
- `FEATURE_BUCKET`
- `DATABASE_URL`
- `LOCAL_FALLBACK_ENABLED`

受け入れ条件:

- env の不足時に分かりやすい error を出す。
- local fallback が有効な場合は GCP env 不足を許容できる。

## Phase 2: Shared schemas

### 2.1 `realtime_feature` schema を定義する

場所:

- `backend/src/schemas/realtime-feature.ts`

含めるフィールド:

- `event_id`
- `type`
- `schema_version`
- `session_id`
- `t_ms`
- `server_received_at_ms`
- `meeting_provider`
- `source`
- `features`
  - `face_visible`
  - `face_count`
  - `face_tracks`
  - `motion_score`
  - `attention_score`
  - `gaze_estimate`
  - `gestures`
  - `client_model_version`

注意:

- Chrome 拡張から送られる時点では `event_id` と `server_received_at_ms` はなくてもよい。
- Gateway internal event では必須にする。

受け入れ条件:

- inbound payload と enriched payload の schema が分かれている。
- TypeBox から TypeScript type を export している。

### 2.2 `feedback_event` schema を定義する

場所:

- `backend/src/schemas/feedback-event.ts`

含めるフィールド:

- `type`
- `session_id`
- `t_ms`
- `feedback_type`
- `severity`
- `message`
- `reason_codes`
- `confidence`
- `cooldown_ms`

受け入れ条件:

- Chrome 拡張 sidebar が表示しやすい形になっている。
- message は短く、発表者がすぐ行動できる表現にする。

### 2.3 Pub/Sub envelope を定義する

場所:

- `backend/src/schemas/pubsub-event.ts`

含めるフィールド:

- `event_id`
- `type`
- `schema_version`
- `session_id`
- `t_ms`
- `server_received_at_ms`
- `payload`

受け入れ条件:

- Durable Writer が `event_id` で冪等処理できる。
- 将来 schema migration できるよう `schema_version` がある。

## Phase 3: Gateway MVP

### 3.1 Fastify app を作る

場所:

- `backend/src/server.ts`
- `backend/src/gateway/register-routes.ts`

エンドポイント:

- `GET /healthz`
- `GET /readyz`
- `GET /realtime` WebSocket

受け入れ条件:

- `npm run dev:gateway` で起動できる。
- `GET /healthz` が 200 を返す。
- WebSocket 接続ができる。

### 3.2 WebSocket message handler を作る

場所:

- `backend/src/gateway/websocket.ts`

処理:

1. client から JSON message を受け取る。
2. `type === "realtime_feature"` を検証する。
3. payload schema validation を行う。
4. `event_id` を採番する。
5. `server_received_at_ms` を付与する。
6. compact raw feature に正規化する。
7. Redis recent state writer に渡す。
8. Redis recent window を読み、システム演算層で signal summary を計算する。
9. feedback decision を実行し、decision log を作る。
10. Pub/Sub publisher に compact raw feature + signal summary + decision log を渡す。
11. feedback があれば WebSocket に返す。

受け入れ条件:

- 不正 JSON で gateway が落ちない。
- schema validation error を log に出せる。
- 正常 event で signal summary / feedback decision / Pub/Sub publish まで到達する。

### 3.3 ローカル互換を維持する

現在の `scripts/ws-log-server.cjs` はローカル検証用として残す。

方針:

- 新 gateway が動いたら README に推奨を切り替える。
- 古い log server は simple receiver として維持してよい。

受け入れ条件:

- 既存 Chrome 拡張の WebSocket URL に新 gateway を指定できる。
- 既存 log server を壊さない。

## Phase 4: Memorystore / Redis recent state

### 4.1 Redis client adapter を作る

場所:

- `backend/src/realtime/redis-client.ts`
- `backend/src/realtime/recent-state.ts`

実装:

- `ioredis` client
- local fallback in-memory implementation
- interface を定義する

interface 案:

```ts
interface RecentStateStore {
  writeFeature(event: EnrichedRealtimeFeatureEvent): Promise<void>;
  readWindow(sessionId: string, fromMs: number, toMs: number): Promise<CompactFeature[]>;
  readLatest(sessionId: string): Promise<CompactFeature | null>;
  getCooldown(sessionId: string, feedbackType: string): Promise<number | null>;
  setCooldown(sessionId: string, feedbackType: string, untilMs: number): Promise<void>;
}
```

受け入れ条件:

- Redis 実装と in-memory 実装を差し替えられる。
- unit test で window read/write を確認できる。

### 4.2 Redis key design を実装する

keys:

- `features:recent:{session_id}`
- `session:state:{session_id}`
- `feedback:cooldown:{session_id}`

write:

- `ZADD features:recent:{session_id} t_ms compact_payload`
- `ZREMRANGEBYSCORE features:recent:{session_id} -inf now-60000`
- `HSET session:state:{session_id} latest_feature compact_payload latest_t_ms t_ms`
- `EXPIRE ... 3600`

受け入れ条件:

- 直近 60秒より古い event が削除される。
- TTL が設定される。
- Redis 書き込み失敗時に gateway 全体が落ちない。

## Phase 5: Signal summary and Pub/Sub publish

### 5.1 realtime signal summary を作る

場所:

- `backend/src/realtime/window-aggregation.ts`
- `backend/src/realtime/signal-summary.ts`

計算する値:

- latest
- 5秒平均
- 10秒平均
- 30秒平均
- 直前 window との差分
- session baseline との差分
- slope
- low / high state の継続時間
- confidence

対象 signal:

- `attention_score`
- `motion_score`
- `face_count`
- `gaze_estimate`
- `gestures.nod_count`
- 将来: `audio_level`, `silence_ms`, `speaking_rate`

受け入れ条件:

- 1件の latest feature と Redis recent window から signal summary を作れる。
- 欠損 signal があっても落ちない。
- unit test で avg / delta / duration を確認できる。

### 5.2 decision log を作る

場所:

- `backend/src/realtime/decision-log.ts`

含める情報:

- `event_id`
- `session_id`
- `t_ms`
- `rule_version`
- `signal_summary`
- `feedback_type`
- `reason_codes`
- `confidence`
- `cooldown_applied`
- `feedback_emitted`

受け入れ条件:

- feedback が出ない場合も decision log を作れる。
- 後から「なぜ feedback が出た/出なかったか」を追える。

### 5.3 Pub/Sub publisher adapter を作る

場所:

- `backend/src/events/pubsub-publisher.ts`

実装:

- `@google-cloud/pubsub`
- topic name は env から読む。
- local fallback は console log / local file でもよい。

受け入れ条件:

- gateway から compact raw feature + signal summary + decision log を publish できる。
- publish 失敗時は log に出る。
- retry するか、gateway の response 方針を明記する。

### 5.4 Pub/Sub message attributes を決める

attributes:

- `event_type`
- `session_id`
- `schema_version`
- `source`

受け入れ条件:

- subscription filtering を将来追加できる。
- logs で session 単位に追える。

## Phase 6: Realtime feedback decision

### 6.1 MVP rule engine を作る

場所:

- `backend/src/realtime/decision.ts`

最初の rule:

- face_count が 0 の状態が 5秒以上続く
- attention_score が 10秒平均で一定以下
- motion_score が 10秒平均で極端に低い
- cooldown 中は同じ feedback を出さない

出力:

- `feedback_event`

受け入れ条件:

- signal summary から feedback が生成される。
- cooldown が効く。
- feedback message が短く行動可能な文言になっている。

### 6.2 feedback policy を分ける

場所:

- `backend/src/realtime/feedback-policy.ts`

責務:

- severity
- confidence
- cooldown_ms
- wording

受け入れ条件:

- rule と文言が密結合しすぎない。

## Phase 7: Durable Writer MVP

### 7.1 writer entrypoint を作る

場所:

- `backend/src/writer/main.ts`

形:

- Pub/Sub pull worker か push endpoint のどちらかを決める。
- Cloud Run では push subscription endpoint の方が運用しやすい。
- local dev では pull でもよい。

受け入れ条件:

- Pub/Sub event を受け取れる。
- invalid payload を dead-letter または error log に回せる。

### 7.2 Cloud Storage JSONL writer を作る

場所:

- `backend/src/storage/feature-jsonl-writer.ts`

paths:

```text
gs://{FEATURE_BUCKET}/sessions/{session_id}/features/compact-raw/{yyyyMMdd-HHmmss}-{chunk_id}.jsonl
gs://{FEATURE_BUCKET}/sessions/{session_id}/features/signal-summary/{yyyyMMdd-HHmmss}-{chunk_id}.jsonl
gs://{FEATURE_BUCKET}/sessions/{session_id}/features/decision-log/{yyyyMMdd-HHmmss}-{chunk_id}.jsonl
```

方針:

- Cloud Storage は append ではなく chunk file として保存する。
- MVP では 1 message 1 JSONL file でもよい。
- 次段階で batch chunk にする。
- full face landmarks / face_parts は常時保存しない。

受け入れ条件:

- Pub/Sub event の compact raw feature / signal summary / decision log が Cloud Storage に JSONL として保存される。
- JSONL 1行が valid JSON。
- `event_id` が含まれる。

### 7.3 Cloud SQL summary writer を作る

場所:

- `backend/src/db/client.ts`
- `backend/src/db/schema.ts`
- `backend/src/db/repositories/signal-summary-repository.ts`
- `backend/src/db/repositories/decision-log-repository.ts`
- `backend/src/db/repositories/session-summary-repository.ts`

最小テーブル:

- `sessions`
- `feedback_events`
- `signal_summaries`
- `decision_logs`
- `session_summaries`
- `processed_events`

注意:

- compact raw feature を全件 Cloud SQL に入れない。
- Cloud SQL には signal summary / decision log / feedback history を入れる。
- `processed_events.event_id` で冪等性を担保する。

受け入れ条件:

- 同じ `event_id` を再処理しても signal summary / decision log / summary が二重保存されない。
- DB 未設定時は writer が local JSONL だけで動ける fallback を用意する。

## Phase 8: Database migrations

### 8.1 migration tool を決める

候補:

- Kysely migration
- node-pg-migrate
- drizzle-kit は Drizzle を採用する場合のみ

推奨:

- Kysely migration

受け入れ条件:

- `backend/migrations/` に migration が置ける。
- `npm run db:migrate` の方針が README に書かれている。

### 8.2 初期 schema を作る

最小 schema:

```sql
sessions (
  session_id text primary key,
  meeting_provider text not null,
  started_at timestamptz not null,
  ended_at timestamptz,
  status text not null,
  created_at timestamptz not null
)

processed_events (
  event_id text primary key,
  session_id text not null,
  event_type text not null,
  processed_at timestamptz not null
)

feedback_events (
  id bigserial primary key,
  event_id text,
  session_id text not null,
  t_ms bigint not null,
  feedback_type text not null,
  severity text not null,
  message text not null,
  confidence double precision,
  created_at timestamptz not null
)

session_summaries (
  session_id text primary key,
  event_count bigint not null default 0,
  avg_attention_score double precision,
  avg_motion_score double precision,
  max_face_count integer,
  updated_at timestamptz not null
)

signal_summaries (
  event_id text primary key,
  session_id text not null,
  t_ms bigint not null,
  attention_10s_avg double precision,
  attention_delta_prev_10s double precision,
  motion_10s_avg double precision,
  motion_delta_prev_10s double precision,
  low_duration_ms bigint,
  confidence double precision,
  created_at timestamptz not null
)

decision_logs (
  event_id text primary key,
  session_id text not null,
  t_ms bigint not null,
  rule_version text not null,
  feedback_type text,
  reason_codes jsonb not null,
  confidence double precision,
  cooldown_applied boolean not null,
  feedback_emitted boolean not null,
  created_at timestamptz not null
)
```

受け入れ条件:

- Durable Writer が signal summary / decision log を書ける。
- report 用の詳細 schema は後続 phase に回せる。

## Phase 9: Terraform MVP

### 9.1 dev environment の最小リソース

場所:

- `infra/environments/dev/`

最初に作るもの:

- Pub/Sub topic `feature-events`
- Pub/Sub subscription `feature-events-durable-writer`
- dead-letter topic
- Cloud Storage bucket
- service accounts
- basic IAM bindings
- Secret Manager placeholder

受け入れ条件:

- `terraform plan` が通る。
- backend service account が Pub/Sub publish / subscribe と Cloud Storage write に必要な権限を持つ。

### 9.2 Cloud Run / Memorystore / Cloud SQL を追加

次段階で追加:

- Cloud Run `reaction-gateway`
- Cloud Run `reaction-writer`
- Cloud Run Job `reaction-analysis-job`
- Memorystore for Redis
- Cloud SQL for PostgreSQL
- Serverless VPC Access

受け入れ条件:

- Cloud Run から Memorystore に接続できる。
- Cloud Run から Cloud SQL に接続できる。
- secrets は Secret Manager 経由で渡す。

## Phase 10: Cloud Run deployment

### 10.1 Dockerfile を作る

場所:

- `backend/Dockerfile`

方針:

- Node.js 22
- production dependencies のみ
- `npm run build`
- entrypoint を env で切り替えるか、service ごとに command を変える。

受け入れ条件:

- gateway image が build できる。
- writer image が build できる。

### 10.2 service entrypoint を分ける

entrypoints:

- `backend/src/gateway/main.ts`
- `backend/src/writer/main.ts`
- `backend/src/jobs/analysis-main.ts`

受け入れ条件:

- 同じ image で command だけ変えて Cloud Run service / job を動かせる。

## Phase 11: End-to-end local test

### 11.1 Chrome extension -> local gateway

手順:

1. `npm run dev:gateway`
2. Chrome extension sidebar の WebSocket URL に local gateway を設定
3. `Start Capture`
4. `realtime_feature` が gateway log に出る
5. feedback rule に合えば `feedback_event` が sidebar に出る

受け入れ条件:

- extension の既存 payload を gateway が受け取れる。
- 1秒ごとの event で gateway が落ちない。

### 11.2 local gateway -> local writer fallback

手順:

1. Pub/Sub fallback を local file にする。
2. writer を起動する。
3. compact raw / signal summary / decision log JSONL が local output に保存される。

受け入れ条件:

- compact raw event と signal summary / decision log が欠損なく JSONL に残る。
- `event_id` が入る。

## Phase 12: GCP dev smoke test

### 12.1 Cloud Run gateway smoke test

確認:

- `/healthz`
- `/readyz`
- WebSocket connect
- Redis write
- Pub/Sub publish

受け入れ条件:

- Cloud Logging で session_id / event_id が追える。
- Pub/Sub topic に message が入る。

### 12.2 Durable writer smoke test

確認:

- Pub/Sub subscription から message を受け取る。
- Cloud Storage に compact raw / signal summary / decision log JSONL ができる。
- Cloud SQL に signal summary / decision log / session summary が入る。
- 成功時 ack される。

受け入れ条件:

- 同じ message が再配信されても二重処理されない。

## Phase 13: Post-session analysis skeleton

### 13.1 analysis job placeholder

場所:

- `backend/src/jobs/analysis-main.ts`

処理:

1. `session_id` を受け取る。
2. Cloud Storage の compact raw feature JSONL と signal summary JSONL 一覧を取得する。
3. event を読み込む。
4. simple summary report を作る。
5. Cloud SQL に report placeholder を保存する。

受け入れ条件:

- LLM なしで report placeholder まで作れる。
- 後で Speech-to-Text / Vertex AI を差し込める。

## Phase 14: Documentation

### 14.1 backend README を更新する

書く内容:

- local dev setup
- required env
- gateway 起動手順
- writer 起動手順
- GCP dev smoke test
- known fallbacks

受け入れ条件:

- 新しい開発者が README だけで gateway を起動できる。

### 14.2 architecture と plan の同期

更新対象:

- `architecture.md`
- `plan/1st-plan.md`
- `plan/implementation-todo.md`

受け入れ条件:

- Redis Stream 前提の記述が復活していない。
- Google Cloud 構成と backend 実装が一致している。

## 実装順の推奨

最初の PR は小さく分ける。

1. backend TypeScript skeleton
2. schemas
3. Fastify WebSocket Gateway local only
4. Redis recent state adapter with in-memory fallback
5. signal summary / decision log
6. Pub/Sub publisher with local fallback
7. MVP feedback rules
8. Durable Writer local JSONL
9. Cloud Storage writer
10. Cloud SQL schema + signal summary / decision log writer
11. Terraform dev MVP
12. Cloud Run deployment
13. analysis job placeholder

## Done の定義

MVP backend が done と言える状態:

- Chrome extension から Cloud Run Gateway に WebSocket 接続できる。
- `realtime_feature` が 1秒ごとに受信される。
- Memorystore に recent state が入る。
- Gateway が signal summary / decision log を生成する。
- Pub/Sub に compact raw feature + signal summary + decision log が publish される。
- feedback rule により `feedback_event` が返る。
- Durable Writer が Pub/Sub event を Cloud Storage JSONL に保存する。
- Cloud SQL に signal summary / decision log / session summary / feedback history が保存される。
- session_id を指定して analysis job placeholder が report を作れる。
- README に local / dev GCP の起動手順がある。
