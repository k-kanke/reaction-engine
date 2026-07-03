# Reaction Engine 実装 TODO

この TODO は、AI agent が次に実装へ入れる粒度で未実装項目を整理したものです。
現状の Chrome Extension MVP は、side panel から画面キャプチャを開始し、MediaPipe / fallback で顔・ランドマーク・motion・簡易 attention を算出し、`realtime_feature` を sidebar log または WebSocket に出すところまで実装済み。

## 現状実装済みの範囲

- MV3 Chrome extension の骨格
  - `extension/manifest.json`
  - `extension/src/service-worker.js`
  - `extension/src/content-script.js`
  - `extension/src/sidebar.html`
  - `extension/src/sidebar.css`
  - `extension/src/sidebar.js`
- Chrome side panel UI
  - session id 表示
  - WebSocket URL 入力・保存
  - Connect / Disconnect
  - Start Capture / Stop
  - preview canvas
  - feature event log
- `navigator.mediaDevices.getDisplayMedia` による画面キャプチャ
- canvas への定期描画
- MediaPipe Face Detector による face bbox 検出
- MediaPipe Face Landmarker による目・口・鼻 landmark 特徴量
- native `FaceDetector` fallback
- motion-only fallback
- 簡易 `head_pose_estimate`
- 簡易 `gaze_estimate`
- 簡易 `attention_score`
- 簡易 nod gesture 検出
- bbox 中心距離ベースの一時的な `aud_N` tracking
- 1秒ごとの `realtime_feature` 生成
- WebSocket 接続中の event 送信
- ローカル WebSocket ログサーバー
  - `scripts/ws-log-server.cjs`

## P0: 次に実装するべきもの

### 1. Google Meet タイル情報の抽出

目的:
Google Meet の DOM から参加者タイル候補を抽出し、画面上の tile bbox と参加者名候補を取得できるようにする。

背景:
現状は画面キャプチャ上の顔 bbox しかなく、誰の反応か、どの Meet タイルに属する顔かが分からない。

触るファイル:
- `extension/src/content-script.js`
- 必要なら `extension/src/sidebar.js`

実装方針:
- `content-script.js` に Meet DOM の観測処理を追加する。
- `MutationObserver` でタイル候補の変化を監視する。
- まずは壊れやすい class name 依存を避け、以下を組み合わせて候補を作る。
  - `role`
  - `aria-label`
  - 表示テキスト
  - video 要素の近傍
  - `getBoundingClientRect()`
- 抽出結果を `chrome.runtime.onMessage` / `chrome.tabs.sendMessage` 経由で sidebar へ渡せる形にする。
- MVP では完全な精度を求めず、debug log に tile candidates を出せればよい。

出力データ案:

```json
{
  "type": "meet_tile_snapshot",
  "t_ms": 1780000000000,
  "tiles": [
    {
      "tile_id": "tile_1",
      "participant_name": "山田 太郎",
      "tile_bbox_viewport": { "x": 100, "y": 120, "w": 320, "h": 180 },
      "is_speaking_candidate": false,
      "source": "meet_dom"
    }
  ]
}
```

受け入れ条件:
- Meet ページで content script が tile candidate snapshot を作れる。
- sidebar または event log で最新 tile snapshot を確認できる。
- DOM 取得に失敗しても face analysis は止まらない。
- `npm run check` が通る。

### 2. face bbox と Meet tile bbox の対応づけ

目的:
検出した顔がどの Meet タイルに属するかを推定し、`realtime_feature.features.face_tracks[]` に `tile_id` と `participant_name` を追加する。

背景:
現状の `aud_N` は bbox 中心距離だけで付与される一時 ID。Meet のタイル移動や再検出で意味が変わる。

触るファイル:
- `extension/src/sidebar.js`
- `extension/src/content-script.js`

実装方針:
- content script から受け取った `tile_bbox_viewport` を、capture preview canvas 座標系へ変換する。
- 変換が難しい場合、まずは同じ viewport / capture 対象が Meet tab である前提の近似でよい。
- face bbox の中心点が tile bbox 内にあるか、または重なり率が最大の tile を選ぶ。
- 対応できない場合は現状通り `aud_N` のみを使う。
- `face_tracks[]` に以下を追加する。
  - `tile_id`
  - `participant_name`
  - `match_confidence`

受け入れ条件:
- `realtime_feature` に tile / participant 情報が含まれる。
- tile と対応できない顔も送信対象から落とさない。
- 複数人が映る画面で、少なくとも bbox の近い tile に紐づく。
- `npm run check` が通る。

### 3. 音声特徴量の MVP

目的:
capture stream の audio track から、音量・無音・発話らしさを `realtime_feature` に追加する。

背景:
`getDisplayMedia` では `audio: true` を要求しているが、現状 audio track は解析されていない。

触るファイル:
- `extension/src/sidebar.js`
- 必要なら `extension/src/sidebar.html`

実装方針:
- `AudioContext` + `MediaStreamAudioSourceNode` + `AnalyserNode` を使う。
- 250ms から 1000ms 程度で RMS volume を算出する。
- 直近 window から以下を作る。
  - `audio_level`
  - `silence_ms`
  - `is_audio_active`
- 発話者推定までは P0 では不要。
- Stop Capture 時に `AudioContext` と node を必ず破棄する。

出力データ案:

```json
{
  "audio_level": 0.23,
  "silence_ms": 1200,
  "is_audio_active": true
}
```

受け入れ条件:
- audio が許可された capture で `features.audio_level` が変化する。
- audio がない環境でもエラーで止まらず `audio_level: null` などで表現する。
- Stop Capture 後に audio processing が残らない。
- `npm run check` が通る。

## P1: MVP の精度・運用性を上げるもの

### 4. tracking の安定化

目的:
一時的な bbox 消失、タイル移動、顔検出の揺れに対して `audience_id` を安定させる。

触るファイル:
- `extension/src/sidebar.js`

実装方針:
- 現状の `findClosestTrack()` を拡張する。
- 距離だけでなく bbox サイズ差、tile_id 一致、participant_name 一致を score に入れる。
- track に以下の状態を持たせる。
  - `created_at_ms`
  - `last_seen_ms`
  - `miss_count`
  - `tile_id`
  - `participant_name`
- `last_seen_ms` から短時間だけ track を保持する。

受け入れ条件:
- 同じ tile / participant の顔は再検出後も同じ `audience_id` になりやすい。
- 別 tile の顔と ID が入れ替わりにくい。
- `npm run check` が通る。

### 5. debug overlay の実表示

目的:
Meet 画面上に tile bbox / participant name / match status を重ねて確認できるようにする。

現状:
`content-script.js` は overlay root を作るだけで、中身は描画していない。

触るファイル:
- `extension/src/content-script.js`
- `extension/src/sidebar.js`
- 必要なら `extension/src/sidebar.html`

実装方針:
- sidebar に debug overlay の ON/OFF toggle を追加する。
- content script に tile bbox 用の absolutely positioned div を描画する。
- overlay は `pointer-events: none` のままにする。
- 更新頻度は 1秒程度でよい。

受け入れ条件:
- sidebar から overlay を ON/OFF できる。
- Meet 画面上に tile bbox と participant name が表示される。
- OFF にすると DOM が非表示または削除される。
- `npm run check` が通る。

### 6. feedback_event の UI 表示

目的:
WebSocket で受け取った `feedback_event` を log だけでなく、発表者向けの短いフィードバックとして見せる。

触るファイル:
- `extension/src/sidebar.html`
- `extension/src/sidebar.css`
- `extension/src/sidebar.js`

実装方針:
- sidebar 上部に最新 feedback 表示領域を追加する。
- WebSocket message を parse し、以下のような payload を受け付ける。

```json
{
  "type": "feedback_event",
  "severity": "info",
  "message": "反応が少し落ちています。問いかけを入れてください。",
  "cooldown_ms": 10000
}
```

受け入れ条件:
- `feedback_event.message` が sidebar に表示される。
- 古い feedback は一定時間で消える、または次の feedback で置き換わる。
- 不正 JSON を受けても落ちない。
- `npm run check` が通る。

## P2: バックエンド・分析基盤

### 7. Cloud Run Realtime Gateway + Memorystore / Pub/Sub 経路の最小実装

目的:
現在のログサーバーを、`realtime_feature` を受け取り、Memorystore for Redis の recent state と Pub/Sub に保存し、簡単な `feedback_event` を返せる gateway に発展させる。

触るファイル:
- `scripts/ws-log-server.cjs`
- 必要なら `package.json`

実装方針:
- Redis client と Pub/Sub publisher を追加する。MVP では Redis / Pub/Sub が未設定なら in-memory fallback でもよい。
- 受信した feature に `event_id` と `server_received_at_ms` を付ける。
- `features:recent:{session_id}` に compact payload を `ZADD` する。
- `session:state:{session_id}` に latest feature を `HSET` する。
- Pub/Sub topic `feature-events` に full payload を publish する。
- Redis recent window は `ZREMRANGEBYSCORE` と `EXPIRE` で保持量を制御する。
- 直近 10秒程度の window で簡単な rule を作る。
  - face_count が 0 に近い
  - attention_score が低い
  - motion_score が極端に低い
- 条件に合えば `feedback_event` を同じ WebSocket に返す。

受け入れ条件:
- `node scripts/ws-log-server.cjs` で起動できる。
- extension から feature を送ると console に記録される。
- Redis / Pub/Sub が設定されている場合、recent ZSET / state HASH / Pub/Sub topic に event が保存される。
- Redis / Pub/Sub が未設定または接続失敗の場合でも、ローカル開発用の in-memory 処理で最低限動く。
- 条件に応じて sidebar が feedback を受け取れる。
- `npm run check` が通る。

### 8. Cloud Run Durable Writer の最小実装

目的:
Pub/Sub に publish された `realtime_feature` を読み、後分析用の raw feature JSONL と summary を保存する。

触るファイル:
- 追加候補: `scripts/durable-writer.cjs`
- 必要なら `package.json`

実装方針:
- Pub/Sub subscription `feature-events-durable-writer` を作成する。
- Pub/Sub から event を pull または push で受け取る。
- session_id ごとに group し、まずはローカルファイルまたは将来の Cloud Storage 相当へ JSONL chunk として保存する。
- 保存成功後に Pub/Sub message を ack する。
- 失敗時は retry し、繰り返し失敗する message は dead-letter topic に送れる設計にする。
- `event_id` で重複処理を許容できるようにする。

受け入れ条件:
- Pub/Sub に入った event を writer が読み取れる。
- JSONL として raw event が保存される。
- 保存成功した event は ack される。
- writer が途中で落ちても Pub/Sub retry で再処理できる設計メモまたは実装がある。
- `npm run check` が通る。

### 9. Session / Ingest API の設計と stub

目的:
将来のバックエンド実装に向けて、session lifecycle と ingest payload の contract を固定する。

追加候補:
- `plan/api-contract.md`
- `plan/event-schema.md`

書く内容:
- `POST /sessions`
- `POST /sessions/:session_id/events`
- `POST /sessions/:session_id/end`
- `GET /sessions/:session_id/report`
- `realtime_feature` schema
- `feedback_event` schema
- Memorystore key design
- Pub/Sub event envelope
- Cloud Run Durable Writer の保存先と ack/retry/dead-letter 方針
- privacy / retention 前提

受け入れ条件:
- Chrome extension の event payload と矛盾しない schema がある。
- 画像本体を常時送らない方針が明記されている。
- リアルタイム判定用 Memorystore state と後分析用 raw JSONL の役割が分かれている。

### 10. セッション後分析の設計

目的:
発話内容、反応時系列、改善点を統合する後処理 pipeline を設計する。

追加候補:
- `plan/post-session-analysis.md`

書く内容:
- transcript chunk の扱い
- reaction timeline の集計
- reaction drop 区間の検出
- LLM report prompt の入力形式
- report JSON schema
- evidence の持たせ方

受け入れ条件:
- async worker が何を入力し何を出力するか分かる。
- report UI 実装前に必要な schema が揃う。

## P3: 品質・検証

### 11. feature 生成ロジックの単体テスト

目的:
`attention_score`、tracking、gesture detection、event payload の regression を防ぐ。

触るファイル:
- `extension/src/sidebar.js`
- `package.json`
- 追加候補: `extension/src/sidebar.test.js`

実装方針:
- まずは純粋関数を切り出す。
  - `calculateMotionScore`
  - `estimateGazeFromHeadPose`
  - `detectNodGesture`
  - `buildFeatures`
  - tracking score
- Node で実行しやすい test runner を選ぶ。
- DOM / Chrome API 依存部分とは分ける。

受け入れ条件:
- `npm test` で主要ロジックのテストが走る。
- `npm run check` と `npm test` が通る。

### 12. 手動 QA 手順の明文化

目的:
Chrome extension は自動テストだけで検証しづらいため、手動確認手順を固定する。

追加候補:
- `plan/manual-qa.md`

書く内容:
- extension reload 手順
- Meet tab capture 手順
- MediaPipe 有効確認
- fallback 確認
- WebSocket 送受信確認
- audio feature 確認
- tile / participant match 確認

受け入れ条件:
- 新しい agent / developer が手順通りに確認できる。
- 期待される log message と event shape が書かれている。

## 注意点

- Google Meet の DOM は変更されやすい。class name だけに依存しない。
- 画像本体を常時 WebSocket 送信する設計にはしない。主経路は特徴量 event。
- MediaPipe が使えない環境でも motion-only fallback を壊さない。
- Stop Capture 時は stream track、timer、audio context、状態を確実に解放する。
- 既存の `realtime_feature` shape を変える場合は `extension/README.md` のサンプルも更新する。
- 実装後は最低限 `npm run check` を実行する。
