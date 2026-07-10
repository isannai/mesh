import React from "react";
import { useTranslation } from "@i18n";
import Toggle, { RestartBadge } from "@components/Toggle";
import Dropdown from "@components/Dropdown/Dropdown";
import useConfigForm from "./useConfigForm";
import "./settings-form.scss";

const RESTART_FIELDS = ["listen_addr", "tls_cert", "tls_key"];

export default function BrokerTab() {
  const { t } = useTranslation();
  const { config, setField, save, loading, saving, dirty, restartRequired, error, notFound } =
    useConfigForm({ useAdminApi: true, restartFields: RESTART_FIELDS });

  const saveAll = async () => {
    await save();
  };

  if (loading) return <div className="detail-card"><div className="detail-card-body"><p>{t("settings.loading")}</p></div></div>;
  if (notFound) return (
    <div className="detail-card"><div className="detail-card-body">
      <p className="settings-empty-title">{t("settings.not_installed_title")}</p>
      <p className="settings-empty-hint">{t("settings.not_installed_hint")}</p>
    </div></div>
  );
  if (error) return <div className="detail-card"><div className="detail-card-body"><p className="settings-error">{error}</p></div></div>;
  if (!config) return null;

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

        {/* Network */}
        <div className="detail-card-group">
          <div className="detail-card-group-title">{t("settings.group_network")}</div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.listen_addr")} <RestartBadge /></span>
            <span className="detail-card-value">
              <input type="text" value={config.listen_addr || ""} onChange={e => setField("listen_addr", e.target.value)} className="settings-mono-input-inline" />
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.target_addr")}</span>
            <span className="detail-card-value">
              <input type="text" value={config.target_addr || ""} onChange={e => setField("target_addr", e.target.value)} className="settings-mono-input-inline" />
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.router_addr")}</span>
            <span className="detail-card-value">
              <input type="text" value={config.router_addr || ""} onChange={e => setField("router_addr", e.target.value)} className="settings-mono-input-inline" />
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.target_proxy_id")}</span>
            <span className="detail-card-value">
              <input type="text" value={config.target_proxy_id || ""} onChange={e => setField("target_proxy_id", e.target.value)} className="settings-mono-input" />
            </span>
          </div>
        </div>

        {/* Discovery */}
        <div className="detail-card-group">
          <div className="detail-card-group-title">Discovery</div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.rendezvous_addr")}</span>
            <span className="detail-card-value">
              <input type="text" value={config.rendezvous_addr || ""} onChange={e => setField("rendezvous_addr", e.target.value)} className="settings-mono-input" />
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.signaling_addr")}</span>
            <span className="detail-card-value">
              <input type="text" value={config.signaling_addr || ""} onChange={e => setField("signaling_addr", e.target.value)} className="settings-mono-input" />
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.signaling_transport")}</span>
            <span className="detail-card-value">
              <Dropdown
                value={config.signaling_transport || "auto"}
                options={[{ value: "auto", label: "Auto" }, { value: "quic", label: "QUIC" }, { value: "tcp", label: "TCP" }]}
                onChange={(val) => setField("signaling_transport", val)}
              />
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.gate_addr")}</span>
            <span className="detail-card-value">
              <input type="text" value={config.gate_addr || ""} onChange={e => setField("gate_addr", e.target.value)} className="settings-mono-input" />
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.region")}</span>
            <span className="detail-card-value">
              <input type="text" value={config.region || ""} onChange={e => setField("region", e.target.value)} className="settings-mono-input-inline" />
            </span>
          </div>
        </div>

        {/* Info */}
        <div className="detail-card-group">
          <div className="detail-card-group-title">Info</div>
          <div className="detail-card-row">
            <span className="detail-card-label">Owner</span>
            <span className="detail-card-value settings-mono-readonly">
              {config.auth_owner || "—"}
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">Node ID</span>
            <span className="detail-card-value settings-mono-readonly">
              {config.id || "—"}
            </span>
          </div>
        </div>

        {/* Security */}
        <div className="detail-card-group">
          <div className="detail-card-group-title">{t("settings.group_security")}</div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.tls_cert")} <RestartBadge /></span>
            <span className="detail-card-value">
              <input type="text" value={config.tls_cert || ""} onChange={e => setField("tls_cert", e.target.value)} />
            </span>
          </div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.tls_key")} <RestartBadge /></span>
            <span className="detail-card-value">
              <input type="text" value={config.tls_key || ""} onChange={e => setField("tls_key", e.target.value)} />
            </span>
          </div>
        </div>

        {/* Save */}
        <div className="settings-save-bar">
          <button className="btn btn-primary" onClick={saveAll} disabled={!dirty || saving}>
            {saving ? t("settings.saving") : t("common.save")}
          </button>
        </div>
      </div>
    </div>
  );
}
