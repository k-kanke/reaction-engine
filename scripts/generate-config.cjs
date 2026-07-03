#!/usr/bin/env node
// Reads .env and generates extension/src/config.local.js
const fs = require("fs");
const path = require("path");

const envPath = path.resolve(__dirname, "..", ".env");
if (!fs.existsSync(envPath)) {
  console.error(".env file not found. Copy .env.example to .env and fill in your keys.");
  process.exit(1);
}

const env = Object.fromEntries(
  fs.readFileSync(envPath, "utf-8")
    .split("\n")
    .filter((line) => line.trim() && !line.startsWith("#"))
    .map((line) => {
      const idx = line.indexOf("=");
      return [line.slice(0, idx).trim(), line.slice(idx + 1).trim()];
    })
);

const outPath = path.resolve(__dirname, "..", "extension", "src", "config.local.js");
const content = `// Auto-generated from .env — do not edit manually\nexport const GEMINI_API_KEY = ${JSON.stringify(env.GEMINI_API_KEY || "")};\n`;

fs.writeFileSync(outPath, content);
console.log("Generated extension/src/config.local.js");
