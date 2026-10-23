// Only persisted product QR images and public Session Artifacts are media sources.
export const qrImagePattern = /^\/app\/feishu\/provision\/([a-f0-9-]{36})\/qrcode$/;
export const maxMediaBytes = 5 * 1024 * 1024;
export const artifactContentPath = (sessionId, artifactId) =>
  `/v1/agents/sessions/${encodeURIComponent(sessionId)}/artifacts/${encodeURIComponent(artifactId)}/content`;
export function qrImages(text) {
  return [...new Set([...text.matchAll(/!\[[^\]\n]*\]\((\/app\/feishu\/provision\/[a-f0-9-]{36}\/qrcode)\)/g)].map((match) => match[1]))];
}
export function imageMime(bytes) {
  const starts = (signature) => signature.every((byte, index) => bytes[index] === byte);
  if (starts([137, 80, 78, 71, 13, 10, 26, 10])) return "image/png";
  if (starts([255, 216, 255])) return "image/jpeg";
  if (starts([71, 73, 70, 56]) && [55, 57].includes(bytes[4]) && bytes[5] === 97) return "image/gif";
  if (starts([82, 73, 70, 70]) && [87, 69, 66, 80].every((byte, i) => bytes[i + 8] === byte)) return "image/webp";
  return "";
}
export async function readMediaBytes(response) {
  if (Number(response.headers.get("content-length")) > maxMediaBytes) {
    await response.body?.cancel();
    throw new RangeError("附件不能超过 5 MiB。");
  }
  const reader = response.body?.getReader();
  if (!reader) throw new Error("附件内容不可用。");
  const chunks = [];
  let size = 0;
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      size += value.length;
      if (size > maxMediaBytes) throw new RangeError("附件不能超过 5 MiB。");
      chunks.push(value);
    }
  } finally {
    await reader.cancel();
  }
  const bytes = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.length; }
  return bytes;
}
