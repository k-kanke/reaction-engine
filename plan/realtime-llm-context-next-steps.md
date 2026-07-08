# Realtime LLM context next steps

## 目的

リアルタイムFBフローを、現在の `mood_wave` 中心の通信から、設計している「直近30秒の mood_wave + 音声 transcript + trigger evidence frame + baseline frame」を LLM 入力にする形へ進める。

この文書は、2026-07-08 時点の実装確認結果に基づく次の実装順をまとめる。

## 現状

### できている

- Chrome は `mood_wave_sample` を Gateway WebSocket に送信している。
- Gateway は `mood_wave_sample` を Redis の recent window に保存している。
- `mood_wave_sample.trigger` がある場合、Gateway は realtime 処理へ渡している。
- Realtime 処理は Redis から直近30秒の mood_wave window を再構成している。
- Realtime 処理は transcript window / baseline frame refs / evidence frame refs を含む `EvidencePack` を組み立てる形になっている。
- `ENABLE_REAL_LLM=true` の場合、Gateway は Vertex AI / Gemini adapter を使って `EvidencePack` を LLM に渡す。
- baseline frame は Chrome から Media API に upload する経路がある。
- trigger 時の feedback は Chrome に返って表示されるところまで確認済み。

### まだできていない

- Chrome は trigger 時の evidence frame を Media API に upload していない。
- Chrome は `mood_wave_sample.evidence_frame` を同梱していない。
- Chrome は `audio_chunk` を Gateway に送っていない。
- Gateway 側の transcript 生成は本物の Speech-to-Text ではなく、5秒ごとの stub text である。
- そのため、現在の realtime LLM context は実質 `mood_wave` と baseline refs 中心で、音声 script と trigger 画像は空になる可能性が高い。

## 目標フロー

```text
Chrome extension
  |
  | 1Hz mood_wave_sample
  v
Gateway / r-gateway
  |
  | Redis に mood_wave recent window を保存
  v
Realtime context builder
  |
  | trigger 発火時
  | - 直近30秒 mood_wave
  | - 直近30秒 transcript
  | - baseline frame media_ref
  | - evidence frame media_ref
  v
Vertex AI / Gemini
  |
  | feedback_event
  v
Chrome extension
```

trigger 時の画像は次の流れにする。

```text
Chrome trigger
  |
  | Media API へ upload-url を要求
  | upload_url / media_ref を受け取る
  v
Chrome
  |
  | 画像 PUT は待たずに並行実行
  | 同じ trigger の mood_wave_sample に evidence_frame(media_ref) を同梱
  v
Gateway / Realtime
  |
  | media_ref を LLM context に入れる
  v
Vertex AI / Gemini
```

## 実装順

### Step 1: evidence frame upload を Chrome に接続する

対象:

- `extension/src/sidebar.js`
- `backend/internal/media/server.go`
- `backend/internal/contract/mood_wave.go`

作業:

- Chrome の `captureMoment` で選択済みの snapshot Blob を使う。
- trigger 発火時に Media API へ `purpose: "evidence_frame"` で upload URL を要求する。
- `trigger_id` / `capture_id` / `media_ref` を取得する。
- 画像 PUT は非同期で開始する。
- 次の `mood_wave_sample` に `trigger` と `evidence_frame` を同梱する。
- `evidence_frame.upload_status` は `"uploading"` とする。

完了条件:

- trigger 発火時の `mood_wave_sample` に `evidence_frame.media_ref` が入る。
- Media API 側に evidence frame の `media_refs` / `capture_snapshots` が作成される。
- Vertex adapter の `fileData.fileUri` に `gs://...` が渡る。

### Step 2: evidence frame upload 完了通知を整理する

対象:

- `extension/src/sidebar.js`
- `backend/internal/media/server.go`
- `backend/internal/gateway/gateway.go`

作業:

- Chrome が signed URL へ PUT 完了後、Media API の complete endpoint を呼ぶ。
- 必要なら WebSocket の `evidence_upload_complete` 受信も Gateway に追加する。
- Realtime LLM は upload 完了を待たず、`media_ref` を持っていれば context に入れる方針を維持する。

完了条件:

- upload 成功後に `media_refs.upload_status` が uploaded 相当になる。
- upload 中でも feedback flow は止まらない。
- upload 失敗時はログに残るが、mood_wave だけで rule fallback / LLM fallback が継続する。

### Step 3: Chrome から audio_chunk を送る

対象:

- `extension/src/sidebar.js`
- `backend/internal/contract/audio.go`
- `backend/internal/gateway/gateway.go`

作業:

- 既存の mic/self と tab/other の audio stream から PCM chunk を作る。
- WebSocket に `audio_chunk` を送る。
- speaker は `"self"` / `"other"` を使う。
- まずは低頻度・短時間 chunk でよい。最初の目的は transcript window にデータが入ることの確認。

完了条件:

- Gateway の `handleAudioChunk` が呼ばれる。
- Redis の `transcript:recent:{session_id}:{speaker}` に transcript chunk が入る。
- trigger 時の `EvidencePack.transcript_window` が空ではなくなる。

### Step 4: STT stub を本物の Speech-to-Text に差し替える

対象:

- `backend/internal/gateway/gateway.go`
- 新規 `backend/internal/speech/` など
- infra env vars

作業:

- Gateway が connection ごとに speaker 別の STT stream を維持する。
- `audio_chunk` の PCM を STT stream に流す。
- final transcript を `TranscriptChunk` として Redis に保存する。
- Durable Writer 経由で Cloud SQL `transcripts` に保存する。

完了条件:

- `transcript_window` に実際の発話テキストが入る。
- LLM の `evidence_quote` が transcript 由来になる。
- STT が失敗しても mood_wave / image のみで feedback が継続する。

### Step 5: LLM context のログと検証を追加する

対象:

- `backend/internal/realtime/realtime.go`
- `backend/internal/realtime/vertex.go`

作業:

- trigger ごとに、LLM に渡す `EvidencePack` の要約ログを出す。
- full payload は大きくなりうるため、通常ログでは件数と `media_ref` のみ出す。
- 必要に応じて debug flag で JSON を出せるようにする。

完了条件:

- Cloud Run logs で、trigger 時に以下が確認できる。
  - mood_wave point count
  - transcript chunk count
  - baseline frame count
  - evidence frame count
  - Vertex response / fallback reason

## 優先順位

最優先は Step 1。

理由:

- 既に Chrome は trigger 時点の snapshot を UI 表示できている。
- Media API と Vertex の画像入力の枠もある。
- evidence frame の接続は、音声 STT より小さな変更で LLM context の情報量を大きく増やせる。

次に Step 3。

理由:

- Gateway 側には `audio_chunk` の受け口が既にある。
- 最初は stub transcript でも、realtime context に transcript window を含める流れを検証できる。

本物の STT は Step 4 として後続に回す。

## 注意点

- Chrome は画像 upload 完了を待ってから `mood_wave_sample` を送らない。
- ただし `upload_url` / `media_ref` の発行は待つ必要がある。
- LLM は `media_ref` を context に含めるが、画像 object がまだ読めない場合は Vertex 呼び出しが失敗する可能性がある。
- その場合は rule fallback で feedback を返す。
- evidence frame は Image Analysis Worker に通さない。baseline 更新に使うのは baseline frame のみ。

## 参照箇所

- LLM context 作成: `backend/internal/realtime/realtime.go`
- Vertex 呼び出し: `backend/internal/realtime/vertex.go`
- Gateway WebSocket: `backend/internal/gateway/gateway.go`
- mood wave contract: `backend/internal/contract/mood_wave.go`
- audio contract: `backend/internal/contract/audio.go`
- Chrome mood wave 送信: `extension/src/sidebar.js`
- Chrome trigger snapshot UI: `extension/src/sidebar.js`
