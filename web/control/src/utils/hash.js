// normalizeSHA256 lowercases, strips common prefixes (sha256:, 0x), and
// returns "" when the result isn't a 64-char hex string. Mirrors
// pkg/setup/filehash.go NormalizeSHA256 so browser-side queries hit the
// broker's index with the same canonical key (a user pasting "SHA256:ABC..."
// must match a stored "abc...").
export function normalizeSHA256(s) {
  if (!s) return "";
  let h = String(s).trim().toLowerCase();
  if (h.startsWith("sha256:")) h = h.slice(7);
  if (h.startsWith("0x")) h = h.slice(2);
  if (h.length !== 64) return "";
  for (let i = 0; i < h.length; i++) {
    const c = h.charCodeAt(i);
    const isDigit = c >= 48 && c <= 57;
    const isLowHex = c >= 97 && c <= 102;
    if (!isDigit && !isLowHex) return "";
  }
  return h;
}

// SHA256(fileSize as 8 bytes LE + first 64KB of file) -> hex first 12 chars
export async function computeFileHash(file) {
  const size = file.size;

  // File size as 8 bytes Little Endian
  const sizeBuf = new ArrayBuffer(8);
  const sizeView = new DataView(sizeBuf);
  sizeView.setBigUint64(0, BigInt(size), true); // true = little endian

  // Read first 64KB
  const chunk = file.slice(0, 64 * 1024);
  const chunkBuf = await chunk.arrayBuffer();

  // Concatenate sizeBuf + chunkBuf
  const combined = new Uint8Array(8 + chunkBuf.byteLength);
  combined.set(new Uint8Array(sizeBuf), 0);
  combined.set(new Uint8Array(chunkBuf), 8);

  // SHA-256
  const hashBuf = await crypto.subtle.digest("SHA-256", combined);
  const hashArr = Array.from(new Uint8Array(hashBuf));
  const hashHex = hashArr.map(b => b.toString(16).padStart(2, "0")).join("");

  return hashHex.slice(0, 12);
}
