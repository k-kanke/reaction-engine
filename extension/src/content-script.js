const ROOT_ID = "reaction-engine-debug-root";

const TILE_SCAN_DEBOUNCE_MS = 400;
const TILE_SCAN_INTERVAL_MS = 2000;
const MIN_TILE_WIDTH_PX = 60;
const MIN_TILE_HEIGHT_PX = 60;
const MAX_ASCEND_LEVELS = 6;
const MAX_TILES = 30;
const NAME_MAX_LENGTH = 60;
const NON_NAME_LABEL_PATTERN =
  /mute|unmute|camera|microphone|more options|pin|unpin|volume|leave call|present|chat|participant|setting|reaction|raise hand|caption|host control|turn on|turn off|full screen|minimize|maximize|^more_|_off$|_on$/i;

function ensureDebugRoot() {
  let root = document.getElementById(ROOT_ID);
  if (root) return root;

  root = document.createElement("div");
  root.id = ROOT_ID;
  root.style.position = "fixed";
  root.style.inset = "0";
  root.style.pointerEvents = "none";
  root.style.zIndex = "2147483647";
  root.style.display = "none";
  document.documentElement.appendChild(root);
  return root;
}

function setDebugEnabled(enabled) {
  ensureDebugRoot().style.display = enabled ? "block" : "none";
}

chrome.runtime.onMessage.addListener((message) => {
  if (message?.type === "reaction_engine:set_debug_overlay") {
    setDebugEnabled(Boolean(message.enabled));
  }
});

ensureDebugRoot();

function cleanNameCandidate(value) {
  if (!value) return null;

  const text = value.trim().replace(/\s+/g, " ");
  if (!text || text.length > NAME_MAX_LENGTH) return null;
  if (NON_NAME_LABEL_PATTERN.test(text)) return null;
  if (/^[a-z_]+$/.test(text)) return null; // likely an icon font ligature

  return text;
}

function extractParticipantName(container) {
  const candidates = [];

  const ownLabel = container.getAttribute?.("aria-label");
  if (ownLabel) candidates.push(ownLabel);

  const labelledDescendants = container.querySelectorAll?.("[aria-label]") ?? [];
  for (const el of labelledDescendants) {
    candidates.push(el.getAttribute("aria-label"));
  }

  const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT);
  let node = walker.nextNode();
  while (node) {
    candidates.push(node.textContent);
    node = walker.nextNode();
  }

  for (const candidate of candidates) {
    const cleaned = cleanNameCandidate(candidate);
    if (cleaned) return cleaned;
  }

  return null;
}

function detectSpeakingCandidate(container) {
  const labelledDescendants = container.querySelectorAll?.("[aria-label]") ?? [];
  for (const el of labelledDescendants) {
    if (/speaking/i.test(el.getAttribute("aria-label") ?? "")) return true;
  }
  return false;
}

function findTileContainer(video) {
  let node = video.parentElement;
  let bestNode = node;

  for (let depth = 0; depth < MAX_ASCEND_LEVELS && node; depth += 1) {
    const rect = node.getBoundingClientRect();
    if (rect.width >= MIN_TILE_WIDTH_PX && rect.height >= MIN_TILE_HEIGHT_PX) {
      bestNode = node;
      if (extractParticipantName(node)) return node;
    }
    node = node.parentElement;
  }

  return bestNode;
}

function buildTileSnapshot() {
  const tiles = [];
  const seenContainers = new Set();
  const videos = Array.from(document.querySelectorAll("video"));

  for (const video of videos) {
    if (tiles.length >= MAX_TILES) break;

    try {
      const container = findTileContainer(video);
      if (!container || seenContainers.has(container)) continue;
      seenContainers.add(container);

      const rect = container.getBoundingClientRect();
      if (rect.width < MIN_TILE_WIDTH_PX || rect.height < MIN_TILE_HEIGHT_PX) continue;

      tiles.push({
        tile_id: `tile_${tiles.length + 1}`,
        participant_name: extractParticipantName(container),
        tile_bbox_viewport: {
          x: Math.round(rect.x),
          y: Math.round(rect.y),
          w: Math.round(rect.width),
          h: Math.round(rect.height)
        },
        is_speaking_candidate: detectSpeakingCandidate(container),
        source: "meet_dom"
      });
    } catch (error) {
      console.debug("[reaction-engine] tile extraction skipped one candidate", error);
    }
  }

  return tiles;
}

function sendTileSnapshot() {
  if (!chrome.runtime?.id) return; // extension context invalidated

  let tiles = [];
  try {
    tiles = buildTileSnapshot();
  } catch (error) {
    console.debug("[reaction-engine] meet tile snapshot failed", error);
    tiles = [];
  }

  const snapshot = {
    type: "meet_tile_snapshot",
    t_ms: Date.now(),
    tiles
  };

  console.debug("[reaction-engine] meet_tile_snapshot", snapshot);

  try {
    const result = chrome.runtime.sendMessage(snapshot);
    result?.catch?.(() => {});
  } catch (error) {
    console.debug("[reaction-engine] failed to send tile snapshot", error);
  }
}

let tileScanTimeout = null;

function scheduleTileScan(delayMs = TILE_SCAN_DEBOUNCE_MS) {
  if (tileScanTimeout) return;
  tileScanTimeout = window.setTimeout(() => {
    tileScanTimeout = null;
    sendTileSnapshot();
  }, delayMs);
}

try {
  const tileObserver = new MutationObserver(() => scheduleTileScan());
  tileObserver.observe(document.documentElement, {
    childList: true,
    subtree: true
  });

  window.setInterval(() => sendTileSnapshot(), TILE_SCAN_INTERVAL_MS);
  scheduleTileScan(1000);
} catch (error) {
  console.debug("[reaction-engine] meet tile observer setup failed", error);
}
