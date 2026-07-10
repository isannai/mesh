// Gate software API — ETag cache per type
import { getAuthHeaders } from "@utils/wallet";
import { fetchWithTimeout } from "./fetchUtil";

const swCache = {};
const swETag = {};

export async function fetchGateSoftware(type) {
  try {
    const headers = { ...getAuthHeaders() };
    if (swETag[type]) headers["If-None-Match"] = swETag[type];
    const res = await fetchWithTimeout(`/gate/v1/software?type=${encodeURIComponent(type)}`, { headers });
    if (res.status === 304 && swCache[type]) return swCache[type];
    const data = res.ok ? await res.json() : [];
    swCache[type] = data;
    swETag[type] = res.headers.get("ETag");
    return data;
  } catch {
    return [];
  }
}

// Package descriptor — ETag cache per type+name (404 포함)
const pkgCache = {};
const pkgETag = {};

export async function fetchGatePackage(type, name) {
  const key = `${type}:${name}`;
  try {
    const headers = { ...getAuthHeaders() };
    if (pkgETag[key]) headers["If-None-Match"] = pkgETag[key];
    const res = await fetchWithTimeout(`/gate/v1/software/package?type=${encodeURIComponent(type)}&name=${encodeURIComponent(name)}`, { headers });
    if (res.status === 304 && pkgCache[key] !== undefined) return pkgCache[key];
    const data = res.ok ? await res.json() : null;
    pkgCache[key] = data;
    pkgETag[key] = res.headers.get("ETag");
    return data;
  } catch {
    return null;
  }
}
