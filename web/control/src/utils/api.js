import config from "@config";
import { getAuthHeaders } from "@utils/wallet";

const BASE = config.apiBaseURL;

async function request(method, path, body) {
  const opts = {
    method,
    headers: { "Content-Type": "application/json", ...getAuthHeaders() },
  };
  if (body) opts.body = JSON.stringify(body);

  const res = await fetch(`${BASE}${path}`, opts);
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    const msg = data?.error || res.statusText;
    throw new Error(msg);
  }
  return data;
}

export const api = {
  get: (path) => request("GET", path),
  post: (path, body) => request("POST", path, body),
  put: (path, body) => request("PUT", path, body),
  del: (path) => request("DELETE", path),
};

// Full path fetch wrapper (no BASE prefix)
async function fetchJSON(method, url, body, opts = {}) {
  const fetchOpts = {
    method,
    headers: { "Content-Type": "application/json", ...getAuthHeaders(), ...(opts.headers || {}) },
    ...opts,
  };
  if (body) fetchOpts.body = JSON.stringify(body);

  const res = await fetch(url, fetchOpts);
  const data = await res.json().catch(() => null);
  if (!res.ok) {
    const msg = data?.error || `HTTP ${res.status}`;
    throw new Error(msg);
  }
  return data;
}

export const http = {
  get: (url, opts) => fetchJSON("GET", url, null, opts),
  post: (url, body, opts) => fetchJSON("POST", url, body, opts),
  put: (url, body, opts) => fetchJSON("PUT", url, body, opts),
  del: (url, opts) => fetchJSON("DELETE", url, null, opts),
};
