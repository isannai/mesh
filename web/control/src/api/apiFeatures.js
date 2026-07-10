// Backend API feature toggles — owner-controlled gate for which broker
// endpoints are reachable. Mirrors the cards.json pattern (per-feature
// boolean), with optional preset buttons.

import { getAuthHeaders } from "@utils/wallet";

/**
 * Fetch current API policy (public).
 * Returns { enabled_features: [...], all_features: [...], presets: [...] }.
 */
export async function fetchAPIPolicy() {
  try {
    const resp = await fetch("/v1/api/policy");
    if (!resp.ok) return { enabled_features: [], all_features: [], presets: [] };
    return await resp.json();
  } catch {
    return { enabled_features: [], all_features: [], presets: [] };
  }
}

/** Owner-only — replace the entire feature toggle map. */
export async function updateAPIFeatures(features) {
  const resp = await fetch("/v1/admin/api-features", {
    method: "PUT",
    headers: { "Content-Type": "application/json", ...getAuthHeaders() },
    body: JSON.stringify({ features }),
  });
  if (!resp.ok) throw new Error("updateAPIFeatures: HTTP " + resp.status);
  return resp.json();
}

/** Owner-only — bulk-apply a named preset (central / personal). */
export async function applyAPIPreset(name) {
  const resp = await fetch("/v1/admin/api-features/preset", {
    method: "POST",
    headers: { "Content-Type": "application/json", ...getAuthHeaders() },
    body: JSON.stringify({ name }),
  });
  if (!resp.ok) throw new Error("applyAPIPreset: HTTP " + resp.status);
  return resp.json();
}

/** True if the named feature is currently enabled in the given policy snapshot. */
export function isFeatureEnabled(featureName, policy) {
  return Array.isArray(policy?.enabled_features) && policy.enabled_features.includes(featureName);
}
