// Returns { initials, gradient } for a node when no emblem image is available.
// Initials = first 2 hex chars after stripping the role prefix (s/p/b/c:) / "0x".
// Gradient = HSL hue derived from first 6 hex chars (consistent per node).
export function getEmblemPalette(nodeId) {
  const clean = (nodeId || "").replace(/^[psbc]:/i, "").replace(/^0x/i, "");
  const initials = (clean.slice(0, 2) || "??").toUpperCase();
  const seed = clean.slice(0, 6) || "808080";
  const hue = parseInt(seed, 16) % 360;
  const c1 = `hsl(${hue}, 60%, 55%)`;
  const c2 = `hsl(${hue}, 70%, 30%)`;
  return { initials, gradient: `linear-gradient(135deg, ${c1} 0%, ${c2} 100%)` };
}
