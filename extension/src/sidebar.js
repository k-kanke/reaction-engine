import {
  FaceDetector as MediaPipeFaceDetector,
  FaceLandmarker,
  FilesetResolver
} from "../vendor/mediapipe/vision_bundle.mjs";
import { analyzeVideoWithGemini } from "./gemini.js";

const ANALYSIS_INTERVAL_MS = 250;
const EVENT_INTERVAL_MS = 1000;
let PREVIEW_WIDTH = 640;
let PREVIEW_HEIGHT = 360;
const MEDIAPIPE_MODEL_PATH = "models/blaze_face_short_range.tflite";
const MEDIAPIPE_LANDMARKER_MODEL_PATH = "models/face_landmarker.task";
const GESTURE_HISTORY_MS = 3500;
const NOD_MIN_PITCH_DELTA = 0.04; // 実測: 通常の頷きのピッチ振幅は0.05〜0.08程度
const NOD_MIN_PHASE_MS = 120;

const elements = {
  statusBadge: document.getElementById("statusBadge"),
  wsUrl: document.getElementById("wsUrl"),
  connectButton: document.getElementById("connectButton"),
  sessionId: document.getElementById("sessionId"),
  faceCount: document.getElementById("faceCount"),
  motionScore: document.getElementById("motionScore"),
  attentionScore: document.getElementById("attentionScore"),
  preview: document.getElementById("preview"),
  sourceVideo: document.getElementById("sourceVideo"),
  startButton: document.getElementById("startButton"),
  stopButton: document.getElementById("stopButton"),
  micPermButton: document.getElementById("micPermButton"),
  eventLog: document.getElementById("eventLog"),
  exportButton: document.getElementById("exportButton"),
  geminiApiKey: document.getElementById("geminiApiKey"),
  saveApiKeyButton: document.getElementById("saveApiKeyButton"),
  geminiAnalyzeButton: document.getElementById("geminiAnalyzeButton"),
  geminiStatus: document.getElementById("geminiStatus"),
  geminiResult: document.getElementById("geminiResult"),
  videoFile: document.getElementById("videoFile"),
  uploadPlayButton: document.getElementById("uploadPlayButton"),
  uploadStopButton: document.getElementById("uploadStopButton"),
  uploadControls: document.querySelector(".upload-controls"),
  unifiedReportButton: document.getElementById("unifiedReportButton"),
  unifiedReportStatus: document.getElementById("unifiedReportStatus"),
  unifiedReport: document.getElementById("unifiedReport"),
  moodWave: document.getElementById("moodWave"),
  momentsGrid: document.getElementById("momentsGrid"),
  momentsEmpty: document.getElementById("momentsEmpty")
};

const canvas = elements.preview;
const ctx = canvas.getContext("2d", { willReadFrequently: true });

let sessionId = createSessionId();
let stream = null;
let analysisTimer = null;
let eventTimer = null;
let previousFrame = null;
let analysisRunning = false;
let latestFeatures = createEmptyFeatures();
let ws = null;
let faceDetectorBackend = null;
let faceLandmarkerBackend = null;
let tracks = [];
let trackHistory = new Map();
let nextTrackId = 1;
let latestTileSnapshot = null;

// --- 音声VAD (§4-1): ローカルのみ。相手=タブ音声, 自分=マイク(AEC) ---
const VAD_INTERVAL_MS = 100; // VADサンプリング間隔
const VAD_RMS_THRESHOLD = 0.02; // 発話判定のRMS閾値（仮値・後で実データ調整）
const SPEECH_WINDOW_MS = 5000; // speech_ratio を出す移動窓
// --- 代表フレーム取得 (§2.2 LLM定期パス用): 最初5分・30秒ごと ---
const FRAME_CAPTURE_INTERVAL_MS = 30000; // 30秒ごと
const FRAME_CAPTURE_DURATION_MS = 5 * 60 * 1000; // 最初の5分だけ
const FRAME_CAPTURE_WIDTH = 480; // 縮小送信（プライバシー/帯域配慮）
let frameTimer = null;
let frameCanvas = null;
let captureStartTs = 0;
// --- baseline frame upload (Phase 10.2): Media API 経由でparticipantごとの
// baseline WebP + feature_snapshot をアップロードする。architecture.md の
// 「baseline ができるまでの最初の30〜60秒」に合わせ、capture開始直後だけ動かす。
const BASELINE_CAPTURE_INTERVAL_MS = 5000; // 数秒おきにWebPを生成
const BASELINE_CAPTURE_DURATION_MS = 60 * 1000; // 最初の60秒だけ
let baselineTimer = null;
let baselineCaptureStartTs = 0;
// --- 雰囲気波形 + モーメント検出: room_engagement をローカルで合成した
// mood スコアを波形表示し、短時間で大きく動いた瞬間のタブ全体スクショを
// 直近リングバッファから確保する。すべてメモリ内のみ(永続化なし)。 ---
const MOOD_WAVE_WINDOW_MS = 60000; // 波形に表示する幅
const MOOD_TRIGGER_DELTA = 0.08; // ベースラインからの偏差|dev|がこれを超えたら発火
const MOOD_TRIGGER_REARM_RATIO = 0.6; // 偏差がこの割合以下に戻ったら再武装(ヒステリシス)
// 頷きは瞬間的なジェスチャーでEMA平滑化に埋もれるため、mood偏差とは独立の
// 専用チャンネルで発火させる(将来の笑い声・声量急変も同パターンで追加する)
const NOD_TRIGGER_RATIO = 0.34; // 可視の顔のうちこの割合以上が頷いていたら
const NOD_TRIGGER_MIN_MS = 500; // この時間継続したら発火
const NOD_TRIGGER_COOLDOWN_MS = 10000;
const MOOD_EMA_ALPHA = 0.35; // 表情検出フリッカー対策の平滑化係数(250ms毎)
const MOOD_BASELINE_ALPHA = 0.02; // 波形の中心線となる移動ベースライン(時定数≒12秒)
const MOOD_WAVE_GAIN = 0.15; // 波形の縦スケール: ベースライン偏差±この値で上下端に達する
const MOOD_TRIGGER_COOLDOWN_MS = 10000; // 発火後の連射防止
const MOOD_TRIGGER_MIN_SAMPLES = 8; // 起動直後の誤発火防止
const SNAPSHOT_INTERVAL_MS = 250; // 解析と同周期でバッファし、発火時刻とのずれを最小化
const SNAPSHOT_BUFFER_MS = 15000; // 直近15秒だけメモリ保持(約60枚≒2MB)
const SNAPSHOT_WIDTH = 480;
const MAX_MOMENTS = 12; // メモリに保持する moment 上限
let moodHistory = []; // {t_ms, mood, attention} ※値は移動ベースラインからの偏差(±)
let moodEma = null;
let moodBaseline = null;
let attentionEma = null;
let attentionBaseline = null;
let moodTriggerMarks = []; // {t_ms, direction}
let lastMoodTriggerTs = 0;
let moodTriggerArmed = true;
let nodActiveSince = null;
let lastNodTriggerTs = 0;
let activeExcursion = null; // {moment, startTs} 発火中(偏差が戻るまで)の盛り上がり区間
let snapshotTimer = null;
let snapshotCanvas = null;
let snapshotBuffer = []; // {t_ms, blob}
let moments = []; // {t_ms, direction, delta, snapshot_t_ms, blob, url, features}
let audioContext = null;
let micStream = null;
let micRequestError = null;
let vadTimer = null;
const vad = {
  self: createVadState(),
  other: createVadState()
};

function createVadState() {
  return {
    analyser: null,
    floatBuf: null,
    speaking: false,
    lastSpeechTs: 0,
    volume: 0, // 直近RMS
    pitchHz: null, // 直近の基本周波数(Hz)
    history: [] // {t, speaking} 直近 SPEECH_WINDOW_MS 分
  };
}

elements.sessionId.textContent = sessionId;
restoreSettings();
initEdgeVision();

elements.startButton.addEventListener("click", startCapture);
elements.stopButton.addEventListener("click", stopCapture);
elements.micPermButton.addEventListener("click", openMicPermission);
elements.connectButton.addEventListener("click", toggleWebSocket);
elements.videoFile.addEventListener("change", handleVideoFileSelect);
elements.uploadPlayButton.addEventListener("click", startVideoFileAnalysis);
elements.uploadStopButton.addEventListener("click", stopVideoFileAnalysis);
elements.exportButton.addEventListener("click", downloadSessionJson);
elements.saveApiKeyButton.addEventListener("click", saveGeminiApiKey);
elements.geminiAnalyzeButton.addEventListener("click", runGeminiAnalysis);
elements.unifiedReportButton.addEventListener("click", generateUnifiedReport);

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === "meet_tile_snapshot") {
    latestTileSnapshot = message;
    logEvent(message);
  }
});

async function restoreSettings() {
  const stored = await chrome.storage.local.get(["wsUrl"]);
  elements.wsUrl.value = stored.wsUrl || window.REACTION_ENGINE_CONFIG?.gatewayWsUrl || "";
}

async function initEdgeVision() {
  const mediaPipeDetector = await createMediaPipeFaceDetector();
  if (mediaPipeDetector) {
    faceDetectorBackend = mediaPipeDetector;
    logEvent({ type: "edge_vision_status", message: "MediaPipe FaceDetector enabled" });
  } else {
    const nativeDetector = createNativeFaceDetector();
    if (nativeDetector) {
      faceDetectorBackend = nativeDetector;
      logEvent({ type: "edge_vision_status", message: "Native FaceDetector enabled" });
    } else {
      logEvent({ type: "edge_vision_status", message: "Face detector unavailable; using motion-only fallback" });
    }
  }

  const mediaPipeLandmarker = await createMediaPipeFaceLandmarker();
  if (mediaPipeLandmarker) {
    faceLandmarkerBackend = mediaPipeLandmarker;
    logEvent({ type: "edge_vision_status", message: "MediaPipe FaceLandmarker enabled" });
  }
}

async function createMediaPipeFaceDetector() {
  try {
    const vision = await FilesetResolver.forVisionTasks(
      chrome.runtime.getURL("vendor/mediapipe/wasm")
    );
    const detector = await MediaPipeFaceDetector.createFromOptions(vision, {
      baseOptions: {
        modelAssetPath: chrome.runtime.getURL(MEDIAPIPE_MODEL_PATH),
        delegate: "CPU"
      },
      runningMode: "VIDEO",
      minDetectionConfidence: 0.3
    });

    return {
      modelVersion: "mediapipe-blaze-face-short-range-v1",
      async detect(source, timestampMs) {
        const result = detector.detectForVideo(source, timestampMs);
        return result.detections.map((detection) => normalizeMediaPipeFace(detection.boundingBox));
      }
    };
  } catch (error) {
    logEvent({ type: "mediapipe_init_error", message: error.message });
    return null;
  }
}

async function createMediaPipeFaceLandmarker() {
  try {
    const vision = await FilesetResolver.forVisionTasks(
      chrome.runtime.getURL("vendor/mediapipe/wasm")
    );
    const landmarker = await FaceLandmarker.createFromOptions(vision, {
      baseOptions: {
        modelAssetPath: chrome.runtime.getURL(MEDIAPIPE_LANDMARKER_MODEL_PATH),
        delegate: "CPU"
      },
      runningMode: "VIDEO",
      numFaces: 8,
      outputFaceBlendshapes: true,
      minFaceDetectionConfidence: 0.2,
      minFacePresenceConfidence: 0.2,
      minTrackingConfidence: 0.2
    });

    return {
      modelVersion: "mediapipe-face-landmarker-v1",
      detect(source, timestampMs) {
        const result = landmarker.detectForVideo(source, timestampMs);
        return result.faceLandmarks.map((landmarks, i) => {
          const parts = buildFacePartsFromLandmarks(landmarks);
          const blendshapes = result.faceBlendshapes?.[i]?.categories;
          if (blendshapes) {
            parts.blendshapes = Object.fromEntries(
              blendshapes.map((b) => [b.categoryName, round(b.score)])
            );
          }
          return parts;
        });
      }
    };
  } catch (error) {
    logEvent({ type: "mediapipe_landmarker_init_error", message: error.message });
    return null;
  }
}

function createNativeFaceDetector() {
  if (!("FaceDetector" in window)) return null;

  try {
    const detector = new window.FaceDetector({
      fastMode: true,
      maxDetectedFaces: 12
    });

    return {
      modelVersion: "shape-detection-face-v1",
      async detect(source) {
        const detected = await detector.detect(source);
        return detected.map((face) => normalizeNativeFace(face.boundingBox));
      }
    };
  } catch (error) {
    logEvent({ type: "native_face_detector_error", message: error.message });
    return null;
  }
}

function openMicPermission() {
  // side panel からは getUserMedia の許可プロンプトが出せないため、
  // 通常タブで一度だけ許可を取得する（許可は拡張オリジンに保存され再利用される）。
  chrome.tabs.create({ url: chrome.runtime.getURL("src/permission.html") });
}

async function startCapture() {
  try {
    setStatus("Requesting capture");
    // マイク要求を画面共有と "同じクリック操作の中" で同時に開始する。
    // 後追いで呼ぶと side panel では許可プロンプトが dismiss されやすいため、
    // クリック直下(awaitより前)で getUserMedia を発火させて安定化する。
    // 失敗しても画面共有は続行できるよう catch でエラーだけ retain。
    micRequestError = null;
    micStream = null;
    const micPromise = navigator.mediaDevices
      .getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true } })
      .catch((error) => {
        micRequestError = error;
        return null;
      });

    stream = await navigator.mediaDevices.getDisplayMedia({
      video: {
        frameRate: { ideal: 15, max: 30 },
        width: { ideal: 1280 },
        height: { ideal: 720 }
      },
      audio: true
    });

    micStream = await micPromise;

    elements.sourceVideo.srcObject = stream;
    await elements.sourceVideo.play();

    stream.getVideoTracks()[0]?.addEventListener("ended", stopCapture);
    elements.startButton.disabled = true;
    elements.stopButton.disabled = false;
    previousFrame = null;

    setupAudioAnalysis(stream);

    analysisTimer = window.setInterval(runAnalysisFrame, ANALYSIS_INTERVAL_MS);
    eventTimer = window.setInterval(sendFeatureEvent, EVENT_INTERVAL_MS);
    vadTimer = window.setInterval(sampleVad, VAD_INTERVAL_MS);
    captureStartTs = Date.now();
    captureFrame(); // 開始直後に1枚
    frameTimer = window.setInterval(captureFrame, FRAME_CAPTURE_INTERVAL_MS);
    startMoodMonitor();
    baselineCaptureStartTs = Date.now();
    baselineTimer = window.setInterval(captureBaselineFrames, BASELINE_CAPTURE_INTERVAL_MS);
    setStatus("Capturing", "active");
  } catch (error) {
    setStatus("Capture failed", "error");
    logEvent({ type: "capture_error", message: error.message });
  }
}

function stopCapture() {
  if (analysisTimer) window.clearInterval(analysisTimer);
  if (eventTimer) window.clearInterval(eventTimer);
  if (vadTimer) window.clearInterval(vadTimer);
  if (frameTimer) window.clearInterval(frameTimer);
  if (baselineTimer) window.clearInterval(baselineTimer);
  baselineTimer = null;
  analysisTimer = null;
  eventTimer = null;
  vadTimer = null;
  frameTimer = null;
  stopMoodMonitor();
  teardownAudioAnalysis();

  if (stream) {
    for (const track of stream.getTracks()) track.stop();
  }

  stream = null;
  elements.sourceVideo.srcObject = null;
  elements.sourceVideo.src = "";
  elements.startButton.disabled = false;
  elements.stopButton.disabled = true;
  previousFrame = null;
  tracks = [];
  trackHistory = new Map();
  latestFeatures = createEmptyFeatures();
  updateMetrics(latestFeatures);
  drawEmptyPreview();
  setStatus(ws ? "Connected" : "Idle", ws ? "active" : "");
}

let videoFileUrl = null;

function handleVideoFileSelect() {
  const file = elements.videoFile.files[0];
  if (!file) {
    elements.uploadControls.style.display = "none";
    return;
  }
  elements.uploadControls.style.display = "";
  elements.uploadPlayButton.disabled = false;
  elements.uploadStopButton.disabled = true;
  logEvent({ type: "video_file_selected", message: file.name });
  updateGeminiButton();
}

async function startVideoFileAnalysis() {
  const file = elements.videoFile.files[0];
  if (!file) return;

  // Stop any live capture first
  stopCapture();

  if (videoFileUrl) URL.revokeObjectURL(videoFileUrl);
  videoFileUrl = URL.createObjectURL(file);

  const video = elements.sourceVideo;
  video.srcObject = null;
  video.src = videoFileUrl;
  video.currentTime = 0;

  sessionId = createSessionId();
  elements.sessionId.textContent = sessionId;

  try {
    await video.play();
  } catch (error) {
    logEvent({ type: "video_file_error", message: error.message });
    return;
  }

  // Use the video's native resolution for maximum face detection accuracy
  setAnalysisResolution(video.videoWidth, video.videoHeight);
  logEvent({ type: "video_file_resolution", message: `${video.videoWidth}x${video.videoHeight}` });

  previousFrame = null;
  tracks = [];
  trackHistory = new Map();
  nextTrackId = 1;

  elements.uploadPlayButton.disabled = true;
  elements.uploadStopButton.disabled = false;
  elements.startButton.disabled = true;

  video.addEventListener("ended", stopVideoFileAnalysis, { once: true });

  analysisTimer = window.setInterval(runAnalysisFrame, ANALYSIS_INTERVAL_MS);
  eventTimer = window.setInterval(sendFeatureEvent, EVENT_INTERVAL_MS);
  startMoodMonitor();
  setStatus("Analyzing file", "active");
  logEvent({ type: "video_file_started", message: file.name });
}

function setAnalysisResolution(w, h) {
  PREVIEW_WIDTH = w;
  PREVIEW_HEIGHT = h;
  canvas.width = w;
  canvas.height = h;
}

function stopVideoFileAnalysis() {
  if (analysisTimer) window.clearInterval(analysisTimer);
  if (eventTimer) window.clearInterval(eventTimer);
  analysisTimer = null;
  eventTimer = null;
  stopMoodMonitor();

  const video = elements.sourceVideo;
  video.pause();
  video.removeEventListener("ended", stopVideoFileAnalysis);

  if (videoFileUrl) {
    URL.revokeObjectURL(videoFileUrl);
    videoFileUrl = null;
  }

  video.src = "";
  setAnalysisResolution(640, 360);
  elements.uploadPlayButton.disabled = false;
  elements.uploadStopButton.disabled = true;
  elements.startButton.disabled = false;
  previousFrame = null;
  tracks = [];
  trackHistory = new Map();
  latestFeatures = createEmptyFeatures();
  updateMetrics(latestFeatures);
  drawEmptyPreview();
  setStatus(ws ? "Connected" : "Idle", ws ? "active" : "");
  logEvent({ type: "video_file_stopped" });
}

async function runAnalysisFrame() {
  if (analysisRunning) return;
  analysisRunning = true;

  try {
    const video = elements.sourceVideo;
    if (!video.videoWidth || !video.videoHeight) return;

    ctx.drawImage(video, 0, 0, PREVIEW_WIDTH, PREVIEW_HEIGHT);

    const imageData = ctx.getImageData(0, 0, PREVIEW_WIDTH, PREVIEW_HEIGHT);
    const motionScore = calculateMotionScore(imageData);
    previousFrame = imageData;

    const faces = await analyzeWithTileCrop();
    const trackedFaces = updateTracks(faces);
    updateGestureHistory(trackedFaces);
    latestFeatures = buildFeatures(trackedFaces, motionScore);
    drawDebugFrame(trackedFaces, latestFeatures);
    updateMetrics(latestFeatures);
    updateMoodMonitor(latestFeatures);
  } finally {
    analysisRunning = false;
  }
}

function detectGridTiles(imageData, width, height) {
  const data = imageData.data;
  const DARK_THRESHOLD = 55;
  const GAP_MIN_PX = 2;
  const TILE_MIN_PX = 50;

  // Scan each row: compute average brightness
  function rowBrightness(y) {
    let sum = 0;
    const step = 4;
    for (let x = 0; x < width; x += step) {
      const i = (y * width + x) * 4;
      sum += (data[i] + data[i + 1] + data[i + 2]) / 3;
    }
    return sum / (width / step);
  }

  // Scan each column: compute average brightness
  function colBrightness(x) {
    let sum = 0;
    const step = 4;
    for (let y = 0; y < height; y += step) {
      const i = (y * width + x) * 4;
      sum += (data[i] + data[i + 1] + data[i + 2]) / 3;
    }
    return sum / (height / step);
  }

  // Find dark bands (gaps between tiles)
  function findGaps(brightnessFn, length) {
    const gaps = [];
    let inGap = false;
    let gapStart = 0;

    for (let i = 0; i < length; i++) {
      const dark = brightnessFn(i) < DARK_THRESHOLD;
      if (dark && !inGap) {
        gapStart = i;
        inGap = true;
      } else if (!dark && inGap) {
        if (i - gapStart >= GAP_MIN_PX) {
          gaps.push({ start: gapStart, end: i });
        }
        inGap = false;
      }
    }
    if (inGap && length - gapStart >= GAP_MIN_PX) {
      gaps.push({ start: gapStart, end: length });
    }
    return gaps;
  }

  const hGaps = findGaps(rowBrightness, height);
  const vGaps = findGaps(colBrightness, width);

  // Convert gaps to tile edges
  function gapsToEdges(gaps, length) {
    const edges = [0];
    for (const gap of gaps) {
      const mid = Math.round((gap.start + gap.end) / 2);
      if (mid > TILE_MIN_PX && mid < length - TILE_MIN_PX) {
        edges.push(mid);
      }
    }
    edges.push(length);
    return edges;
  }

  const yEdges = gapsToEdges(hGaps, height);
  const xEdges = gapsToEdges(vGaps, width);

  // Need at least a 2-tile grid to be useful
  if (xEdges.length < 3 && yEdges.length < 3) return [];

  const tiles = [];
  for (let row = 0; row < yEdges.length - 1; row++) {
    for (let col = 0; col < xEdges.length - 1; col++) {
      const x = xEdges[col];
      const y = yEdges[row];
      const w = xEdges[col + 1] - x;
      const h = yEdges[row + 1] - y;

      if (w < TILE_MIN_PX || h < TILE_MIN_PX) continue;

      tiles.push({
        tile_id: `grid_${tiles.length + 1}`,
        participant_name: null,
        tile_bbox_viewport: { x, y, w, h },
        source: "grid_detect"
      });
    }
  }

  return tiles;
}

async function analyzeWithTileCrop() {
  let tiles = latestTileSnapshot?.tiles;

  // For recorded video files (no live tile data), auto-detect grid
  if (!tiles?.length) {
    const imageData = ctx.getImageData(0, 0, PREVIEW_WIDTH, PREVIEW_HEIGHT);
    tiles = detectGridTiles(imageData, PREVIEW_WIDTH, PREVIEW_HEIGHT);
    if (tiles.length && !analyzeWithTileCrop._logged) {
      analyzeWithTileCrop._logged = true;
      logEvent({ type: "grid_detect", tiles: tiles.length, resolution: `${PREVIEW_WIDTH}x${PREVIEW_HEIGHT}` });
    }
  }

  if (!tiles?.length) {
    // No tiles detected — run detection on the full frame
    const bitmap = await createImageBitmap(canvas);
    const faces = await analyzeFaces(bitmap);
    bitmap.close();
    return faces;
  }

  // Scale tile coords to canvas coords
  // Live tiles use viewport coords (need scaling), detected tiles use canvas coords directly
  const video = elements.sourceVideo;
  const isLiveTile = latestTileSnapshot?.tiles?.length > 0;
  const scaleX = isLiveTile ? PREVIEW_WIDTH / video.videoWidth : 1;
  const scaleY = isLiveTile ? PREVIEW_HEIGHT / video.videoHeight : 1;

  const allFaces = [];

  for (const tile of tiles) {
    const tb = tile.tile_bbox_viewport;
    const cx = Math.max(0, Math.round(tb.x * scaleX));
    const cy = Math.max(0, Math.round(tb.y * scaleY));
    const cw = Math.min(Math.round(tb.w * scaleX), PREVIEW_WIDTH - cx);
    const ch = Math.min(Math.round(tb.h * scaleY), PREVIEW_HEIGHT - cy);

    if (cw < 30 || ch < 30) continue;

    let tileBitmap;
    try {
      tileBitmap = await createImageBitmap(canvas, cx, cy, cw, ch);
    } catch {
      continue;
    }

    const tileFaces = await analyzeFaces(tileBitmap);
    tileBitmap.close();

    // Map tile-local normalized coords back to full-frame normalized coords
    for (const face of tileFaces) {
      const tileNormX = cx / PREVIEW_WIDTH;
      const tileNormY = cy / PREVIEW_HEIGHT;
      const tileNormW = cw / PREVIEW_WIDTH;
      const tileNormH = ch / PREVIEW_HEIGHT;

      face.x = tileNormX + face.x * tileNormW;
      face.y = tileNormY + face.y * tileNormH;
      face.w = face.w * tileNormW;
      face.h = face.h * tileNormH;
      face.tile_id = tile.tile_id;
      face.participant_name = tile.participant_name ?? null;

      if (face.parts) {
        remapPartsToFullFrame(face.parts, tileNormX, tileNormY, tileNormW, tileNormH);
      }

      allFaces.push(face);
    }
  }

  return allFaces;
}

function remapPartsToFullFrame(parts, offX, offY, scaleW, scaleH) {
  // Remap face_bbox
  if (parts.face_bbox) {
    parts.face_bbox.x = offX + parts.face_bbox.x * scaleW;
    parts.face_bbox.y = offY + parts.face_bbox.y * scaleH;
    parts.face_bbox.w = parts.face_bbox.w * scaleW;
    parts.face_bbox.h = parts.face_bbox.h * scaleH;
  }

  // Remap face part landmark points
  if (parts.face_parts) {
    for (const partKey of Object.keys(parts.face_parts)) {
      const group = parts.face_parts[partKey];
      if (group?.points) {
        for (const p of group.points) {
          p.x = round(offX + p.x * scaleW);
          p.y = round(offY + p.y * scaleH);
        }
        if (group.center) {
          group.center.x = round(offX + group.center.x * scaleW);
          group.center.y = round(offY + group.center.y * scaleH);
        }
      }
    }
  }

  // Remap iris centers
  if (parts.iris) {
    for (const side of ["left", "right"]) {
      if (parts.iris[side]?.center) {
        parts.iris[side].center.x = round(offX + parts.iris[side].center.x * scaleW);
        parts.iris[side].center.y = round(offY + parts.iris[side].center.y * scaleH);
      }
    }
    // ratio_x / ratio_y are relative within the eye, no remap needed
  }
}

async function detectFaces(bitmap) {
  if (!faceDetectorBackend) return [];

  try {
    return await faceDetectorBackend.detect(bitmap, performance.now());
  } catch (error) {
    logEvent({ type: "face_detection_error", message: error.message });
    return [];
  }
}

async function detectFaceParts(bitmap) {
  if (!faceLandmarkerBackend) return [];

  try {
    return faceLandmarkerBackend.detect(bitmap, performance.now());
  } catch (error) {
    logEvent({ type: "face_landmarker_error", message: error.message });
    return [];
  }
}

async function analyzeFaces(bitmap) {
  // FaceLandmarker alone provides bbox + landmarks + blendshapes + iris.
  // Only fall back to FaceDetector when FaceLandmarker is unavailable.
  if (faceLandmarkerBackend) {
    const faceParts = await detectFaceParts(bitmap);
    return faceParts.map((parts) => ({ ...parts.face_bbox, parts }));
  }

  return detectFaces(bitmap);
}

function normalizeNativeFace(box) {
  return {
    x: clamp(box.x / PREVIEW_WIDTH, 0, 1),
    y: clamp(box.y / PREVIEW_HEIGHT, 0, 1),
    w: clamp(box.width / PREVIEW_WIDTH, 0, 1),
    h: clamp(box.height / PREVIEW_HEIGHT, 0, 1)
  };
}

function normalizeMediaPipeFace(box) {
  return {
    x: clamp(box.originX / PREVIEW_WIDTH, 0, 1),
    y: clamp(box.originY / PREVIEW_HEIGHT, 0, 1),
    w: clamp(box.width / PREVIEW_WIDTH, 0, 1),
    h: clamp(box.height / PREVIEW_HEIGHT, 0, 1)
  };
}


function buildFacePartsFromLandmarks(landmarks) {
  const face_bbox = bboxFromLandmarks(landmarks);
  // MediaPipe uses subject's perspective: landmarks 33-area = subject's RIGHT, 362-area = subject's LEFT
  const leftEye = summarizeLandmarkGroup(landmarks, [362, 263, 386, 374]);
  const rightEye = summarizeLandmarkGroup(landmarks, [33, 133, 159, 145]);
  const mouth = summarizeLandmarkGroup(landmarks, [61, 291, 13, 14]);
  const nose = summarizeLandmarkGroup(landmarks, [1, 4, 98, 327]);
  const headPose = estimateHeadPose(landmarks);
  const iris = estimateIris(landmarks);
  const gazeFromIris = iris ? estimateGazeFromIris(iris) : null;

  return {
    face_bbox,
    face_parts: {
      left_eye: leftEye,
      right_eye: rightEye,
      mouth,
      nose
    },
    eye_openness: {
      left: round(eyeOpenness(leftEye)),
      right: round(eyeOpenness(rightEye))
    },
    mouth_openness: round(mouthOpenness(mouth, face_bbox)),
    head_pose_estimate: headPose,
    iris,
    gaze_estimate: gazeFromIris ?? estimateGazeFromHeadPose(headPose),
    landmark_count: landmarks.length
  };
}

function estimateIris(landmarks) {
  // Subject's perspective (MediaPipe convention):
  //   LEFT iris: 468-472 (468=center), LEFT eye corners: outer 263, inner 362
  //   RIGHT iris: 473-477 (473=center), RIGHT eye corners: outer 33, inner 133
  const leftCenter = landmarks[468];
  const rightCenter = landmarks[473];
  if (!leftCenter || !rightCenter) return null;

  const leftOuter = landmarks[263];
  const leftInner = landmarks[362];
  const rightOuter = landmarks[33];
  const rightInner = landmarks[133];
  if (!leftInner || !leftOuter || !rightInner || !rightOuter) return null;

  // Iris position ratio within eye (0=outer corner, 1=inner corner).
  // 左右で目頭/目尻の x 順が逆(左目 inner>outer, 右目 inner<outer)なので、
  // 分母を正の下限に潰すと右目が壊れる。符号を保ったまま 0 割りだけ避ける。
  const signedDenomX = (inner, outer) => {
    const d = inner.x - outer.x;
    return Math.abs(d) < 0.001 ? (d < 0 ? -0.001 : 0.001) : d;
  };
  const leftRatioX = (leftCenter.x - leftOuter.x) / signedDenomX(leftInner, leftOuter);
  const rightRatioX = (rightCenter.x - rightOuter.x) / signedDenomX(rightInner, rightOuter);

  const leftTop = landmarks[386];
  const leftBottom = landmarks[374];
  const rightTop = landmarks[159];
  const rightBottom = landmarks[145];

  const leftRatioY = (leftTop && leftBottom)
    ? (leftCenter.y - leftTop.y) / Math.max(0.001, leftBottom.y - leftTop.y)
    : 0.5;
  const rightRatioY = (rightTop && rightBottom)
    ? (rightCenter.y - rightTop.y) / Math.max(0.001, rightBottom.y - rightTop.y)
    : 0.5;

  return {
    left: {
      center: { x: round(leftCenter.x), y: round(leftCenter.y), z: round(leftCenter.z ?? 0) },
      ratio_x: round(clamp(leftRatioX, 0, 1)),
      ratio_y: round(clamp(leftRatioY, 0, 1))
    },
    right: {
      center: { x: round(rightCenter.x), y: round(rightCenter.y), z: round(rightCenter.z ?? 0) },
      ratio_x: round(clamp(rightRatioX, 0, 1)),
      ratio_y: round(clamp(rightRatioY, 0, 1))
    },
    avg_ratio_x: round(clamp((leftRatioX + rightRatioX) / 2, 0, 1)),
    avg_ratio_y: round(clamp((leftRatioY + rightRatioY) / 2, 0, 1))
  };
}

function estimateGazeFromIris(iris) {
  const x = iris.avg_ratio_x;
  const y = iris.avg_ratio_y;
  if (x > 0.35 && x < 0.65 && y > 0.3 && y < 0.7) return "screen";
  if (x <= 0.35) return "left";
  if (x >= 0.65) return "right";
  if (y <= 0.3) return "up";
  if (y >= 0.7) return "down";
  return "unknown";
}

function bboxFromLandmarks(landmarks) {
  let minX = 1;
  let minY = 1;
  let maxX = 0;
  let maxY = 0;

  for (const point of landmarks) {
    minX = Math.min(minX, point.x);
    minY = Math.min(minY, point.y);
    maxX = Math.max(maxX, point.x);
    maxY = Math.max(maxY, point.y);
  }

  return {
    x: clamp(minX, 0, 1),
    y: clamp(minY, 0, 1),
    w: clamp(maxX - minX, 0, 1),
    h: clamp(maxY - minY, 0, 1)
  };
}

function summarizeLandmarkGroup(landmarks, indexes) {
  const points = indexes
    .map((index) => landmarks[index])
    .filter(Boolean)
    .map((point) => ({
      x: round(point.x),
      y: round(point.y),
      z: round(point.z ?? 0)
    }));

  if (!points.length) {
    return {
      center: { x: 0, y: 0, z: 0 },
      points: []
    };
  }

  const center = points.reduce(
    (acc, point) => ({
      x: acc.x + point.x / points.length,
      y: acc.y + point.y / points.length,
      z: acc.z + point.z / points.length
    }),
    { x: 0, y: 0, z: 0 }
  );

  return {
    center: {
      x: round(center.x),
      y: round(center.y),
      z: round(center.z)
    },
    points
  };
}

function eyeOpenness(eye) {
  if (eye.points.length < 4) return 0;

  const horizontal = pointDistance(eye.points[0], eye.points[1]);
  const vertical = pointDistance(eye.points[2], eye.points[3]);
  return horizontal ? clamp(vertical / horizontal, 0, 1) : 0;
}

function mouthOpenness(mouth, faceBox) {
  if (mouth.points.length < 4 || !faceBox.h) return 0;

  const vertical = pointDistance(mouth.points[2], mouth.points[3]);
  return clamp(vertical / faceBox.h, 0, 1);
}

function estimateHeadPose(landmarks) {
  const leftEye = averagePoint(landmarks, [33, 133]);
  const rightEye = averagePoint(landmarks, [362, 263]);
  const nose = landmarks[1];
  const mouthCenter = averagePoint(landmarks, [13, 14]);
  const eyeCenter = averagePoint(landmarks, [33, 133, 362, 263]);
  const faceBox = bboxFromLandmarks(landmarks);

  if (!leftEye || !rightEye || !nose || !mouthCenter || !eyeCenter || !faceBox.w || !faceBox.h) {
    return null;
  }

  const yaw = clamp((nose.x - eyeCenter.x) / faceBox.w, -1, 1);
  const pitch = clamp((nose.y - eyeCenter.y) / faceBox.h - 0.28, -1, 1);
  const roll = clamp((rightEye.y - leftEye.y) / Math.max(0.001, rightEye.x - leftEye.x), -1, 1);

  return {
    yaw: round(yaw),
    pitch: round(pitch),
    roll: round(roll)
  };
}

function estimateGazeFromHeadPose(headPose) {
  if (!headPose) return "unknown";
  if (Math.abs(headPose.yaw) < 0.18 && Math.abs(headPose.pitch) < 0.18) return "screen";
  if (headPose.yaw <= -0.18) return "left";
  if (headPose.yaw >= 0.18) return "right";
  if (headPose.pitch <= -0.18) return "up";
  if (headPose.pitch >= 0.18) return "down";
  return "unknown";
}

function averagePoint(landmarks, indexes) {
  const points = indexes.map((index) => landmarks[index]).filter(Boolean);
  if (!points.length) return null;

  return points.reduce(
    (acc, point) => ({
      x: acc.x + point.x / points.length,
      y: acc.y + point.y / points.length,
      z: acc.z + (point.z ?? 0) / points.length
    }),
    { x: 0, y: 0, z: 0 }
  );
}

function pointDistance(a, b) {
  return Math.hypot(a.x - b.x, a.y - b.y);
}

function updateTracks(faces) {
  const now = Date.now();
  const unmatchedTracks = new Set(tracks.map((track) => track.id));
  const trackedFaces = [];

  for (const face of faces) {
    const match = findClosestTrack(face, unmatchedTracks);
    const trackId = match?.id ?? nextTrackId++;
    unmatchedTracks.delete(trackId);

    const trackedFace = {
      ...face,
      audience_id: `aud_${trackId}`
    };

    trackedFaces.push(trackedFace);
    upsertTrack(trackId, trackedFace, now);
  }

  tracks = tracks.filter((track) => now - track.last_seen_ms < 5000);
  pruneTrackHistory(now);
  return trackedFaces;
}

function findClosestTrack(face, candidateIds) {
  let bestTrack = null;
  let bestDistance = Number.POSITIVE_INFINITY;

  for (const track of tracks) {
    if (!candidateIds.has(track.id)) continue;

    const distance = bboxCenterDistance(face, track.bbox);
    if (distance < bestDistance) {
      bestDistance = distance;
      bestTrack = track;
    }
  }

  return bestDistance < 0.18 ? bestTrack : null;
}

function upsertTrack(id, face, now) {
  const index = tracks.findIndex((track) => track.id === id);
  const nextTrack = {
    id,
    bbox: face,
    last_seen_ms: now
  };

  if (index >= 0) {
    tracks[index] = nextTrack;
    return;
  }

  tracks.push(nextTrack);
}

function bboxCenterDistance(a, b) {
  const ax = a.x + a.w / 2;
  const ay = a.y + a.h / 2;
  const bx = b.x + b.w / 2;
  const by = b.y + b.h / 2;
  return Math.hypot(ax - bx, ay - by);
}

function calculateMotionScore(imageData) {
  if (!previousFrame) return 0;

  const current = imageData.data;
  const previous = previousFrame.data;
  let diff = 0;
  const step = 16;

  for (let i = 0; i < current.length; i += 4 * step) {
    diff += Math.abs(current[i] - previous[i]);
    diff += Math.abs(current[i + 1] - previous[i + 1]);
    diff += Math.abs(current[i + 2] - previous[i + 2]);
  }

  const samples = current.length / (4 * step);
  return clamp(diff / samples / 255 / 3, 0, 1);
}

function updateGestureHistory(faces) {
  const now = Date.now();

  for (const face of faces) {
    const pitch = face.parts?.head_pose_estimate?.pitch;
    if (typeof pitch !== "number") continue;

    const history = trackHistory.get(face.audience_id) ?? [];
    history.push({ t_ms: now, pitch });
    trackHistory.set(
      face.audience_id,
      history.filter((sample) => now - sample.t_ms <= GESTURE_HISTORY_MS)
    );
  }
}

function pruneTrackHistory(now) {
  const activeAudienceIds = new Set(tracks.map((track) => `aud_${track.id}`));

  for (const [audienceId, history] of trackHistory.entries()) {
    if (!activeAudienceIds.has(audienceId)) {
      trackHistory.delete(audienceId);
      continue;
    }

    const nextHistory = history.filter((sample) => now - sample.t_ms <= GESTURE_HISTORY_MS);
    if (nextHistory.length) {
      trackHistory.set(audienceId, nextHistory);
    } else {
      trackHistory.delete(audienceId);
    }
  }
}

// --- room_engagement (§8-1): 2軸・ベースライン相対 ---
// 設計判断:
//  - attention（注意）と valence（感情価）を分離。1本のスコアに混ぜると
//    「画面を見なくなった(gaze↓)が笑った(smile↑)」で打ち消し合い情報が消える。
//  - 絶対値でなくベースライン相対。平常値が人・会議で違うため。
//  - brow_down は意味が割れる(集中/困惑/不満)ので数値に入れず brow_flag で別持ち→LLMが文脈解釈。
//  - 個人baselineは持たない(§3.1)。mean のみ相対、variance/min は瞬間分布。
const ENGAGEMENT_WARMUP_SAMPLES = 40; // 開始~30-60秒は基準を較正するだけ
const ENGAGEMENT_EWMA_ALPHA = 0.03; // 較正後の緩やかなドリフト
const ENGAGEMENT_WARMUP_ALPHA = 0.15; // 較正中は速く基準へ寄せる
const NOD_SCORE_THRESHOLD = 0.5; // これを超えたら「頷いている」
const BROW_FLAG_MARGIN = 0.15; // 基準+マージン超で brow_flag（重みは仮値）

const engagementBaseline = {
  gazeScreenRatio: null,
  nodRatio: null,
  eyeOpen: null,
  smile: null,
  browDown: null,
  samples: 0
};

function updateBaseline(key, value, warming) {
  const b = engagementBaseline;
  if (b[key] == null) {
    b[key] = value;
    return;
  }
  const alpha = warming ? ENGAGEMENT_WARMUP_ALPHA : ENGAGEMENT_EWMA_ALPHA;
  b[key] = b[key] + alpha * (value - b[key]);
}

function faceGazeIsScreen(face) {
  return face.parts?.gaze_estimate === "screen";
}

function faceEyeOpen(face) {
  const e = face.parts?.eye_openness;
  return e ? (e.left + e.right) / 2 : null;
}

function faceSmile(face) {
  const b = face.parts?.blendshapes;
  if (!b) return null;
  return ((b.mouthSmileLeft ?? 0) + (b.mouthSmileRight ?? 0)) / 2;
}

function faceBrowDown(face) {
  const b = face.parts?.blendshapes;
  if (!b) return null;
  return ((b.browDownLeft ?? 0) + (b.browDownRight ?? 0)) / 2;
}

function computeRoomEngagement(faces, nodByFace) {
  const visible = faces.length;
  const warming = engagementBaseline.samples < ENGAGEMENT_WARMUP_SAMPLES;

  if (!visible) {
    // 見えている顔ゼロ = 部分観測。dropと誤読しないよう中立を返す
    return {
      attention_mean: 0,
      attention_variance: 0,
      attention_min: 0,
      valence_mean: 0,
      brow_flag: false,
      gaze_screen_ratio: 0,
      nod_ratio: 0,
      visible_faces: 0,
      calibrating: warming,
      attention_raw: 0
    };
  }

  // per-face 瞬間 attention（variance/min 用。個人baselineは持たない=§3.1）
  const perFaceAttention = faces.map((face) => {
    const gaze = faceGazeIsScreen(face) ? 1 : 0;
    const nodScore = nodByFace.get(face.audience_id)?.nod_score ?? 0;
    const nod = nodScore > NOD_SCORE_THRESHOLD ? 1 : 0;
    const eye = clamp(faceEyeOpen(face) ?? 0, 0, 1);
    return 0.5 * gaze + 0.3 * nod + 0.2 * eye;
  });
  const attnRawMean =
    perFaceAttention.reduce((a, b) => a + b, 0) / perFaceAttention.length;
  const attnVariance =
    perFaceAttention.reduce((a, b) => a + (b - attnRawMean) ** 2, 0) /
    perFaceAttention.length;
  const attnMin = Math.min(...perFaceAttention);

  // 集約 raw ratio（ベースライン相対の mean 用。全員の割合。§3.1）
  const gazeRatio = faces.filter(faceGazeIsScreen).length / visible;
  const nodRatio =
    faces.filter(
      (f) => (nodByFace.get(f.audience_id)?.nod_score ?? 0) > NOD_SCORE_THRESHOLD
    ).length / visible;
  const eyeVals = faces.map(faceEyeOpen).filter((v) => v != null);
  const eyeOpen = eyeVals.length
    ? eyeVals.reduce((a, b) => a + b, 0) / eyeVals.length
    : 0;
  const smileVals = faces.map(faceSmile).filter((v) => v != null);
  const smile = smileVals.length
    ? smileVals.reduce((a, b) => a + b, 0) / smileVals.length
    : 0;
  const browVals = faces.map(faceBrowDown).filter((v) => v != null);
  const browDown = browVals.length
    ? browVals.reduce((a, b) => a + b, 0) / browVals.length
    : 0;

  // ベースライン更新（1フレーム=1サンプル）
  updateBaseline("gazeScreenRatio", gazeRatio, warming);
  updateBaseline("nodRatio", nodRatio, warming);
  updateBaseline("eyeOpen", eyeOpen, warming);
  updateBaseline("smile", smile, warming);
  updateBaseline("browDown", browDown, warming);
  engagementBaseline.samples += 1;

  if (warming) {
    // 較正中: 基準未確定なので偏差は0固定、calibrating を立てる
    return {
      attention_mean: 0,
      attention_variance: round(attnVariance),
      attention_min: 0,
      valence_mean: 0,
      brow_flag: false,
      gaze_screen_ratio: round(gazeRatio),
      nod_ratio: round(nodRatio),
      visible_faces: visible,
      calibrating: true,
      attention_raw: round(attnRawMean)
    };
  }

  // ベースライン相対の合成（重みは仮値・後で実データ調整）
  const dGaze = gazeRatio - engagementBaseline.gazeScreenRatio;
  const dNod = nodRatio - engagementBaseline.nodRatio;
  const dEye = eyeOpen - engagementBaseline.eyeOpen;
  const attentionMean = 0.5 * dGaze + 0.3 * dNod + 0.2 * dEye;
  const valenceMean = smile - engagementBaseline.smile;
  const browFlag = browDown > engagementBaseline.browDown + BROW_FLAG_MARGIN;

  return {
    attention_mean: round(attentionMean),
    attention_variance: round(attnVariance),
    attention_min: round(attnMin),
    valence_mean: round(valenceMean),
    brow_flag: browFlag,
    gaze_screen_ratio: round(gazeRatio),
    nod_ratio: round(nodRatio),
    visible_faces: visible,
    calibrating: false,
    attention_raw: round(attnRawMean)
  };
}

function detectNodGesture(audienceId) {
  const history = trackHistory.get(audienceId) ?? [];
  if (history.length < 5) {
    return {
      nod_count: 0,
      nod_score: 0
    };
  }

  const smoothed = smoothPitchHistory(history);
  const directionChanges = [];
  let previousDirection = 0;

  for (let i = 1; i < smoothed.length; i += 1) {
    const delta = smoothed[i].pitch - smoothed[i - 1].pitch;
    // 0.015では実際の頷き(1ステップ0.01前後)がほぼ全て「静止」扱いになる
    const direction = Math.abs(delta) < 0.005 ? 0 : Math.sign(delta);

    if (!direction) continue;
    if (previousDirection && direction !== previousDirection) {
      directionChanges.push(i);
    }

    previousDirection = direction;
  }

  let nodCount = 0;
  let maxAmplitude = 0;

  for (let i = 1; i < directionChanges.length; i += 1) {
    const start = smoothed[directionChanges[i - 1]];
    const end = smoothed[directionChanges[i]];
    const duration = end.t_ms - start.t_ms;
    const amplitude = pitchRange(smoothed, directionChanges[i - 1], directionChanges[i]);

    if (duration >= NOD_MIN_PHASE_MS && amplitude >= NOD_MIN_PITCH_DELTA) {
      nodCount += 1;
      maxAmplitude = Math.max(maxAmplitude, amplitude);
      i += 1;
    }
  }

  return {
    nod_count: nodCount,
    // 実測レンジに合わせ、振幅0.10でスコア1.0(旧0.22は実際の頷きでは到達不能)
    nod_score: round(clamp(maxAmplitude / 0.1, 0, 1))
  };
}

function smoothPitchHistory(history) {
  return history.map((sample, index) => {
    const start = Math.max(0, index - 1);
    const end = Math.min(history.length, index + 2);
    const window = history.slice(start, end);
    const pitch = window.reduce((sum, item) => sum + item.pitch, 0) / window.length;
    return {
      t_ms: sample.t_ms,
      pitch
    };
  });
}

function pitchRange(samples, startIndex, endIndex) {
  const window = samples.slice(startIndex, endIndex + 1);
  const pitches = window.map((sample) => sample.pitch);
  return Math.max(...pitches) - Math.min(...pitches);
}

async function setupAudioAnalysis(displayStream) {
  const startTs = Date.now();
  vad.self.lastSpeechTs = startTs;
  vad.other.lastSpeechTs = startTs;

  try {
    audioContext = new AudioContext();
    if (audioContext.state === "suspended") await audioContext.resume();
    sendDiagnostic("audio_status", `AudioContext state=${audioContext.state}`);
  } catch (error) {
    sendDiagnostic("audio_init_error", `AudioContext failed: ${error.name} ${error.message}`);
    return;
  }

  // 相手 = タブ音声（getDisplayMedia の audio トラックを流用。スピーカー前のデジタル音声）
  const audioTracks = displayStream.getAudioTracks();
  if (audioTracks[0]) {
    try {
      const src = audioContext.createMediaStreamSource(new MediaStream([audioTracks[0]]));
      vad.other.analyser = makeAnalyser(src);
      sendDiagnostic("audio_status", "other(tab) VAD enabled");
    } catch (error) {
      sendDiagnostic("audio_init_error", `other(tab) failed: ${error.name} ${error.message}`);
    }
  } else {
    sendDiagnostic("audio_status", "other(tab) no audio track — 「タブの音声を共有」未チェックの可能性");
  }

  // 自分 = マイク。startCapture がクリック直下で取得済み。ここでは接続のみ。
  try {
    const perm = await navigator.permissions.query({ name: "microphone" });
    sendDiagnostic("audio_status", `mic permission: ${perm.state}`);
  } catch {
    // permissions API 非対応環境は無視
  }

  if (micStream) {
    try {
      const micSrc = audioContext.createMediaStreamSource(micStream);
      vad.self.analyser = makeAnalyser(micSrc);
      sendDiagnostic("audio_status", "self(mic) VAD enabled");
    } catch (error) {
      sendDiagnostic("audio_init_error", `self(mic) connect failed: ${error.name} ${error.message}`);
    }
  } else {
    const reason = micRequestError
      ? `${micRequestError.name} ${micRequestError.message}`
      : "no mic stream";
    sendDiagnostic("audio_init_error", `self(mic) failed: ${reason}（chrome://settings/content/microphone を確認）`);
  }
}

function makeAnalyser(sourceNode) {
  const analyser = audioContext.createAnalyser();
  analyser.fftSize = 2048; // ピッチ(自己相関)に十分な窓長。RMSにも問題なし
  sourceNode.connect(analyser);
  return analyser;
}

function teardownAudioAnalysis() {
  if (micStream) {
    for (const t of micStream.getTracks()) t.stop();
  }
  micStream = null;
  if (audioContext) audioContext.close().catch(() => {});
  audioContext = null;
  vad.self = createVadState();
  vad.other = createVadState();
}

// 1回の time-domain 読み取りで音量(RMS)とピッチ(基本周波数)を返す
function analyzeAudio(state, sampleRate) {
  const n = state.analyser.fftSize;
  if (!state.floatBuf || state.floatBuf.length !== n) {
    state.floatBuf = new Float32Array(n);
  }
  const buf = state.floatBuf;
  state.analyser.getFloatTimeDomainData(buf);

  // 音量 = RMS
  let sumSq = 0;
  for (let i = 0; i < n; i++) sumSq += buf[i] * buf[i];
  const rms = Math.sqrt(sumSq / n);

  // ピッチ = 自己相関で基本周波数を推定（人声域 70〜400Hz）。小音量/無声は null
  const pitchHz = rms >= 0.015 ? detectPitch(buf, n, sampleRate) : null;

  return { rms, pitchHz };
}

// 正規化自己相関＋放物線補間で基本周波数を推定。周期性が弱ければ null
function detectPitch(buf, n, sampleRate) {
  const minLag = Math.max(2, Math.floor(sampleRate / 400));
  const maxLag = Math.min(n - 2, Math.floor(sampleRate / 70));

  let energy = 0;
  for (let i = 0; i < n; i++) energy += buf[i] * buf[i];
  if (energy <= 0) return null;

  const corr = new Float32Array(maxLag + 2);
  let bestLag = -1;
  let bestVal = 0;
  for (let lag = minLag; lag <= maxLag; lag++) {
    let sum = 0;
    for (let i = 0; i < n - lag; i++) sum += buf[i] * buf[i + lag];
    const c = sum / energy; // 0付近〜1に正規化。長lagほど項数減で自然に減衰=低域誤検出を抑制
    corr[lag] = c;
    if (c > bestVal) {
      bestVal = c;
      bestLag = lag;
    }
  }
  // 十分な周期性がある voiced 区間だけ採用（無声/子音は null）
  if (bestLag < 0 || bestVal < 0.5) return null;

  // 放物線補間でサブサンプル精度
  let lag = bestLag;
  const cl = corr[bestLag - 1];
  const cr = corr[bestLag + 1];
  const denom = 2 * (2 * bestVal - cl - cr);
  if (denom !== 0) lag = bestLag + (cr - cl) / denom;

  const hz = sampleRate / lag;
  return hz >= 70 && hz <= 400 ? Math.round(hz) : null;
}

function sampleVad() {
  const now = Date.now();
  const cutoff = now - SPEECH_WINDOW_MS;
  const sampleRate = audioContext?.sampleRate ?? 48000;
  for (const key of ["self", "other"]) {
    const s = vad[key];
    if (!s.analyser) continue;
    const { rms, pitchHz } = analyzeAudio(s, sampleRate);
    s.volume = rms;
    s.pitchHz = pitchHz;
    const speaking = rms > VAD_RMS_THRESHOLD;
    if (speaking) s.lastSpeechTs = now;
    s.speaking = speaking;
    s.history.push({ t: now, speaking });
    while (s.history.length && s.history[0].t < cutoff) s.history.shift();
  }
}

function buildSpeechFeatures() {
  const now = Date.now();
  const perSource = (key) => {
    const s = vad[key];
    if (!s.analyser) return null; // 音源が無い（例: タブ音声を共有していない）
    const total = s.history.length;
    const speakingCount = s.history.reduce((n, h) => n + (h.speaking ? 1 : 0), 0);
    return {
      speaking: s.speaking,
      pause_ms: s.speaking ? 0 : Math.round(now - s.lastSpeechTs),
      speech_ratio: total ? round(speakingCount / total) : 0,
      volume: round(s.volume ?? 0), // RMS音量 0〜1
      pitch_hz: s.pitchHz // 基本周波数(Hz)。無声/小音量は null
    };
  };
  return { self: perSource("self"), other: perSource("other") };
}

function captureFrame() {
  const video = elements.sourceVideo;
  if (!video || !video.videoWidth) return;

  // 最初5分を過ぎたら自動停止
  if (Date.now() - captureStartTs > FRAME_CAPTURE_DURATION_MS) {
    if (frameTimer) window.clearInterval(frameTimer);
    frameTimer = null;
    sendDiagnostic("frame_capture_done", "5分経過: 代表フレーム取得を終了");
    return;
  }

  if (!frameCanvas) frameCanvas = document.createElement("canvas");
  const w = FRAME_CAPTURE_WIDTH;
  const h = Math.round((video.videoHeight / video.videoWidth) * w) || 270;
  frameCanvas.width = w;
  frameCanvas.height = h;
  frameCanvas.getContext("2d").drawImage(video, 0, 0, w, h);
  const dataUrl = frameCanvas.toDataURL("image/jpeg", 0.5);

  // 画像本体はWSへ（UIログには要約のみ＝肥大化回避）
  const event = { type: "frame_capture", t_ms: Date.now(), w, h, image: dataUrl };
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(event));
  logEvent({ type: "frame_capture", t_ms: event.t_ms, message: `frame ${w}x${h} ~${Math.round(dataUrl.length / 1024)}KB` });
}

// captureBaselineFrames uploads one WebP crop per currently-tracked face to
// Media API (plan/backend-local-docker-runbook.md Phase 10.2), each paired
// with the compact feature_snapshot from that same instant. Images never go
// over the Gateway WebSocket; only realtime_feature/feedback_event does.
function captureBaselineFrames() {
  if (Date.now() - baselineCaptureStartTs > BASELINE_CAPTURE_DURATION_MS) {
    if (baselineTimer) window.clearInterval(baselineTimer);
    baselineTimer = null;
    return;
  }

  const mediaApiBaseUrl = window.REACTION_ENGINE_CONFIG?.mediaApiBaseUrl;
  if (!mediaApiBaseUrl) return;

  const tMs = Date.now();
  for (const track of latestFeatures.face_tracks ?? []) {
    uploadBaselineFrame(mediaApiBaseUrl, track, tMs).catch((error) => {
      logEvent({ type: "baseline_upload_error", audience_id: track.audience_id, message: error.message });
    });
  }
}

async function uploadBaselineFrame(mediaApiBaseUrl, track, tMs) {
  const blob = await cropFaceToWebp(track.face_bbox);
  if (!blob) return;

  const captureId = `cap_${tMs}_${track.audience_id}`;
  const featureSnapshot = {
    attention_score: latestFeatures.attention_score,
    motion_score: latestFeatures.motion_score,
    gaze_estimate: track.gaze_estimate,
    head_pose_estimate: track.head_pose_estimate,
    mouth_openness: track.mouth_openness,
    eye_openness: track.eye_openness,
    face_bbox: track.face_bbox
  };

  const uploadURLRes = await fetch(`${mediaApiBaseUrl}/sessions/${sessionId}/media/upload-url`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      purpose: "baseline_frame",
      content_type: "image/webp",
      capture_id: captureId,
      t_ms: tMs,
      audience_id: track.audience_id,
      tile_id: track.tile_id ?? undefined,
      feature_snapshot: featureSnapshot
    })
  });
  if (!uploadURLRes.ok) throw new Error(`upload-url failed: ${uploadURLRes.status}`);
  const { upload_url: uploadUrl } = await uploadURLRes.json();

  const putRes = await fetch(uploadUrl, {
    method: "PUT",
    headers: { "Content-Type": "image/webp" },
    body: blob
  });
  if (!putRes.ok) throw new Error(`local-upload failed: ${putRes.status}`);

  const completeRes = await fetch(`${mediaApiBaseUrl}/sessions/${sessionId}/media/${captureId}/complete`, {
    method: "POST"
  });
  if (!completeRes.ok) throw new Error(`complete failed: ${completeRes.status}`);

  logEvent({ type: "baseline_frame_uploaded", audience_id: track.audience_id, capture_id: captureId });
}

// cropFaceToWebp crops the face_bbox region (normalized 0..1 against
// PREVIEW_WIDTH/PREVIEW_HEIGHT, same convention as drawDebugFrame) out of the
// preview canvas and encodes it as WebP.
function cropFaceToWebp(faceBbox) {
  return new Promise((resolve) => {
    if (!faceBbox) {
      resolve(null);
      return;
    }

    const x = Math.round(faceBbox.x * PREVIEW_WIDTH);
    const y = Math.round(faceBbox.y * PREVIEW_HEIGHT);
    const w = Math.round(faceBbox.w * PREVIEW_WIDTH);
    const h = Math.round(faceBbox.h * PREVIEW_HEIGHT);
    if (w <= 0 || h <= 0) {
      resolve(null);
      return;
    }

    const tileCanvas = document.createElement("canvas");
    tileCanvas.width = w;
    tileCanvas.height = h;
    tileCanvas.getContext("2d").drawImage(canvas, x, y, w, h, 0, 0, w, h);
    tileCanvas.toBlob((blob) => resolve(blob), "image/webp", 0.8);
  });
}

function buildFeatures(faces, motionScore) {
  const faceVisible = faces.length > 0;
  const firstGaze = faces.find((face) => face.parts?.gaze_estimate)?.parts.gaze_estimate;
  const faceGestures = faces.map((face) => ({
    audience_id: face.audience_id,
    gestures: detectNodGesture(face.audience_id)
  }));
  const nodByFace = new Map(faceGestures.map((g) => [g.audience_id, g.gestures]));
  const totalNodCount = faceGestures.reduce((sum, item) => sum + item.gestures.nod_count, 0);
  const maxNodScore = faceGestures.reduce((max, item) => Math.max(max, item.gestures.nod_score), 0);
  const engagement = computeRoomEngagement(faces, nodByFace);

  return {
    face_visible: faceVisible,
    face_count: faces.length,
    face_tracks: faces.map((face) => ({
      audience_id: face.audience_id,
      tile_id: face.tile_id ?? null,
      participant_name: face.participant_name ?? null,
      face_bbox: {
        x: round(face.x),
        y: round(face.y),
        w: round(face.w),
        h: round(face.h)
      },
      face_parts: face.parts?.face_parts ?? null,
      eye_openness: face.parts?.eye_openness ?? null,
      mouth_openness: face.parts?.mouth_openness ?? null,
      head_pose_estimate: face.parts?.head_pose_estimate ?? null,
      gaze_estimate: face.parts?.gaze_estimate ?? "unknown",
      iris: face.parts?.iris ?? null,
      blendshapes: face.parts?.blendshapes ?? null,
      landmark_count: face.parts?.landmark_count ?? 0,
      gestures: faceGestures.find((item) => item.audience_id === face.audience_id)?.gestures ?? {
        nod_count: 0,
        nod_score: 0
      }
    })),
    motion_score: round(motionScore),
    attention_score: engagement.attention_raw, // 旧UI互換: 瞬間rawの平均
    room_engagement: {
      attention_mean: engagement.attention_mean,
      attention_variance: engagement.attention_variance,
      attention_min: engagement.attention_min,
      valence_mean: engagement.valence_mean,
      brow_flag: engagement.brow_flag,
      gaze_screen_ratio: engagement.gaze_screen_ratio,
      nod_ratio: engagement.nod_ratio,
      visible_faces: engagement.visible_faces,
      calibrating: engagement.calibrating
    },
    speech: buildSpeechFeatures(),
    gaze_estimate: faceVisible ? firstGaze ?? "unknown" : "not_visible",
    gestures: {
      nod_count: totalNodCount,
      nod_score: round(maxNodScore)
    },
    client_model_version: {
      face_detector: faceDetectorBackend?.modelVersion ?? "motion-fallback-v1",
      face_landmarker: faceLandmarkerBackend?.modelVersion ?? null
    }
  };
}

function drawDebugFrame(faces, features) {
  for (const face of faces) {
    ctx.strokeStyle = "#34a853";
    ctx.lineWidth = 3;
    ctx.strokeRect(
      face.x * PREVIEW_WIDTH,
      face.y * PREVIEW_HEIGHT,
      face.w * PREVIEW_WIDTH,
      face.h * PREVIEW_HEIGHT
    );
    ctx.fillStyle = "#34a853";
    ctx.font = "12px system-ui, sans-serif";
    const label = face.participant_name || face.audience_id;
    ctx.fillText(label, face.x * PREVIEW_WIDTH, Math.max(14, face.y * PREVIEW_HEIGHT - 6));
    drawFacePartPoints(face.parts?.face_parts);
  }

  ctx.fillStyle = "rgba(16, 24, 40, 0.72)";
  ctx.fillRect(10, 10, 230, 58);
  ctx.fillStyle = "#ffffff";
  ctx.font = "13px system-ui, sans-serif";
  ctx.fillText(`faces: ${features.face_count}`, 20, 32);
  ctx.fillText(`motion: ${features.motion_score} attention: ${features.attention_score}`, 20, 54);
}

function drawFacePartPoints(faceParts) {
  if (!faceParts) return;

  ctx.fillStyle = "#fbbc04";
  for (const part of Object.values(faceParts)) {
    for (const point of part.points) {
      ctx.beginPath();
      ctx.arc(point.x * PREVIEW_WIDTH, point.y * PREVIEW_HEIGHT, 2, 0, Math.PI * 2);
      ctx.fill();
    }
  }
}

function drawEmptyPreview() {
  ctx.fillStyle = "#101828";
  ctx.fillRect(0, 0, PREVIEW_WIDTH, PREVIEW_HEIGHT);
}

function sendDiagnostic(type, message) {
  const event = { type, message, t_ms: Date.now() };
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(event));
  logEvent(event);
}

function sendFeatureEvent() {
  const event = {
    type: "realtime_feature",
    session_id: sessionId,
    t_ms: Date.now(),
    meeting_provider: "google_meet",
    source: "chrome_side_panel",
    features: latestFeatures
  };

  if (ws?.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify(event));
  }

  storeFeatureEvent(event);
  logEvent(event);
}

async function toggleWebSocket() {
  if (ws) {
    ws.close();
    ws = null;
    elements.connectButton.textContent = "Connect";
    setStatus(stream ? "Capturing" : "Idle", stream ? "active" : "");
    return;
  }

  const url = elements.wsUrl.value.trim();
  await chrome.storage.local.set({ wsUrl: url });

  if (!url) {
    logEvent({ type: "websocket_skipped", message: "WebSocket URL is empty" });
    return;
  }

  try {
    ws = new WebSocket(url);
    ws.addEventListener("open", () => {
      elements.connectButton.textContent = "Disconnect";
      setStatus("Connected", "active");
      logEvent({ type: "websocket_open", url });
    });
    ws.addEventListener("message", (event) => {
      logEvent({ type: "feedback_event", payload: safeParse(event.data) });
    });
    ws.addEventListener("close", () => {
      ws = null;
      elements.connectButton.textContent = "Connect";
      setStatus(stream ? "Capturing" : "Idle", stream ? "active" : "");
      logEvent({ type: "websocket_close" });
    });
    ws.addEventListener("error", () => {
      setStatus("WebSocket error", "error");
      logEvent({ type: "websocket_error" });
    });
  } catch (error) {
    setStatus("WebSocket failed", "error");
    logEvent({ type: "websocket_error", message: error.message });
  }
}

function updateMetrics(features) {
  elements.faceCount.textContent = String(features.face_count ?? 0);
  elements.motionScore.textContent = Number(features.motion_score ?? 0).toFixed(2);
  elements.attentionScore.textContent = Number(features.attention_score ?? 0).toFixed(2);
}

function setStatus(label, variant = "") {
  elements.statusBadge.textContent = label;
  elements.statusBadge.className = `badge ${variant}`.trim();
}

function logEvent(event) {
  const item = document.createElement("li");
  item.textContent = JSON.stringify(event);
  elements.eventLog.prepend(item);

  while (elements.eventLog.children.length > 30) {
    elements.eventLog.lastElementChild?.remove();
  }
}

function createEmptyFeatures() {
  return {
    face_visible: false,
    face_count: 0,
    face_tracks: [],
    motion_score: 0,
    attention_score: 0,
    speech: { self: null, other: null },
    gaze_estimate: "not_visible",
    gestures: {
      nod_count: 0,
      nod_score: 0
    },
    client_model_version: {
      face_detector: "uninitialized",
      face_landmarker: null
    }
  };
}

// --- Gemini Analysis ---

let geminiApiKeyStored = null;
let latestGeminiResult = null;

async function restoreGeminiApiKey() {
  // Try config.local.js first (hardcoded key for dev convenience)
  try {
    const config = await import("./config.local.js");
    if (config.GEMINI_API_KEY) {
      geminiApiKeyStored = config.GEMINI_API_KEY;
      elements.geminiApiKey.value = "••••••••";
      updateGeminiButton();
      logEvent({ type: "gemini_api_key_loaded", source: "config.local.js" });
      return;
    }
  } catch {
    // config.local.js not found or empty — fall through to chrome.storage
  }

  const stored = await chrome.storage.local.get(["geminiApiKey"]);
  if (stored.geminiApiKey) {
    geminiApiKeyStored = stored.geminiApiKey;
    elements.geminiApiKey.value = "••••••••";
    updateGeminiButton();
  }
}
restoreGeminiApiKey();

async function saveGeminiApiKey() {
  const key = elements.geminiApiKey.value.trim();
  if (!key || key === "••••••••") return;
  await chrome.storage.local.set({ geminiApiKey: key });
  geminiApiKeyStored = key;
  elements.geminiApiKey.value = "••••••••";
  updateGeminiButton();
  logEvent({ type: "gemini_api_key_saved" });
}

function updateGeminiButton() {
  const hasKey = !!geminiApiKeyStored;
  const hasFile = !!elements.videoFile.files[0];
  elements.geminiAnalyzeButton.disabled = !(hasKey && hasFile);
}

async function runGeminiAnalysis() {
  const file = elements.videoFile.files[0];
  if (!file || !geminiApiKeyStored) return;

  elements.geminiAnalyzeButton.disabled = true;
  elements.geminiResult.style.display = "none";
  elements.geminiStatus.textContent = "Starting...";

  try {
    const result = await analyzeVideoWithGemini(
      geminiApiKeyStored,
      file,
      (status) => { elements.geminiStatus.textContent = status; }
    );

    latestGeminiResult = result;
    elements.geminiResult.textContent = JSON.stringify(result, null, 2);
    elements.geminiResult.style.display = "";
    elements.geminiStatus.textContent = "Analysis complete";
    logEvent({ type: "gemini_analysis_complete", participant_count: result.participants?.length ?? 0 });
    updateUnifiedReportButton();
  } catch (error) {
    elements.geminiStatus.textContent = `Error: ${error.message}`;
    logEvent({ type: "gemini_analysis_error", message: error.message });
  } finally {
    updateGeminiButton();
  }
}

// --- Unified Report ---

function updateUnifiedReportButton() {
  // Enable when we have session data OR gemini result
  const hasGemini = !!latestGeminiResult;
  elements.unifiedReportButton.disabled = !hasGemini;
}

async function generateUnifiedReport() {
  elements.unifiedReportButton.disabled = true;
  elements.unifiedReportStatus.textContent = "レポート生成中...";

  try {
    const events = await exportSessionData(sessionId);
    const featureEvents = events.filter((e) => e.type === "realtime_feature");
    const gemini = latestGeminiResult;

    const report = buildUnifiedReport(featureEvents, gemini);
    renderUnifiedReport(report);
    elements.unifiedReportStatus.textContent = "";
    elements.unifiedReport.style.display = "";
    logEvent({ type: "unified_report_generated", participants: report.participants.length });
  } catch (error) {
    elements.unifiedReportStatus.textContent = `Error: ${error.message}`;
    logEvent({ type: "unified_report_error", message: error.message });
  } finally {
    updateUnifiedReportButton();
  }
}

function buildUnifiedReport(featureEvents, gemini) {
  // Aggregate per-participant stats from realtime data
  const participantStats = new Map();
  let totalFrames = 0;

  for (const event of featureEvents) {
    const features = event.features;
    if (!features) continue;
    totalFrames++;

    for (const track of features.face_tracks ?? []) {
      const id = track.participant_name || track.audience_id;
      if (!participantStats.has(id)) {
        participantStats.set(id, {
          id,
          participant_name: track.participant_name,
          audience_id: track.audience_id,
          frames: 0,
          gaze_screen: 0,
          total_eye_openness: 0,
          total_mouth_openness: 0,
          total_nod_score: 0,
          nod_events: 0,
          total_smile: 0,
          smile_frames: 0,
          total_brow_down: 0,
          brow_frames: 0,
          gaze_directions: { screen: 0, left: 0, right: 0, up: 0, down: 0, unknown: 0 },
          attention_scores: [],
          t_start: event.t_ms,
          t_end: event.t_ms
        });
      }

      const stats = participantStats.get(id);
      stats.frames++;
      stats.t_end = event.t_ms;

      if (track.gaze_estimate === "screen") stats.gaze_screen++;
      stats.gaze_directions[track.gaze_estimate] = (stats.gaze_directions[track.gaze_estimate] ?? 0) + 1;

      if (track.eye_openness) {
        stats.total_eye_openness += (track.eye_openness.left + track.eye_openness.right) / 2;
      }
      stats.total_mouth_openness += track.mouth_openness ?? 0;
      stats.total_nod_score += track.gestures?.nod_score ?? 0;
      stats.nod_events += track.gestures?.nod_count ?? 0;

      const smile = track.blendshapes
        ? ((track.blendshapes.mouthSmileLeft ?? 0) + (track.blendshapes.mouthSmileRight ?? 0)) / 2
        : null;
      if (smile != null) {
        stats.total_smile += smile;
        stats.smile_frames++;
      }
      const browDown = track.blendshapes
        ? ((track.blendshapes.browDownLeft ?? 0) + (track.blendshapes.browDownRight ?? 0)) / 2
        : null;
      if (browDown != null) {
        stats.total_brow_down += browDown;
        stats.brow_frames++;
      }
    }
  }

  // Room-level aggregation
  const roomEngagementTimeline = featureEvents
    .filter((e) => e.features?.room_engagement)
    .map((e) => ({
      t_ms: e.t_ms,
      ...e.features.room_engagement,
      motion: e.features.motion_score,
      face_count: e.features.face_count
    }));

  // Merge realtime stats with Gemini per-participant insights
  const participants = [];
  const geminiParticipants = gemini?.participants ?? [];

  for (const [, stats] of participantStats) {
    const avg = (total, count) => count > 0 ? round(total / count) : 0;

    // Try to match with Gemini participant by name/position
    const geminiMatch = findGeminiMatch(stats, geminiParticipants);

    participants.push({
      id: stats.id,
      participant_name: stats.participant_name,
      audience_id: stats.audience_id,
      duration_sec: Math.round((stats.t_end - stats.t_start) / 1000),
      visible_frames: stats.frames,
      realtime: {
        gaze_screen_ratio: avg(stats.gaze_screen, stats.frames),
        avg_eye_openness: avg(stats.total_eye_openness, stats.frames),
        avg_mouth_openness: avg(stats.total_mouth_openness, stats.frames),
        avg_nod_score: avg(stats.total_nod_score, stats.frames),
        total_nod_events: stats.nod_events,
        avg_smile: avg(stats.total_smile, stats.smile_frames),
        avg_brow_down: avg(stats.total_brow_down, stats.brow_frames),
        gaze_distribution: stats.gaze_directions
      },
      gemini: geminiMatch ? {
        overall_engagement: geminiMatch.overall_engagement,
        engagement_summary: geminiMatch.engagement_summary,
        timeline: geminiMatch.timeline
      } : null
    });
  }

  // If Gemini has participants not in realtime data, add them too
  for (const gp of geminiParticipants) {
    const alreadyMatched = participants.some((p) => p.gemini && findGeminiMatch(p, [gp]));
    if (!alreadyMatched) {
      const existingById = participants.find((p) =>
        p.participant_name === gp.name_or_position || p.id === gp.name_or_position
      );
      if (!existingById) {
        participants.push({
          id: gp.name_or_position,
          participant_name: gp.name_or_position,
          audience_id: null,
          duration_sec: 0,
          visible_frames: 0,
          realtime: null,
          gemini: {
            overall_engagement: gp.overall_engagement,
            engagement_summary: gp.engagement_summary,
            timeline: gp.timeline
          }
        });
      }
    }
  }

  return {
    session_id: sessionId,
    generated_at: new Date().toISOString(),
    total_frames: totalFrames,
    duration_sec: featureEvents.length > 0
      ? Math.round((featureEvents[featureEvents.length - 1].t_ms - featureEvents[0].t_ms) / 1000)
      : 0,
    participants,
    meeting_summary: gemini?.meeting_summary ?? null,
    room_engagement_timeline: roomEngagementTimeline
  };
}

function findGeminiMatch(stats, geminiParticipants) {
  if (!geminiParticipants.length) return null;

  // Exact name match
  if (stats.participant_name) {
    const match = geminiParticipants.find((gp) =>
      gp.name_or_position === stats.participant_name
    );
    if (match) return match;
  }

  // Position-based heuristic: if only one participant in both, match them
  if (geminiParticipants.length === 1 && stats.frames > 0) {
    return geminiParticipants[0];
  }

  return null;
}

function renderUnifiedReport(report) {
  const el = elements.unifiedReport;
  let html = "";

  // Meeting overview
  html += `<h3>Meeting Overview</h3>`;
  html += `<table>`;
  html += `<tr><th>Session</th><td>${report.session_id}</td></tr>`;
  html += `<tr><th>Duration</th><td>${formatDuration(report.duration_sec)}</td></tr>`;
  html += `<tr><th>Total Frames</th><td>${report.total_frames}</td></tr>`;
  html += `<tr><th>Participants</th><td>${report.participants.length}</td></tr>`;
  html += `</table>`;

  // Meeting summary from Gemini
  if (report.meeting_summary) {
    const ms = report.meeting_summary;
    html += `<h3>Meeting Summary (Gemini)</h3>`;
    html += `<p>Overall: ${engagementTag(ms.overall_engagement)}</p>`;
    if (ms.key_moments?.length) {
      html += `<ul>${ms.key_moments.map((m) => `<li>${escapeHtml(m)}</li>`).join("")}</ul>`;
    }
    if (ms.recommendations?.length) {
      html += `<p><strong>Recommendations:</strong></p>`;
      html += `<ul>${ms.recommendations.map((r) => `<li>${escapeHtml(r)}</li>`).join("")}</ul>`;
    }
  }

  // Per-participant details
  html += `<h3>Participants</h3>`;
  for (const p of report.participants) {
    html += `<table>`;
    html += `<tr><th colspan="2">${escapeHtml(p.participant_name || p.id)}`;
    if (p.gemini) html += ` ${engagementTag(p.gemini.overall_engagement)}`;
    html += `</th></tr>`;

    if (p.realtime) {
      html += `<tr><th>Visible</th><td>${formatDuration(p.duration_sec)} (${p.visible_frames} frames)</td></tr>`;
      html += `<tr><th>Gaze on screen</th><td>${pct(p.realtime.gaze_screen_ratio)}</td></tr>`;
      html += `<tr><th>Avg eye openness</th><td>${p.realtime.avg_eye_openness}</td></tr>`;
      html += `<tr><th>Avg smile</th><td>${p.realtime.avg_smile}</td></tr>`;
      html += `<tr><th>Nod events</th><td>${p.realtime.total_nod_events} (avg score: ${p.realtime.avg_nod_score})</td></tr>`;
    }

    if (p.gemini) {
      html += `<tr><th>Gemini summary</th><td>${escapeHtml(p.gemini.engagement_summary ?? "-")}</td></tr>`;
      if (p.gemini.timeline?.length) {
        html += `<tr><th>Timeline</th><td>`;
        html += `<table>`;
        html += `<tr><th>Time</th><th>Expression</th><th>Level</th><th>Reaction</th></tr>`;
        for (const t of p.gemini.timeline) {
          html += `<tr>`;
          html += `<td>${escapeHtml(t.time_range ?? "")}</td>`;
          html += `<td>${escapeHtml(t.expression ?? "")}</td>`;
          html += `<td>${engagementTag(t.engagement_level)}</td>`;
          html += `<td>${escapeHtml(t.notable_reaction ?? "-")}</td>`;
          html += `</tr>`;
        }
        html += `</table></td></tr>`;
      }
    }

    html += `</table>`;
  }

  // Export button
  html += `<button class="report-export" onclick="this.dispatchEvent(new CustomEvent('export-report', {bubbles:true}))">レポートをJSONエクスポート</button>`;

  el.innerHTML = html;

  // Attach export handler
  el.querySelector(".report-export")?.addEventListener("click", () => {
    const blob = new Blob([JSON.stringify(report, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `unified_report_${sessionId}.json`;
    a.click();
    URL.revokeObjectURL(url);
    logEvent({ type: "unified_report_exported" });
  });
}

function engagementTag(level) {
  if (!level) return "";
  const cls = level === "high" ? "tag-high" : level === "medium" ? "tag-medium" : "tag-low";
  return `<span class="tag ${cls}">${escapeHtml(level)}</span>`;
}

function formatDuration(sec) {
  if (!sec) return "0s";
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return m > 0 ? `${m}m ${s}s` : `${s}s`;
}

function pct(ratio) {
  return `${Math.round(ratio * 100)}%`;
}

function escapeHtml(str) {
  const div = document.createElement("div");
  div.textContent = String(str);
  return div.innerHTML;
}

// --- 雰囲気波形 + モーメント検出 ---

function startMoodMonitor() {
  moodHistory = [];
  moodTriggerMarks = [];
  snapshotBuffer = [];
  moodEma = null;
  moodBaseline = null;
  attentionEma = null;
  attentionBaseline = null;
  lastMoodTriggerTs = 0;
  moodTriggerArmed = true;
  nodActiveSince = null;
  lastNodTriggerTs = 0;
  activeExcursion = null;
  if (snapshotTimer) window.clearInterval(snapshotTimer);
  snapshotTimer = window.setInterval(captureSnapshotFrame, SNAPSHOT_INTERVAL_MS);
  drawMoodWave();
}

function stopMoodMonitor() {
  finalizeExcursion(Date.now()); // 進行中の区間があれば閉じる
  if (snapshotTimer) window.clearInterval(snapshotTimer);
  snapshotTimer = null;
  snapshotBuffer = [];
  moodHistory = [];
  moodTriggerMarks = [];
  drawMoodWave();
}

// room_engagement の合成値。attention_mean はベースライン偏差(0中心±0.02程度)
// なので 0.5 中心に増幅して使う。係数は実ログの分布に合わせた仮値。
function computeMoodScore(features) {
  const eng = features.room_engagement ?? {};
  const speech = features.speech ?? {};
  const valence = clamp(((eng.valence_mean ?? 0) + 1) / 2, 0, 1); // -1..1 → 0..1
  const attention = clamp(0.5 + 2.5 * (eng.attention_mean ?? 0), 0, 1); // 偏差を増幅
  const nod = clamp(eng.nod_ratio ?? 0, 0, 1);
  const speechRatio = Math.max(speech.self?.speech_ratio ?? 0, speech.other?.speech_ratio ?? 0);
  return {
    mood: clamp(0.4 * valence + 0.25 * attention + 0.2 * nod + 0.15 * speechRatio, 0, 1),
    attention: round(attention)
  };
}

function updateMoodMonitor(features) {
  // 較正中は room_engagement が全て0固定のため、そのまま使うと較正明けに
  // 段差が生じて誤発火する。較正明けの実測値からEMA/ベースラインを始める。
  if (features.room_engagement?.calibrating) return;

  const now = Date.now();
  const { mood: rawMood, attention: rawAttention } = computeMoodScore(features);

  // 短いEMAでフリッカーを均し、長いEMA(移動ベースライン)との差分=偏差を波形にする
  moodEma = moodEma == null ? rawMood : moodEma + MOOD_EMA_ALPHA * (rawMood - moodEma);
  moodBaseline = moodBaseline == null ? moodEma : moodBaseline + MOOD_BASELINE_ALPHA * (moodEma - moodBaseline);
  attentionEma = attentionEma == null ? rawAttention : attentionEma + MOOD_EMA_ALPHA * (rawAttention - attentionEma);
  attentionBaseline =
    attentionBaseline == null ? attentionEma : attentionBaseline + MOOD_BASELINE_ALPHA * (attentionEma - attentionBaseline);

  moodHistory.push({
    t_ms: now,
    mood: round(moodEma - moodBaseline),
    attention: round(attentionEma - attentionBaseline)
  });

  const cutoff = now - MOOD_WAVE_WINDOW_MS;
  while (moodHistory.length && moodHistory[0].t_ms < cutoff) moodHistory.shift();
  moodTriggerMarks = moodTriggerMarks.filter((mark) => mark.t_ms >= cutoff);

  detectMoodTrigger(now);
  detectNodTrigger(now, features);
  drawMoodWave();
}

function detectNodTrigger(now, features) {
  const ratio = features.room_engagement?.nod_ratio ?? 0;
  if (ratio < NOD_TRIGGER_RATIO) {
    nodActiveSince = null;
    return;
  }
  if (nodActiveSince == null) nodActiveSince = now;
  if (now - nodActiveSince < NOD_TRIGGER_MIN_MS) return;
  if (now - lastNodTriggerTs < NOD_TRIGGER_COOLDOWN_MS) return;

  lastNodTriggerTs = now;
  moodTriggerMarks.push({ t_ms: now, direction: "nod" });
  captureMoment({ t_ms: now, peak_t_ms: now, direction: "nod", delta: round(ratio) });
}

function detectMoodTrigger(now) {
  const latest = moodHistory[moodHistory.length - 1];
  if (!latest) return;
  const dev = latest.mood; // ベースラインからの偏差(±)

  // 発火後はベースライン付近に戻るまで再武装しない。
  // ピーク→平常への「戻り」を発火として扱わないためのヒステリシス。
  if (!moodTriggerArmed) {
    if (Math.abs(dev) < MOOD_TRIGGER_DELTA * MOOD_TRIGGER_REARM_RATIO) {
      moodTriggerArmed = true;
      finalizeExcursion(now); // 盛り上がり区間の終了=継続時間が確定
    }
    return;
  }

  if (moodHistory.length < MOOD_TRIGGER_MIN_SAMPLES) return;
  if (now - lastMoodTriggerTs < MOOD_TRIGGER_COOLDOWN_MS) return;
  if (Math.abs(dev) < MOOD_TRIGGER_DELTA) return;

  const direction = dev > 0 ? "rise" : "drop";
  moodTriggerArmed = false;
  lastMoodTriggerTs = now;
  moodTriggerMarks.push({ t_ms: now, direction });
  // 発火条件を満たしたまさにその時刻のフレームを使う
  captureMoment({ t_ms: now, peak_t_ms: now, direction, delta: round(Math.abs(dev)) });
}

// タブ全体(sourceVideo)を縮小JPEG化してリングバッファに積む
function captureSnapshotFrame() {
  const video = elements.sourceVideo;
  if (!video || !video.videoWidth) return;

  if (!snapshotCanvas) snapshotCanvas = document.createElement("canvas");
  const w = SNAPSHOT_WIDTH;
  const h = Math.round((video.videoHeight / video.videoWidth) * w) || 270;
  snapshotCanvas.width = w;
  snapshotCanvas.height = h;
  snapshotCanvas.getContext("2d").drawImage(video, 0, 0, w, h);

  const tMs = Date.now();
  snapshotCanvas.toBlob(
    (blob) => {
      if (!blob) return;
      snapshotBuffer.push({ t_ms: tMs, blob });
      if (snapshotBuffer.length === 1) {
        sendDiagnostic("snapshot_buffer_started", `first frame ${w}x${h} ~${Math.round(blob.size / 1024)}KB`);
      }
      const cutoff = Date.now() - SNAPSHOT_BUFFER_MS;
      while (snapshotBuffer.length && snapshotBuffer[0].t_ms < cutoff) snapshotBuffer.shift();
    },
    "image/jpeg",
    0.6
  );
}

function findClosestSnapshot(tMs) {
  let best = null;
  let bestDiff = Infinity;
  for (const snap of snapshotBuffer) {
    const diff = Math.abs(snap.t_ms - tMs);
    if (diff < bestDiff) {
      best = snap;
      bestDiff = diff;
    }
  }
  return best;
}

function captureMoment(trigger) {
  const snap = findClosestSnapshot(trigger.peak_t_ms);
  const moment = {
    t_ms: trigger.t_ms,
    direction: trigger.direction,
    delta: trigger.delta,
    snapshot_t_ms: snap?.t_ms ?? null,
    blob: snap?.blob ?? null,
    url: snap ? URL.createObjectURL(snap.blob) : null,
    features: structuredClone(latestFeatures)
  };

  moments.unshift(moment);
  while (moments.length > MAX_MOMENTS) {
    const removed = moments.pop();
    if (removed.url) URL.revokeObjectURL(removed.url);
  }

  renderMoments();
  // 画像本体は送らずメタデータのみWSへ(検証・将来のタイムライン記録用)
  const event = {
    type: "moment_trigger",
    session_id: sessionId,
    t_ms: trigger.t_ms,
    peak_t_ms: trigger.peak_t_ms,
    direction: trigger.direction,
    delta: trigger.delta,
    snapshot_t_ms: moment.snapshot_t_ms,
    snapshot_lag_ms: moment.snapshot_t_ms == null ? null : trigger.peak_t_ms - moment.snapshot_t_ms,
    has_snapshot: moment.blob != null,
    snapshot_buffer_size: snapshotBuffer.length
  };
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(event));
  logEvent(event);

  // rise/drop は「区間」として継続時間を追跡する(nod は瞬間イベントなので対象外)
  if (trigger.direction !== "nod") {
    activeExcursion = { moment, startTs: trigger.t_ms };
  }
}

// 偏差がベースライン付近へ戻った時点で盛り上がり区間を閉じ、継続時間を確定する
function finalizeExcursion(now) {
  if (!activeExcursion) return;
  const { moment, startTs } = activeExcursion;
  activeExcursion = null;

  moment.duration_ms = now - startTs;
  renderMoments();

  const event = {
    type: "moment_update",
    session_id: sessionId,
    t_ms: startTs,
    direction: moment.direction,
    duration_ms: moment.duration_ms
  };
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(event));
  logEvent(event);
}

function renderMoments() {
  const grid = elements.momentsGrid;
  if (!grid) return;

  elements.momentsEmpty.style.display = moments.length ? "none" : "";
  grid.textContent = "";

  for (const moment of moments) {
    const card = document.createElement("figure");
    card.className = "moment";

    if (moment.url) {
      const img = document.createElement("img");
      img.src = moment.url;
      img.alt = "moment snapshot";
      img.title = "クリックでJPEGを保存";
      img.addEventListener("click", () => downloadMoment(moment));
      card.appendChild(img);
    }

    const caption = document.createElement("figcaption");
    const time = document.createElement("span");
    time.textContent = new Date(moment.t_ms).toLocaleTimeString("ja-JP", { hour12: false });
    const dir = document.createElement("span");
    dir.className = `moment-dir ${moment.direction}`;
    const dirLabel = moment.direction === "rise" ? "↑" : moment.direction === "drop" ? "↓" : "NOD";
    const duration = moment.duration_ms != null ? ` · ${(moment.duration_ms / 1000).toFixed(1)}s` : "";
    dir.textContent = `${dirLabel} ${moment.delta.toFixed(2)}${duration}`;
    caption.append(time, dir);
    card.appendChild(caption);

    grid.appendChild(card);
  }
}

function downloadMoment(moment) {
  if (!moment.url) return;
  const a = document.createElement("a");
  a.href = moment.url;
  a.download = `moment_${sessionId}_${moment.t_ms}.jpg`;
  a.click();
}

function drawMoodWave() {
  const cv = elements.moodWave;
  if (!cv) return;

  const g = cv.getContext("2d");
  const W = cv.width;
  const H = cv.height;
  const now = Date.now();
  const t0 = now - MOOD_WAVE_WINDOW_MS;
  const toX = (t) => ((t - t0) / MOOD_WAVE_WINDOW_MS) * W;
  // 中心線=移動ベースライン。偏差±MOOD_WAVE_GAIN で上下端に達する
  const toY = (dev) => H / 2 - (clamp(dev, -MOOD_WAVE_GAIN, MOOD_WAVE_GAIN) / MOOD_WAVE_GAIN) * (H / 2 - 4);

  g.clearRect(0, 0, W, H);

  // ベースライン(中心線)
  g.strokeStyle = "rgba(230, 234, 242, 0.12)";
  g.lineWidth = 1;
  g.setLineDash([3, 5]);
  g.beginPath();
  g.moveTo(0, H / 2);
  g.lineTo(W, H / 2);
  g.stroke();
  g.setLineDash([]);

  for (const mark of moodTriggerMarks) {
    g.strokeStyle =
      mark.direction === "rise"
        ? "rgba(240, 166, 62, 0.55)"
        : mark.direction === "nod"
          ? "rgba(70, 211, 154, 0.55)"
          : "rgba(122, 162, 232, 0.55)";
    g.lineWidth = 1;
    g.beginPath();
    g.moveTo(toX(mark.t_ms), 0);
    g.lineTo(toX(mark.t_ms), H);
    g.stroke();
  }

  if (moodHistory.length < 2) return;

  // mood 偏差エリアの塗り(中心線との間。上=盛り上がり、下=冷え込み)
  g.fillStyle = "rgba(240, 166, 62, 0.15)";
  g.beginPath();
  g.moveTo(toX(moodHistory[0].t_ms), H / 2);
  for (const s of moodHistory) g.lineTo(toX(s.t_ms), toY(s.mood));
  g.lineTo(toX(moodHistory[moodHistory.length - 1].t_ms), H / 2);
  g.closePath();
  g.fill();

  drawWaveLine(g, moodHistory, (s) => s.attention, toX, toY, "rgba(128, 144, 166, 0.45)", 1);
  drawWaveLine(g, moodHistory, (s) => s.mood, toX, toY, "#f0a63e", 2);
}

function drawWaveLine(g, samples, pick, toX, toY, color, width) {
  g.strokeStyle = color;
  g.lineWidth = width;
  g.beginPath();
  for (let i = 0; i < samples.length; i += 1) {
    const x = toX(samples[i].t_ms);
    const y = toY(pick(samples[i]));
    if (i === 0) g.moveTo(x, y);
    else g.lineTo(x, y);
  }
  g.stroke();
}

// --- Session Storage (IndexedDB) ---

const SESSION_DB_NAME = "reaction_engine_sessions";
const SESSION_DB_VERSION = 1;
const SESSION_STORE_NAME = "events";

let sessionDb = null;

async function openSessionDb() {
  if (sessionDb) return sessionDb;
  return new Promise((resolve, reject) => {
    const request = indexedDB.open(SESSION_DB_NAME, SESSION_DB_VERSION);
    request.onupgradeneeded = () => {
      const db = request.result;
      if (!db.objectStoreNames.contains(SESSION_STORE_NAME)) {
        const store = db.createObjectStore(SESSION_STORE_NAME, { autoIncrement: true });
        store.createIndex("session_id", "session_id", { unique: false });
        store.createIndex("t_ms", "t_ms", { unique: false });
      }
    };
    request.onsuccess = () => {
      sessionDb = request.result;
      resolve(sessionDb);
    };
    request.onerror = () => reject(request.error);
  });
}

async function storeFeatureEvent(event) {
  try {
    const db = await openSessionDb();
    const tx = db.transaction(SESSION_STORE_NAME, "readwrite");
    tx.objectStore(SESSION_STORE_NAME).add(event);
  } catch (error) {
    console.debug("[reaction-engine] failed to store event", error);
  }
}

async function exportSessionData(targetSessionId) {
  const db = await openSessionDb();
  return new Promise((resolve, reject) => {
    const tx = db.transaction(SESSION_STORE_NAME, "readonly");
    const index = tx.objectStore(SESSION_STORE_NAME).index("session_id");
    const request = index.getAll(targetSessionId);
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
}

async function downloadSessionJson() {
  try {
    const events = await exportSessionData(sessionId);
    if (!events.length) {
      logEvent({ type: "export_error", message: "No data for current session" });
      return;
    }
    const blob = new Blob([JSON.stringify(events, null, 2)], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `${sessionId}.json`;
    a.click();
    URL.revokeObjectURL(url);
    logEvent({ type: "export_complete", message: `${events.length} events exported` });
  } catch (error) {
    logEvent({ type: "export_error", message: error.message });
  }
}

function createSessionId() {
  const random = crypto.getRandomValues(new Uint32Array(2));
  return `sess_${Date.now().toString(36)}_${random[0].toString(36)}${random[1].toString(36)}`;
}

function safeParse(value) {
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
}

function round(value) {
  return Math.round(value * 1000) / 1000;
}

function clamp(value, min, max) {
  return Math.min(max, Math.max(min, value));
}

drawEmptyPreview();
drawMoodWave();
