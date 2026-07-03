const statusEl = document.getElementById("status");

document.getElementById("grant").addEventListener("click", async () => {
  statusEl.textContent = "許可を要求中...";
  statusEl.className = "";
  try {
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    // 許可を取得するのが目的なので、トラックはすぐ停止する
    stream.getTracks().forEach((track) => track.stop());
    statusEl.textContent =
      "✅ マイクが許可されました。\nこのタブを閉じて、side panel の Start Capture を押してください。";
    statusEl.className = "ok";
  } catch (error) {
    statusEl.textContent = `❌ 失敗: ${error.name} ${error.message}\nchrome://settings/content/microphone や macOS のマイク設定も確認してください。`;
    statusEl.className = "err";
  }
});
