const crypto = require("crypto");
const http = require("http");

const PORT = Number(process.env.PORT || 8787);

const server = http.createServer((req, res) => {
  res.writeHead(200, { "content-type": "text/plain; charset=utf-8" });
  res.end("Reaction Engine WebSocket log server\n");
});

server.on("upgrade", (req, socket) => {
  if (req.headers.upgrade?.toLowerCase() !== "websocket") {
    socket.destroy();
    return;
  }

  const key = req.headers["sec-websocket-key"];
  if (!key) {
    socket.destroy();
    return;
  }

  const accept = crypto
    .createHash("sha1")
    .update(`${key}258EAFA5-E914-47DA-95CA-C5AB0DC85B11`)
    .digest("base64");

  socket.write([
    "HTTP/1.1 101 Switching Protocols",
    "Upgrade: websocket",
    "Connection: Upgrade",
    `Sec-WebSocket-Accept: ${accept}`,
    "",
    ""
  ].join("\r\n"));

  console.log(`[ws] connected ${req.url}`);

  let buffer = Buffer.alloc(0);

  socket.on("data", (chunk) => {
    buffer = Buffer.concat([buffer, chunk]);
    buffer = readFrames(buffer, (message) => {
      console.log(`[event] ${new Date().toISOString()}`);
      console.log(message);
      sendText(socket, JSON.stringify({
        type: "feedback_event",
        message: "WebSocket log server received feature_event",
        received_at: Date.now()
      }));
    });
  });

  socket.on("close", () => {
    console.log("[ws] disconnected");
  });

  socket.on("error", (error) => {
    console.error("[ws] error", error.message);
  });
});

server.listen(PORT, () => {
  console.log(`WebSocket log server listening on ws://localhost:${PORT}/realtime`);
});

function readFrames(buffer, onText) {
  let offset = 0;

  while (buffer.length - offset >= 2) {
    const first = buffer[offset];
    const second = buffer[offset + 1];
    const opcode = first & 0x0f;
    const masked = (second & 0x80) !== 0;
    let payloadLength = second & 0x7f;
    let headerLength = 2;

    if (payloadLength === 126) {
      if (buffer.length - offset < 4) break;
      payloadLength = buffer.readUInt16BE(offset + 2);
      headerLength = 4;
    } else if (payloadLength === 127) {
      if (buffer.length - offset < 10) break;
      const high = buffer.readUInt32BE(offset + 2);
      const low = buffer.readUInt32BE(offset + 6);
      payloadLength = high * 2 ** 32 + low;
      headerLength = 10;
    }

    const maskLength = masked ? 4 : 0;
    const frameLength = headerLength + maskLength + payloadLength;
    if (buffer.length - offset < frameLength) break;

    const maskStart = offset + headerLength;
    const payloadStart = maskStart + maskLength;
    const payload = Buffer.from(buffer.subarray(payloadStart, payloadStart + payloadLength));

    if (masked) {
      const mask = buffer.subarray(maskStart, maskStart + 4);
      for (let i = 0; i < payload.length; i += 1) {
        payload[i] ^= mask[i % 4];
      }
    }

    if (opcode === 0x1) {
      onText(payload.toString("utf8"));
    } else if (opcode === 0x8) {
      return Buffer.alloc(0);
    }

    offset += frameLength;
  }

  return buffer.subarray(offset);
}

function sendText(socket, text) {
  const payload = Buffer.from(text, "utf8");
  let header;

  if (payload.length < 126) {
    header = Buffer.from([0x81, payload.length]);
  } else if (payload.length < 65536) {
    header = Buffer.alloc(4);
    header[0] = 0x81;
    header[1] = 126;
    header.writeUInt16BE(payload.length, 2);
  } else {
    header = Buffer.alloc(10);
    header[0] = 0x81;
    header[1] = 127;
    header.writeUInt32BE(0, 2);
    header.writeUInt32BE(payload.length, 6);
  }

  socket.write(Buffer.concat([header, payload]));
}
