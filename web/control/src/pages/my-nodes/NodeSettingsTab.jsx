import React, { useState, useEffect, useCallback } from "react";
import { useTranslation } from "@i18n";
import Toggle, { RestartBadge } from "@components/Toggle";
import Dropdown from "@components/Dropdown/Dropdown";
import ServiceTab from "@pages/settings/ServiceTab";
import useConfigForm from "@pages/settings/useConfigForm";
import ProviderAuthTab from "./ProviderAuthTab";
import { getAuthHeaders } from "@utils/wallet";
import "./NodeSettingsTab.scss";

const PROVIDER_RESTART_FIELDS = [
  "listen_addr", "router_addr", "target_proxy_id",
  "discovery.rendezvous_addr", "discovery.signaling_addr", "discovery.signaling_transport", "discovery.gate_addr",
  "tls.enabled", "tls.cert", "tls.key",
];

function EmblemEditor({ nodeId, emblem, onUpdate }) {
  const fileRef = React.useRef(null);
  const [uploading, setUploading] = useState(false);
  const [imgFailed, setImgFailed] = useState(false);
  const prefix = `/node/${encodeURIComponent(nodeId)}/provider`;

  const emblemSrc = emblem
    ? `${prefix}/file?path=${encodeURIComponent(emblem)}&t=${Date.now()}`
    : null;

  const handleUpload = () => fileRef.current?.click();

  const handleFile = async (e) => {
    const file = e.target.files?.[0];
    if (!file) return;
    setUploading(true);
    try {
      const dataUrl = await new Promise((res, rej) => {
        const reader = new FileReader();
        reader.onload = () => res(reader.result);
        reader.onerror = rej;
        reader.readAsDataURL(file);
      });
      const resp = await fetch(`${prefix}/emblem`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ image: dataUrl }),
      });
      if (resp.ok) {
        const data = await resp.json();
        onUpdate(data.emblem);
        setImgFailed(false);
      }
    } catch (err) {
      console.error("emblem upload failed", err);
    } finally {
      setUploading(false);
      if (fileRef.current) fileRef.current.value = "";
    }
  };

  const handleRemove = async () => {
    await fetch(`${prefix}/emblem`, { method: "DELETE" });
    onUpdate("");
  };

  return (
    <div className="detail-card-row emblem-row">
      <span className="detail-card-label">Emblem</span>
      <span className="detail-card-value emblem-content">
        <div className="emblem-frame">
          {emblemSrc && !imgFailed ? (
            <img src={emblemSrc} alt="emblem" onError={() => setImgFailed(true)} />
          ) : (
            <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="var(--text-muted)" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round"><rect x="2" y="2" width="20" height="20" rx="3" /><circle cx="8" cy="8" r="2" fill="var(--text-muted)" opacity="0.3" /><path d="M22 16l-4.5-4.5a1 1 0 00-1.4 0L11 16" /><path d="M16 21L7.5 12.5a1 1 0 00-1.4 0L2 16.5" /></svg>
          )}
        </div>
        <div className="emblem-controls">
          <button className="btn btn-primary btn-small" onClick={handleUpload} disabled={uploading}>
            {uploading ? "Uploading..." : "Upload"}
          </button>
          {emblem && (
            <button className="btn btn-outline btn-small" onClick={handleRemove}>Remove</button>
          )}
          <input ref={fileRef} type="file" accept="image/png,image/jpeg,image/webp" className="file-input-hidden" onChange={handleFile} />
        </div>
      </span>
    </div>
  );
}

function AboutEditor({ nodeId }) {
  const [text, setText] = useState("");
  const [saved, setSaved] = useState(false);

  useEffect(() => {
    if (!nodeId) return;
    fetch(`/node/${encodeURIComponent(nodeId)}/provider/about`)
      .then(r => r.ok ? r.json() : {})
      .then(d => setText(d.about || ""))
      .catch(() => {});
  }, [nodeId]);

  const handleSave = async () => {
    await fetch(`/node/${encodeURIComponent(nodeId)}/provider/about`, {
      method: "POST", headers: { "Content-Type": "application/json", ...getAuthHeaders() },
      body: JSON.stringify({ about: text }),
    });
    setSaved(true);
    setTimeout(() => setSaved(false), 2000);
  };

  return (
    <div className="detail-card-row about-row">
      <span className="detail-card-label about-label">About (Markdown)</span>
      <textarea rows={10} className="about-textarea" value={text} onChange={e => { setText(e.target.value); setSaved(false); }} placeholder={"# Node Title\n\nDescribe your node here...\n\n- **Service 1**: description\n- **Service 2**: description"} />
      <div className="about-actions">
        <button className="btn btn-primary btn-about-save" onClick={handleSave}>Save About</button>
        {saved && <span className="saved-indicator">Saved</span>}
      </div>
    </div>
  );
}

function ProviderForm({ nodeId }) {
  const { t } = useTranslation();
  const { config, setField, save, loading, saving, dirty, restartRequired, error } =
    useConfigForm({ serviceName: "provider", nodeId, restartFields: PROVIDER_RESTART_FIELDS });

  const [newSvc, setNewSvc] = useState({ name: "", addr: "", type: "", manifest: "" });

  const addService = () => {
    if (!newSvc.name || !newSvc.addr) return;
    const entry = { name: newSvc.name, addr: newSvc.addr };
    if (newSvc.type) entry.type = newSvc.type;
    if (newSvc.manifest) entry.manifest = newSvc.manifest;
    setField("services", [...(config.services || []), entry]);
    setNewSvc({ name: "", addr: "", type: "", manifest: "" });
  };

  const removeService = (idx) => {
    setField("services", (config.services || []).filter((_, i) => i !== idx));
  };

  const updateService = (idx, patch) => {
    const next = (config.services || []).map((s, i) => {
      if (i !== idx) return s;
      const merged = { ...s, ...patch };
      // Strip empty optional fields so the JSON stays clean.
      if (merged.type === "") delete merged.type;
      if (merged.manifest === "") delete merged.manifest;
      return merged;
    });
    setField("services", next);
  };

  const TYPE_OPTIONS = [
    { value: "", label: "local" },
    { value: "vllm", label: "vllm" },
  ];

  const isEnabled = (svc) => svc.enable === undefined || svc.enable === null || svc.enable === true;

  if (loading) return <p className="settings-loading">{t("settings.loading")}</p>;
  if (error) return <p className="settings-error">{error}</p>;
  if (!config) return null;

  return (
    <>
      {restartRequired && (
        <div className="restart-banner">
          {t("settings.restart_required")}
        </div>
      )}

      <div className="detail-card-group">
        <div className="detail-card-group-title">{t("settings.group_network")}</div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("settings.listen_addr")} <RestartBadge /></span>
          <span className="detail-card-value">
            <input type="text" value={config.listen_addr || ""} onChange={e => setField("listen_addr", e.target.value)} className="mono-input" />
          </span>
        </div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("settings.router_addr")} <RestartBadge /></span>
          <span className="detail-card-value">
            <input type="text" value={config.router_addr || ""} onChange={e => setField("router_addr", e.target.value)} className="mono-input" />
          </span>
        </div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("settings.target_proxy_id")} <RestartBadge /></span>
          <span className="detail-card-value">
            <input type="text" value={config.target_proxy_id || ""} onChange={e => setField("target_proxy_id", e.target.value)} className="mono-input-full" />
          </span>
        </div>
      </div>

      <div className="detail-card-group">
        <div className="detail-card-group-title">Discovery</div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("settings.rendezvous_addr")} <RestartBadge /></span>
          <span className="detail-card-value">
            <input type="text" value={config.discovery?.rendezvous_addr || ""} onChange={e => setField("discovery.rendezvous_addr", e.target.value)} className="mono-input-full" />
          </span>
        </div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("settings.signaling_addr")} <RestartBadge /></span>
          <span className="detail-card-value">
            <input type="text" value={config.discovery?.signaling_addr || ""} onChange={e => setField("discovery.signaling_addr", e.target.value)} className="mono-input-full" />
          </span>
        </div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("settings.signaling_transport")} <RestartBadge /></span>
          <span className="detail-card-value">
            <Dropdown
              value={config.discovery?.signaling_transport || "auto"}
              options={[{ value: "auto", label: "Auto" }, { value: "quic", label: "QUIC" }, { value: "tcp", label: "TCP" }]}
              onChange={(val) => setField("discovery.signaling_transport", val)}
            />
          </span>
        </div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("settings.gate_addr")} <RestartBadge /></span>
          <span className="detail-card-value">
            <input type="text" value={config.discovery?.gate_addr || ""} onChange={e => setField("discovery.gate_addr", e.target.value)} className="mono-input-full" />
          </span>
        </div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("settings.region")}</span>
          <span className="detail-card-value">
            <input type="text" value={config.discovery?.region || ""} onChange={e => setField("discovery.region", e.target.value)} className="mono-input" />
          </span>
        </div>
      </div>

      <div className="detail-card-group">
        <div className="detail-card-group-title">{t("settings.group_security")}</div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("settings.tls_enabled")} <RestartBadge /></span>
          <span className="detail-card-value">
            <Toggle value={config.tls?.enabled || false} onChange={v => setField("tls.enabled", v)} />
          </span>
        </div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("settings.tls_cert")} <RestartBadge /></span>
          <span className="detail-card-value">
            <input type="text" value={config.tls?.cert || ""} onChange={e => setField("tls.cert", e.target.value)} className="mono-input-full" />
          </span>
        </div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("settings.tls_key")} <RestartBadge /></span>
          <span className="detail-card-value">
            <input type="text" value={config.tls?.key || ""} onChange={e => setField("tls.key", e.target.value)} className="mono-input-full" />
          </span>
        </div>
      </div>

      <div className="detail-card-group">
        <div className="detail-card-group-title">Profile</div>
        <div className="detail-card-row">
          <span className="detail-card-label">Home Dir</span>
          <span className="detail-card-value">
            <input type="text" value={config.home_dir || ""} onChange={e => setField("home_dir", e.target.value)} />
          </span>
        </div>
        <EmblemEditor nodeId={nodeId} emblem={config.emblem || ""} onUpdate={(v) => setField("emblem", v)} />
        <AboutEditor nodeId={nodeId} />
      </div>

      <div className="detail-card-group">
        <div className="detail-card-group-title">{t("settings.group_services")}</div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("settings.expose_hw")}</span>
          <span className="detail-card-value">
            <Toggle value={config.expose_hardware_info || false} onChange={v => setField("expose_hardware_info", v)} />
          </span>
        </div>
        <table className="services-table">
          <thead>
            <tr>
              <th className="col-enable">Enable</th>
              <th>Name</th>
              <th>Address</th>
              <th className="col-type">Type</th>
              <th>Manifest</th>
              <th className="col-action"></th>
            </tr>
          </thead>
          <tbody>
            {(config.services || []).map((svc, i) => (
              <tr key={i}>
                <td className="col-enable">
                  <Toggle value={isEnabled(svc)} onChange={v => updateService(i, { enable: v })} />
                </td>
                <td>{svc.name}</td>
                <td className="col-addr">{svc.addr}</td>
                <td className="col-type">
                  <Dropdown
                    value={svc.type || ""}
                    options={TYPE_OPTIONS}
                    onChange={v => updateService(i, { type: v })}
                  />
                </td>
                <td className="cell-input">
                  <input
                    type="text"
                    placeholder="manifests/engines/vllm.json"
                    value={svc.manifest || ""}
                    onChange={e => updateService(i, { manifest: e.target.value })}
                    className="mono-input-full"
                    disabled={!svc.type}
                  />
                </td>
                <td className="cell-action">
                  <button className="btn-remove-svc" onClick={() => removeService(i)}>✕</button>
                </td>
              </tr>
            ))}
            <tr>
              <td className="col-enable" />
              <td className="cell-input">
                <input type="text" placeholder="name" value={newSvc.name} onChange={e => setNewSvc(p => ({ ...p, name: e.target.value }))} />
              </td>
              <td className="cell-input">
                <input type="text" placeholder="host:port" value={newSvc.addr} onChange={e => setNewSvc(p => ({ ...p, addr: e.target.value }))} />
              </td>
              <td className="col-type">
                <Dropdown
                  value={newSvc.type}
                  options={TYPE_OPTIONS}
                  onChange={v => setNewSvc(p => ({ ...p, type: v }))}
                />
              </td>
              <td className="cell-input">
                <input
                  type="text"
                  placeholder="manifests/engines/vllm.json"
                  value={newSvc.manifest}
                  onChange={e => setNewSvc(p => ({ ...p, manifest: e.target.value }))}
                  className="mono-input-full"
                  disabled={!newSvc.type}
                />
              </td>
              <td className="cell-input">
                <button className="btn-add-svc" onClick={addService}>+</button>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div className="save-bar">
        <button className="btn btn-primary" onClick={save} disabled={!dirty || saving}>
          {saving ? t("settings.saving") : t("common.save")}
        </button>
      </div>
    </>
  );
}

export default function NodeSettingsTab({ node, t }) {
  const [activeSubTab, setActiveSubTab] = useState("provider");
  const [serviceTabs, setServiceTabs] = useState([]);

  // Load installed versions to discover service tabs
  useEffect(() => {
    if (!node.auth || !node.id) return;
    fetch(`/node/${encodeURIComponent(node.id)}/provider/packages`, { headers: { ...getAuthHeaders() } })
      .then(r => r.ok ? r.json() : [])
      .then(data => {
        const services = (Array.isArray(data) ? data : [])
          .filter(v => v.type === "service")
          .map(v => v.name);
        setServiceTabs(services);
      })
      .catch(() => setServiceTabs([]));
  }, [node.id, node.auth]);

  if (!node.auth) {
    return (
      <p className="not-authed-message">
        {t("my_nodes.not_authenticated")}
      </p>
    );
  }

  const allSubTabs = ["provider", "auth", ...serviceTabs];

  const tabLabel = (tab) => {
    if (tab === "provider") return t("settings.tab_provider");
    if (tab === "auth") return t("settings.tab_auth");
    return tab;
  };

  return (
    <div>
      {/* Sub tabs */}
      <div className="sub-tabs">
        {allSubTabs.map(tab => (
          <button
            key={tab}
            className={`sub-tab ${activeSubTab === tab ? "active" : ""}`}
            onClick={() => setActiveSubTab(tab)}
          >
            {tabLabel(tab)}
          </button>
        ))}
      </div>

      {/* Sub tab content */}
      {activeSubTab === "provider" && <ProviderForm nodeId={node.id} />}
      {activeSubTab === "auth" && <ProviderAuthTab nodeId={node.id} />}
      {serviceTabs.includes(activeSubTab) && <ServiceTab name={activeSubTab} nodeId={node.id} />}
    </div>
  );
}
