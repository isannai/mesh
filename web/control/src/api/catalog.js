// Browser-direct catalog adapters for HuggingFace and Civitai.
//
// Why browser-direct (not broker-proxied):
//   - HF/Civitai allow CORS from public origins
//   - Per-user IPs spread the rate limit instead of pooling on the broker
//   - Catalog search keeps working even when the broker is unreachable
//
// All adapters return the same normalized shape `CatalogItem`, so the search
// page renders results without caring about the source. Pagination is opaque
// (cursor-style) — callers pass back `nextCursor` from the previous response
// without inspecting its shape.
//
// Note on hashes: HF returns LFS sha256 in `siblings[].lfs.oid` for git-lfs
// files. Civitai returns `files[].hashes.SHA256`. Both are 64-hex strings;
// we normalize via @utils/hash::normalizeSHA256 before exposing them so the
// shape matches what the broker's /v1/search/nodes index stores.

import { fetchWithTimeout } from "./fetchUtil";
import { normalizeSHA256 } from "@utils/hash";

const HF_BASE = "https://huggingface.co";
const CIVITAI_BASE = "https://civitai.com";

// In-memory cache for catalog responses. Key = source + querystring,
// value = { data, etag, fetchedAt }. The browser also revalidates HF/Civitai
// via If-None-Match (both APIs return ETag), but that still costs a round-trip.
// This cache lets us paint instantly on back navigation while a background
// revalidate refreshes the data.
//
// TTL chosen so a search done 5 minutes ago returns immediately if the user
// re-navigates; longer than that, we show stale results until revalidation
// completes. The trade-off is cheap because catalog responses are small.
const TTL_MS = 5 * 60 * 1000;
const cache = new Map();

function cacheGet(key) {
  const e = cache.get(key);
  if (!e) return null;
  if (Date.now() - e.fetchedAt > TTL_MS * 4) {
    // Hard expiry — don't even use as stale.
    cache.delete(key);
    return null;
  }
  return e;
}

function cacheSet(key, data, etag) {
  cache.set(key, { data, etag: etag || "", fetchedAt: Date.now() });
}

// getCachedCatalog returns the cached catalog response synchronously when
// present (or null), so the SearchPage can paint immediately on back nav
// before issuing the revalidation fetch.
export function getCachedCatalog(source, query, opts = {}) {
  const key = `${source}|${cacheKeyFor(query, opts)}`;
  return cacheGet(key)?.data || null;
}

function cacheKeyFor(query, opts) {
  // Stable key — sort opts so option-order doesn't bust the cache.
  const flat = [];
  for (const k of Object.keys(opts).sort()) {
    const v = opts[k];
    if (v == null || v === "" || (Array.isArray(v) && v.length === 0)) continue;
    flat.push(`${k}=${Array.isArray(v) ? v.join(",") : v}`);
  }
  return `q=${query || ""};${flat.join(";")}`;
}

// CatalogItem shape (returned by every adapter):
//   {
//     source: "huggingface" | "civitai",
//     id:        "<source-specific id>",   // e.g. "stabilityai/stable-diffusion-xl-base-1.0"
//     name:      "<display name>",
//     description: "<short description>",
//     tags:      ["text-to-image", ...],
//     license:   "openrail-m",            // optional
//     downloads: 12000000,                // optional
//     likes:     28400,                   // optional
//     url:       "<canonical web url>",   // for "view source" link
//     thumbnail: "<image url>",           // optional
//     primaryHash: "abc...",              // pure hex, may be ""
//     primarySize: 4823373824,             // bytes — largest file's size
//     hashes:    [{ file, hash, size }],  // all known sha256s
//     kind:      "base" | "lora" | "vae" | "embedding" | "controlnet" | "other",
//     baseModel: "SDXL 1.0",              // Civitai's baseModel; HF tag-derived
//     raw:       { /* original response */ },
//   }

// searchHF queries HF model index. Returns { items, nextCursor }.
//
// HF /api/models supports:
//   - `search` for free-text query
//   - `filter` for tag filtering ("lora", "text-to-image", ...)
//   - `limit` + `cursor` for pagination
//
// We expand `siblings` to get file-level LFS hashes. HF caps card metadata
// at 1000 chars so we keep description short.
export async function searchHF(query, opts = {}) {
  const {
    limit = 20,
    cursor = "",
    filter = "",  // e.g. "text-to-image"
    sort = "downloads",
  } = opts;

  const params = new URLSearchParams();
  if (query) params.set("search", query);
  params.set("limit", String(limit));
  if (cursor) params.set("cursor", cursor);
  if (filter) params.set("filter", filter);
  params.set("sort", sort);
  // full=true returns siblings.lfs (sha256, size), likes, license, pipeline_tag
  // — `expand[]=siblings` alone returns siblings without lfs metadata, which
  // is why card-meta showed only downloads (no size, no hash, no likes).
  params.set("full", "true");

  const url = `${HF_BASE}/api/models?${params.toString()}`;
  const cacheKey = `hf|${cacheKeyFor(query, opts)}`;
  const cached = cacheGet(cacheKey);
  const headers = {};
  if (cached?.etag) headers["If-None-Match"] = cached.etag;

  const res = await fetchWithTimeout(url, { headers });
  // Revalidation hit — server says cached body is still good.
  if (res.status === 304 && cached) {
    return cached.data;
  }
  if (!res.ok) {
    // On any other error, fall back to stale cached data when present —
    // a transient HF outage shouldn't blank the user's results.
    if (cached) return cached.data;
    throw new Error(`HF search failed: ${res.status}`);
  }
  const data = await res.json();
  const items = (Array.isArray(data) ? data : []).map(normalizeHF);
  const link = res.headers.get("Link") || "";
  const nextCursor = parseHFNextCursor(link);
  const result = { items, nextCursor };
  cacheSet(cacheKey, result, res.headers.get("ETag"));
  return result;
}

// fetchHFById gets a single model with full metadata. Used by the model
// detail page (one-shot lookup by id, not a search) and by lazy enrichment
// in the catalog card when list summaries miss license/sha256/size.
//
// `?blobs=true` is REQUIRED to get siblings[].lfs.sha256 and size — without
// it HF returns siblings as bare `{ rfilename }` even on the detail endpoint.
// Verified empirically against TouchNight/Meissa-Qwen2.5-14B-Instruct-Q4_K_M-GGUF
// (no blobs=true → no lfs; with blobs=true → lfs.sha256 + size populated).
//
// Path encoding gotcha: HF wants literal slashes between owner and repo
// segments. `encodeURIComponent` would encode "/" → "%2F" and HF rejects
// that with 400 Bad Request. Encode each segment separately so special
// chars within names still get escaped but the path separator stays raw.
//
// Do NOT add `expand[]`. expand[] on the detail endpoint trims response to
// only the fields named, which hides the very LFS data we want.
export async function fetchHFById(modelId) {
  const path = String(modelId || "").split("/").map(encodeURIComponent).join("/");
  const url = `${HF_BASE}/api/models/${path}?blobs=true`;
  const res = await fetchWithTimeout(url, {});
  if (!res.ok) throw new Error(`HF fetch failed: ${res.status}`);
  return normalizeHF(await res.json());
}

function normalizeHF(m) {
  const id = m.modelId || m.id || "";
  const cardData = m.cardData || {};
  const tags = Array.isArray(m.tags) ? m.tags : [];
  const siblings = Array.isArray(m.siblings) ? m.siblings : [];

  // Extract sha256s from LFS files. HF stores them in siblings[i].lfs.oid
  // (sha256 hex) — strip prefixes via normalizeSHA256 to be safe.
  const hashes = [];
  let primarySize = 0;
  let primaryHash = "";
  for (const s of siblings) {
    const filename = s.rfilename || "";
    const lfs = s.lfs || {};
    const rawHash = lfs.oid || lfs.sha256 || "";
    const hash = normalizeSHA256(rawHash);
    if (!hash) continue;
    const size = lfs.size || 0;
    hashes.push({ file: filename, hash, size });
    if (size > primarySize) {
      primarySize = size;
      primaryHash = hash;
    }
  }

  return {
    source: "huggingface",
    id,
    name: cardData.name || id.split("/").pop() || id,
    description: cardData.description || "",
    tags,
    license: cardData.license || "",
    // HF returns two download counters: `downloads` (30-day rolling) and
    // `downloadsAllTime` (cumulative). The HF web UI shows all-time so we
    // prefer that when available — keeps our card numbers matching what the
    // operator sees on the source page.
    downloads: m.downloadsAllTime || m.downloads || 0,
    likes: m.likes || 0,
    url: `${HF_BASE}/${id}`,
    thumbnail: "",
    primaryHash,
    primarySize,
    hashes,
    kind: deriveKindFromHF(m, tags),
    baseModel: deriveBaseModelFromHF(tags),
    pipelineTag: m.pipeline_tag || cardData.pipeline_tag || "",
    libraryName: m.library_name || cardData.library_name || "",
    raw: m,
  };
}

function deriveKindFromHF(m, tags) {
  if (m.library_name === "peft") return "lora";
  for (const t of tags) {
    const lc = String(t).toLowerCase();
    if (lc === "lora" || lc === "lycoris" || lc === "locon") return "lora";
    if (lc === "vae") return "vae";
    if (lc === "embedding" || lc === "textual-inversion") return "embedding";
    if (lc === "controlnet") return "controlnet";
  }
  return "base";
}

function deriveBaseModelFromHF(tags) {
  // Heuristic — HF doesn't have a canonical baseModel field. Pull common
  // marker tags so the search result can show "SDXL" / "SD 1.5" badges.
  for (const t of tags) {
    const lc = String(t).toLowerCase();
    if (lc.includes("sdxl")) return "SDXL";
    if (lc.includes("sd-1.5") || lc === "stable-diffusion") return "SD 1.5";
    if (lc.includes("flux")) return "Flux";
    if (lc.includes("llama-3")) return "Llama-3";
    if (lc.includes("llama-2")) return "Llama-2";
    if (lc.includes("qwen")) return "Qwen";
  }
  return "";
}

// parseHFNextCursor pulls the next-cursor from the Link header.
// Format: `<https://...?cursor=XYZ&...>; rel="next", <...>; rel="prev"`.
function parseHFNextCursor(link) {
  if (!link) return "";
  const parts = link.split(",");
  for (const p of parts) {
    if (!p.includes('rel="next"')) continue;
    const m = p.match(/<([^>]+)>/);
    if (!m) continue;
    try {
      const url = new URL(m[1]);
      return url.searchParams.get("cursor") || "";
    } catch {
      return "";
    }
  }
  return "";
}

// searchCivitai queries Civitai model index. Same shape as searchHF.
//
// Civitai differs from HF:
//   - browse mode (no query) paginated by `page` integer
//   - search mode (with `query`) requires cursor-based pagination — passing
//     `page` alongside `query` returns 400: "Cannot use page param with
//     query search. Use cursor-based pagination."
//   - `types[]` filter for Checkpoint/LORA/VAE/...
//   - optional API key (Authorization: Bearer <key>) for private models
//   - response wrapped in `{ items, metadata }`
export async function searchCivitai(query, opts = {}) {
  const {
    limit = 20,
    cursor = "",
    types = [],                   // ["LORA"], ["Checkpoint"], etc.
    apiKey = "",
    sort = "",                    // optional: "Highest Rated" | "Most Downloaded" | "Newest". Empty = Civitai default.
  } = opts;

  const params = new URLSearchParams();
  if (query) params.set("query", query);
  params.set("limit", String(limit));
  if (sort) params.set("sort", sort);
  // Pagination: cursor-based when searching (required by Civitai), page-based
  // otherwise. Do not mix — passing both 400s.
  if (query) {
    if (cursor) params.set("cursor", cursor);
  } else {
    const page = cursor ? parseInt(cursor, 10) || 1 : 1;
    params.set("page", String(page));
  }
  for (const t of types) params.append("types", t);

  const url = `${CIVITAI_BASE}/api/v1/models?${params.toString()}`;
  const cacheKey = `civitai|${cacheKeyFor(query, opts)}`;
  const cached = cacheGet(cacheKey);
  const headers = {};
  if (apiKey) headers["Authorization"] = `Bearer ${apiKey}`;
  if (cached?.etag) headers["If-None-Match"] = cached.etag;

  const res = await fetchWithTimeout(url, { headers });
  if (res.status === 304 && cached) return cached.data;
  if (!res.ok) {
    if (cached) return cached.data;  // serve stale on transient failures
    throw new Error(`Civitai search failed: ${res.status}`);
  }
  const data = await res.json();
  const arr = Array.isArray(data.items) ? data.items : [];
  const items = arr.map(normalizeCivitai);
  const meta = data.metadata || {};
  const nextCursor = (meta.totalPages && page < meta.totalPages)
    ? String(page + 1)
    : "";
  const result = { items, nextCursor };
  cacheSet(cacheKey, result, res.headers.get("ETag") || res.headers.get("etag"));
  return result;
}

// fetchCivitaiByHash is the Civitai-specific shortcut for "which model has
// this sha256?". Returns a single CatalogItem or null. Used when a user
// pastes a hash into the search bar.
export async function fetchCivitaiByHash(sha256, opts = {}) {
  const norm = normalizeSHA256(sha256);
  if (!norm) return null;
  const headers = {};
  if (opts.apiKey) headers["Authorization"] = `Bearer ${opts.apiKey}`;
  const url = `${CIVITAI_BASE}/api/v1/model-versions/by-hash/${norm}`;
  const res = await fetchWithTimeout(url, { headers });
  if (res.status === 404) return null;
  if (!res.ok) throw new Error(`Civitai by-hash failed: ${res.status}`);
  const ver = await res.json();
  // by-hash returns a model-version, not a model. Synthesize the catalog
  // item from version data — most fields the UI cares about live there.
  const files = Array.isArray(ver.files) ? ver.files : [];
  const hashes = [];
  let primaryHash = "";
  let primarySize = 0;
  let primaryDownloadUrl = "";
  for (const f of files) {
    const h = normalizeSHA256(f.hashes?.SHA256);
    if (!h) continue;
    hashes.push({ file: f.name, hash: h, size: f.sizeKB ? f.sizeKB * 1024 : 0 });
    if (f.sizeKB && f.sizeKB * 1024 > primarySize) {
      primarySize = f.sizeKB * 1024;
      primaryHash = h;
      // Civitai's response already carries the canonical download URL
      // for this exact file — don't reconstruct with synthetic
      // ?type/format/size/fp params; use what the API gave us so the
      // bytes (and hash) match what civitai.com would download.
      primaryDownloadUrl = f.downloadUrl || "";
    }
  }
  return {
    source: "civitai",
    id: String(ver.modelId || ver.id || ""),
    // versionId = the model-VERSION id (= what `civitai.com/api/download/models/<id>`
    // expects). modelId alone is not enough to pick a downloadable file —
    // each model has many versions and downloads target a specific one.
    versionId: ver.id ? String(ver.id) : "",
    name: ver.model?.name || ver.name || "",
    description: ver.description || "",
    tags: [],
    license: "",
    downloads: ver.stats?.downloadCount || 0,
    likes: ver.stats?.thumbsUpCount || 0,
    url: `${CIVITAI_BASE}/models/${ver.modelId}?modelVersionId=${ver.id}`,
    thumbnail: ver.images?.[0]?.url || "",
    primaryHash,
    primarySize,
    primaryDownloadUrl,
    hashes,
    kind: civitaiTypeToKind(ver.model?.type || ""),
    baseModel: ver.baseModel || "",
    raw: ver,
  };
}

function normalizeCivitai(m) {
  const versions = Array.isArray(m.modelVersions) ? m.modelVersions : [];
  const latest = versions[0] || {};
  const files = Array.isArray(latest.files) ? latest.files : [];
  const hashes = [];
  let primaryHash = "";
  let primarySize = 0;
  for (const f of files) {
    const h = normalizeSHA256(f.hashes?.SHA256);
    if (!h) continue;
    const size = f.sizeKB ? f.sizeKB * 1024 : 0;
    hashes.push({ file: f.name, hash: h, size });
    if (size > primarySize) {
      primarySize = size;
      primaryHash = h;
    }
  }
  const thumbnail = latest.images?.[0]?.url || m.images?.[0]?.url || "";
  return {
    source: "civitai",
    id: String(m.id),
    // Civitai's per-file download URL embeds the version ID, not the
    // model ID — exposing versionId here lets matching logic build the
    // same key the disk's download_url produces (api/download/models/<versionId>).
    versionId: latest.id ? String(latest.id) : "",
    name: m.name || "",
    description: m.description || "",
    tags: Array.isArray(m.tags) ? m.tags : [],
    license: "",
    downloads: m.stats?.downloadCount || 0,
    likes: m.stats?.thumbsUpCount || 0,
    url: `${CIVITAI_BASE}/models/${m.id}`,
    thumbnail,
    primaryHash,
    primarySize,
    hashes,
    kind: civitaiTypeToKind(m.type),
    baseModel: latest.baseModel || "",
    raw: m,
  };
}

function civitaiTypeToKind(type) {
  switch (String(type || "").toUpperCase()) {
    case "LORA":
    case "LOCON":
    case "LYCORIS":
      return "lora";
    case "VAE":
      return "vae";
    case "TEXTUALINVERSION":
      return "embedding";
    case "CONTROLNET":
      return "controlnet";
    case "CHECKPOINT":
      return "base";
    default:
      return "other";
  }
}

// searchCatalogs runs HF text search only. Civitai is intentionally not
// queried for text: the public /api/v1/models endpoint matches only the
// model-name field (database substring), while Civitai's web UI uses
// Meilisearch and hits descriptions / tags / file names. The result is
// the API returns 0 hits where the web finds thousands — confusing.
// Civitai stays bound to fetchCivitaiByHash for sha256 paste-ins; the
// search UI nudges users to grab a hash from Civitai web for those.
export async function searchCatalogs(query, opts = {}) {
  const errors = {};
  const cursors = {};
  let items = [];
  try {
    const r = await searchHF(query, opts.hf || {});
    items = r.items || [];
    if (r.nextCursor) cursors.huggingface = r.nextCursor;
  } catch (err) {
    errors.huggingface = String(err);
  }
  return { items, errors, cursors };
}
