# architecture.md 準拠のためのローカルサーバー修正計画

## この文書の位置づけ

`architecture.md` は `mood_wave_sample` を中心とした設計に更新済みだが、`backend/`（Phase 0〜13, `plan/backend-local-docker-runbook.md`）は更新前の `realtime_feature`（Chrome が毎秒 raw な `face_tracks[]` を丸ごと送る方式）を前提に実装されている。`architecture.md` 自身がこのズレを明言している。

> 現状実装では、Chrome 側に `moodHistory`、`moodTimeline`、`snapshotBuffer` があり、簡易 mood wave と moment snapshot はすでにブラウザ内で成立している。今後は WebSocket payload を `realtime_feature` 中心から `mood_wave_sample` 中心へ寄せる。

この文書は、そのズレを解消するための修正ステップを一つにまとめたものである。`plan/gcp-adapter-migration-phase14.md`（ローカルスタンドイン → 実 GCP サービスへの差し替え）とは直交する作業であり、**Phase 14 より前に完了させるべき**（Phase 14 は「今の local 実装が正しい契約を実装している」ことを前提に GCP へ差し替える作業なので、契約自体がズレている今やっても手戻りになる）。

Chrome 拡張側も一部変更が要るが（WebSocket payload の型自体を変えるため、送信側・受信側は不可分）、この文書はユーザー指示どおり **バックエンド（`backend/`）の修正を主眼**に置き、拡張側の変更は「バックエンドを直すために最低限必要な差分」として Step 9 にまとめる。

## 現状のズレ一覧（根拠付き）

| # | 領域 | architecture.md の設計 | 現状実装 | 該当箇所 |
| --- | --- | --- | --- | --- |
| 1 | WebSocket ingress | `mood_wave_sample`（`(t_ms, y, attention_y)` の圧縮点列 + `signals`/`quality`）を受信 | `realtime_feature`（`face_tracks[]` に bbox・landmark・attention_score 等の raw feature をそのまま含む）を受信。`mood` フィールドは付いているが型自体は `realtime_feature` のまま | `backend/internal/gateway/gateway.go:117-125`, `backend/internal/contract/realtime_feature.go`, `extension/src/sidebar.js:1714-1738` |
| 2 | Redis データモデル | `mood_wave:recent:{session_id}` にセッション単位で1系列 | `features:recent:{session_id}:{audience_id}` に参加者ごとの raw feature 系列。`transcript:recent` の window も 10秒（30秒 window に対して短すぎる） | `backend/internal/redis/client.go:14-58` |
| 3 | trigger / evidence frame | Chrome が Media API から `upload_url`+`media_ref` を取得 → `mood_wave_sample.trigger`/`evidence_frame`（`upload_status: uploading`）を同梱 → 画像PUT → `evidence_upload_complete` | Chrome は `moment_trigger` というメタデータのみを WS に送り、画像は一切 upload しない（`snapshotBuffer` はローカル保持のみ）。Media API 側も `evidence_frame` purpose や `trigger_id` を扱う経路がない | `extension/src/sidebar.js:2381-2420`, `backend/internal/media/server.go`, `backend/internal/contract/media.go` |
| 4 | Realtime Worker | 直近30秒の mood wave window + summary（slope/volatility/min/max）+ transcript window + baseline/evidence frame refs を組み立て、cooldown/LLM budget を通してから LLM 呼び出し | `handleRealtimeFeature` が毎メッセージ・参加者ごとに即時ルール判定するだけ。30秒 window も summary も cooldown も存在しない | `backend/internal/gateway/gateway.go:128-198, 272-308` |
| 5 | feedback_event 契約 | `trigger_id` / `reason_codes` / `evidence_quote` / `model_version` / `confidence` / `cooldown_ms` を含む | `contract.FeedbackEvent` にこれらのフィールドが無い（`DecisionDetail` には既に入っているのに feedback_event へ転記されていない） | `backend/internal/contract/realtime_feature.go:25-34` 対 `backend/internal/contract/decision.go:24-32` |
| 6 | feedback_events の永続化 | `feedback_event` を Pub/Sub 経由で Durable Writer が Cloud SQL `feedback_events` に保存 | `feedback_events` テーブルは既に正しいスキーマで存在する（`reason_codes`/`evidence_quote`/`model_version`/`confidence`/`cooldown_ms` 列あり）が、**どこからも INSERT されていない**。writer は `decision_logs`/`transcripts` のみ書く | `backend/migrations/000001_initial_schema.up.sql:115-133`, `backend/cmd/writer/main.go:82-121` |
| 7 | trigger_events の永続化 | `trigger_event` を Cloud SQL `trigger_events` に保存 | `trigger_events` テーブル自体が存在しない。`capture_snapshots`/`media_refs` にも `trigger_id` 列が無い | `backend/migrations/000001_initial_schema.up.sql`（該当テーブル無し） |
| 8 | Image Analysis Worker | baseline frame だけを処理し、`baseline_visual_profile` を作る。evidence frame は通さない | `purpose` によるフィルタが無く、`media_uploaded` イベントを無条件に baseline 計算へ回す。evidence frame の upload が来た場合に誤って baseline を上書きする実装 | `backend/internal/imageanalysis/worker.go:47-87` |
| 9 | 保存する時系列データ | `mood_wave_sample` の JSONL（`sessions/{id}/mood-wave/part-0001.jsonl`） | `features/compact-raw/part-0001.jsonl`（raw な `face_tracks`/`attention_score` 単位） | `backend/internal/writer/jsonl.go:38-45` |
| 10 | 全体FBレポート入力 | `wave_overview`（drop_sections 等）、`important_windows`、`realtime_feedback_history`、`baseline_context` | 参加者ごとの `attention_score` 平均/最小/閾値越え点のみの stub レポート。`feedback_events`/`trigger_events` を一切参照しない（7,6 が未実装なので参照しようがない） | `backend/internal/postsession/report.go` |
| 11 | PDF / Gmail 配信 | `PDF Renderer` → `Gmail Sender` → `report_delivery` 保存 | 実装が全く存在しない（`post-session-job` は report JSON を Cloud SQL に保存して終了） | `backend/cmd/post-session-job/main.go`（`grep -rl "pdf\|gmail"` 該当なし） |

## 進め方の方針

- `plan/gcp-adapter-migration-phase14.md` と同じ運用: **1 Step = 1 ブランチ = 1 PR**、`backend-runbook-phase` skill と同様に develop から分岐して develop へ戻す。
- Step は依存順（データが生まれる場所 → 直近状態 → 判定 → 永続化 → レポート）に並べる。Step 1〜3 は必須の下地、Step 4〜8 はバックエンド本体、Step 9 は拡張側の対になる変更、Step 10〜11 は現状ゼロから作る新機能。
- 各 Step の最後に、`backend-local-docker-runbook.md` で使ってきたのと同じ手作業検証（`curl` / `redis-cli` / `psql`）と `go test ./...` / `npm run check` を通す。
- 契約が変わる Step 4 以降は、Chrome 側が新 payload を送るまでバックエンドが動かなくなる。Step 9（拡張側）を Step 4 の直後、実機検証の前に必ず挟む。ブランチは分けてよいが、develop へのマージ順は 1→2→3→4→9→5→6→7→8→10→11 を推奨する。

---

## Step 1: DB migration — trigger_events テーブル追加 + trigger_id 紐付け

- 新規 migration（`000003_add_trigger_events.up.sql` / `.down.sql`）を追加する。
  - `trigger_events` テーブル: `event_id`, `session_id`, `trigger_id`, `type`（`wave_drop`/`wave_rise`/`nod` 等）, `source`（`chrome`）, `peak_t_ms`, `delta`, `t_ms`, `created_at`。architecture.md の `trigger` payload（§画像フロー / データ契約）に合わせる。
  - `capture_snapshots` と `media_refs` に `trigger_id text NULL` 列を追加する（evidence frame は trigger に紐づき、baseline frame は NULL のまま）。
- `backend/internal/media/capture_snapshot.go` の `CaptureSnapshot`/`MediaRef`/`CaptureRecord` に `TriggerID` を追加し、`PGStore` の INSERT/SELECT 文を更新する。

DoD: `go run ./cmd/...`（あるいは既存の migrate コマンド）で up/down が通り、`\d trigger_events` / `\d capture_snapshots` で列が確認できる。

## Step 2: Redis キー設計をセッション単位の mood wave に作り直す

`backend/internal/redis/client.go` を次のように変更する。

- `featuresRecentKey` を廃止し、`moodWaveRecentKey(sessionID string) string`（`mood_wave:recent:{session_id}`）を追加。ZSET の member は `mood_wave_sample` 全体（`t_ms`, `y`, `attention_y`, `signals`, `quality`）。
- `StoreRecentFeature` を `StoreRecentMoodWaveSample` に置き換える。architecture.md の擬似コードどおり `recentWindow` は 10分（`now-10min` まで trim）にし、30秒 window の切り出しは呼び出し側（Step 4）で行う。
- `transcriptRecentWindow` を 10秒 → 30秒以上（mood wave window と揃える）に広げる。
- 追加するキー:
  - `trigger:recent:{session_id}` — 直近 trigger（cooldown 判定の入力）
  - `feedback:cooldown:{session_id}` — 最終 feedback 発火時刻（`SET ... EX` で TTL 管理する方が `cooldown_ms` の判定がシンプル）
  - `session:state:{session_id}` — 最新 mood sample のスナップショット（`latest_mood_sample`）
- `GetBaselineState` はそのまま維持（baseline は参加者単位で正しい設計のため変更不要）。

DoD: 新しいメソッドに対するユニットテストを `backend/internal/redis`（もしなければ新規）に追加し、`go test ./internal/redis/...` が通る。

## Step 3: contract パッケージに mood_wave_sample / trigger / evidence_frame 型を追加

- 新規ファイル `backend/internal/contract/mood_wave.go` に、architecture.md の「データ契約」節そのままの構造体を定義する。
  - `MoodWaveSampleMessage`（`Type`, `SchemaVersion`, `SessionID`, `TMs`, `MeetingProvider`, `Source`, `Mood{Value,Baseline,Y}`, `AttentionY`, `Signals{VisibleFaces,NodRatio,SpeechRatio,BrowFlag}`, `Quality{Calibrating,Confidence}`, `ClientModelVersion`, `Trigger *TriggerInfo`, `EvidenceFrame *EvidenceFrameRef`）
  - `TriggerInfo`（`TriggerID`, `Type`, `Source`, `PeakTMs`, `Delta`）
  - `EvidenceFrameRef`（`CaptureID`, `MediaRef`, `UploadStatus`, `SnapshotTMs`, `SnapshotLagMs`, `ContentType`）
  - `EvidenceUploadCompleteMessage`（`Type`, `SessionID`, `TriggerID`, `CaptureID`, `MediaRef`, `UploadStatus`）
- `backend/internal/contract/realtime_feature.go` の `FeedbackEvent` に不足フィールドを追加する: `TriggerID`, `ReasonCodes []string`, `EvidenceQuote *string`, `ModelVersion string`, `Confidence float64`, `CooldownMs int`（architecture.md の feedback_event 契約と揃える）。`DecisionDetail`（`decision.go`）は既にこれらを持っているので、そのまま転記できる形にする。
- `RealtimeFeatureMessage`/`FaceTrack`/`CompactFeature` は Step 4 で置き換わるまで残してよいが、置き換え後は削除する（未使用コードを残さない）。

DoD: `go build ./...` が通る（この時点ではまだ配線していないので未使用型があっても構わないが、最終的に Step 4〜8 で全て使われる）。

## Step 4: Gateway を mood_wave_sample ingress に置き換える

`backend/internal/gateway/gateway.go` の書き換え。

- `ServeWS` の `switch envelope.Type` に `"mood_wave_sample"` を追加し、`"realtime_feature"` は削除する。
- `handleMoodWaveSample`:
  1. `event_id` / `server_received_at_ms` を付与
  2. `redis.StoreRecentMoodWaveSample` で保存
  3. `analysis-events`（`feature-events` topic を改称するかは Step 8 で判断）へ publish
  4. `msg.Trigger` が非 nil なら trigger 受理処理へ渡す（Step 5）
- `handleAudioChunk` はほぼそのまま流用可能（audio_chunk の契約は architecture.md でも変わっていない）。ただし transcript window は Step 2 で広げた 30秒に合わせる。
- 参加者単位 (`audience_id`) の feedback fan-out（現在の `buildFeedback`/`decideFeedback`/`compactFeatures` ループ）は Step 5 の Realtime Worker ロジックに置き換わるため、ここでは mood wave の取り込みと trigger の受け渡しに専念させる。

DoD: `wscat` 等で `mood_wave_sample`（`architecture.md` の例 JSON）を送り、`redis-cli ZRANGE mood_wave:recent:sess_123 0 -1` で保存を確認する。

## Step 5: Realtime Worker（window summary + trigger + evidence pack + cooldown）

新規パッケージ `backend/internal/realtime/`（ディレクトリは既に存在するので中身を実装する: `find internal/realtime` で現状確認する）。

- `BuildMoodWaveWindow(samples []MoodWaveSample, endTMs int64, durationSec int) MoodWaveWindow`: `start_t_ms`/`end_t_ms`/`points[]`/`summary`（`overall`/`start_y`/`end_y`/`min_y`/`max_y`/`slope_per_sec`/`volatility`）を architecture.md の realtime LLM 入力例どおりに計算する。
- `AcceptTrigger(ctx, sessionID string, trigger TriggerInfo) (accepted bool, reason string)`: `trigger:recent` と `feedback:cooldown` を見て cooldown / LLM budget を判定する（architecture.md 「9. Realtime Worker は trigger 付き sample を受理し、cooldown / LLM budget を確認する」）。
- `BuildEvidencePack(...)`: 30秒 window + `transcript_window`（Redis から）+ `baseline_frames`（`session:baseline:*` 経由で media_ref を引く。現状 baseline は attention_score のみのスタブなので、`media_ref` も一緒に持たせるよう Step 6/Image Analysis Worker 側で保存対象を広げる）+ `evidence_frames`（`media_refs` テーブルから `upload_status=uploaded` のものだけを、未完了なら画像なしで進める — architecture.md 「11.」）。
- `decideFeedback` を刷新: 現状の「参加者ごと・毎メッセージ判定」から「trigger 受理時のみ」に変える。ルール fallback は mood wave summary（`slope_per_sec` が閾値未満で `overall: declined` なら `reaction_down_candidate` 等）を使うよう作り直す（`architecture.md` の `feedback_type: "reaction_down_candidate"` 例に合わせる）。LLM stub 呼び出し（`generateLLMStubCandidate`）は evidence pack 全体を受け取る形に拡張する。
- 生成した `feedback_event` は Step 3 で拡張したフィールド（`trigger_id`/`reason_codes`/`evidence_quote`/`model_version`/`confidence`/`cooldown_ms`）を全て埋める。

DoD: trigger 付き `mood_wave_sample` を送ってから cooldown 期間内に再度送っても feedback が連続発火しないことを確認する。`go test ./internal/realtime/...` を追加する。

## Step 6: feedback_events / trigger_events の永続化

- `backend/cmd/writer/main.go` の `writeEvent` に分岐を追加:
  - `payload` に `TriggerEvent` があれば `trigger_events` へ INSERT（Step 1 のテーブル）
  - `payload` に `FeedbackEvent` があれば `feedback_events` へ INSERT（既存テーブル、Step 3 で埋めたフィールドをそのまま挿入）
- `backend/internal/writer/`（`jsonl.go` 相当）に `InsertFeedbackEvent`/`InsertTriggerEvent` を追加する `writer.Store` のメソッドを実装する。
- `contract.FeatureEventPayload` に `TriggerEvents []TriggerEvent` と `FeedbackEvents []FeedbackEvent` を追加する（Feature/TranscriptChunks/DecisionLogs と同様、排他ではなく Gateway 側からの発行元ごとに詰める）。

DoD: trigger 付き mood wave を送った後、`psql` で `SELECT * FROM feedback_events` / `SELECT * FROM trigger_events` に行が入ることを確認する。

## Step 7: Media API の evidence_frame 対応

`backend/internal/media/server.go` / `signed_url.go` / `capture_snapshot.go` の変更。

- `handleUploadURL`: `purpose == "evidence_frame"` のとき `req` に `trigger_id` を必須にする（`validateUploadURLRequest` へ分岐追加）。`CaptureSnapshot`/`MediaRef` に `TriggerID` を持たせて INSERT する（Step 1 の列）。
- 画像本体の PUT 完了を待たずに `media_ref` を返す現状の設計（`upload_status: pending`）は architecture.md の「予約してから返す」方式と一致しているので維持する。ただし `mood_wave_sample.evidence_frame.upload_status` は Chrome 側が `"uploading"` を名乗る前提（`architecture.md`）なので、Media API 側のレスポンス直後の内部ステータスは `pending`、Chrome が mood_wave_sample に載せる文字列は `uploading` という対応関係をコード上のコメントで明記する。
- `handleUploadComplete` はほぼそのままだが、`media_uploaded` イベントの `purpose` を Step 8 の Image Analysis Worker 側フィルタで使う。

DoD: `curl` で `purpose=evidence_frame` + `trigger_id` を含む upload-url リクエストが通り、`media_refs.trigger_id` に値が入ることを確認する。

## Step 8: Image Analysis Worker を baseline frame 専用にする

`backend/internal/imageanalysis/worker.go` の変更。

- `ProcessMediaUploaded` の先頭で `payload.Purpose != "baseline_frame"` なら即 return（何もしない）ガードを追加する。architecture.md 「Realtime Worker は evidence frame を画像解析 worker に通さない」「Image Analysis Worker は baseline frame から baseline_visual_profile を作るのが主責務」に対応。
- コメントに「evidence frame の事後解析は将来の別 worker/別トピックで行う（このワーカーの責務ではない）」ことを明記する。

DoD: `purpose=evidence_frame` の `media_uploaded` イベントを流しても `participant_baselines`/`visual_summaries` が更新されないことをテストで確認する（`worker_test.go` があれば追加、無ければ新規作成）。

## Step 9: Chrome 拡張側の対になる変更（バックエンド契約変更の前提）

Step 4 以降がマージされたら、Chrome 側もこれに合わせて変更しないと WS が壊れる。`extension/src/sidebar.js` の変更点:

- `sendFeatureEvent`（1097-1738行付近）: 送信 payload の `type` を `"mood_wave_sample"` に変更し、`face_tracks` 等の raw feature を含めず、architecture.md の最小 payload（`mood.value/baseline/y`, `attention_y`, `signals.{visible_faces,nod_ratio,speech_ratio,brow_flag}`, `quality.{calibrating,confidence}`, `client_model_version.mood_wave`）だけを送るよう絞り込む。raw feature をサーバーに送らないという設計原則（architecture.md 冒頭）に一致させる。
- `captureMoment`（2381-2420行）: `moment_trigger` を送るだけの現状をやめ、architecture.md の trigger フローに合わせる。
  1. Media API `POST /sessions/{id}/media/upload-url`（`purpose: "evidence_frame"`, `trigger_id`）を呼ぶ
  2. 返ってきた `upload_url`/`media_ref` を使い、次の `mood_wave_sample` 送信に `trigger`/`evidence_frame(upload_status: "uploading")` を同梱する
  3. 並行して signed URL へ画像 PUT → 完了後 `evidence_upload_complete` を送る（もしくは Media API の `/complete` エンドポイントを直接叩く。バックエンド側は Step 7 の `/complete` で対応済み）
- baseline frame の upload（既存の 1543-1571行）はほぼそのまま維持できる。

DoD: 実機（ローカル Docker Compose）で Meet 相当のテストページに接続し、`mood_wave_sample` が Gateway に届いて `redis-cli` で確認できる。trigger を発火させ、evidence frame が `./tmp/media` に保存され `media_refs.upload_status=uploaded` になることを確認する。

## Step 10: 全体FBレポートを architecture.md の入力形状に作り直す

`backend/internal/postsession/report.go` の作り直し。

- 入力を「`features/compact-raw` JSONL + `attention_score`」から「`mood-wave` JSONL（Step 8 で writer が書くようになる）+ `trigger_events` + `feedback_events`」に変更する。
- 出力を architecture.md の `post_session_report` 入力例（`wave_overview.{overall,peak_positive_sections,drop_sections}`, `important_windows[].{start,end,mood_wave_summary,transcript_summary,evidence_refs}`, `realtime_feedback_history`, `baseline_context`）に合わせる。
- `important_windows` の抽出は「trigger_events の前後 ±90秒」を基本ロジックにし、Step 5 の `BuildMoodWaveWindow` を再利用する。
- `Source: "post_session_stub"` は維持してよい（実 LLM 呼び出しは Phase 14 の範囲）。

DoD: 既存の `report_test.go` を新しい入力/出力形状に合わせて書き換え、`go test ./internal/postsession/...` が通る。

## Step 11: PDF レンダラー + Gmail 送信（新規実装）

現状ゼロなので、architecture.md の MVP 実装順 12・13 に沿って最小実装を追加する。

- 新規 `backend/cmd/pdf-renderer/`（または `post-session-job` に統合）: report JSON → 簡易 PDF（HTML → PDF、まずは headless Chromium か Go の PDF ライブラリでよい）を生成し `./tmp/media` 相当のローカルストレージに保存、`reports` テーブルにパスを追記する列が必要なら migration を追加する。
- 新規 `backend/cmd/gmail-sender/`: ローカルでは実 Gmail API を呼ばず、送信ログを標準出力/ファイルに書くスタブから始める（Phase 14 で実 Gmail API に差し替える前提。`gcp-adapter-migration-phase14.md` の方針と揃える）。
- `report_delivery` を保存するテーブルが無いので migration を追加する（`report_deliveries`: `report_id`, `status`, `sent_at`, `error`）。

DoD: `post-session-job` 実行後に PDF ファイルが生成され、`report_deliveries` に `status=sent`（スタブ）が記録される。

---

## 各 Step 完了後の共通チェック

- `go build ./... && go test ./...`（backend）
- `npm run check`（extension、Step 9 のみ）
- `docker compose up` で一連の流れ（mood_wave_sample → trigger → evidence upload → feedback_event → writer → post-session-job）を通しで一度動かす
- 変更した契約が `architecture.md` の該当 JSON 例と一致しているか diff で目視確認する

## 既存 plan ドキュメントとの関係

- `plan/backend-local-docker-runbook.md`（Phase 0〜13）: この文書が対象にしているのは Phase 0〜13 で作られた実装そのもの。Phase 14/15 はそのまま参照してよい。
- `plan/gcp-adapter-migration-phase14.md`: 本文書の Step 1〜11 が完了してから着手する（今の local 実装を GCP に「そのまま」差し替えると、ズレた契約を Cloud 上に固定してしまうため）。
- `plan/implementation-todo.md`: Chrome 拡張側の Meet tile 抽出などは本文書と独立に進めてよい（`tile_id`/`participant_name` は mood_wave_sample の必須フィールドではなく、evidence frame の付随情報として残る）。
