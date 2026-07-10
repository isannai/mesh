import React, { useState, useEffect, useRef, useMemo } from "react";
import { Link } from "react-router-dom";
import { useSessionState } from "@utils/hooks";
import { useTranslation } from "@i18n";
import { useAuth } from "../../context/AuthContext";
import LoginModal from "@components/LoginModal/LoginModal";
import { fetchMyNodes } from "../../api/nodes";
import DataTable from "@components/DataTable";
import ConfirmDialog from "@components/ConfirmDialog";
import CustomModelForm from "./CustomModelForm";
import { getAuthHeaders } from "@utils/wallet";
import { listCustomModels, createCustomModel, updateCustomModel } from "@utils/customStore";

import { formatSize } from "@utils/format";
import useInstallManager from "@hooks/useInstallManager";
import "./index.scss";

const TYPE_TABS = [
  { key: "model", label: "Models" },
];

export default function Deploy() {
  const { t } = useTranslation();
  const { isLoggedIn } = useAuth();
  const [showLogin, setShowLogin] = useState(false);

  // Active type tab
  const [activeType, setActiveType] = useSessionState("dpl.activeType", "model");
  const isModelTab = activeType === "model";

  // Node data
  const [myNodes, setMyNodes] = useState([]);
  const [allNodes, setAllNodes] = useState([]);
  const [nodeMode, setNodeMode] = useSessionState("dpl.nodeMode", "all");
  const [manualNodeIds, setManualNodeIds] = useSessionState("dpl.manualNodes", []);
  const [nodeSearchText, setNodeSearchText] = useState("");
  const [manualOpen, setManualOpen] = useState(false);

  // Software data
  const [gateSoftware, setGateSoftware] = useState({}); // { type: [...] }
  const [customModels, setCustomModels] = useState([]);
  const [nodeVersions, setNodeVersions] = useState({});
  const [gateManifests, setGateManifests] = useState({});
  const [loading, setLoading] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [confirmAction, setConfirmAction] = useState(null); // { message, onConfirm }

  // Filters
  const [search, setSearch] = useSessionState("dpl.search", "");
  const [modelTypeFilter, setModelTypeFilter] = useSessionState("dpl.modelTypeFilter", "all");
  const [statusFilter, setStatusFilter] = useSessionState("dpl.statusFilter", "all");

  // UI
  const [showModelForm, setShowModelForm] = useState(null);
  const [panelHeight, setPanelHeight] = useState(200);
  const dragging = useRef(false);
  const startY = useRef(0);
  const startH = useRef(0);

  // Load nodes
  useEffect(() => { fetchMyNodes().then(d => setMyNodes(Array.isArray(d) ? d : [])).catch(() => {}); }, []);
  useEffect(() => {
    const load = () => fetch("/v1/nodes").then(r => r.json()).then(d => setAllNodes(Array.isArray(d) ? d : [])).catch(() => {});
    load(); const timer = setInterval(load, 30000); return () => clearInterval(timer);
  }, []);

  const authNodes = useMemo(() => myNodes.filter(n => n.auth).map(mn => {
    const live = allNodes.find(n => n.id === mn.id);
    return { ...mn, status: live?.status || "offline", online: !!live };
  }), [myNodes, allNodes]);
  const onlineNodes = useMemo(() => authNodes.filter(n => n.status !== "offline"), [authNodes]);
  const selectedNodeIds = useMemo(() => {
    if (nodeMode === "all") return onlineNodes.map(n => n.id);
    return manualNodeIds.filter(id => authNodes.some(n => n.id === id && n.status !== "offline"));
  }, [nodeMode, onlineNodes, manualNodeIds, authNodes]);
  const totalSelected = selectedNodeIds.length;

  const toggleManualNode = (nid) => setManualNodeIds(prev => prev.includes(nid) ? prev.filter(id => id !== nid) : [...prev, nid]);
  const selectAllManual = () => setManualNodeIds(onlineNodes.map(n => n.id));
  const deselectAllManual = () => setManualNodeIds([]);

  // 1) Gate software + custom models — 최초 1회 + refreshKey(install/uninstall 후)
  const [swLoaded, setSwLoaded] = useState(false);
  const gateSoftwareRef = useRef({});
  useEffect(() => {
    const fetchSw = async () => {
      try {
        const typeResults = await Promise.all(TYPE_TABS.map(async ({ key }) => {
          try { const r = await fetch(`/gate/v1/software?type=${key}`); return [key, r.ok ? await r.json() : []]; }
          catch { return [key, []]; }
        }));
        const sw = Object.fromEntries(typeResults.map(([k, v]) => [k, Array.isArray(v) ? v : []]));
        gateSoftwareRef.current = sw;
        setGateSoftware(sw);
      } catch {}
      try { setCustomModels(await listCustomModels()); } catch { setCustomModels([]); }
      setSwLoaded(true);
    };
    fetchSw();
  }, [refreshKey]);

  // 2) Installed versions — 노드 변경 또는 refreshKey(install/uninstall 후)
  useEffect(() => {
    if (!swLoaded) return;
    const fetchVersions = async () => {
      const versions = {};
      await Promise.all(selectedNodeIds.map(async (nid) => {
        try {
          const c = new AbortController(); const t = setTimeout(() => c.abort(), 5000);
          const r = await fetch(`/node/${encodeURIComponent(nid)}/provider/packages`, { signal: c.signal, headers: { ...getAuthHeaders() } });
          clearTimeout(t); versions[nid] = r.ok ? await r.json() : [];
        } catch { versions[nid] = []; }
      }));
      setNodeVersions(versions);
    };
    fetchVersions();
  }, [refreshKey, selectedNodeIds.join(","), swLoaded]);

  // 3) Manifests — 탭 변경 시만 (캐시된 gate software 사용)
  useEffect(() => {
    if (!swLoaded) return;
    setLoading(true);
    const fetchManifests = async () => {
      const swList = gateSoftwareRef.current[activeType] || [];
      const mf = {};
      await Promise.all(swList.map(async (s) => {
        try { const r = await fetch(`/gate/v1/software/package?type=${activeType}&name=${s.name}`); if (r.ok) mf[s.name] = await r.json(); } catch {}
      }));
      setGateManifests(mf);
      setLoading(false);
    };
    fetchManifests();
  }, [activeType, swLoaded]);

  // Build list for current type
  const currentGateSw = gateSoftware[activeType] || [];
  const combinedList = useMemo(() => {
    const gateItems = currentGateSw.map(sw => {
      const mf = gateManifests[sw.name];
      const nodeStatus = {};
      selectedNodeIds.forEach(nid => {
        const inst = (nodeVersions[nid] || []).find(i => i.name === sw.name && i.type === activeType);
        nodeStatus[nid] = inst ? inst.version : null;
      });
      const cnt = Object.values(nodeStatus).filter(v => v).length;
      return {
        name: sw.name, service_name: sw.service_name || "", latestVersion: mf?.version || "",
        nodeStatus, installedCount: cnt, _source: "supported",
        _status: cnt === totalSelected && totalSelected > 0 ? "installed" : cnt > 0 ? "partial" : "not_installed",
      };
    });

    if (!isModelTab) return gateItems;

    const customItems = (Array.isArray(customModels) ? customModels : []).map(cm => {
      const nodeStatus = {};
      selectedNodeIds.forEach(nid => {
        const inst = (nodeVersions[nid] || []).find(i => i.name === cm.name && i.type === "model");
        nodeStatus[nid] = inst ? inst.version : null;
      });
      const cnt = Object.values(nodeStatus).filter(v => v).length;
      const isImport = !cm.download_url;
      return {
        name: cm.name, service_name: cm.service || "", latestVersion: isImport ? "import" : "custom",
        nodeStatus, installedCount: cnt, _source: isImport ? "import" : "custom", _customModel: cm, _isPublic: cm.is_public,
        _status: cnt === totalSelected && totalSelected > 0 ? "installed" : cnt > 0 ? "partial" : "not_installed",
      };
    });

    // 노드에만 있고 DB/Gate에 없는 모델 감지
    const knownNames = new Set([...gateItems.map(g => g.name), ...customItems.map(c => c.name)]);
    const unknownItems = [];
    selectedNodeIds.forEach(nid => {
      (nodeVersions[nid] || []).filter(i => i.type === "model" && !knownNames.has(i.name)).forEach(i => {
        if (unknownItems.find(u => u.name === i.name)) {
          unknownItems.find(u => u.name === i.name).nodeStatus[nid] = i.version;
          unknownItems.find(u => u.name === i.name).installedCount++;
        } else {
          unknownItems.push({
            name: i.name, service_name: i.service || "", latestVersion: i.version || "unknown",
            nodeStatus: { [nid]: i.version }, installedCount: 1, _source: "unknown",
            _nodeManifest: i,
            _status: "installed",
          });
        }
      });
    });

    return [...gateItems, ...customItems, ...unknownItems];
  }, [currentGateSw, customModels, gateManifests, nodeVersions, selectedNodeIds, totalSelected, activeType, isModelTab]);

  const filteredList = combinedList.filter(m => {
    if (search && !m.name.toLowerCase().includes(search.toLowerCase())) return false;
    if (isModelTab && modelTypeFilter === "supported" && m._source !== "supported") return false;
    if (isModelTab && modelTypeFilter === "custom" && m._source !== "custom" && m._source !== "import" && m._source !== "unknown") return false;
    if (statusFilter === "installed" && m._status !== "installed" && m._status !== "partial") return false;
    if (statusFilter === "not_installed" && m._status !== "not_installed") return false;
    return true;
  });


  // Install/Uninstall/Remove/Import 공통 훅
  const {
    installJobs, setInstallJobs,
    handleInstall, handleInstallCustom,
    handleUninstall, handleRemove, handleSaveToDB,
    isJobRunning,
  } = useInstallManager({ selectedNodeIds, onlineNodes, activeType, myNodes, setRefreshKey, setConfirmAction });

  // Custom model CRUD (IndexedDB)
  const handleSaveCustomModel = async (payload, editId) => {
    try {
      if (editId) await updateCustomModel(editId, payload);
      else await createCustomModel(payload);
      setShowModelForm(null);
      setRefreshKey(k => k + 1);
    } catch (e) {
      alert(e.message || "save failed");
      throw e;
    }
  };

  const MAX_CONCURRENT = 5;

  // Splitter
  const onSplitterMouseDown = (e) => { dragging.current = true; startY.current = e.clientY; startH.current = panelHeight; e.preventDefault(); };
  useEffect(() => {
    const onMove = (e) => { if (!dragging.current) return; setPanelHeight(Math.min(400, Math.max(80, startH.current + (startY.current - e.clientY)))); };
    const onUp = () => { dragging.current = false; };
    document.addEventListener("mousemove", onMove); document.addEventListener("mouseup", onUp);
    return () => { document.removeEventListener("mousemove", onMove); document.removeEventListener("mouseup", onUp); };
  }, []);

  // Helpers
  // statusCls maps a file install status to a state class. CSS rules in
  // ./index.scss (.file-progress-bar-fill.is-*, .file-progress-status.is-*)
  // pick up the corresponding color so the fill bar and the % label share
  // one color signal per row.
  const statusCls = (s) => s === "file_done" ? "is-done"
    : s === "checking" ? "is-checking"
    : s === "error" ? "is-error"
    : s === "skip" ? "is-skip"
    : "is-default";
  const statusLbl = (s, p) => s === "file_done" ? t("models.status_done") : s === "checking" ? t("models.status_checking") : s === "skip" ? t("models.status_skip") : s === "error" ? t("models.status_error") : `${p ?? 0}%`;
  const completedJobs = installJobs.filter(j => j.done);
  const failedJobs = installJobs.filter(j => j.error);
  const activeJobs = installJobs.filter(j => !j.done && !j.error);

  const filteredNodes = authNodes.filter(n => n.id.toLowerCase().includes(nodeSearchText.toLowerCase()) || (n.label || "").toLowerCase().includes(nodeSearchText.toLowerCase()));

  if (!isLoggedIn) {
    return (
      <div className="page">
        <div className="page-header"><h2>{t("deploy.title")}</h2></div>
        <div className="login-required-card">
          <div className="login-required-icon">&#128274;</div>
          <h4 className="login-required-title">
            {t("auth.required_title")}
          </h4>
          <p className="login-required-desc">
            {t("auth.required_desc")}
          </p>
          <button className="login-required-btn" onClick={() => setShowLogin(true)}>
            {t("auth.connect_wallet")}
          </button>
        </div>
        {showLogin && <LoginModal onClose={() => setShowLogin(false)} />}
      </div>
    );
  }

  return (
    <div className="page">
      <div className="page-header"><h2>{t("deploy.title")}</h2></div>

      <div className="page-body models-body">

        <InstallProviderBanner />

        {/* Node selector */}
        <div className="node-selector">
          <div className="node-selector-row">
            <label className={`node-mode-radio ${nodeMode === "all" ? "active" : ""}`} onClick={() => { setNodeMode("all"); setManualOpen(false); }}>
              <input type="radio" name="nodeMode" checked={nodeMode === "all"} readOnly />
              {t("models.all_online", { n: onlineNodes.length })}
            </label>
            <label className={`node-mode-radio ${nodeMode === "manual" ? "active" : ""}`} onClick={() => { setNodeMode("manual"); setManualOpen(true); }}>
              <input type="radio" name="nodeMode" checked={nodeMode === "manual"} readOnly />
              {t("models.select_manually")}
            </label>
            <span className="node-selector-count">{t("models.selected_count", { n: selectedNodeIds.length, total: authNodes.length })}</span>
          </div>
          {nodeMode === "manual" && manualOpen && (
            <div className="manual-picker">
              <div className="picker-header">
                <input type="text" className="picker-search" placeholder={t("models.search_nodes")} value={nodeSearchText} onChange={e => setNodeSearchText(e.target.value)} />
                <button className="picker-link" onClick={selectAllManual}>{t("models.select_all")}</button>
                <button className="picker-link" onClick={deselectAllManual}>{t("models.deselect_all")}</button>
                <span className="picker-count">{t("models.online_total_count", { online: onlineNodes.length, total: authNodes.length })}</span>
              </div>
              <div className="picker-body">
                {filteredNodes.map(node => {
                  const off = node.status === "offline", sel = manualNodeIds.includes(node.id);
                  return (
                    <label key={node.id} className={`node-chip ${sel ? "selected" : ""} ${off ? "offline" : ""}`} onClick={() => !off && toggleManualNode(node.id)}>
                      <input type="checkbox" checked={sel} readOnly disabled={off} />
                      <span className={`node-dot ${off ? "offline" : ""}`} />
                      {node.label || node.id.slice(0, 10) + ".."}
                    </label>
                  );
                })}
              </div>
            </div>
          )}
        </div>

        {/* Type tabs */}
        <div className="software-tabs software-tabs-gap">
          {TYPE_TABS.map(tab => (
            <button key={tab.key} onClick={() => setActiveType(tab.key)} className={`software-tab ${activeType === tab.key ? "active" : ""}`}>{tab.label}</button>
          ))}
        </div>

        {/* Toolbar */}
        <div className="models-toolbar">
          <div className="toolbar-left">
            <input type="text" className="search-input" placeholder={t("deploy.search_software")} value={search} onChange={e => setSearch(e.target.value)} />
            {isModelTab && (
              <>
                <span className="filter-label">{t("models.filter_type")}</span>
                <button className={`filter-btn ${modelTypeFilter === "all" ? "active" : ""}`} onClick={() => setModelTypeFilter("all")}>{t("models.filter_all")}</button>
                <button className={`filter-btn ${modelTypeFilter === "supported" ? "active" : ""}`} onClick={() => setModelTypeFilter("supported")}>{t("models.filter_supported")}</button>
                <button className={`filter-btn ${modelTypeFilter === "custom" ? "active" : ""}`} onClick={() => setModelTypeFilter("custom")}>{t("models.filter_custom")}</button>
                <span className="filter-divider" />
              </>
            )}
            <span className="filter-label">{t("models.filter_status")}</span>
            <button className={`filter-btn ${statusFilter === "all" ? "active" : ""}`} onClick={() => setStatusFilter("all")}>{t("setup.all")}</button>
            <button className={`filter-btn ${statusFilter === "installed" ? "active" : ""}`} onClick={() => setStatusFilter("installed")}>{t("setup.installed")}</button>
            <button className={`filter-btn ${statusFilter === "not_installed" ? "active" : ""}`} onClick={() => setStatusFilter("not_installed")}>{t("setup.not_installed")}</button>
          </div>
          <div className="toolbar-right">
            {isModelTab && (
              <button className="btn btn-add" onClick={() => setShowModelForm({})}>+ {t("models.add_model")}</button>
            )}
            <button className="btn" onClick={() => setRefreshKey(k => k + 1)} disabled={loading}>
              {loading ? "..." : t("setup.refresh")}
            </button>
          </div>
        </div>

        {/* Table */}
        <div className="table-wrap">
          {loading ? <DataTable columns={[
                { key: "name", label: t("setup.name") },
                { key: "status", label: t("setup.status") },
                { key: "version", label: t("setup.version") },
              ]} data={[]} loading={true} loadingRows={6} />
          : filteredList.length === 0 ? (
            <div className="models-empty">
              <p>{isModelTab ? t("models.no_models") : t("setup.no_software")}</p>
              {isModelTab && (
                <button className="btn btn-add add-model-btn-gap" onClick={() => setShowModelForm({})}>+ {t("models.add_model")}</button>
              )}
            </div>
          ) : (
            <div className="sw-card-grid">
              {filteredList.map((row, i) => (
                <SoftwareCard
                  key={row.name + i}
                  row={row}
                  t={t}
                  type={activeType}
                  running={isJobRunning(row.name)}
                  totalSelected={totalSelected}
                  onInstall={() => handleInstall(row)}
                  onInstallCustom={() => handleInstallCustom(row)}
                  onUninstall={() => handleUninstall(row)}
                  onRemove={() => handleRemove(row)}
                  onSaveToDB={() => handleSaveToDB(row)}
                  onEdit={() => setShowModelForm(row._customModel)}
                  isModelTab={isModelTab}
                />
              ))}
            </div>
          )}
        </div>

        {/* Modals */}
        {showModelForm && <CustomModelForm initial={showModelForm} onSave={handleSaveCustomModel} onClose={() => setShowModelForm(null)} t={t} />}
        {confirmAction && <ConfirmDialog message={confirmAction.message} onConfirm={confirmAction.onConfirm} onCancel={() => setConfirmAction(null)} />}

        {/* Progress panel */}
        {installJobs.length > 0 && (
          <>
            <div className="splitter-bar" onMouseDown={onSplitterMouseDown} />
            <div className="progress-panel" style={{ height: panelHeight }}>
              <div className="panel-header">
                <span className="panel-title">{t("setup.install_queue")}</span>
                <div className="panel-meta-row">
                  <span className="panel-meta">{t("models.concurrency_info", { max: MAX_CONCURRENT, n: Math.max(0, activeJobs.length - MAX_CONCURRENT) })}</span>
                  <button className="panel-close" onClick={() => setInstallJobs([])}>&#10005;</button>
                </div>
              </div>
              {/* Summary */}
              <div className="summary-section">
                <div className="summary-row">
                  <div className="summary-bar-bg">
                    <div className="summary-bar-fill" style={{ width: `${installJobs.length > 0 ? Math.round(completedJobs.length / installJobs.length * 100) : 0}%` }} />
                  </div>
                  <span className="summary-count">{completedJobs.length} / {installJobs.length} nodes</span>
                </div>
                <div className="summary-legend">
                  <span className="legend-item"><span className="legend-dot dot-completed" /> {t("models.legend_completed", { n: completedJobs.length })}</span>
                  <span className="legend-item"><span className="legend-dot dot-active" /> {t("models.legend_active", { n: Math.min(activeJobs.length, MAX_CONCURRENT) })}</span>
                  <span className="legend-item"><span className="legend-dot dot-failed" /> {t("models.legend_failed", { n: failedJobs.length })}</span>
                </div>
              </div>
              {/* Active slots */}
              <div className="slots-section">
                <div className="slots-label">{t("models.active_slots", { n: Math.min(activeJobs.length, MAX_CONCURRENT), max: MAX_CONCURRENT })}</div>
                {activeJobs.slice(0, MAX_CONCURRENT).map(job => (
                  <div key={job.id} className="slot-item">
                    <div className="slot-header">
                      <span className="slot-node-label">{job.nodeLabel}</span>
                      <span className="slot-sw-name">{job.swName}</span>
                    </div>
                    {Object.entries(job.progress).map(([file, p]) => (
                      <div key={file} className="file-progress-row">
                        <span className="file-progress-name">{file}</span>
                        <div className="file-progress-bar-bg">
                          <div className={`file-progress-bar-fill ${statusCls(p.status)}`} style={{ width: `${p.status === "file_done" || p.status === "skip" ? 100 : p.percent ?? 0}%` }} />
                        </div>
                        <span className={`file-progress-status ${statusCls(p.status)}`}>{statusLbl(p.status, p.percent)}</span>
                      </div>
                    ))}
                  </div>
                ))}
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}

function InstallProviderBanner() {
  const { t } = useTranslation();
  return (
    <div className="install-provider-banner">
      <span className="install-provider-banner-icon">🚀</span>
      <div className="install-provider-banner-body">
        <div className="install-provider-banner-title">{t("deploy.install_banner_title")}</div>
        <div className="install-provider-banner-desc">
          {t("deploy.install_banner_desc")}
        </div>
      </div>
      <Link to="/welcome" className="install-provider-banner-cta">
        {t("deploy.install_banner_cta")}
      </Link>
    </div>
  );
}

// ── SoftwareCard ────────────────────────────────────────────────
// Replaces the DataTable row view with a richer card. One card per software
// entry, with an icon, title/description, and a primary action on the right.

const ICON_MAP = {
  "sd-api":       "🎨",
  "diffusion":    "🎨",
  "llm-api":      "💬",
  "llm":          "💬",
  "voice-api":    "🎤",
  "voice":        "🎤",
  "tts":          "🎤",
  "stt":          "🎤",
  "terminal":     "💻",
  "shell":        "💻",
  "core":         "🔧",
  "provider":     "🔧",
  "installer":    "📥",
};

function pickIcon(name, type) {
  const key = (name || "").toLowerCase();
  if (ICON_MAP[key]) return ICON_MAP[key];
  for (const k of Object.keys(ICON_MAP)) {
    if (key.includes(k)) return ICON_MAP[k];
  }
  if (type === "model")  return "📦";
  if (type === "engine") return "⚙️";
  if (type === "core")   return "🔧";
  return "🔌";
}

function describeSoftware(row, type) {
  const parts = [];
  if (row.service_name) parts.push(row.service_name);
  else parts.push(type.charAt(0).toUpperCase() + type.slice(1));
  if (row.latestVersion && row.latestVersion !== "custom" && row.latestVersion !== "import") {
    parts.push("v" + row.latestVersion);
  }
  return parts.join(" ");
}

function SoftwareCard({
  row, t, type, running, totalSelected,
  onInstall, onInstallCustom, onUninstall, onRemove, onSaveToDB, onEdit, isModelTab,
}) {
  const src = row._source;
  const isCustomOrImport = src === "custom" || src === "import";
  const isUnknown = src === "unknown";
  const installed = row._status === "installed";
  const partial = row._status === "partial";

  // Primary action — right side of the card. Mirrors DataTable actions but
  // shows only the most relevant button so the card stays clean.
  let primary = null;
  if (isUnknown) {
    primary = <button className="btn btn-sm btn-primary" onClick={onSaveToDB}>{t("models.save_to_db")}</button>;
  } else if (installed) {
    primary = <span className="sw-badge installed">✓ {t("setup.installed")}</span>;
  } else if (partial) {
    primary = (
      <button className="btn btn-sm btn-update" onClick={src === "custom" ? onInstallCustom : onInstall} disabled={running}>
        {running ? "..." : t("setup.update")}
      </button>
    );
  } else if (src === "supported" || (src === "custom" && row._customModel?.download_url)) {
    primary = (
      <button className="btn btn-sm btn-primary" onClick={src === "custom" ? onInstallCustom : onInstall} disabled={running}>
        {running ? "..." : t("setup.install")}
      </button>
    );
  }

  return (
    <div className={`sw-card ${isUnknown ? "unknown" : ""} ${installed ? "installed" : ""}`}>
      <div className="sw-card-ico">{pickIcon(row.name, type)}</div>

      <div className="sw-card-body">
        <div className="sw-card-title">
          {isUnknown && <span className="sw-unknown-mark">⚠ </span>}
          {row.name}
          {isModelTab && src && src !== "supported" && (
            <span className={`sw-tag sw-tag-${src}`}>
              {{ custom: t("models.source_custom"), import: t("models.source_import"), unknown: t("models.source_unknown") }[src] || src}
            </span>
          )}
        </div>
        <div className="sw-card-desc">
          {describeSoftware(row, type)}
          {totalSelected > 1 && row.installedCount > 0 && (
            <span className="sw-card-meta"> · {row.installedCount}/{totalSelected} nodes</span>
          )}
        </div>
      </div>

      <div className="sw-card-actions">
        {primary}
        {!isUnknown && row._status !== "not_installed" && !installed && (
          <button className="btn btn-sm btn-delete" onClick={onUninstall}>
            {t("setup.uninstall")}
          </button>
        )}
        {installed && (
          <button className="btn btn-sm btn-delete-ghost" onClick={onUninstall}>
            {t("setup.uninstall")}
          </button>
        )}
        {isCustomOrImport && (
          <button className="btn btn-sm btn-remove-light" onClick={onRemove}>{t("models.remove")}</button>
        )}
        {src === "custom" && isModelTab && (
          <button className="btn btn-sm btn-edit" onClick={onEdit}>{t("common.edit")}</button>
        )}
      </div>
    </div>
  );
}
