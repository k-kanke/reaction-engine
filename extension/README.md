# Reaction Engine Chrome Extension MVP

Google Meet 用の Chrome 拡張 MVP です。sidebar から画面キャプチャを開始し、ブラウザ内で簡易 Edge Vision Pipeline を動かして `realtime_feature` を生成します。

## できること

- Chrome side panel / sidebar の表示
- `getDisplayMedia` による Meet 画面キャプチャ
- preview canvas への表示
- MediaPipe Face Detector による顔 bbox 検出
- MediaPipe Face Landmarker による顔パーツ特徴量の抽出
- MediaPipe が初期化できない場合の native `FaceDetector` fallback
- 顔検出が使えない場合の motion score fallback
- `attention_score` の暫定算出
- 1秒ごとの `realtime_feature` 生成
- WebSocket URL が設定されている場合の event 送信
- Google Meet DOM からの参加者タイル候補抽出(`meet_tile_snapshot`)

## ロード手順

1. Chrome で `chrome://extensions` を開く。
2. 右上の Developer mode を有効にする。
3. Load unpacked を押す。
4. このリポジトリの `extension/` ディレクトリを選択する。
5. Google Meet を開く。
6. 拡張アイコンを押して side panel を開く。
7. `Start Capture` を押し、Google Meet のタブまたは画面を選択する。

拡張のコードを変更した後は、`chrome://extensions` で Reaction Engine MVP の Reload を押してから side panel を開き直します。Reload しないと古い manifest の CSP が残り、MediaPipe の WASM 初期化が失敗します。

## WebSocket

WebSocket URL は任意です。未設定でも sidebar 上で feature event を確認できます。

ローカルで受信ログを見たい場合は、リポジトリルートで次を実行します。

```sh
node scripts/ws-log-server.cjs
```

その後、sidebar の WebSocket URL に次を入れて `Connect` を押します。

```text
ws://localhost:8787/realtime
```

設定した場合、1秒ごとに以下のような JSON を送信します。

```json
{
  "type": "realtime_feature",
  "session_id": "sess_example",
  "t_ms": 1780000000000,
  "meeting_provider": "google_meet",
  "source": "chrome_side_panel",
  "features": {
    "face_visible": true,
    "face_count": 1,
    "face_tracks": [
      {
        "audience_id": "aud_1",
        "face_bbox": { "x": 0.12, "y": 0.2, "w": 0.1, "h": 0.18 },
        "eye_openness": { "left": 0.28, "right": 0.31 },
        "mouth_openness": 0.04,
        "head_pose_estimate": { "yaw": -0.08, "pitch": 0.02, "roll": 0.01 },
        "gaze_estimate": "screen",
        "landmark_count": 478,
        "gestures": {
          "nod_count": 1,
          "nod_score": 0.68
        }
      }
    ],
    "motion_score": 0.08,
    "attention_score": 0.58,
    "gaze_estimate": "screen",
    "gestures": {
      "nod_count": 1,
      "nod_score": 0.68
    },
    "client_model_version": {
      "face_detector": "mediapipe-blaze-face-short-range-v1",
      "face_landmarker": "mediapipe-face-landmarker-v1"
    }
  }
}
```

## Meet タイル抽出

Google Meet ページの content script が `MutationObserver` と定期スキャンで参加者タイル候補を抽出し、以下のような `meet_tile_snapshot` を side panel へ送ります。side panel の Feature Events ログで最新の snapshot を確認できます。

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

class name には依存せず、`role` / `aria-label` / 表示テキスト / `video` 要素との近傍関係 / `getBoundingClientRect()` を組み合わせた heuristic で候補を作っています。Meet の DOM 構造が変わると `participant_name` が取得できないことがありますが、その場合も `tile_bbox_viewport` のみの候補として扱われ、画面キャプチャの顔解析には影響しません。

## トラブルシューティング

### 顔が認識されない / `activeTexture` エラーが出る

Chrome のハードウェアアクセラレーションが無効だと、MediaPipe が内部で使用する WebGL コンテキストを作成できず、顔検出が動作しません。Feature Events に以下のようなエラーが繰り返し表示されます。

```
{"type":"face_landmarker_error","message":"Cannot read properties of undefined (reading 'activeTexture')"}
```

**対処方法:**

1. `chrome://settings/system` を開く。
2. **「ハードウェア アクセラレーションが使用可能な場合は使用する」** を ON にする。
3. Chrome を完全に再起動する（すべてのウィンドウを閉じて開き直す）。
4. `chrome://gpu` を開き、**WebGL** が `Hardware accelerated` になっていることを確認する。

## 注意

- MVP では本格的な視線推定や人物同一性 tracking は未実装です。
- MediaPipe runtime と face detector model は拡張内に同梱しています。
- MediaPipe が初期化できない環境では native `FaceDetector` を試し、それも使えない場合は motion score のみで動きます。
- MediaPipe が動いている場合は `edge_vision_status` に `MediaPipe FaceDetector enabled` が出ます。
- Face Landmarker が動いている場合は `edge_vision_status` に `MediaPipe FaceLandmarker enabled` が出ます。
- `head_pose_estimate` と `gaze_estimate` はランドマーク位置から計算した簡易推定です。精密な視線推定ではありません。
- `gestures.nod_count` と `gestures.nod_score` は直近約3.5秒の `head_pose_estimate.pitch` 変化から計算した簡易推定です。
- 次の段階ではタイル追跡、参加者名との紐づけ、音声特徴量を追加します。
