import React, { useState } from "react";
import { useTranslation } from "@i18n";
import Toggle from "@components/Toggle";
import useConfigForm from "./useConfigForm";
import "./settings-form.scss";

/** Convert snake_case key to Title Case label */
function keyToLabel(key) {
  return key
    .replace(/^_+/, "")
    .replace(/_/g, " ")
    .replace(/\b\w/g, c => c.toUpperCase());
}

/** Detect if a key name implies a path-style input */
function isPathKey(key) {
  return /_dir|_path|_bin/.test(key);
}

/** Detect if a key name implies monospace */
function isMonoKey(key) {
  return /port|addr|address/.test(key);
}

/** Render a single field based on type inference */
function JsonField({ keyPath, value, onChange }) {
  const key = keyPath.split(".").pop();

  // Skip keys starting with _
  if (key.startsWith("_")) return null;

  if (typeof value === "boolean") {
    return (
      <div className="detail-card-row">
        <span className="detail-card-label">{keyToLabel(key)}</span>
        <span className="detail-card-value">
          <Toggle value={value} onChange={onChange} />
        </span>
      </div>
    );
  }

  if (typeof value === "number") {
    const isDecimal = !Number.isInteger(value);
    return (
      <div className="detail-card-row">
        <span className="detail-card-label">{keyToLabel(key)}</span>
        <span className="detail-card-value">
          <input
            type="number"
            step={isDecimal ? 0.1 : 1}
            value={value}
            onChange={e => onChange(isDecimal ? parseFloat(e.target.value) : parseInt(e.target.value, 10))}
            style={isMonoKey(key) ? { fontFamily: "monospace" } : {}}
          />
        </span>
      </div>
    );
  }

  if (typeof value === "string") {
    return (
      <div className="detail-card-row">
        <span className="detail-card-label">{keyToLabel(key)}</span>
        <span className="detail-card-value">
          <input
            type="text"
            value={value}
            onChange={e => onChange(e.target.value)}
            className={`settings-full-input${(isMonoKey(key) || isPathKey(key)) ? " settings-mono-input-inline" : ""}`}
          />
        </span>
      </div>
    );
  }

  return null;
}

/** Recursively render JSON object as grouped form */
function JsonGroup({ data, prefix, onChange }) {
  if (!data || typeof data !== "object") return null;

  const scalars = [];
  const objects = [];

  Object.entries(data).forEach(([key, value]) => {
    if (key.startsWith("_")) return;
    const path = prefix ? `${prefix}.${key}` : key;
    if (value !== null && typeof value === "object" && !Array.isArray(value)) {
      objects.push({ key, path, value });
    } else if (!Array.isArray(value)) {
      scalars.push({ key, path, value });
    }
  });

  return (
    <>
      {scalars.map(({ key, path, value }) => (
        <JsonField
          key={path}
          keyPath={path}
          value={value}
          onChange={v => onChange(path, v)}
        />
      ))}
      {objects.map(({ key, path, value }) => (
        <div className="detail-card-group" key={path}>
          <div className="detail-card-group-title">{keyToLabel(key)}</div>
          <JsonGroup data={value} prefix={path} onChange={onChange} />
        </div>
      ))}
    </>
  );
}

export default function GenericJsonForm({ serviceName, nodeId }) {
  const { t } = useTranslation();
  const { config, setField, save, loading, saving, dirty, error, notFound } =
    useConfigForm({ serviceName, nodeId });
  const [showRaw, setShowRaw] = useState(false);
  const [rawText, setRawText] = useState("");

  if (loading) {
    return <div className="detail-card"><div className="detail-card-body"><p>{t("settings.loading")}</p></div></div>;
  }
  if (notFound) {
    return (
      <div className="detail-card"><div className="detail-card-body">
        <p className="settings-empty-title">{t("settings.not_installed_title")}: <code>{serviceName}</code></p>
        <p className="settings-empty-hint">{t("settings.not_installed_hint")}</p>
      </div></div>
    );
  }
  if (error) {
    return <div className="detail-card"><div className="detail-card-body"><p className="settings-error">{error}</p></div></div>;
  }
  if (!config) return null;

  const toggleRaw = () => {
    if (!showRaw) setRawText(JSON.stringify(config, null, 2));
    setShowRaw(!showRaw);
  };

  return (
    <div className="detail-card">
      <div className="detail-card-body">
        {error && (
          <div className="settings-error-banner">
            {error}
          </div>
        )}

        <div className="settings-raw-toggle-row">
          <button className="settings-raw-toggle" onClick={toggleRaw}>
            {showRaw ? "Form" : "JSON"}
          </button>
        </div>

        {showRaw ? (
          <textarea
            className="settings-raw-textarea"
            value={rawText}
            onChange={e => setRawText(e.target.value)}
          />
        ) : (
          <div className="detail-card-group">
            <JsonGroup data={config} prefix="" onChange={setField} />
          </div>
        )}

        <div className="settings-save-bar">
          <button className="btn btn-primary" onClick={save} disabled={!dirty || saving}>
            {saving ? t("settings.saving") : t("common.save")}
          </button>
        </div>
      </div>
    </div>
  );
}
