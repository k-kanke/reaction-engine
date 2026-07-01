# Reaction Engine Chrome Extension MVP

Google Meet 用の Chrome 拡張 MVP です。sidebar から画面キャプチャを開始し、ブラウザ内で簡易 Edge Vision Pipeline を動かして `realtime_feature` を生成します。

## できること

- Chrome side panel / sidebar の表示
- `getDisplayMedia` による Meet 画面キャプチャ
- preview canvas への表示
- `FaceDetector` が使える環境での顔 bbox 検出
- `FaceDetector` が使えない環境での motion score fallback
- `attention_score` の暫定算出
- 1秒ごとの `realtime_feature` 生成
- WebSocket URL が設定されている場合の event 送信

## ロード手順

1. Chrome で `chrome://extensions` を開く。
2. 右上の Developer mode を有効にする。
3. Load unpacked を押す。
4. このリポジトリの `extension/` ディレクトリを選択する。
5. Google Meet を開く。
6. 拡張アイコンを押して side panel を開く。
7. `Start Capture` を押し、Google Meet のタブまたは画面を選択する。

## WebSocket

WebSocket URL は任意です。未設定でも sidebar 上で feature event を確認できます。

ローカルで受信ログを見たい場合は、リポジトリルートで次を実行します。

```sh
node scripts/ws-log-server.js
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
        "face_bbox": { "x": 0.12, "y": 0.2, "w": 0.1, "h": 0.18 }
      }
    ],
    "motion_score": 0.08,
    "attention_score": 0.58,
    "gaze_estimate": "unknown",
    "client_model_version": "shape-detection-face-v1"
  }
}
```

## 注意

- MVP では本格的な視線推定や人物同一性 tracking は未実装です。
- `FaceDetector` は Chrome の環境や設定によって利用できない場合があります。その場合は motion score のみで動きます。
- 本番では MediaPipe Tasks Vision や ONNX Runtime Web に置き換え、顔ランドマーク、頭部姿勢、視線推定、タイル追跡を追加します。
