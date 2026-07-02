# Chrome Extension MVP 実装ステップ

## Goal

Google Meet を対象に、Chrome の sidebar から画面キャプチャを開始し、ブラウザ内で簡易 Edge Vision Pipeline を動かし、数秒間隔で `realtime_feature` を生成できる状態にする。

この MVP ではバックエンドは必須にしない。WebSocket URL が設定されている場合だけ feature event を送信し、未設定の場合は sidebar 内のログで確認する。

## Step

1. **拡張の骨格を作る**
   - MV3 `manifest.json`
   - `service_worker`
   - Google Meet 用 `content_script`
   - `side_panel`

2. **sidebar UI を作る**
   - session id 表示
   - WebSocket URL 入力
   - capture start / stop
   - status 表示
   - preview canvas
   - feature event log

3. **画面キャプチャを実装する**
   - sidebar から `navigator.mediaDevices.getDisplayMedia` を呼ぶ
   - Google Meet のタブ/画面をユーザーに選択してもらう
   - hidden video に stream を流す
   - canvas に定期描画する

4. **Edge Vision Pipeline の MVP を実装する**
   - MediaPipe Face Detector で顔 bbox を検出
   - MediaPipe Face Landmarker で目・口・鼻の landmark 特徴量を抽出
   - MediaPipe が利用不可なら native `FaceDetector` を試す
   - どちらも利用不可なら frame differencing で motion score を算出
   - `attention_score` は face visibility + motion から暫定算出
   - preview canvas に debug bbox を描画

5. **Realtime Feature Event を作る**
   - 1秒間隔で最新特徴量を集計
   - `realtime_feature` JSON を生成
   - sidebar log に出す
   - WebSocket 接続中なら送信する

6. **Google Meet 側 content script を作る**
   - Meet ページで拡張が有効なことを検出
   - 必要に応じて debug overlay を作れる入口を用意
   - MVP では sidebar 主体で、Meet 画面への常時表示はしない

7. **ロード手順を書く**
   - `chrome://extensions`
   - Developer mode
   - `extension/` を Load unpacked
   - Google Meet を開く
   - 拡張アイコンから side panel を開く
