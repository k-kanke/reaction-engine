// Gemini API integration for recorded video analysis

const GEMINI_UPLOAD_URL = "https://generativelanguage.googleapis.com/upload/v1beta/files";
const GEMINI_GENERATE_URL = "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent";

const ANALYSIS_PROMPT = `You are analyzing a Google Meet recording. For each visible participant, analyze their engagement and reactions throughout the video.

Output a JSON object with this structure:
{
  "participants": [
    {
      "name_or_position": "string (name if visible, otherwise position like 'top-left')",
      "overall_engagement": "high | medium | low",
      "engagement_summary": "1-2 sentence summary",
      "timeline": [
        {
          "time_range": "MM:SS - MM:SS",
          "expression": "string (e.g. smiling, focused, distracted, nodding)",
          "engagement_level": "high | medium | low",
          "notable_reaction": "string or null"
        }
      ]
    }
  ],
  "meeting_summary": {
    "overall_engagement": "high | medium | low",
    "key_moments": ["string"],
    "recommendations": ["string"]
  }
}

The video is sampled at 1 frame per second. Analyze every second of the video. For each participant, report their facial expressions, body language, gaze direction, nodding, and any visible reactions. Be specific about timing (use MM:SS format).
Respond ONLY with the JSON object, no markdown fences.`;

export async function analyzeVideoWithGemini(apiKey, videoFile, onProgress) {
  onProgress?.("Uploading video...");

  // Step 1: Upload file
  const uploadResult = await uploadFile(apiKey, videoFile, onProgress);

  // Step 2: Wait for processing
  onProgress?.("Processing video...");
  await waitForFileReady(apiKey, uploadResult.name, onProgress);

  // Step 3: Generate analysis
  onProgress?.("Analyzing with Gemini...");
  const analysis = await generateAnalysis(apiKey, uploadResult.uri);

  return analysis;
}

async function uploadFile(apiKey, file, onProgress) {
  // Start resumable upload
  const startResponse = await fetch(`${GEMINI_UPLOAD_URL}?key=${apiKey}`, {
    method: "POST",
    headers: {
      "X-Goog-Upload-Protocol": "resumable",
      "X-Goog-Upload-Command": "start",
      "X-Goog-Upload-Header-Content-Length": file.size,
      "X-Goog-Upload-Header-Content-Type": file.type || "video/mp4",
      "Content-Type": "application/json"
    },
    body: JSON.stringify({
      file: { displayName: file.name }
    })
  });

  if (!startResponse.ok) {
    const text = await startResponse.text();
    throw new Error(`Upload start failed: ${startResponse.status} ${text}`);
  }

  const uploadUrl = startResponse.headers.get("X-Goog-Upload-URL");
  if (!uploadUrl) throw new Error("No upload URL returned");

  // Upload the file data
  onProgress?.(`Uploading ${Math.round(file.size / 1024 / 1024)}MB...`);

  const uploadResponse = await fetch(uploadUrl, {
    method: "PUT",
    headers: {
      "Content-Length": file.size,
      "X-Goog-Upload-Offset": "0",
      "X-Goog-Upload-Command": "upload, finalize"
    },
    body: file
  });

  if (!uploadResponse.ok) {
    const text = await uploadResponse.text();
    throw new Error(`Upload failed: ${uploadResponse.status} ${text}`);
  }

  const result = await uploadResponse.json();
  return { name: result.file.name, uri: result.file.uri };
}

async function waitForFileReady(apiKey, fileName, onProgress) {
  const fileUrl = `https://generativelanguage.googleapis.com/v1beta/${fileName}?key=${apiKey}`;
  const maxWait = 300000; // 5 minutes
  const start = Date.now();

  while (Date.now() - start < maxWait) {
    const response = await fetch(fileUrl);
    if (!response.ok) throw new Error(`File status check failed: ${response.status}`);

    const fileInfo = await response.json();
    if (fileInfo.state === "ACTIVE") return;
    if (fileInfo.state === "FAILED") throw new Error("File processing failed");

    onProgress?.(`Processing video... (${fileInfo.state})`);
    await new Promise((r) => setTimeout(r, 3000));
  }

  throw new Error("File processing timed out");
}

async function generateAnalysis(apiKey, fileUri) {
  const response = await fetch(`${GEMINI_GENERATE_URL}?key=${apiKey}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      contents: [
        {
          parts: [
            { fileData: { mimeType: "video/mp4", fileUri } },
            { text: ANALYSIS_PROMPT }
          ]
        }
      ],
      generationConfig: {
        temperature: 0.3,
        maxOutputTokens: 8192
      }
    })
  });

  if (!response.ok) {
    const text = await response.text();
    throw new Error(`Gemini API error: ${response.status} ${text}`);
  }

  const result = await response.json();
  const text = result.candidates?.[0]?.content?.parts?.[0]?.text;
  if (!text) throw new Error("No response from Gemini");

  try {
    return JSON.parse(text);
  } catch {
    // Gemini sometimes wraps in markdown
    const cleaned = text.replace(/^```json\n?/, "").replace(/\n?```$/, "");
    return JSON.parse(cleaned);
  }
}
