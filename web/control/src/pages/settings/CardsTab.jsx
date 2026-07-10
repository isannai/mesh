import React, { useState, useEffect, useCallback } from "react";
import { fetchCards, updateCards } from "../../api/cards";
import { useTranslation } from "@i18n";
import "./settings-form.scss";

// Card catalogue — kept in sync with workspace/index.jsx & sidebar.js.
const CARDS = [
  { id: "nodes",     title: "Nodes",            desc: "Discover provider nodes" },
  { id: "my-nodes",  title: "My Nodes",         desc: "Manage your own nodes" },
  { id: "pipeline",  title: "Pipeline Studio",  desc: "Multi-node pipeline editor" },
  { id: "resources", title: "Resources",        desc: "Guides and tutorials" },
  { id: "api",       title: "API",              desc: "REST API reference" },
  { id: "install",   title: "Install Provider", desc: "Provider installation guide" },
  { id: "settings",  title: "Settings",         desc: "Personal preferences (this page)" },
  { id: "logs",      title: "Logs",             desc: "Unified log viewer" },
];

export default function CardsTab() {
  const { t } = useTranslation();
  const [config, setConfig] = useState({});
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState("");

  useEffect(() => {
    fetchCards().then(c => { setConfig(c || {}); setLoading(false); });
  }, []);

  const isOn = useCallback((id) => {
    const e = config[id];
    if (!e) return true;
    return e.enabled !== false;
  }, [config]);

  const toggle = (id) => {
    setConfig(prev => {
      const cur = prev[id]?.enabled !== false;
      return { ...prev, [id]: { enabled: !cur } };
    });
    setStatus("");
  };

  const handleSave = async () => {
    setSaving(true);
    setStatus("");
    try {
      // Normalize — always send a complete map so the server has explicit
      // values for every card.
      const normalized = {};
      for (const c of CARDS) {
        normalized[c.id] = { enabled: isOn(c.id) };
      }
      await updateCards(normalized);
      setStatus("Saved.");
    } catch (e) {
      setStatus("Save failed: " + (e.message || e));
    }
    setSaving(false);
  };

  if (loading) return <div className="settings-form-empty">{t("common.loading_short")}</div>;

  return (
    <div className="settings-form">
      <div className="settings-form-section">
        <h3 className="settings-form-section-title">{t("settings.cards_section")}</h3>
        <p className="settings-form-section-desc">
          Toggle which cards appear in the workspace and sidebar. Saved to broker config.
        </p>

        <div className="cards-toggle-list">
          {CARDS.map(c => (
            <label key={c.id} className="cards-toggle-row">
              <input
                type="checkbox"
                checked={isOn(c.id)}
                onChange={() => toggle(c.id)}
              />
              <span className="cards-toggle-text">
                <span className="cards-toggle-title">{c.title}</span>
                <span className="cards-toggle-desc">{c.desc}</span>
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
