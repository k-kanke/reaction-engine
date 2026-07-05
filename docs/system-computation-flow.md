# システム演算フロー

このドキュメントは、Chrome 拡張から送られる特徴量ログをそのまま LLM に渡さず、システム側で圧縮・変化検出・基準値補正を行ってからリアルタイム FB と全体 FB の両方に利用するための設計メモ。

## 目的

システム演算層の目的は、raw feature log を LLM に大量投入しないこと。LLM には顔ランドマークや瞬間値の羅列ではなく、発表者に返すフィードバックを作るために必要な「観察結果」を渡す。

具体的には次の情報に変換する。

- 秒単位の安定した反応シグナル
- baseline との差分
- 変化点
- 変化の継続時間
- 何人に起きたか
- 該当する発話内容
- LLM に渡すための window evidence pack

## 前提

Chrome 拡張は Google Meet 上で参加者ごとの特徴量を取得し、`r-gateway` に送る。現時点の想定は 1fps から開始し、必要なら 2fps 程度まで上げる。

画像は毎回取得しない。画像は baseline 作成用に数枚だけ取得する。画像と、その時点の `feature_snapshot` を使って `participant_baseline` と `baseline_visual_profile` を作る。

現在の DB スキーマ上は `visual_summaries` という名前を使っているが、意味としては「リアルタイム時点の画像説明」ではなく「参加者の基準状態を説明する visual profile」。以降このドキュメントでは `baseline_visual_profile` と呼ぶ。

## コンポーネント責務

### r-gateway

Gateway は WebSocket の受信と中継を担当する。重い分析は持たない。

- Chrome から `realtime_feature` を受ける
- Chrome から `audio_chunk` を受ける
- Redis に raw feature window を保存する
- Redis に transcript window を保存する
- `feature-events` に raw feature / transcript を配送する
- Redis に新しい `feedback_candidate` があれば Chrome に返す

Gateway は LLM を同期的に呼ばない。リアルタイム FB の生成は `r-realtime-worker` が行う。

### r-realtime-worker

Realtime worker はシステム演算層の中心。

- Redis から raw feature window を読む
- 1秒ごとに `system_signal` を作る
- `system_signal` から `change_event` を作る
- 20秒から30秒ごとに `window_evidence_pack` を作る
- transcript window、participant baseline、baseline visual profile を合わせる
- LLM に `window_evidence_pack` を渡す
- `feedback_candidate` を Redis に保存する
- `system_signal`、`change_event`、`window_evidence_pack`、`feedback_candidate` を `feature-events` に流す

### r-writer

Writer は永続化担当。

- `feature-events` を読む
- Cloud Storage に JSONL を保存する
- Cloud SQL に検索・集計しやすい summary 系データを保存する

### r-post-session-job

Post-session job は全体 FB を作る。

- 保存済みの `system_signal`、`change_event`、`window_evidence_pack` を読む
- transcript、participant baseline、baseline visual profile を読む
- セッション全体の傾向を LLM またはルールでまとめる
- report を保存する

## 推奨周期

最初の推奨値:

| 処理 | 推奨周期 | 理由 |
| --- | --- | --- |
| Chrome feature 送信 | 1fps | MVP では十分。負荷と精度のバランスがよい |
| Chrome feature 送信の上限候補 | 2fps | 短い変化を拾いたい場合の上限候補 |
| Redis raw window 保存 | 受信ごと | 後段の演算が取りこぼさないようにする |
| system_signal 作成 | 1秒ごと | 発表中の反応変化として意味があり、LLM入力にも圧縮しやすい |
| change_event 検出 | 1秒ごと | system_signal 更新と同じタイミングでよい |
| LLM リアルタイム FB | 20秒から30秒ごと | 発話内容と反応変化を合わせた提案に必要な長さ |
| Redis raw feature 保持 | 60秒から120秒 | realtime worker の再処理と遅延吸収用 |
| Redis system_signal 保持 | セッション中または数時間 | Gateway 返却、再集計、障害時確認用 |

10秒未満の LLM FB は短すぎる可能性が高い。発話内容と反応の因果を作りにくい。まずは20秒または30秒を標準にする。

## データフロー

```mermaid
flowchart TD
  chrome["Chrome拡張"]
  gateway["r-gateway<br/>受信と中継"]
  redisRaw[("Redis<br/>raw feature window<br/>transcript window")]
  realtimeWorker["r-realtime-worker<br/>システム演算とLLM FB生成"]
  redisComputed[("Redis<br/>system_signal<br/>feedback_candidate")]
  pubsub["feature-events"]
  writer["r-writer"]
  storage[("Cloud Storage<br/>JSONL")]
  sql[("Cloud SQL<br/>summary tables")]
  postJob["r-post-session-job"]
  chromeFeedback["Chrome拡張<br/>feedback表示"]

  chrome -->|"realtime_feature と audio_chunk"| gateway
  gateway -->|"raw window 保存"| redisRaw
  gateway -->|"raw event 配送"| pubsub
  redisRaw -->|"直近 window 読み込み"| realtimeWorker
  realtimeWorker -->|"system_signal と feedback_candidate"| redisComputed
  realtimeWorker -->|"computed event 配送"| pubsub
  redisComputed -->|"latest feedback 読み込み"| gateway
  gateway -->|"feedback_candidate"| chromeFeedback
  pubsub --> writer
  writer --> storage
  writer --> sql
  storage --> postJob
  sql --> postJob
```

## 演算結果の3層

システム演算層は、次の3層のデータを作る。

### 1. system_signal

1秒単位の演算済みシグナル。raw feature を LLM に渡さないための最小単位。

例:

```json
{
  "type": "system_signal",
  "schema_version": 1,
  "session_id": "sess_123",
  "audience_id": "aud_1",
  "bucket_start_ms": 1783175200000,
  "bucket_end_ms": 1783175201000,
  "sample_count": 2,
  "attention": {
    "avg": 0.54,
    "min": 0.49,
    "max": 0.58,
    "delta_from_prev": -0.08,
    "delta_from_baseline": -0.16,
    "state": "down"
  },
  "gaze": {
    "screen_ratio": 0.5,
    "away_ratio": 0.5,
    "state": "unstable"
  },
  "face": {
    "visible_ratio": 1.0,
    "lost": false
  },
  "gesture": {
    "nod_count": 0,
    "nod_score_avg": 0.0
  },
  "motion": {
    "avg": 0.02,
    "state": "low"
  },
  "mouth": {
    "openness_avg": 0.01,
    "activity": "low"
  },
  "flags": [
    "attention_drop",
    "gaze_away"
  ]
}
```

### 2. change_event

意味のある変化点。LLM が「どこで反応が変わったか」を理解するための evidence。

例:

```json
{
  "type": "change_event",
  "schema_version": 1,
  "session_id": "sess_123",
  "audience_id": "aud_1",
  "event_type": "attention_drop",
  "start_ms": 1783175212000,
  "end_ms": 1783175219000,
  "duration_sec": 7,
  "severity": "medium",
  "baseline_delta": -0.18,
  "confidence": 0.72,
  "reason_codes": [
    "attention_below_baseline",
    "gaze_away_increased"
  ]
}
```

### 3. window_evidence_pack

LLM に渡す20秒から30秒単位の圧縮コンテキスト。リアルタイム FB と全体 FB の両方で使う。

例:

```json
{
  "type": "window_evidence_pack",
  "schema_version": 1,
  "session_id": "sess_123",
  "window_start_ms": 1783175200000,
  "window_end_ms": 1783175220000,
  "window_sec": 20,
  "speaker_transcript": "次に料金プランについて説明します。基本プランは月額...",
  "topic_hint": "料金プラン",
  "group_reaction": {
    "overall": "attention_declined",
    "participants_total": 12,
    "participants_affected": 4,
    "participants_recovered": 2
  },
  "notable_events": [
    {
      "event_type": "attention_drop",
      "timing_hint": "料金プランの説明開始直後",
      "audience_ids": ["aud_1", "aud_4", "aud_7"],
      "duration_sec": 8,
      "evidence": "baseline比で平均 -0.16"
    }
  ],
  "participant_context": [
    {
      "audience_id": "aud_1",
      "baseline_attention_avg": 0.68,
      "current_low": 0.49,
      "baseline_visual_profile": "通常時は画面注視が多く、表情変化は少なめ"
    }
  ]
}
```

## baseline と baseline_visual_profile

画像処理フローは、リアルタイム中に毎回画像を解析するためのものではない。最初の数枚の baseline frame と、その時点の `feature_snapshot` から、参加者ごとの基準を作る。

### participant_baseline

数値的な基準値。

例:

```json
{
  "attention_score_avg": 0.62,
  "attention_score_stddev": 0.08,
  "gaze_screen_ratio_avg": 0.78,
  "head_pose_pitch_avg": -0.08,
  "motion_avg": 0.03,
  "mouth_openness_avg": 0.01
}
```

### baseline_visual_profile

画像から得た、その参加者の通常状態の説明。DB 上は当面 `visual_summaries.visual_summary` に保存する。

例:

```json
{
  "description": "通常時は画面を見ており、表情変化は少なめ。軽いうなずきは少ないタイプ。",
  "baseline_expression": "neutral",
  "confidence": 0.72
}
```

リアルタイム FB と全体 FB では、画像本体ではなく `participant_baseline` と `baseline_visual_profile` を使う。

## 演算ルール

### attention

入力:

- `attention_score`
- `participant_baseline.attention_score_avg`
- 直前 bucket の `attention.avg`

計算:

- `avg`: bucket 内平均
- `min`: bucket 内最小
- `max`: bucket 内最大
- `delta_from_prev`: 現 bucket 平均 - 直前 bucket 平均
- `delta_from_baseline`: 現 bucket 平均 - baseline 平均

初期ルール:

- `delta_from_baseline <= -0.15` なら `attention_drop`
- `delta_from_prev <= -0.10` なら `attention_drop_candidate`
- `attention_drop` が3秒以上継続したら `change_event` に昇格
- baseline 未準備なら session default threshold を使うが、confidence を下げる

### gaze

入力:

- `gaze_estimate`
- `face_visible`

計算:

- `screen_ratio`: bucket 内で screen と判定された割合
- `away_ratio`: bucket 内で screen 以外の割合
- `state`: `stable` / `unstable` / `away`

初期ルール:

- `away_ratio >= 0.5` なら `gaze_away`
- `gaze_away` が3秒以上継続し、attention 低下もある場合は severity を上げる

### face

入力:

- `face_visible`
- `face_count`

計算:

- `visible_ratio`: bucket 内で顔が見えていた割合
- `lost`: `visible_ratio < 0.5`

初期ルール:

- face lost 中の attention 低下は confidence を下げる
- face lost 自体を発表内容への反応低下として強く扱わない

### gesture

入力:

- `nod_count`
- `nod_score`

計算:

- `nod_count`: bucket 内合計
- `nod_score_avg`: bucket 内平均

初期ルール:

- nod 増加は positive signal として扱う
- nod が少ないこと単体では negative と判定しない

### motion

入力:

- `motion_score`

計算:

- `avg`: bucket 内平均
- `state`: `low` / `normal` / `high`

初期ルール:

- high motion はノイズの可能性があるため、attention や gaze の confidence 補正に使う
- motion 単体では FB の主根拠にしない

### mouth

入力:

- `mouth_openness`

計算:

- `openness_avg`
- `activity`: `low` / `normal` / `high`

初期ルール:

- audience 側の mouth activity は発話や笑いの可能性があるが、初期版では補助情報に留める

## LLM に渡す情報

LLM には raw feature log を渡さない。渡すのは次の情報。

- 直近20秒から30秒の transcript
- window evidence pack
- group reaction summary
- notable change events
- audience ごとの baseline 差分
- baseline visual profile の短い説明

LLM に渡さない情報:

- face landmark 座標
- bbox の全履歴
- eye / nose / mouth の point 配列
- raw feature の全サンプル
- baseline frame の画像本体

## リアルタイム FB の作り方

LLM には次のような役割を持たせる。

- 数値の再計算をさせない
- 変化イベントを発話内容と結びつける
- 発表者向けの短い提案に変換する
- 断定を避ける

出力例:

```json
{
  "type": "feedback_candidate",
  "session_id": "sess_123",
  "window_start_ms": 1783175200000,
  "window_end_ms": 1783175220000,
  "message": "料金プランの説明に入った直後、複数人の反応が基準より少し下がっています。価格の根拠や具体例を一つ補足するとよさそうです。",
  "severity": "suggestion",
  "confidence": 0.74,
  "reason_codes": [
    "group_attention_declined",
    "pricing_topic_detected"
  ]
}
```

## 保存方針

### Redis

Redis はリアルタイム参照用。

```text
features:recent:{session_id}:{audience_id}
transcript:recent:{session_id}:{speaker}
system_signal:recent:{session_id}:{audience_id}
change_events:recent:{session_id}
feedback:latest:{session_id}
feedback:history:{session_id}
session:baseline:{session_id}:{audience_id}
session:visual_summary:{session_id}:{audience_id}
session:baseline_status:{session_id}:{audience_id}
```

### Pub/Sub feature-events

`feature-events` は永続化と全体 FB 用の配送路。

流すもの:

- raw compact feature
- transcript_chunk
- system_signal
- change_event
- window_evidence_pack
- feedback_candidate
- decision_log

### Cloud Storage

Cloud Storage は高頻度 JSONL の保存先。

- raw compact feature JSONL
- system_signal JSONL
- change_event JSONL
- window_evidence_pack JSONL
- transcript JSONL

### Cloud SQL

Cloud SQL は検索・集計・レポート生成に必要な構造化データを保存する。

- sessions
- participants
- participant_baselines
- visual_summaries
- signal_summaries
- decision_logs
- transcripts
- feedback_events
- reports

## 設計上の判断

### システム演算は一度だけ行う

リアルタイム FB 用と全体 FB 用に別々に演算しない。`r-realtime-worker` が一度だけ `system_signal`、`change_event`、`window_evidence_pack` を作り、Redis と `feature-events` の両方に流す。

### LLM は外部分析器として扱う

Gateway は LLM を同期的に待たない。LLM 処理は `r-realtime-worker` が20秒から30秒ごとに行い、結果を Redis に置く。Gateway は新しい `feedback_candidate` があれば Chrome に返す。

### 画像は毎回使わない

画像は baseline 作成用。リアルタイム FB と全体 FB では、画像本体ではなく `participant_baseline` と `baseline_visual_profile` を判断基準として使う。

### raw feature は監査と再解析用に残す

通常の LLM 入力には raw feature を使わない。ただし、後から演算ルールを変更したい場合やバグ調査に備えて、compact raw feature JSONL は保存する。

