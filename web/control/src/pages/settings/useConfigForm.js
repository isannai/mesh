import { useState, useEffect, useCallback, useRef } from "react";
import { getAuthHeaders } from "@utils/wallet";

/**
 * Shared hook for loading/saving config.
 *
 * Modes:
 *   - useAdminApi: true  → GET/PUT /v1/admin/config  (broker self config)
 *   - nodeId set         → /node/{nodeId}/provider/config  (remote provider service)
 *   - neither            → invalid: hook will error out. /v1/local/config was
 *                          removed; broker's own service config goes through
 *                          /v1/admin/config or via the provider tunnel.
 *
 * @param {Object} opts
 * @param {string}  opts.serviceName  - e.g. "sd-api", "provider"
 * @param {string}  opts.nodeId       - target provider node id (required unless useAdminApi)
 * @param {boolean} opts.useAdminApi  - if true, uses GET/PUT /admin/config (broker local)
 * @param {string[]} opts.restartFields - keys that require restart when changed
 */
export default function useConfigForm({ serviceName, nodeId, useAdminApi = false, restartFields = [] } = {}) {
  const [config, setConfig] = useState(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState(null);
  // Separate from `error` so callers can distinguish "service config does
  // not exist on the node" (404 — service likely not installed yet) from
  // generic load failures (500, network drop, etc.). The 404 case wants
  // a "not installed" empty-state UI; other errors want a real error banner.
  const [notFound, setNotFound] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [restartRequired, setRestartRequired] = useState(false);
  const originalRef = useRef(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    setNotFound(false);
    try {
      let url;
      if (useAdminApi) {
        url = "/v1/admin/config";
      } else if (nodeId) {
        url = `/node/${encodeURIComponent(nodeId)}/provider/config?name=${encodeURIComponent(serviceName)}`;
      } else {
        throw new Error("useConfigForm: nodeId required (or set useAdminApi)");
      }
      const res = await fetch(url, { headers: { ...getAuthHeaders() } });
      if (res.status === 404) {
        setNotFound(true);
        setConfig(null);
        setLoading(false);
        return;
      }
      if (!res.ok) throw new Error(`Failed to load config (${res.status})`);
      const data = await res.json();
      setConfig(data);
      originalRef.current = JSON.parse(JSON.stringify(data));
      setDirty(false);
      setRestartRequired(false);
    } catch (e) {
      setError(e.message);
      setConfig(null);
    }
    setLoading(false);
  }, [serviceName, nodeId, useAdminApi]);

  useEffect(() => { load(); }, [load]);

  const setField = useCallback((key, value) => {
    setConfig(prev => {
      const next = { ...prev };
      const parts = key.split(".");
      if (parts.length === 1) {
        next[key] = value;
      } else {
        let obj = next;
        for (let i = 0; i < parts.length - 1; i++) {
          obj[parts[i]] = { ...obj[parts[i]] };
          obj = obj[parts[i]];
        }
        obj[parts[parts.length - 1]] = value;
      }
      setDirty(true);
      if (restartFields.includes(key)) setRestartRequired(true);
      return next;
    });
  }, [restartFields]);

  const save = useCallback(async () => {
    setSaving(true);
    setError(null);
    try {
      if (useAdminApi) {
        const res = await fetch("/v1/admin/config", {
          method: "PUT",
          headers: { "Content-Type": "application/json", ...getAuthHeaders() },
          body: JSON.stringify(config),
        });
        if (!res.ok) throw new Error(`Save failed (${res.status})`);
        const result = await res.json();
        if (result.restart_required) setRestartRequired(true);
      } else {
        if (!nodeId) throw new Error("useConfigForm.save: nodeId required");
        const url = `/node/${encodeURIComponent(nodeId)}/provider/config`;
        const res = await fetch(url, {
          method: "POST",
          headers: { "Content-Type": "application/json", ...getAuthHeaders() },
          body: JSON.stringify({ name: serviceName, config }),
        });
        if (!res.ok) throw new Error(`Save failed (${res.status})`);
      }
      originalRef.current = JSON.parse(JSON.stringify(config));
      setDirty(false);
    } catch (e) {
      setError(e.message);
    }
    setSaving(false);
  }, [config, serviceName, nodeId, useAdminApi]);

  return { config, setConfig, setField, save, load, loading, saving, dirty, restartRequired, error, notFound };
}
