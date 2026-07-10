// Per-service profile management. Each service has a profile set at
// conf/profiles/<service>.json with one or more named profiles + an active
// selector. The provider's `/provider/profiles` and
// `/provider/active-profile` endpoints expose this to the broker UI.

import { getAuthHeaders } from "@utils/wallet";

/** Fetch the profile set for a service on a node.
 *  Returns { engine, active, profiles: [{name, label, values}] } or null. */
export async function fetchProfiles(nid, service) {
  if (!nid || !service) return null;
  try {
    const resp = await fetch(
      `/node/${encodeURIComponent(nid)}/provider/profiles?service=${encodeURIComponent(service)}`,
      { headers: { ...getAuthHeaders() } }
    );
    if (!resp.ok) return null;
    return await resp.json();
  } catch {
    return null;
  }
}

/** Owner-only — change the active profile and trigger a service restart. */
export async function setActiveProfile(nid, service, name) {
  const resp = await fetch(
    `/node/${encodeURIComponent(nid)}/provider/active-profile`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json", ...getAuthHeaders() },
      body: JSON.stringify({ service, name }),
    }
  );
  if (!resp.ok) throw new Error("setActiveProfile: HTTP " + resp.status);
  return resp.json();
}

/** Owner-only — create or update a profile. set_active=true makes it the
 *  active profile (triggering a service restart for managed services). */
export async function upsertProfile(nid, payload) {
  const resp = await fetch(
    `/node/${encodeURIComponent(nid)}/provider/profile`,
    {
      method: "POST",
      headers: { "Content-Type": "application/json", ...getAuthHeaders() },
      body: JSON.stringify(payload),
    }
  );
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    throw new Error("upsertProfile: " + (text || `HTTP ${resp.status}`));
  }
  return resp.json();
}

/** Owner-only — delete a profile from a service's set. Refused for the last
 *  remaining profile; when the deleted profile was active, the first
 *  remaining one becomes active and the service restarts. */
export async function deleteProfile(nid, service, name) {
  const url = `/node/${encodeURIComponent(nid)}/provider/profile`
    + `?service=${encodeURIComponent(service)}`
    + `&name=${encodeURIComponent(name)}`;
  const resp = await fetch(url, {
    method: "DELETE",
    headers: { ...getAuthHeaders() },
  });
  if (!resp.ok) {
    const text = await resp.text().catch(() => "");
    throw new Error("deleteProfile: " + (text || `HTTP ${resp.status}`));
  }
  return resp.json();
}
