// GPU display helpers shared by node card / sidebar / panel summaries.

// Strip "NVIDIA GeForce" / "NVIDIA" prefix so the card shows just "RTX 3060".
export function shortGpuName(name) {
  return (name || "")
    .replace(/NVIDIA\s+GeForce\s+/i, "")
    .replace(/NVIDIA\s+/i, "");
}

// Group a node's GPU list into a single-line summary like:
//   [RTX 3060]                       → "RTX 3060 X 1"
//   [RTX 3060, RTX 3060]             → "RTX 3060 X 2"
//   [RTX 4090, RTX 3060]             → "RTX 4090 X 1, RTX 3060 X 1"
//   [RTX 4090, RTX 4090, RTX 3060]   → "RTX 4090 X 2, RTX 3060 X 1"
// Empty / nil → null so callers can hide the row.
//
// Order follows the input slot order (slot 0's GPU appears first). Map
// preserves insertion order in JS, so we don't accidentally alphabetize.
export function summarizeGpus(gpus) {
  if (!Array.isArray(gpus) || gpus.length === 0) return null;
  const counts = new Map();
  for (const g of gpus) {
    const name = shortGpuName(g?.name);
    if (!name) continue;
    counts.set(name, (counts.get(name) || 0) + 1);
  }
  if (counts.size === 0) return null;
  return Array.from(counts.entries())
    .map(([name, n]) => `${name} X ${n}`)
    .join(", ");
}
