const ROOT_ID = "reaction-engine-debug-root";

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

