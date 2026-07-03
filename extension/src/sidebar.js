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
let analysisRunning = false;
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
      outputFaceBlendshapes: true,
      minFaceDetectionConfidence: 0.45,
      minFacePresenceConfidence: 0.45,
      minTrackingConfidence: 0.45
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
  if (analysisRunning) return;
  analysisRunning = true;

  try {
    const video = elements.sourceVideo;
    if (!video.videoWidth || !video.videoHeight) return;

    ctx.drawImage(video, 0, 0, PREVIEW_WIDTH, PREVIEW_HEIGHT);

    const imageData = ctx.getImageData(0, 0, PREVIEW_WIDTH, PREVIEW_HEIGHT);
    const motionScore = calculateMotionScore(imageData);
    previousFrame = imageData;

    // Create ImageBitmap for MediaPipe — avoids WebGL context issues in side panel
    const bitmap = await createImageBitmap(canvas);
    const faces = await analyzeFaces(bitmap);
    bitmap.close();
    const trackedFaces = updateTracks(faces);
    updateGestureHistory(trackedFaces);
    latestFeatures = buildFeatures(trackedFaces, motionScore);
    drawDebugFrame(trackedFaces, latestFeatures);
    updateMetrics(latestFeatures);
  } finally {
    analysisRunning = false;
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
  const leftEye = summarizeLandmarkGroup(landmarks, [33, 133, 159, 145]);
  const rightEye = summarizeLandmarkGroup(landmarks, [362, 263, 386, 374]);
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
  // Iris landmarks: left 468-472 (468=center), right 473-477 (473=center)
  // Eye corner landmarks: left inner 133, outer 33; right inner 362, outer 263
  const leftCenter = landmarks[468];
  const rightCenter = landmarks[473];
  if (!leftCenter || !rightCenter) return null;

  const leftInner = landmarks[133];
  const leftOuter = landmarks[33];
  const rightInner = landmarks[362];
  const rightOuter = landmarks[263];
  if (!leftInner || !leftOuter || !rightInner || !rightOuter) return null;

  // Iris position ratio within eye (0=outer corner, 1=inner corner)
  const leftRatioX = (leftCenter.x - leftOuter.x) / Math.max(0.001, leftInner.x - leftOuter.x);
  const rightRatioX = (rightCenter.x - rightOuter.x) / Math.max(0.001, rightInner.x - rightOuter.x);

  const leftTop = landmarks[159];
  const leftBottom = landmarks[145];
  const rightTop = landmarks[386];
  const rightBottom = landmarks[374];

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
