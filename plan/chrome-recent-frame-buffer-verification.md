# Chrome recent frame buffer verification

## Purpose

Chrome 拡張側で、直近の画像フレームを一時的に保持できるかを検証する。

想定する本番方針は以下。

- baseline frame: 最初に数枚だけ upload し、participant_baseline / baseline_visual_profile 作成に使う
- evidence frame: 常時 upload せず、Chrome 側の短期リングバッファに保持する
- evidence frame は time trigger または wave trigger が発火した時だけ upload する
- r-realtime-worker が正式な system_signal / reaction_wave を作り、発火条件と LLM 入力に使う

この検証ではサーバー upload まで行わず、Chrome 拡張内で「保存できる」「必要時に取り出せる」「メモリ上限を制御できる」ことを確認する。

## Hypothesis

Chrome 拡張の side panel は `getDisplayMedia -> video -> canvas` の経路を既に持っているため、canvas から WebP Blob を生成し、JavaScript のメモリ上に短時間リングバッファとして保持できる。

MV3 service worker は停止されやすいため、短期 evidence frame buffer は service worker ではなく、capture と同じ side panel document 側に置くのが妥当。

## Verification Scope

対象:

- `extension/src/sidebar.js`
- `extension/src/sidebar.html`
- `extension/src/sidebar.css`

検証すること:

- capture 中に一定間隔で WebP Blob を生成できる
- リングバッファが最大件数または最大保持秒数を超えたフレームを破棄できる
- trigger 相当の操作で、直近 N 秒の frame metadata と Blob を取り出せる
- Blob URL で画像を preview / download できる
- feature_snapshot と同じ `t_ms` 付近の frame を対応づけられる

検証しないこと:

- Cloud Storage upload
- Media API signed URL flow
- r-realtime-worker の system_signal / reaction_wave 生成
- LLM 入力の品質評価

## Proposed PoC

### Buffer settings

初期値:

- frame interval: 1000 ms
- retention window: 30 sec
- max frames: 30
- format: `image/webp`
- quality: 0.7
- width: 480 px

1枚あたり数十 KB 程度を想定する。30枚なら概ね数 MB 以内に収まる見込み。

### Frame record shape

```js
{
  id: "ef_1780000000000",
  t_ms: 1780000000000,
  width: 480,
  height: 270,
  mime: "image/webp",
  size_bytes: 42131,
  blob,
  feature_snapshot: {
    attention_score,
    motion_score,
    face_count,
    face_tracks
  }
}
```

`feature_snapshot` は本番の `capture_snapshots.feature_snapshot` とは別で、検証用の compact snapshot として扱う。

### UI

side panel に検証用セクションを追加する。

- buffer status: `frames / bytes / oldest age`
- `Capture Evidence` button: trigger 相当で直近数秒を選択する
- `Download Latest` button: 直近 frame を WebP として download する
- event log: `evidence_frame_buffered`, `evidence_frame_selected`, `evidence_frame_downloaded`

### Selection logic

time trigger / wave trigger の代替として、ボタン押下時点 `trigger_t_ms` を使う。

選択ルール:

- `trigger_t_ms - 5000 <= frame.t_ms <= trigger_t_ms`
- 最大 5 枚
- trigger に近い順

将来 r-realtime-worker から trigger event を受ける場合も、同じ `t_ms` 範囲選択を使える。

## Acceptance Criteria

- Google Meet または録画ファイル解析中に、buffer count が増える
- retention window を超えると古い frame が消える
- `Capture Evidence` で直近 frame の metadata が event log に出る
- `Download Latest` で WebP ファイルを保存でき、画像として開ける
- `npm run check` が通る
- capture 停止時に timer が止まり、Blob URL を使う場合は revoke される

## Risks

- Blob を長く保持するとメモリ使用量が増える
- side panel を閉じるとメモリ上の evidence frame は消える
- canvas に debug overlay を描いた後に保存すると、overlay 入り画像になる
- `toBlob` は非同期なので、capture 停止や video の無効化と競合する可能性がある

## Notes

本番では evidence frame を永続化せず、短期メモリ保持に限定するのがよい。baseline frame は既存方針どおり Media API の signed upload URL で保存し、evidence frame は trigger 発火後に必要な範囲だけ upload する。

overlay なしの evidence frame が必要な場合は、debug 描画済み preview canvas ではなく、source video から別 canvas に draw して WebP 化する。
