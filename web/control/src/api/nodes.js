// Broker nodes API
import { getAuthHeaders } from "@utils/wallet";
import { fetchWithTimeout } from "./fetchUtil";
import * as myNodesStore from "@utils/myNodesStore";

function authedFetch(url, opts = {}) {
  return fetchWithTimeout(url, {
    ...opts,
    headers: {
      ...(opts.headers || {}),
      ...getAuthHeaders(),
    },
  });
}

// Fetch nodes from Rendezvous — ETag cache
let nodesCache = null;
let nodesETag = null;

export async function fetchNodes() {
  const headers = { ...getAuthHeaders() };
  if (nodesETag) headers["If-None-Match"] = nodesETag;
  const res = await fetchWithTimeout("/v1/nodes", { headers });
  if (res.status === 304 && nodesCache) return nodesCache;
  const data = await res.json();
  nodesCache = data;
  nodesETag = res.headers.get("ETag");
  return data;
}

// Fetch curated models (Starter / Recommend cards on workspace home).
// `category` is optional — empty returns both. Per-category ETag cache so
// the two starter/recommend fetches don't share state.
const cmCache = {};
const cmETag = {};

export async function fetchCuratedModels(category = "") {
  const key = category || "_all";
  const headers = { ...getAuthHeaders() };
  if (cmETag[key]) headers["If-None-Match"] = cmETag[key];
  const url = "/gate/v1/curated-models" + (category ? `?category=${encodeURIComponent(category)}` : "");
  try {
    const res = await fetchWithTimeout(url, { headers });
    if (res.status === 304 && cmCache[key]) return cmCache[key];
    const data = await res.json();
    const arr = Array.isArray(data) ? data : [];
    // Cache only non-empty responses + valid ETag. A transient empty (Gate
    // glitch / cold cache / partial outage) returning [] then sticking via
    // 304-revalidate would hide curated sections on the workspace home for
    // the rest of the page lifetime. Skipping the cache on empty forces a
    // real refetch next call instead of locking the empty state in.
    if (arr.length > 0) {
      cmCache[key] = arr;
      cmETag[key] = res.headers.get("ETag");
    }
    return arr;
  } catch {
    return cmCache[key] || [];
  }
}

// Fetch Rendezvous list from Gate — ETag cache
let rvCache = null;
let rvETag = null;

export async function fetchRendezvousList() {
  const headers = { ...getAuthHeaders() };
  if (rvETag) headers["If-None-Match"] = rvETag;
  const res = await fetchWithTimeout("/gate/v1/rendezvous", { headers });
  if (res.status === 304 && rvCache) return rvCache;
  const data = await res.json();
  rvCache = data;
  rvETag = res.headers.get("ETag");
  return data;
}

// Fetch nodes by Rendezvous ID from Gate — ETag cache
let gateNodesCache = null;
let gateNodesETag = null;
let gateNodesCacheKey = null;

export async function fetchNodesByRendezvous(rvId, page = 1, limit = 10) {
  const params = new URLSearchParams({ rv_id: rvId, page, limit });
  const key = params.toString();
  const headers = { ...getAuthHeaders() };
  if (gateNodesETag && gateNodesCacheKey === key) headers["If-None-Match"] = gateNodesETag;
  const res = await fetchWithTimeout("/gate/v1/nodes?" + params, { headers });
  if (res.status === 304 && gateNodesCache) return gateNodesCache;
  const data = await res.json();
  gateNodesCache = data;
  gateNodesETag = res.headers.get("ETag");
  gateNodesCacheKey = key;
  return data;
}

// Parse an RV addr URL into {host, proto} for X-Forwarded-* headers.
// Accepts forms: "https://host:port", "http://host:port", or bare "host:port"
// (assumed https). Empty / unparseable → returns null so callers fall back
// to the default routing (no headers = broker uses its configured RV).
function parseRVAddr(addr) {
  if (!addr) return null;
  let proto = "https";
  let hostPart = addr;
  if (addr.startsWith("https://")) {
    hostPart = addr.slice(8);
  } else if (addr.startsWith("http://")) {
    proto = "http";
    hostPart = addr.slice(7);
  }
  // Strip any path / query — we only want host:port.
  const slash = hostPart.indexOf("/");
  if (slash >= 0) hostPart = hostPart.slice(0, slash);
  if (!hostPart) return null;
  return { host: hostPart, proto };
}

// Fetch nodes from a rendezvous server. Broker decides the upstream:
//   - X-Forwarded-Host set  → direct-dial that host (multi-RV path).
//   - X-Forwarded-Host empty → broker's configured RV (via isannd bridge).
// Earlier this endpoint took ?addr= and broker dialed it directly with
// http3.Transport — replaced by /rv/v1/nodes + header pair so the routing
// decision is colocated with the broker's allowlist enforcement.
export async function fetchNodesByRendezvousAddr(addr) {
  const target = parseRVAddr(addr);
  const headers = {};
  if (target) {
    headers["X-Forwarded-Host"] = target.host;
    headers["X-Forwarded-Proto"] = target.proto;
  }
  const res = await authedFetch("/rv/v1/nodes", { headers });
  return res.json();
}

// Fetch volatile hardware metrics from a rendezvous server. Same routing
// rules as fetchNodesByRendezvousAddr.
export async function fetchNodeMetricsByAddr(addr) {
  const target = parseRVAddr(addr);
  const headers = {};
  if (target) {
    headers["X-Forwarded-Host"] = target.host;
    headers["X-Forwarded-Proto"] = target.proto;
  }
  const res = await authedFetch("/rv/v1/metrics", { headers });
  return res.json();
}

// Fetch /v1/metrics — per-service volatile data (queue_depth, status, jobs).
// Broker proxies to its configured rendezvous.
// Shape: [{node_id, service, status, queue_depth, total_jobs_done, avg_job_sec, ...}]
export async function fetchMetrics() {
  try {
    const res = await authedFetch("/v1/metrics");
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data) ? data : [];
  } catch {
    return [];
  }
}

// Fetch metrics from a specific rendezvous (legacy /v1/nodes/metrics path).
export async function fetchMetricsByAddr(addr) {
  return fetchNodeMetricsByAddr(addr);
}

// Merge metric rows into nodes:
//   - service-level metrics (queue_depth, status, jobs) per service
//   - node-level liveness (conn_status, online, last_seen_ms) — moved here
//     from /v1/nodes so that endpoint stays ETag-stable.
// Rows shape: [{node_id, service, status, queue_depth, ..., updated_at,
//               conn_status, online}]
export function mergeMetricsIntoNodes(nodes, metricRows) {
  if (!Array.isArray(nodes) || !Array.isArray(metricRows) || metricRows.length === 0) return nodes;
  // Build: nodeID -> { serviceName -> row, _any -> row }  (_any = first row, used for node-level liveness)
  const byNode = {};
  for (const row of metricRows) {
    const nid = row.node_id;
    if (!nid) continue;
    if (!byNode[nid]) byNode[nid] = { _services: {} };
    byNode[nid]._services[row.service] = row;
    // Any row carries node-level conn_status/online (same value across rows of one node)
    if (!byNode[nid]._any) byNode[nid]._any = row;
  }
  for (const n of nodes) {
    const nid = n.id || n.node_id;
    const entry = byNode[nid];
    if (!entry) continue;
    // Node-level liveness
    if (entry._any) {
      n.conn_status = entry._any.conn_status;
      n.online = entry._any.online;
      n.last_seen_ms = entry._any.updated_at;
      // Node-level status (idle/busy) — fallback when /v1/nodes omits it.
      if (entry._any.status && !n.status) {
        n.status = entry._any.status;
      }
    }
    // Per-service merge
    if (Array.isArray(n.services)) {
      for (const s of n.services) {
        const row = entry._services[s.name];
        if (!row) continue;
        s.queue_depth = row.queue_depth || 0;
        s.total_jobs_done = row.total_jobs_done || 0;
        s.avg_job_sec = row.avg_job_sec || 0;
        s.last_job_ms = row.last_job_ms || 0;
        s.running_job_id = row.running_job_id || "";
        if (row.status) s.status = row.status;
      }
    }
  }
  return nodes;
}

// My Nodes — IndexedDB
export function fetchMyNodes() {
  return myNodesStore.listMyNodes();
}

export function addMyNode(id, label = "") {
  return myNodesStore.addMyNode(id, label);
}

export function deleteMyNode(id) {
  return myNodesStore.deleteMyNode(id);
}

export function updateMyNode(id, label) {
  return myNodesStore.updateMyNode(id, label);
}

// Authenticate a node — still server-side (Provider verification via QUIC)
export async function authMyNode(nodeId) {
  const res = await authedFetch("/v1/my-nodes/" + encodeURIComponent(nodeId) + "/auth", {
    method: "POST",
  });
  const data = await res.json();
  // Persist auth result to IndexedDB
  if (data.auth) {
    await myNodesStore.updateMyNodeAuth(nodeId, true);
  }
  return data;
}

// Available nodes for SD Studio — filter authed nodes from IndexedDB
export async function fetchAvailableNodes() {
  const nodes = await myNodesStore.listMyNodes();
  return { my_nodes: nodes.filter((n) => n.auth) };
}
