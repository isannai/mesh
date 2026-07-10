// SD API client — calls through Broker's unified service proxy:
// /node/{nodeId}/svc/{service}/*
//
// `service` defaults to "sd-api" for backward compatibility, but any
// OpenAI/SD-compatible service name (e.g. alternative image engines
// registered under a different service key) can be passed through.
import { fetchWithTimeout } from "./fetchUtil";
import { wrapSdCppExtraArgs } from "@utils/sdcpp";

function sdBase(nodeId, service = "sd-api") {
  return `/node/${encodeURIComponent(nodeId)}/svc/${encodeURIComponent(service)}`;
}

// nodeJobsBase points at the provider's JobsHandler at /v1/jobs* (not
// /svc/{name}/*). The new async submission flow: POST /v1/jobs with
// {service, path, params, wait:false} → 202 + jobID → poll /v1/jobs/{id}
// → fetch /v1/jobs/{id}/result. broker handleNodeProxy passes /v1/jobs*
// straight through to provider's HTTP server.
function nodeJobsBase(nodeId) {
  return `/node/${encodeURIComponent(nodeId)}/v1/jobs`;
}

async function sdGet(nodeId, path, service = "sd-api") {
  const res = await fetchWithTimeout(`${sdBase(nodeId, service)}${path}`);
  return res.json();
}

async function sdPost(nodeId, path, body, service = "sd-api", timeoutMs = 600000) {
  // 10 min default — sd.cpp image generation can take 30s~5min depending
  // on model size / steps / batch. fetchUtil's 10s default would abort
  // long-running gens before the response comes back.
  const res = await fetchWithTimeout(
    `${sdBase(nodeId, service)}${path}`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    },
    { timeoutMs }
  );
  const data = await res.json();
  if (!res.ok) {
    throw new Error(data.error || `HTTP ${res.status}`);
  }
  return data;
}

export function getHealth(nodeId, service = "sd-api") {
  return sdGet(nodeId, "/health", service);
}

export function getModels(nodeId, service = "sd-api") {
  return sdGet(nodeId, "/v1/models", service);
}

export function getQueueStats(nodeId, service = "sd-api") {
  return sdGet(nodeId, "/v1/queue/stats", service);
}

// generateImage submits an image gen job to the provider's queue and
// returns the submission response { job_id, service, position, ... }.
// Caller (e.g. node-detail/index.jsx) handles polling via pollJob /
// subscribeJob and fetches the final body with getJobResult.
//
// Replaces the legacy single long-polling POST against the engine — the
// HTTP layer can't safely hold a 30s~5min request open across the broker
// → isannd → peer isannd → provider HTTP/3 chain.
export async function generateImage(nodeId, params, service = "sd-api") {
  const wrapped = wrapSdCppExtraArgs(params);
  const submitRes = await fetchWithTimeout(
    nodeJobsBase(nodeId),
    {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        service,
        path: "/v1/images/generations",
        params: wrapped,
        wait: false,
      }),
    },
    { timeoutMs: 30000 }
  );
  const submit = await submitRes.json();
  if (!submitRes.ok) {
    throw new Error(submit.error || `submit HTTP ${submitRes.status}`);
  }
  return submit;
}

export function editImage(nodeId, params, service = "sd-api") {
  return sdPost(nodeId, "/v1/images/edits", wrapSdCppExtraArgs(params), service);
}

export function subscribeJob(nodeId, jobId, onUpdate, onError, service = "sd-api") {
  // SSE not implemented on the new HTTP path — provider's JobsHandler is
  // poll-based. Trigger onError immediately so the caller falls back to
  // the pollJob loop.
  setTimeout(() => { if (onError) onError(); }, 0);
  return { close: () => {} };
}

export async function pollJob(nodeId, jobId, service = "sd-api") {
  const res = await fetchWithTimeout(`${nodeJobsBase(nodeId)}/${encodeURIComponent(jobId)}`);
  return res.json();
}

export function getOutputUrl(nodeId, filename, service = "sd-api") {
  return `${sdBase(nodeId, service)}/outputs/${filename}`;
}

// Fetch the final result body of a finished job. poll/subscribe only carry
// progress metadata now — clients call this after seeing status="done".
// Pass { consume: true } to evict the job from the queue (and delete the
// on-disk artifact) immediately after a successful fetch.
// Returns one of:
//   { type: "image", url: <blob URL>, contentType }
//   { type: "json",  data, contentType }
//   { type: "text",  text, contentType }
export async function getJobResult(nodeId, jobId, service = "sd-api", { consume = false } = {}) {
  const qs = consume ? "?consume=true" : "";
  const res = await fetchWithTimeout(`${nodeJobsBase(nodeId)}/${encodeURIComponent(jobId)}/result${qs}`);
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    try {
      const j = await res.json();
      if (j?.error) msg = j.error;
    } catch (_) { /* non-JSON body */ }
    throw new Error(msg);
  }
  const contentType = res.headers.get("Content-Type") || "";
  if (contentType.startsWith("image/")) {
    const blob = await res.blob();
    return { type: "image", url: URL.createObjectURL(blob), contentType };
  }
  if (contentType.includes("json")) {
    return { type: "json", data: await res.json(), contentType };
  }
  return { type: "text", text: await res.text(), contentType };
}

// Explicitly delete a finished job. Returns true on 204; throws on
// 404/409/etc. with the server message.
export async function deleteJob(nodeId, jobId, service = "sd-api") {
  const res = await fetchWithTimeout(
    `${sdBase(nodeId, service)}/v1/jobs/${encodeURIComponent(jobId)}`,
    { method: "DELETE" }
  );
  if (res.status === 204) return true;
  let msg = `HTTP ${res.status}`;
  try {
    const j = await res.json();
    if (j?.error) msg = j.error;
  } catch (_) { /* ignore */ }
  throw new Error(msg);
}
