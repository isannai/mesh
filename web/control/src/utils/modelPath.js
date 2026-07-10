// Shared helpers for rendering a service's model name with a
// human-readable HF / Civitai prefix derived from the package's
// download_url. Used by every node card surface (Trending Models,
// my-nodes, nodes, node-detail, search Running Nodes) so the same
// "owner/model" / "imported/model" format reads identically everywhere.

// extractModelOriginRepo returns the namespace prefix to lead the model
// label with:
//   HuggingFace download → "owner"           (e.g. "jc-builds")
//   Civitai download     → "<modelId>"       (numeric)
//   anything else / empty → "imported"       (placeholder, file:// imports)
export function extractModelOriginRepo(url) {
  if (!url) return "imported";
  let m;
  m = url.match(/^https?:\/\/(?:huggingface\.co|hf\.co)\/([^/]+)\//i);
  if (m) return m[1];
  m = url.match(/^https?:\/\/civitai\.com\/api\/download\/models\/(\d+)/i);
  if (m) return m[1];
  m = url.match(/^https?:\/\/civitai\.com\/models\/(\d+)/i);
  if (m) return m[1];
  return "imported";
}

// formatModelDisplay returns { prefix, name, muted } for rendering.
// `muted` is true when prefix is the "imported" placeholder so callers
// can dim that segment to signal "not a real namespace".
export function formatModelDisplay(modelName, originUrl) {
  if (!modelName) return { prefix: "", name: "", muted: false };
  const prefix = extractModelOriginRepo(originUrl);
  return { prefix, name: modelName, muted: prefix === "imported" };
}

// externalSearchURL returns the upstream-site search URL for a model so
// node card UIs can open Civitai / HF in a new tab on click.
//   Civitai (sd-api) → search Civitai by sha256 hash (round-trips to the
//                       exact model version page when the hash matches).
//                       Falls back to the file name when no hash is set.
//   HuggingFace      → HF model search with "owner/name" so the prefix
//                       narrows the result to the actual repo.
//   imported / unknown → HF model search with the bare model name.
// Returns null when there's nothing useful to search for.
export function externalSearchURL({ name, hash, originUrl }) {
  const url = originUrl || "";
  if (/^https?:\/\/civitai\.com/i.test(url)) {
    const q = hash || name || "";
    return q ? `https://civitai.com/search/models?query=${encodeURIComponent(q)}` : null;
  }
  if (/^https?:\/\/(?:huggingface\.co|hf\.co)/i.test(url)) {
    const owner = extractModelOriginRepo(url);
    const q = owner !== "imported" && name
      ? `${owner}/${name}`
      : (name || "");
    return q ? `https://huggingface.co/models?search=${encodeURIComponent(q)}` : null;
  }
  // Unknown / file:// import — fall back to a plain HF name search so
  // the operator still has a starting point even when there's no
  // origin metadata recorded.
  if (name) return `https://huggingface.co/models?search=${encodeURIComponent(name)}`;
  return null;
}

// modelSearchQuery picks the best round-trippable string to drop into
// the broker search bar when an operator clicks a model card:
//   Civitai  → sha256 hash (numeric model id won't text-search; hash hits
//              the by-hash endpoint cleanly)
//   HF       → "owner/name" path (HF text search is path-aware)
//   imported → name (text search; the hash won't match any external
//              catalog and would force a hash-only lookup that always
//              comes up empty)
// Falls back to the hash only when there is literally no name to use.
export function modelSearchQuery({ name, hash, originUrl }) {
  const url = originUrl || "";
  if (/^https?:\/\/civitai\.com/i.test(url)) {
    return hash || name || "";
  }
  if (/^https?:\/\/(?:huggingface\.co|hf\.co)\/([^/]+)\//i.test(url)) {
    const owner = extractModelOriginRepo(url);
    if (owner && owner !== "imported" && name) return `${owner}/${name}`;
    return name || hash || "";
  }
  return name || hash || "";
}
