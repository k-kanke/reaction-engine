import {
  FaceDetector as MediaPipeFaceDetector,
  FaceLandmarker,
  FilesetResolver
} from "../vendor/mediapipe/vision_bundle.mjs";

const ANALYSIS_INTERVAL_MS = 250;
const EVENT_INTERVAL_MS = 1000;
const PREVIEW_WIDTH = 640;
const PREVIEW_HEIGHT = 360;
const MEDIAPIPE_MODEL_PATH = "models/blaze_face_short_range.tflite";
const MEDIAPIPE_LANDMARKER_MODEL_PATH = "models/face_landmarker.task";
const GESTURE_HISTORY_MS = 3500;
const NOD_MIN_PITCH_DELTA = 0.08;
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
let faceLandmarkerBackend = null;
let tracks = [];
let trackHistory = new Map();
let nextTrackId = 1;
let latestTileSnapshot = null;

elements.sessionId.textContent = sessionId;
restoreSettings();
initEdgeVision();

elements.startButton.addEventListener("click", startCapture);
elements.stopButton.addEventListener("click", stopCapture);
elements.connectButton.addEventListener("click", toggleWebSocket);

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === "meet_tile_snapshot") {
    latestTileSnapshot = message;
    logEvent(message);
  }
});

async function restoreSettings() {
  const stored = await chrome.storage.local.get(["wsUrl"]);
  if (stored.wsUrl) elements.wsUrl.value = stored.wsUrl;
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
      numFaces: 4,
      outputFaceBlendshapes: false,
      minFaceDetectionConfidence: 0.45,
      minFacePresenceConfidence: 0.45,
      minTrackingConfidence: 0.45
    });

    return {
      modelVersion: "mediapipe-face-landmarker-v1",
      detect(source, timestampMs) {
        const result = landmarker.detectForVideo(source, timestampMs);
        return result.faceLandmarks.map((landmarks) => buildFacePartsFromLandmarks(landmarks));
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
  tracks = [];
  trackHistory = new Map();
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

  const faces = await analyzeFaces();
  const trackedFaces = updateTracks(faces);
  updateGestureHistory(trackedFaces);
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

async function detectFaceParts() {
  if (!faceLandmarkerBackend) return [];

  try {
    return faceLandmarkerBackend.detect(canvas, performance.now());
  } catch (error) {
    logEvent({ type: "face_landmarker_error", message: error.message });
    return [];
  }
}

async function analyzeFaces() {
  const [faces, faceParts] = await Promise.all([
    detectFaces(),
    detectFaceParts()
  ]);

  if (!faceParts.length) return faces;
  if (!faces.length) return faceParts.map((parts) => ({ ...parts.face_bbox, parts }));

  const unmatchedPartIndexes = new Set(faceParts.map((_, index) => index));
  const enrichedFaces = faces.map((face) => {
    const matchIndex = findClosestFacePartIndex(face, faceParts, unmatchedPartIndexes);
    if (matchIndex === null) return face;

    unmatchedPartIndexes.delete(matchIndex);
    return {
      ...face,
      parts: faceParts[matchIndex]
    };
  });

  for (const index of unmatchedPartIndexes) {
    const parts = faceParts[index];
    enrichedFaces.push({ ...parts.face_bbox, parts });
  }

  return enrichedFaces;
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

function findClosestFacePartIndex(face, faceParts, candidateIndexes) {
  let bestIndex = null;
  let bestDistance = Number.POSITIVE_INFINITY;

  for (const index of candidateIndexes) {
    const distance = bboxCenterDistance(face, faceParts[index].face_bbox);
    if (distance < bestDistance) {
      bestDistance = distance;
      bestIndex = index;
    }
  }

  return bestDistance < 0.22 ? bestIndex : null;
}

function buildFacePartsFromLandmarks(landmarks) {
  const face_bbox = bboxFromLandmarks(landmarks);
  const leftEye = summarizeLandmarkGroup(landmarks, [33, 133, 159, 145]);
  const rightEye = summarizeLandmarkGroup(landmarks, [362, 263, 386, 374]);
  const mouth = summarizeLandmarkGroup(landmarks, [61, 291, 13, 14]);
  const nose = summarizeLandmarkGroup(landmarks, [1, 4, 98, 327]);
  const headPose = estimateHeadPose(landmarks);

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
    gaze_estimate: estimateGazeFromHeadPose(headPose),
    landmark_count: landmarks.length
  };
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
    const direction = Math.abs(delta) < 0.015 ? 0 : Math.sign(delta);

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
    nod_score: round(clamp(maxAmplitude / 0.22, 0, 1))
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

function buildFeatures(faces, motionScore) {
  const faceVisible = faces.length > 0;
  const attentionScore = clamp((faceVisible ? 0.55 : 0.2) + motionScore * 0.35, 0, 1);
  const firstGaze = faces.find((face) => face.parts?.gaze_estimate)?.parts.gaze_estimate;
  const faceGestures = faces.map((face) => ({
    audience_id: face.audience_id,
    gestures: detectNodGesture(face.audience_id)
  }));
  const totalNodCount = faceGestures.reduce((sum, item) => sum + item.gestures.nod_count, 0);
  const maxNodScore = faceGestures.reduce((max, item) => Math.max(max, item.gestures.nod_score), 0);

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
      },
      face_parts: face.parts?.face_parts ?? null,
      eye_openness: face.parts?.eye_openness ?? null,
      mouth_openness: face.parts?.mouth_openness ?? null,
      head_pose_estimate: face.parts?.head_pose_estimate ?? null,
      gaze_estimate: face.parts?.gaze_estimate ?? "unknown",
      landmark_count: face.parts?.landmark_count ?? 0,
      gestures: faceGestures.find((item) => item.audience_id === face.audience_id)?.gestures ?? {
        nod_count: 0,
        nod_score: 0
      }
    })),
    motion_score: round(motionScore),
    attention_score: round(attentionScore),
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
    ctx.fillText(face.audience_id, face.x * PREVIEW_WIDTH, Math.max(14, face.y * PREVIEW_HEIGHT - 6));
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
