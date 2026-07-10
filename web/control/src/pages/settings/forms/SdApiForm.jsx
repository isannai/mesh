import React from "react";
import { useTranslation } from "@i18n";
import Toggle, { RestartBadge } from "@components/Toggle";
import Dropdown from "@components/Dropdown/Dropdown";
import useConfigForm from "../useConfigForm";
import "../settings-form.scss";

const RESTART_FIELDS = ["manifest", "model.dir", "threads"];

export default function SdApiForm({ serviceName, nodeId }) {
  const { t } = useTranslation();
  const { config, setField, save, loading, saving, dirty, restartRequired, error, notFound } =
    useConfigForm({ serviceName: serviceName || "sd-api", nodeId, restartFields: RESTART_FIELDS });

  if (loading) return <div className="detail-card"><div className="detail-card-body"><p>{t("settings.loading")}</p></div></div>;
  if (notFound) return (
    <div className="detail-card"><div className="detail-card-body">
      <p className="settings-empty-title">{t("settings.not_installed_title")}: <code>{serviceName || "sd-api"}</code></p>
      <p className="settings-empty-hint">{t("settings.not_installed_hint")}</p>
    </div></div>
  );
  if (error) return <div className="detail-card"><div className="detail-card-body"><p className="settings-error">{error}</p></div></div>;
  if (!config) return null;

  const model = config.model || {};
  const output = config.output || {};

  return (
    <div className="detail-card">
      <div className="detail-card-body">
        {restartRequired && (
          <div className="settings-warn-banner">
            {t("settings.restart_required")}
          </div>
        )}
        {error && (
          <div className="settings-error-banner">
            {error}
          </div>
        )}

        {/* Engine */}
        <div className="detail-card-group">
          <div className="detail-card-group-title">Engine</div>
          <div className="detail-card-row">
            <span className="detail-card-label">Manifest <RestartBadge /></span>
            <span className="detail-card-value">
              <input type="text" value={config.manifest || ""} onChange={e => setField("manifest", e.target.value)} className="settings-mono-input" />
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.threads")} <RestartBadge /></span>
            <span className="detail-card-value">
              <input type="text" value={config.threads ?? ""} onChange={e => setField("threads", e.target.value)} placeholder="auto" />
            </span>
          </div>
        </div>

        {/* Model */}
        <div className="detail-card-group">
          <div className="detail-card-group-title">Model</div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.default_model")}</span>
            <span className="detail-card-value">
              <input type="text" value={model.default || ""} onChange={e => setField("model.default", e.target.value)} className="settings-full-input" />
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.model_dir")} <RestartBadge /></span>
            <span className="detail-card-value">
              <input type="text" value={model.dir || ""} onChange={e => setField("model.dir", e.target.value)} className="settings-mono-input" />
            </span>
          </div>
        </div>

        {/* Output */}
        <div className="detail-card-group">
          <div className="detail-card-group-title">Output</div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.output_dir")}</span>
            <span className="detail-card-value">
              <input type="text" value={output.dir || ""} onChange={e => setField("output.dir", e.target.value)} className="settings-mono-input" />
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">TTL (sec)</span>
            <span className="detail-card-value">
              <input type="number" value={output.ttl_sec ?? 1800} onChange={e => setField("output.ttl_sec", parseInt(e.target.value, 10))} />
            </span>
          </div>
        </div>

        {/* VAE / LLM */}
        <div className="detail-card-group">
          <div className="detail-card-group-title">VAE / LLM</div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.vae")}</span>
            <span className="detail-card-value">
              <input type="text" value={config.vae || ""} onChange={e => setField("vae", e.target.value)} className="settings-full-input" />
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.vae_on_cpu")}</span>
            <span className="detail-card-value">
              <Toggle value={!!config.vae_on_cpu} onChange={v => setField("vae_on_cpu", v ? "true" : "")} />
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.llm")}</span>
            <span className="detail-card-value">
              <input type="text" value={config.llm || ""} onChange={e => setField("llm", e.target.value)} className="settings-full-input" />
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.llm_vision")}</span>
            <span className="detail-card-value">
              <input type="text" value={config.llm_vision || ""} onChange={e => setField("llm_vision", e.target.value)} className="settings-full-input" />
            </span>
          </div>
        </div>

        {/* Save */}
        <div className="settings-save-bar">
          <button className="btn btn-primary" onClick={save} disabled={!dirty || saving}>
            {saving ? t("settings.saving") : t("common.save")}
          </button>
        </div>
      </div>
    </div>
  );
}
