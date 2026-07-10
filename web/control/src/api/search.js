// Wraps the broker's /v1/search/nodes endpoint (Phase 2) with auth headers
// and a simple ETag cache so repeated identical queries don't hit the
// network. The broker itself caches the underlying RV /v1/nodes response
// for ~5s, so even a cache miss here is cheap.
//
// Why: the search page hammers this on every Enter, and back/forward
// navigation will replay the same query — round-trip-free re-render is
// nicer than a flash of empty results.

import { fetchWithTimeout } from "./fetchUtil";
import { getAuthHeaders } from "@utils/wallet";

// Per-key ETag cache. Key = querystring (so "q=abc&type=hash" and
// "q=abc&type=auto" cache separately). Trade memory for cheap re-renders
// — these payloads are KB-sized.
const cache = new Map();  // key -> { data, etag }

// buildSearchKey builds the deterministic cache key from query+opts so the
// SearchPage can do a synchronous cache lookup at mount time (back-button
// flow) before falling back to a network call.
export function buildSearchKey(query, opts = {}) {
  const { type = "auto", order, filters } = opts;
  const params = new URLSearchParams();
  if (query) params.set("q", query);
  if (type) params.set("type", type);
  if (order) params.set("order", order);
  if (filters && typeof filters === "object") {
    for (const [k, v] of Object.entries(filters)) {
      if (v == null || v === "") continue;
      params.set(k, String(v));
    }
  }
  return params.toString();
}

// getCachedSearch returns the cached search response synchronously (no
// network round-trip) when present, or null. Used by the search page to
// render previous results instantly on browser-back navigation.
export function getCachedSearch(query, opts = {}) {
  const entry = cache.get(buildSearchKey(query, opts));
  return entry ? entry.data : null;
}

export async function searchNodes(query, opts = {}) {
  const {
    type = "auto",
    order,
    filters,
    signal,
  } = opts;

  const params = new URLSearchParams();
  if (query) params.set("q", query);
  if (type) params.set("type", type);
  if (order) params.set("order", order);
  if (filters && typeof filters === "object") {
    for (const [k, v] of Object.entries(filters)) {
      if (v == null || v === "") continue;
      params.set(k, String(v));
    }
  }
  const key = params.toString();
  const headers = { ...getAuthHeaders() };
  const cached = cache.get(key);
  if (cached?.etag) headers["If-None-Match"] = cached.etag;

  const res = await fetchWithTimeout(`/v1/search/nodes?${key}`, {
    headers,
    signal,
  });
  if (res.status === 304 && cached) return cached.data;
  if (!res.ok) {
    // Surface a parseable error — search page shows a "search failed" toast.
    throw new Error(`search failed: ${res.status}`);
  }
  const data = await res.json();
  cache.set(key, { data, etag: res.headers.get("ETag") || "" });
  return data;
}
