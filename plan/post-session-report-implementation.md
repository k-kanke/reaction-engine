# 全体FBレポートフロー実装計画

## 目的

リアルタイムFBフロー(gateway / media-api / Vertex AI Gemini)は本番で動作確認済み。次は
architecture.md の「全体FBレポートフロー」(セッション終了後に mood wave / transcript /
trigger / feedback 履歴 / baseline を集約し、Gemini でレポートを作り、PDF化して Gmail で送る)
を実装する。

この文書は 2026-07-09 時点のコード調査に基づく現状把握と、依存関係を踏まえた実装順をまとめる。

## 現状

### できている(コードとして存在する)

- `backend/cmd/writer` + `backend/internal/writer`: `feature-events`(Postgres の
  `local_events` テーブルで代替した Pub/Sub スタブ)を読み、mood_wave_sample を GCS/ local
  JSONL に、trigger_events / feedback_events / transcripts を Postgres + JSONL の両方に書く。
  ロジック自体は実装済み。
- `backend/cmd/post-session-job` + `backend/internal/postsession`: JSONL から
  mood_wave_sample を読み戻し、Postgres から trigger_events / feedback_events /
  transcripts / evidence media_refs / participant_baselines / visual_summaries を読み、
  `postsession.BuildReport` で `reports.report`(jsonb)を組み立てて保存する。
- `backend/cmd/pdf-renderer` + `backend/internal/pdf`: 最新の report を読み、テキストとして
  PDF 化し、`internal/media`(Local/GCS 共通)経由で `sessions/{id}/reports/report.pdf` に
  保存し、`reports.pdf_path` を更新する。
- `backend/cmd/gmail-sender`: `reports.pdf_path` を引いて `--to` の宛先に「送ったふりをして」
  `report_deliveries` に記録する。
- 必要な DB スキーマ(`reports` / `report_deliveries` / `trigger_events` /
  `participant_baselines` / `visual_summaries` など)はマイグレーション
  `000001`〜`000004` として存在する。

### まだできていない

- **`r-writer` が本番 Cloud Run にデプロイされていない。** `gcloud run services list` で
  確認できるのは `r-gateway` / `r-media-api` の2つのみ。つまり本番では
  mood_wave_sample / trigger_events / feedback_events / transcripts が今何一つ永続化
  されていない(Redis の短期 window から消えたら跡形もなくなる)。
- **`image-analysis-worker` も本番にデプロイされていない。** 前回のリアルタイムFB調査
  (`plan/realtime-llm-context-next-steps.md` 相当の続き)で判明済み。これが無いと
  `participant_baselines` / `visual_summaries` が空のままになり、post-session report の
  `baseline_context` も常に空になる。
- **`post-session-job` / `pdf-renderer` / `gmail-sender` を動かす Cloud Run Job が
  1つも存在しない。** `infra/modules/cloud-run-job` は README だけのプレースホルダーで
  `main.tf` が無い。3つとも `--session-id` を渡す CLI として書かれているが、誰が・いつ・
  どうやって起動するかの仕組みが無い。
- **セッション終了を検知する仕組みが無い。** architecture.md は「session end で
  Post-session Job を起動する」とだけ書いてあるが、gateway にも extension にも
  「セッションが終わった」を明示的に伝えるメッセージ/イベントが実装されていない
  (`rg` で `session_end` / `SessionEnd` が1件もヒットしない)。
- **`postsession.BuildReport` は完全に非LLM。** `Source: "post_session_stub"` の通り、
  `wave_overview.overall` も `important_windows[].transcript_summary` も if/switch と
  文字列結合だけで作っている。architecture.md の Step 7「Gemini で全体 feedback report
  を生成する」がまだ無い。
- **PDF レンダラーが日本語を全部捨てている。** `internal/pdf.go` の `sanitizeLine` は
  ASCII (32-126) 以外を破棄し `"(non-ASCII content omitted)"` を付け足すだけ。フィード
  バックメッセージも transcript もほぼ全て日本語なので、**今のままレポートPDFを出すと
  ほぼ空になる**。base14 Helvetica しか使っていないための制約で、実装コメントにも
  「out of scope for this local stand-in」と明記されている既知の割り切り。
- **Gmail 送信が完全にスタブ。** README にも明記の通り「ログを出すだけ」。実際の
  Gmail API 呼び出しも、送信先メールアドレスをどこから得るか(今は手動 `--to` 引数)も
  未実装。
- **media-api で経験した「CI/CDが無いサービスは古いコードのまま動く」問題**が、
  writer / image-analysis-worker / 各Jobにも同様に当てはまる。`deploy-gateway.yml` /
  (新設した)`deploy-media-api.yml` 以外のデプロイワークフローは無い。

## 目標フロー

```text
Chrome extension (session end)
  |
  | (未実装) session end 通知
  v
Gateway / r-gateway
  |
  | (未実装) Cloud Run Jobs 起動 or Pub/Sub でトリガー
  v
r-post-session-job
  |
  | JSONL(GCS) + Postgres から
  | mood_wave / transcript / trigger / feedback / baseline を読む
  | (未実装) Gemini で report 生成
  v
reports (Postgres jsonb) 保存
  |
  v
r-pdf-renderer
  |
  | (要修正) 日本語対応PDF生成
  v
sessions/{id}/reports/report.pdf (GCS) + reports.pdf_path 更新
  |
  v
r-gmail-sender
  |
  | (未実装) 実 Gmail API 呼び出し
  v
report_deliveries 保存
```

## 実装順

依存関係上、下から積み上げないと動かない。特に Step 1・2 が無い限り、post-session-job は
「常に空に近いレポート」しか作れない。

### Step 1: `r-writer` を Cloud Run にデプロイする

対象:

- `infra/environments/prod/main.tf`
- `.github/workflows/deploy-writer.yml`(新規、`deploy-media-api.yml` を雛形にする)
- `infra/environments/prod/variables.tf`(GCS JSONL バケット名の変数)

作業:

- writer 用の service account を作る(`roles/cloudsql.client` + JSONL バケットへの
  `roles/storage.objectAdmin`)。
- JSONL の保存先バケットを決める(後述「未決定事項」参照。まずは既存の
  `module.media_bucket` を再利用し、`sessions/{id}/mood-wave/...` 等のプレフィックスで
  同居させるのが最短)。
- `module.writer_service`(`cloud-run-service` モジュール流用)を追加。
  `env_vars` に `JSONL_STORE_BACKEND=gcs`, `GCS_JSONL_BUCKET`, `DATABASE_URL` を渡す。
  gateway と同様 Cloud SQL 接続が要るので `cloudsql_connection_names` を設定。
  常時ポーリングのワーカーなので `allow_unauthenticated` は不要(外部から呼ばれない)。
- `deploy-media-api.yml` を雛形に `deploy-media-api.yml` 同様のCloud Buildビルド +
  `terraform apply -var="writer_image_tag=..."` を行う `deploy-writer.yml` を作る。

完了条件:

- `gcloud run services list` に `r-writer` が出る。
- 実セッションを1本回した後、`trigger_events` / `feedback_events` / `transcripts` の
  Postgres テーブルと GCS の `sessions/{id}/mood-wave/*.jsonl` にデータが入っている。

### Step 2: `image-analysis-worker` を Cloud Run にデプロイする

対象:

- `infra/environments/prod/main.tf`
- `.github/workflows/deploy-image-analysis-worker.yml`(新規)

作業:

- Step 1 と同様の手順。`backend/internal/imageanalysis/worker.go` が
  `media_uploaded` イベント(`media-analysis-events` トピック相当)を購読して
  `SetBaselineReady` を呼ぶ構成なので、gateway 同様 Redis 接続(VPCコネクタ)が要る。
- service account に `roles/cloudsql.client` と media バケットの
  `roles/storage.objectViewer`(baseline frame を読むため)を付与。

完了条件:

- baseline frame アップロード後、`realtime: evidence pack` ログの
  `baseline_frames=` が 0 以外になる(リアルタイムFBの改善にも直結)。
- `participant_baselines` / `visual_summaries` テーブルに行ができる。

### Step 3: DB マイグレーション適用を確認する

対象:

- 本番 Cloud SQL インスタンス(`reaction-engine-db`)

作業:

- `000001`〜`000004` が prod に適用済みか確認する(未適用なら適用する)。
- マイグレーション実行の自動化手段が無い(`backend/scripts` は空)ので、少なくとも
  手順を `backend/migrations/README.md` 等に残す。CI/CDに組み込むかは別途判断。

完了条件:

- `reports` / `report_deliveries` / `trigger_events` / `participant_baselines` /
  `visual_summaries` の各テーブルが prod に存在する。

### Step 4: `infra/modules/cloud-run-job` を実装する

対象:

- `infra/modules/cloud-run-job/main.tf`(新規、現状 README のみ)
- `infra/modules/cloud-run-job/variables.tf` / `outputs.tf`

作業:

- `google_cloud_run_v2_job` リソースを持つモジュールを、`cloud-run-service` モジュールの
  形(project_id/location/image/service_account_email/env_vars/cloudsql_connection_names
  等)に寄せて作る。
- `post-session-job` / `pdf-renderer` / `gmail-sender` はそれぞれ `--session-id` を
  引数に取る CLI なので、Job の `template.template.containers[0].args` で渡せるように
  するか、環境変数 `SESSION_ID` を読むオプションを各 `main.go` に追加する(実行時に
  可変な引数を渡す一般的な方法は Jobs の実行時オーバーライド `gcloud run jobs execute
  --args` / Admin API の overrides なので、どちらの経路で起動するか Step 5 と合わせて
  決める)。

完了条件:

- `terraform plan` で3つの Cloud Run Job(`r-post-session-job` / `r-pdf-renderer` /
  `r-gmail-sender`)が作成される。

### Step 5: セッション終了検知 と Job 起動の仕組みを作る

対象:

- `extension/src/sidebar.js`(`stopCapture`)
- `backend/internal/gateway/gateway.go`
- `infra/environments/prod/main.tf`(gateway の service account に Job 起動権限を追加)

作業:

- `stopCapture()` から WebSocket 経由で明示的な `session_end` メッセージを送る
  (「未決定事項」参照。タブが閉じられる/画面共有が切れるケースも `stream.getVideoTracks()[0]
  'ended'` で `stopCapture` に来るので拾える)。
- gateway が `session_end` を受け取ったら、Cloud Run Admin API
  (`run.googleapis.com/v2/.../jobs/r-post-session-job:run`)を叩いて
  `session_id` をオーバーライド引数で渡す。pdf-renderer / gmail-sender も同様に
  連鎖起動する(post-session-job 完了を待ってから、が理想。素朴には3つを1つの
  Job/スクリプトにまとめて直列実行してもよい)。
- gateway の service account に、その3 Job に対する `roles/run.invoker` 相当
  (`run.jobs.run` を含むロール)を付与する。

完了条件:

- 実セッション終了後、人手を介さず `reports` に新しい行ができ、`report.pdf` が
  生成される。

### Step 6: `post-session-job` に実 LLM 呼び出しを実装する

対象:

- 新規 `backend/internal/postsession/vertex.go`(`internal/realtime/vertex.go` と
  同じ構造)
- `backend/cmd/post-session-job/main.go`
- `backend/internal/postsession/report.go`(`BuildReport` はフォールバック用に残す)

作業:

- architecture.md の `post_session_report` 入力例(`purpose`, `session`,
  `wave_overview`, `important_windows`, `realtime_feedback_history`,
  `baseline_context`)をそのまま Gemini に渡す adapter を作る。realtime 版で踏んだ
  地雷を踏襲する:
  - `thinkingConfig.thinkingBudget` を明示的に設定する(全体レポートは1.5秒制約が
    無いので thinking を有効にしてもよいが、`maxOutputTokens` を十分大きく取り、
    実際に空応答にならないか curl 等で確認してから固定する)。
  - プロンプトに「transcript / important_windows の内容を踏まえて具体的に書く」ことを
    明示する(realtime と同じく、指示しないと抽象的な文言に留まった経験がある)。
- LLM 呼び出し失敗時は `BuildReport` の決定論的な内容にフォールバックし、
  `Source` フィールドで区別する(`"post_session_llm"` / `"post_session_stub"`)。

完了条件:

- 実セッションのレポートで `wave_overview.overall` や `important_windows` の説明文が
  文字起こし内容に言及した自然な日本語になる。

### Step 7: PDF レンダラーの日本語(CJK)対応

対象:

- `backend/internal/pdf/pdf.go`

作業:

- 現状の「自前でPDFバイト列を組み立てる base14 Helvetica のみ」の実装では日本語が
  出せない。CJK 埋め込みフォント対応の Go PDF ライブラリ(例:
  `github.com/go-pdf/fpdf`、`github.com/signintech/gopdf` など、TTF埋め込みに対応する
  もの)に置き換えるか、Noto Sans JP 等のサブセットフォントを埋め込む形に拡張する。
- `sanitizeLine` の「非ASCIIを捨てる」ロジックを削除する。
- 依存追加や既存 `pdf_test.go` への影響があるため、既存の一枚もの実装を丸ごと置き換える
  前提でテストを書き直す。

完了条件:

- 実際にフィードバックメッセージ・transcript 抜粋を含む report.pdf を開いて、日本語が
  正しく表示される。

### Step 8: Gmail Sender の実装

対象:

- `backend/cmd/gmail-sender/main.go`
- 送信先メールアドレスの取得経路(extension 側 UI か、session metadata)

作業:

- 認証方式を決める(「未決定事項」参照)。
- 決めた方式で実際に PDF を添付して Gmail API 経由送信する処理に置き換える。
- 送信失敗時は `report_deliveries` に失敗ステータスを記録し、architecture.md の
  「PDF送信に失敗してもreport自体は保存済みとして扱う」方針を守る(リトライは
  別管理)。

完了条件:

- 実際に自分のGmail宛にレポートPDFが届く。

### Step 9: 各サービス/JobのCI/CDワークフローを整理する

対象:

- `.github/workflows/deploy-writer.yml`
- `.github/workflows/deploy-image-analysis-worker.yml`
- `.github/workflows/deploy-post-session-jobs.yml`(3 Job まとめてでも可)

作業:

- `deploy-media-api.yml` と同じパターンで、対象パスをそれぞれの
  `backend/internal/{writer,imageanalysis,postsession,pdf}/**` /
  `backend/cmd/{writer,image-analysis-worker,post-session-job,pdf-renderer,gmail-sender}/**`
  に絞って自動デプロイされるようにする。
- 今回の調査で「CI/CDが存在しないサービスは古いコードのまま放置される」ことが
  繰り返し問題になった(media-api がまさにそれだった)ので、新しいコンポーネントを
  追加するたびにワークフローも同時に作る運用にする。

完了条件:

- 対象パスへの push で該当サービス/Jobが自動的に再ビルド・再デプロイされる。

### Step 10: E2E 検証

作業:

- 実際にGoogle Meetセッションを最初から最後まで回す。
- Cloud Run ログ + Postgres + GCS で、writer の永続化 → post-session-job の
  レポート生成 → pdf-renderer → gmail-sender の一連が人手を介さず完走することを
  確認する。

## 未決定事項(先に決めたいこと)

1. **JSONLの保存先バケット**: 既存の `module.media_bucket`
   (`reaction-engine-501316-sessions`)を使い回すか、新しいバケットを切るか。
   architecture.md の例は同一バケット配下に `mood-wave/` `transcript/` `triggers/`
   `feedback/` `reports/` を並べているので、既存流用が最短。
2. **post-session Job の起動方式**: gateway から Cloud Run Admin API を直接叩く
   (Step 5 案)か、Pub/Sub + Eventarc を挟むか。前者はシンプルだが gateway に
   インフラ操作の責務が増える。後者は architecture.md の Pub/Sub 中心設計に忠実だが
   実装量が増える。MVPとしてはまず前者で通す想定。
3. **Gmail送信の認証方式**: (a) Workspaceドメイン全体委任のサービスアカウント、
   (b) セッション開始時にGoogleサインインしたユーザー自身の `gmail.send` スコープ
   トークンを使う、のどちらか。(b) は以前話した「アクセス制御用のGoogle認証と
   兼用できる」という利点があるが、センシティブスコープの審査が要る点に留意。
4. **送信先メールアドレスの入力経路**: 拡張機能側でセッション開始時に入力させるか、
   Google Sign-Inのメールアドレスをそのまま使うか。

## 優先順位

最優先は Step 1(`r-writer` デプロイ)。理由: これが無いと post-session-job が読む
データが本番に一切存在せず、以降の全ステップの検証ができない。

次に Step 4・5(Job化とトリガー)。データはあってもレポートを起動する経路が無ければ
Step 6以降を通しで試せない。

Step 6(LLM)・Step 7(PDF日本語化)・Step 8(Gmail)は互いに独立なので並行に進めても
よいが、**Step 7 は特に優先度を上げるべき**。日本語を全部捨てる現状のPDFのまま
Step 8 まで進めても、届くメールの添付が実質空のPDFになり、ユーザーにとって
意味のある成果物にならない。

## 参照箇所

- 全体設計: `architecture.md` の「全体FBレポートフロー」「Pub/Sub / Durable Writer」
- Durable Writer: `backend/cmd/writer/main.go`, `backend/internal/writer/`
- レポート組み立て: `backend/internal/postsession/report.go`
- PDF生成: `backend/internal/pdf/pdf.go`, `backend/cmd/pdf-renderer/main.go`
- Gmail送信: `backend/cmd/gmail-sender/main.go`
- Image Analysis Worker: `backend/internal/imageanalysis/worker.go`
- Cloud Run Job placeholder: `infra/modules/cloud-run-job/README.md`
- 既存デプロイワークフローの雛形: `.github/workflows/deploy-media-api.yml`
- realtime LLM 実装の先例(踏んだ地雷込み): `backend/internal/realtime/vertex.go`
