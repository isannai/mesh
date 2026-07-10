import React, { useState, useEffect, useCallback } from "react";
import { fetchAPIPolicy, updateAPIFeatures, applyAPIPreset } from "../../api/apiFeatures";
import { useTranslation } from "@i18n";
import "./settings-form.scss";

// Feature catalogue — kept in sync with pkg/broker/apipolicy/features.go.
// Order here drives the toggle list order in the UI.
const FEATURES = [
  { id: "info",                 title: "info",                 desc: "Health / version / node-id (always on)", locked: true },
  { id: "node_discovery",       title: "node_discovery",       desc: "Node catalog: /v1/nodes, /v1/metrics" },
  { id: "gate_proxy",           title: "gate_proxy",           desc: "Gate API proxy: /gate/v1/*" },
  { id: "auth_verify",          title: "auth_verify",          desc: "Wallet signature verification" },
  { id: "my_nodes",             title: "my_nodes",             desc: "Node-auth delegation: /v1/my-nodes/*" },
  { id: "pipeline",             title: "pipeline",             desc: "Pipeline Studio API" },
  { id: "node_proxy_svc",       title: "node_proxy_svc",       desc: "Service tunnel (sd-api / llm-api / vllm-api)" },
  { id: "node_proxy_provider",  title: "node_proxy_provider",  desc: "Node management API (provider-side wallet auth gates this)" },
  { id: "node_proxy_installer", title: "node_proxy_installer", desc: "Service install / uninstall SSE" },
];

export default function ApiTab() {
  const { t } = useTranslation();
  const [policy, setPolicy] = useState(null);
  const [draft, setDraft] = useState({}); // local map: id -> bool
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState("");

  const reload = useCallback(async () => {
    setLoading(true);
    const p = await fetchAPIPolicy();
    setPolicy(p);
    const enabled = new Set(p?.enabled_features || []);
    const next = {};
    for (const f of FEATURES) next[f.id] = enabled.has(f.id);
    setDraft(next);
    setLoading(false);
  }, []);

  useEffect(() => { reload(); }, [reload]);

  const isOn = (id) => !!draft[id];

  // Local toggle only — does not hit server. Save button below commits.
  const handleToggle = (id) => {
    if (FEATURES.find(f => f.id === id)?.locked) return;
    setDraft(prev => ({ ...prev, [id]: !prev[id] }));
    setStatus("");
  };

  const handleSave = async () => {
    setSaving(true);
    setStatus("");
    try {
      const features = {};
      for (const f of FEATURES) features[f.id] = { enabled: !!draft[f.id] };
      await updateAPIFeatures(features);
      await reload();
      setStatus("Saved.");
    } catch (e) {
      setStatus("Save failed: " + (e.message || e));
    }
    setSaving(false);
  };

  const handlePreset = async (name) => {
    setSaving(true);
    setStatus("");
    try {
      await applyAPIPreset(name);
      await reload();
      setStatus(`Preset "${name}" applied.`);
    } catch (e) {
      setStatus("Preset failed: " + (e.message || e));
    }
    setSaving(false);
  };

  if (loading) return <div className="settings-form-empty">{t("common.loading_short")}</div>;

  return (
    <div className="settings-form">
      <div className="settings-form-section">
        <h3 className="settings-form-section-title">API Features</h3>
        <p className="settings-form-section-desc">
          Per-feature on/off for backend endpoints. Owner sigs always bypass these gates;
          public clients hit 403 <code>feature_disabled</code> on disabled routes.
          Frontend cards/menus also read this to hide UI for endpoints that won't reach the server.
        </p>

        <div className="api-presets">
          <span className="api-presets-label">{t("settings.apply_preset")}</span>
          {(policy?.presets || []).map(name => (
            <button
              key={name}
              className="btn btn-sm"
              onClick={() => handlePreset(name)}
              disabled={saving}
            >
              {name}
            </button>
          ))}
        </div>

        <div className="cards-toggle-list mt-14">
          {FEATURES.map(f => (
            <label
              key={f.id}
              className={"cards-toggle-row" + (f.locked ? " cards-toggle-row-locked" : "")}
            >
              <input
                type="checkbox"
                checked={isOn(f.id)}
                onChange={() => handleToggle(f.id)}
                disabled={f.locked || saving}
              />
              <span className="cards-toggle-text">
                <span className="cards-toggle-title">
                  {f.title}
                  {f.locked && <span className="cards-toggle-badge">always on</span>}
                </span>
                <span className="cards-toggle-desc">{f.desc}</span>
              </span>
            </label>
          ))}
        </div>

        <div className="settings-form-actions">
          <button className="btn btn-primary" onClick={handleSave} disabled={saving}>
            {saving ? "Saving..." : "Save"}
          </button>
          {status && <span className="settings-form-status">{status}</span>}
        </div>
      </div>
    </div>
  );
}
