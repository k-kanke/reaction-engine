import {
  FaceDetector as MediaPipeFaceDetector,
  FilesetResolver
} from "../vendor/mediapipe/vision_bundle.mjs";

const ANALYSIS_INTERVAL_MS = 250;
const EVENT_INTERVAL_MS = 1000;
const PREVIEW_WIDTH = 640;
const PREVIEW_HEIGHT = 360;
const MEDIAPIPE_MODEL_PATH = "models/blaze_face_short_range.tflite";

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
  eventLog: document.getElementById("eventLog")
};

const canvas = elements.preview;
const ctx = canvas.getContext("2d", { willReadFrequently: true });

let sessionId = createSessionId();
let stream = null;
let analysisTimer = null;
let eventTimer = null;
let previousFrame = null;
let latestFeatures = createEmptyFeatures();
let ws = null;
let faceDetectorBackend = null;
let tracks = [];
let nextTrackId = 1;

elements.sessionId.textContent = sessionId;
restoreSettings();
initFaceDetector();

elements.startButton.addEventListener("click", startCapture);
elements.stopButton.addEventListener("click", stopCapture);
elements.connectButton.addEventListener("click", toggleWebSocket);

async function restoreSettings() {
  const stored = await chrome.storage.local.get(["wsUrl"]);
  if (stored.wsUrl) elements.wsUrl.value = stored.wsUrl;
}

async function initFaceDetector() {
  const mediaPipeDetector = await createMediaPipeFaceDetector();
  if (mediaPipeDetector) {
    faceDetectorBackend = mediaPipeDetector;
    logEvent({ type: "edge_vision_status", message: "MediaPipe FaceDetector enabled" });
    return;
  }

  const nativeDetector = createNativeFaceDetector();
  if (nativeDetector) {
    faceDetectorBackend = nativeDetector;
    logEvent({ type: "edge_vision_status", message: "Native FaceDetector enabled" });
    return;
  }

  logEvent({ type: "edge_vision_status", message: "Face detector unavailable; using motion-only fallback" });
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
      minDetectionConfidence: 0.45
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

async function startCapture() {
  try {
    setStatus("Requesting capture");
    stream = await navigator.mediaDevices.getDisplayMedia({
      video: {
        frameRate: { ideal: 15, max: 30 },
        width: { ideal: 1280 },
        height: { ideal: 720 }
      },
      audio: true
    });

    elements.sourceVideo.srcObject = stream;
    await elements.sourceVideo.play();

    stream.getVideoTracks()[0]?.addEventListener("ended", stopCapture);
    elements.startButton.disabled = true;
    elements.stopButton.disabled = false;
    previousFrame = null;

    analysisTimer = window.setInterval(runAnalysisFrame, ANALYSIS_INTERVAL_MS);
    eventTimer = window.setInterval(sendFeatureEvent, EVENT_INTERVAL_MS);
    setStatus("Capturing", "active");
  } catch (error) {
    setStatus("Capture failed", "error");
    logEvent({ type: "capture_error", message: error.message });
  }
}

function stopCapture() {
  if (analysisTimer) window.clearInterval(analysisTimer);
  if (eventTimer) window.clearInterval(eventTimer);
  analysisTimer = null;
  eventTimer = null;

  if (stream) {
    for (const track of stream.getTracks()) track.stop();
  }

  stream = null;
  elements.sourceVideo.srcObject = null;
  elements.startButton.disabled = false;
  elements.stopButton.disabled = true;
  previousFrame = null;
  latestFeatures = createEmptyFeatures();
  updateMetrics(latestFeatures);
  drawEmptyPreview();
  setStatus(ws ? "Connected" : "Idle", ws ? "active" : "");
}

async function runAnalysisFrame() {
  const video = elements.sourceVideo;
  if (!video.videoWidth || !video.videoHeight) return;

  ctx.drawImage(video, 0, 0, PREVIEW_WIDTH, PREVIEW_HEIGHT);

  const imageData = ctx.getImageData(0, 0, PREVIEW_WIDTH, PREVIEW_HEIGHT);
  const motionScore = calculateMotionScore(imageData);
  previousFrame = imageData;

  const faces = await detectFaces();
  const trackedFaces = updateTracks(faces);
  latestFeatures = buildFeatures(trackedFaces, motionScore);
  drawDebugFrame(trackedFaces, latestFeatures);
  updateMetrics(latestFeatures);
}

async function detectFaces() {
  if (!faceDetectorBackend) return [];

  try {
    return await faceDetectorBackend.detect(canvas, performance.now());
  } catch (error) {
    logEvent({ type: "face_detection_error", message: error.message });
    return [];
  }
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

function buildFeatures(faces, motionScore) {
  const faceVisible = faces.length > 0;
  const attentionScore = clamp((faceVisible ? 0.55 : 0.2) + motionScore * 0.35, 0, 1);

  return {
    face_visible: faceVisible,
    face_count: faces.length,
    face_tracks: faces.map((face) => ({
      audience_id: face.audience_id,
      face_bbox: {
        x: round(face.x),
        y: round(face.y),
        w: round(face.w),
        h: round(face.h)
      }
    })),
    motion_score: round(motionScore),
    attention_score: round(attentionScore),
    gaze_estimate: faceVisible ? "unknown" : "not_visible",
    client_model_version: faceDetectorBackend?.modelVersion ?? "motion-fallback-v1"
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
    ctx.fillText(face.audience_id, face.x * PREVIEW_WIDTH, Math.max(14, face.y * PREVIEW_HEIGHT - 6));
  }

  ctx.fillStyle = "rgba(16, 24, 40, 0.72)";
  ctx.fillRect(10, 10, 230, 58);
  ctx.fillStyle = "#ffffff";
  ctx.font = "13px system-ui, sans-serif";
  ctx.fillText(`faces: ${features.face_count}`, 20, 32);
  ctx.fillText(`motion: ${features.motion_score} attention: ${features.attention_score}`, 20, 54);
}

function drawEmptyPreview() {
  ctx.fillStyle = "#101828";
  ctx.fillRect(0, 0, PREVIEW_WIDTH, PREVIEW_HEIGHT);
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
    gaze_estimate: "not_visible",
    client_model_version: "uninitialized"
  };
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
