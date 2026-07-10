import React, { useState, useEffect, useCallback, useMemo, useRef } from "react";
import { useSessionState } from "@utils/hooks";
import { useTranslation } from "@i18n";
import { useAuth } from "../../context/AuthContext";
import LoginModal from "@components/LoginModal/LoginModal";
import { fetchMyNodes, addMyNode, deleteMyNode, updateMyNode, authMyNode, fetchRendezvousList, fetchNodesByRendezvousAddr, fetchMetrics, fetchMetricsByAddr, mergeMetricsIntoNodes } from "../../api/nodes";
import { fetchProfiles, setActiveProfile, upsertProfile, deleteProfile } from "../../api/profiles";
import ProfileEditor from "@components/ProfileEditor";
import { getAuthHeaders } from "@utils/wallet";
import { ServiceList } from "@components/ServiceCard";
import useServiceActions from "@hooks/useServiceActions";
import StatusTag from "@components/StatusTag/StatusTag";
import CopyButton from "@components/CopyButton/CopyButton";
import { useToast } from "@components/Toast/ToastContext";
import FormModal from "@components/FormModal/FormModal";
import ConfirmDialog from "@components/ConfirmDialog/ConfirmDialog";
import Dropdown from "@components/Dropdown/Dropdown";
import Skeleton from "@components/Skeleton/Skeleton";
import NodeSettingsTab from "./NodeSettingsTab";
import SyncTab from "./SyncTab";
import DeployTab from "./DeployTab";
import ProfilesTab from "./ProfilesTab";
import { getEmblemPalette } from "@utils/emblem";
import { summarizeGpus } from "@utils/gpu";
import { formatUptime } from "@utils/format";
import ModelLabel from "@components/ModelLabel/ModelLabel";
import "./index.scss";


function EmblemPlaceholder({ nodeId }) {
  const { initials, gradient } = getEmblemPalette(nodeId);
  return (
    <div className="emblem-placeholder" style={{ background: gradient }}>{initials}</div>
  );
}

function EmblemImg({ src, nodeId }) {
  const [failed, setFailed] = useState(false);
  if (!src || failed) return <EmblemPlaceholder nodeId={nodeId} />;
  return <img src={src} alt="" onError={() => setFailed(true)} />;
}

function NodeDetail({ node, t, onAuth, onLabelSave, onDelete }) {
  const hw = node.hardware || {};
  const gpus = hw.gpus || [];
  const gpuSummary = summarizeGpus(gpus);
  const cpus = hw.cpus || [];
  const ram = hw.ram || {};
  const [editLabel, setEditLabel] = useState(false);
  const [labelVal, setLabelVal] = useState(node.label || "");

  useEffect(() => { setLabelVal(node.label || ""); setEditLabel(false); }, [node.id]);

  const saveLabel = () => {
    if (labelVal !== (node.label || "")) {
      onLabelSave(node.id, labelVal);
    }
    setEditLabel(false);
  };

  return (
    <div>
      <div className="detail-card-group">
        <div className="detail-card-group-title">{t("nodes.basic")}</div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("nodes.node_id")}</span>
          <span className="detail-card-value">{node.id}</span>
        </div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("my_nodes.detail_label")}</span>
          <span className="detail-card-value">
            {editLabel ? (
              <span className="label-edit-row">
                <input
                  type="text"
                  className="label-edit-input"
                  value={labelVal}
                  onChange={(e) => setLabelVal(e.target.value)}
                  onKeyDown={(e) => { if (e.key === "Enter") saveLabel(); if (e.key === "Escape") setEditLabel(false); }}
                  autoFocus
                />
                <button className="btn btn-sm btn-primary" onClick={saveLabel}>{t("common.save")}</button>
                <button className="btn btn-sm" onClick={() => setEditLabel(false)}>{t("common.cancel")}</button>
              </span>
            ) : (
              <span className="label-display" onClick={() => setEditLabel(true)}>
                {node.label || <span className="label-placeholder">{t("my_nodes.click_to_set_label")}</span>}
                <span className="label-edit-icon">&#9998;</span>
              </span>
            )}
          </span>
        </div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("nodes.address")}</span>
          <span className="detail-card-value">{node.addr || "-"}</span>
        </div>
        <div className="detail-card-row">
          <span className="detail-card-label">{t("nodes.status")}</span>
          <span className="detail-card-value"><StatusTag value={getDisplayStatus(node)} /></span>
        </div>
        {formatUptime(node.started_at) && (
          <div className="detail-card-row">
            <span className="detail-card-label">{t("my_nodes.detail_uptime")}</span>
            <span className="detail-card-value">{formatUptime(node.started_at)}</span>
          </div>
        )}
        <div className="detail-card-row">
          <span className="detail-card-label">{t("my_nodes.auth")}</span>
          <span className="detail-card-value">
            <span className={`auth-flag ${node.auth ? "authed" : "not-authed"}`}>
              {node.auth ? "\u2713 " + t("my_nodes.authenticated") : "\u2717 " + t("my_nodes.not_authenticated")}
            </span>
          </span>
        </div>
        {node.owner_address && (
          <div className="detail-card-row">
            <span className="detail-card-label">{t("my_nodes.detail_owner_address")}</span>
            <span className="detail-card-value detail-owner-address">{node.owner_address}<CopyButton value={node.owner_address} title={t("my_nodes.copy_owner")} /></span>
          </div>
        )}
        {node.version && (
          <div className="detail-card-row">
            <span className="detail-card-label">{t("nodes.version")}</span>
            <span className="detail-card-value">{node.version}</span>
          </div>
        )}
        {node.bin_hash && (
          <div className="detail-card-row">
            <span className="detail-card-label">{t("nodes.hash")}</span>
            <span className="detail-card-value">{node.bin_hash}</span>
          </div>
        )}
      </div>

      <div className="detail-card-group">
        <div className="detail-card-group-title">{t("nodes.hardware")}</div>
        {gpuSummary && (
          <div className="detail-card-row">
            <span className="detail-card-label">{t("nodes.gpu")}</span>
            <span className="detail-card-value">{gpuSummary}</span>
          </div>
        )}
        {cpus.map((c, i) => (
          <div className="detail-card-row" key={i}>
            <span className="detail-card-label">{t("nodes.cpu")}</span>
            <span className="detail-card-value">{c.name}</span>
          </div>
        ))}
        <div className="detail-card-row">
          <span className="detail-card-label">{t("nodes.ram")}</span>
          <span className="detail-card-value">
            {ram.total_gb ? `${ram.total_gb}GB` : "-"}
          </span>
        </div>
      </div>

      {(node.tpm_verified || node.ek_cert_issuer) && (
        <div className="detail-card-group">
          <div className="detail-card-group-title">TPM</div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("my_nodes.detail_status")}</span>
            <span className="detail-card-value">
              {node.tpm_verified
                ? <span style={{ color: "var(--color-success)" }}>&#10003; Verified</span>
                : <span style={{ color: "var(--text-muted)" }}>{t("my_nodes.detail_not_verified")}</span>
              }
            </span>
          </div>
          {node.ek_cert_issuer && (
            <div className="detail-card-row">
              <span className="detail-card-label">{t("my_nodes.detail_issuer")}</span>
              <span className="detail-card-value">{node.ek_cert_issuer}</span>
            </div>
          )}
        </div>
      )}

      <div className="detail-card-group detail-danger-group">
        <button className="btn detail-danger-btn" onClick={() => onDelete(node)}>
          Remove from My Nodes
        </button>
      </div>
    </div>
  );
}

function ManageTab({ node, t, onAuth, authLoading, onRefresh }) {
  const toast = useToast();
  const [installedServices, setInstalledServices] = useState([]);
  const [manageLoading, setManageLoading] = useState(true);
  const [regBusy, setRegBusy] = useState(false);
  const [profileSets, setProfileSets] = useState({});   // { [svcName]: { engine, active, profiles[], schema[] } }
  const [profileBusy, setProfileBusy] = useState({});   // { [svcName]: bool }
  const [editorState, setEditorState] = useState(null); // { service, schema, editing, busy }
  const installedServicesRef = useRef([]);

  const nodeIdRef = useRef(node.id);
  const nodeRef = useRef(node);
  nodeIdRef.current = node.id;
  nodeRef.current = node;

  const enginesRef = useRef([]);
  const modelsRef = useRef([]);
  const engineMapRef = useRef({});
  const hasModelMapRef = useRef({});
  const enableMapRef = useRef({}); // last-known enable map; preserved on fetch failure

  // loadCatalog fetches this node's installed packages (one call) and
  // the gate-side service↔engine mapping. Service list comes from
  // n.services (rendezvous payload) — not duplicated here.
  const loadCatalog = useCallback(async () => {
    const nid = nodeIdRef.current;
    if (!nid) return;
    try {
      const c = new AbortController(); setTimeout(() => c.abort(), 5000);
      const resp = await fetch(`/node/${encodeURIComponent(nid)}/provider/packages`, { signal: c.signal, headers: { ...getAuthHeaders() } });
      const all = resp.ok ? await resp.json() : [];
      const arr = Array.isArray(all) ? all : [];
      enginesRef.current = arr.filter((v) => v.type === "engine");
      modelsRef.current = arr.filter((v) => v.type === "model");
    } catch {
      enginesRef.current = [];
      modelsRef.current = [];
    }
    try {
      const resp = await fetch("/gate/v1/software?type=engine");
      const engines = resp.ok ? await resp.json() : [];
      const map = {};
      (Array.isArray(engines) ? engines : []).forEach(e => {
        if (e.service_name) map[e.name] = e.service_name;
      });
      engineMapRef.current = map;
    } catch {
      engineMapRef.current = {};
    }
    try {
      const resp = await fetch("/gate/v1/software?type=service");
      const services = resp.ok ? await resp.json() : [];
      const map = {};
      (Array.isArray(services) ? services : []).forEach(s => {
        if (s.has_model) map[s.name] = true;
      });
      hasModelMapRef.current = map;
    } catch {
      hasModelMapRef.current = {};
    }
  }, []);

  const loadServices = useCallback(async () => {
    const nid = nodeIdRef.current;
    const n = nodeRef.current;
    if (!n.auth || !nid) return;

    // provider.json 의 services[].enable — fetch 실패 시 직전 값 유지하여
    // 화면에서 disabled 서비스가 잠시 보였다 사라지는 깜빡임 방지.
    let enableMap = enableMapRef.current;
    try {
      const c = new AbortController(); setTimeout(() => c.abort(), 5000);
      const r = await fetch(`/node/${encodeURIComponent(nid)}/provider/config?name=provider`, { signal: c.signal, headers: { ...getAuthHeaders() } });
      if (r.ok) {
        const cfg = await r.json();
        const fresh = {};
        for (const svc of (cfg?.services || [])) {
          fresh[svc.name] = svc.enable !== false; // missing/null/true → enabled
        }
        enableMap = fresh;
        enableMapRef.current = fresh;
      }
    } catch {}

    try {
      // PID 기반 process list 는 wrapped 시대 잔재 — 컨테이너 패턴에선
      // 의미 없음. running 판정은 svcInfo.server_ready 로 통일.
      const procs = [];

      // Service list now comes from the rendezvous-published n.services so
      // there's a single source of truth (provider.json). Engines are joined
      // in from the installed-package catalog to surface the dependency line
      // (└─ {engine} ✓ v...) under each service card.
      const engines = Array.isArray(enginesRef.current) ? enginesRef.current : [];
      const eMap = engineMapRef.current;
      const svcEntries = (n.services || []);
      const list = await Promise.all(svcEntries.map(async (svcInfo) => {
          const v = { name: svcInfo.name, type: "service" };
          const proc = (Array.isArray(procs) ? procs : []).find(p => p.name === v.name);
          // engine→service link is taken from gate's `service_name` first, then
          // falls back to the engine package.json's own `service` field. Lets
          // engines run without being registered in the central gate DB.
          const depEngine = engines.find(e => eMap[e.name] === v.name || e.service === v.name) || null;
          // docker 서비스는 모델/실행인자가 매니페스트 + .env 에 있으니 UI 의
          // 파일 기반 모델 선택자 (sd 의 .safetensors picker) 가 의미 없음.
          // hasModel=false 로 두면 Start 버튼의 `hasModel && !currentModel`
          // 가드도 자동으로 비활성화 → docker 서비스는 항상 Start 가능.
          const launcherForHasModel = svcInfo?.launcher || "";
          const hasModel = launcherForHasModel === "docker" ? false : !!hasModelMapRef.current[v.name];
          // Model comes from manifest.inspect.fields[].value (key=model_default).
          // svcInfo.inspect carries it through the rendezvous register payload.
          const currentModel = svcInfo?.inspect?.model_default || "";
          // Running 판정은 RV 의 svcInfo.server_ready/loading 단일 소스로 통일.
          // PID 기반은 engine-runner 시절 잔재, docker / external 둘 다 HTTP
          // probe 결과가 진실의 채널 — 분기 없이 그대로 사용.
          const launcher = svcInfo?.launcher || "";
          const isDocker = launcher === "docker";
          const isExternalKind = !isDocker && (v.kind === "vllm" || v.kind === "external" || launcher === "external");
          const running = !!(svcInfo?.server_ready || svcInfo?.server_loading);

          let healthData = null;
          let jobs = [];
          if (svcInfo) {
            healthData = {
              status: svcInfo.server_ready ? "ready" : (svcInfo.server_loading ? "loading" : ""),
              server: svcInfo.server_ready || false,
              server_loading: svcInfo.server_loading || false,
              model: svcInfo.model || "",
              bin_hash: svcInfo.bin_hash || "",
              queue_depth: svcInfo.queue_depth || 0,
              total_jobs_done: svcInfo.total_jobs_done || 0,
            };
          }

          if (running && healthData && healthData.server && !healthData.server_loading) {
            try {
              const hc = new AbortController(); setTimeout(() => hc.abort(), 5000);
              const jResp = await fetch(`/node/${encodeURIComponent(nid)}/svc/${encodeURIComponent(v.name)}/v1/jobs/`, { signal: hc.signal });
              if (jResp.ok) jobs = await jResp.json();
            } catch {}
          }

          // Companion = the engine child process (sd-server, llama-server, etc.)
          // Sources: 1) processes list (pid file — works even when stopped/zombie)
          //          2) /health child_name/child_pid (when running)
          let companion = null;
          const childName = (svcInfo?.child_name || "").replace(/\.exe$/i, "");
          const childPid = svcInfo?.child_pid || 0;
          // Try pid-file based process first (survives parent crash)
          if (childName && running) {
            companion = {
              name: childName,
              pid: childPid,
              running: true,
            };
          }

          const enabled = enableMap[v.name] !== false; // missing → enabled (default)
          // Mark the lifecycle kind so ServiceCard chooses the right badge
          // and Start/Stop visibility. Docker services are IANN-managed
          // through isannd; external endpoints are read-only.
          const kind = isDocker ? "docker" : (v.kind || svcInfo?.type || svcInfo?.launcher || "");
          return { ...v, kind, running, enabled, external: isExternalKind, pid: proc?.pid || 0, companion, svcInfo, depEngine, hasModel, currentModel, healthData, jobs: Array.isArray(jobs) ? jobs : [] };
        }));

      // Hide disabled managed services from the list — same UX as external below.
      const filtered = list.filter(s => s.enabled !== false);
      list.length = 0;
      list.push(...filtered);

      // Externally-managed services (e.g. vLLM) are NOT in the installer's
      // /v1/versions response. Anything the RV knows about but the installer
      // doesn't must be external — the installer only tracks what it spawned.
      // This is more robust than gating on svcInfo.type, since older provider
      // or RV binaries may not propagate the type field yet.
      const knownNames = new Set(list.map(s => s.name));
      for (const svcInfo of (n.services || [])) {
        if (knownNames.has(svcInfo.name)) continue;
        // Skip disabled external services — provider's heartbeat may have
        // stopped already but RV cache could still hold the last svcInfo
        // until it ages out. Frontend filter ensures the card disappears
        // immediately on toggle.
        if (enableMap[svcInfo.name] === false) continue;
        const hd = {
          status: svcInfo.server_ready ? "ready" : (svcInfo.server_loading ? "loading" : ""),
          server: svcInfo.server_ready || false,
          server_loading: svcInfo.server_loading || false,
          model: svcInfo.model || "",
          bin_hash: "",
          queue_depth: svcInfo.queue_depth || 0,
          total_jobs_done: svcInfo.total_jobs_done || 0,
        };
        // Lifecycle-controllable when isannd manages the container
        // (launcher=docker); external endpoints (vllm etc.) stay
        // read-only and show "externally managed" badge.
        const isDocker = (svcInfo.launcher || "") === "docker";
        list.push({
          name: svcInfo.name,
          type: "service",
          kind: isDocker ? "docker" : (svcInfo.type || svcInfo.launcher || "external"),
          external: !isDocker,
          version: svcInfo.version || "",
          running: svcInfo.server_ready || svcInfo.server_loading || false,
          pid: 0,
          companion: null,
          svcInfo,
          depEngine: null,
          hasModel: false,
          currentModel: svcInfo.model || "",
          healthData: hd,
          jobs: [],
        });
      }

      setInstalledServices(prev => {
        const newJson = JSON.stringify(list);
        const prevJson = JSON.stringify(prev);
        if (newJson === prevJson) return prev;
        installedServicesRef.current = list;
        return list;
      });
    } catch {
      setInstalledServices(prev => prev.length === 0 ? prev : []);
    }
  }, []);

  useEffect(() => {
    if (!node.auth || !node.id) { setManageLoading(false); return; }
    setManageLoading(true);
    // Safety timeout — if the provider tunnel is flaky (502 / slow), never
    // leave the skeleton stuck on screen. After 8s we reveal whatever we
    // have (empty list → "No services installed", otherwise stale cache).
    const safetyTimer = setTimeout(() => setManageLoading(false), 8000);
    loadCatalog()
      .then(loadServices)
      .finally(() => {
        clearTimeout(safetyTimer);
        setManageLoading(false);
      });
    return () => clearTimeout(safetyTimer);
  }, [node.id]); // eslint-disable-line

  const { handleStart, handleStop, actionLoading, actionLoadingRef, setActionLoading } = useServiceActions(node.id, loadServices, onRefresh, installedServicesRef);

  useEffect(() => {
    const al = actionLoadingRef.current;
    if (!al) return;
    const svc = installedServices.find(s => s.name === al.name);
    if ((al.action === "starting" && svc?.running) || (al.action === "stopping" && (!svc || !svc.running))) {
      setActionLoading(null);
    }
  }, [installedServices]); // eslint-disable-line

  useEffect(() => {
    if (!node.auth || !node.id) return;
    let active = true;
    const poll = async () => {
      if (!active) return;
      await loadServices();
      if (!active) return;
      const hasAction = !!actionLoadingRef.current;
      const svcs = installedServicesRef.current;
      const loading = svcs.some(s => s.running && (!s.healthData || s.healthData.server !== true));
      const recentStart = svcs.some(s => s.running && !s.healthData);
      const hasRunning = svcs.some(s => s.running);
      setTimeout(poll, (hasAction || loading || recentStart) ? 1000 : hasRunning ? 5000 : 30000);
    };
    setTimeout(poll, 5000);
    return () => { active = false; };
  }, [node.id]); // eslint-disable-line

  const installedModels = useMemo(() => {
    const models = Array.isArray(modelsRef.current) ? modelsRef.current : [];
    return models.map(v => {
      const fileName = v.files?.[0]?.file_name || v.name;
      return { name: v.name, fileName, version: v.version, service: v.service || "" };
    });
  }, [installedServices]);

  const handleModelChange = async (svcName, modelFileName) => {
    const nid = nodeIdRef.current;
    if (!nid) return;
    const newConfig = {
      model: { default: modelFileName },
    };

    const models = Array.isArray(modelsRef.current) ? modelsRef.current : [];
    const modelManifest = models.find(v => Array.isArray(v.files) && v.files.some(f => f.file_name === modelFileName));
    if (modelManifest?.architecture) {
      newConfig.model_arch = modelManifest.architecture;
    }
    const fileEntry = modelManifest?.files?.find(f => f.file_name === modelFileName);
    if (fileEntry?.install_path && fileEntry.install_path !== "ai/models" && fileEntry.install_path !== "ai/models/" && fileEntry.install_path !== "./ai/models") {
      newConfig.model.dir = fileEntry.install_path;
    }
    try {
      const resp = await fetch(`/node/${encodeURIComponent(nid)}/provider/config`, {
        method: "POST", headers: { "Content-Type": "application/json", ...getAuthHeaders() },
        body: JSON.stringify({ name: svcName, config: newConfig }),
      });
      const data = await resp.json();
      if (resp.ok) {
        toast.success(`Model set: ${modelFileName}`);
        loadServices();
      } else {
        toast.error(data.error || "Failed to set model");
      }
    } catch (e) {
      toast.error(e.message || "Failed to set model");
    }
  };

  // Loads the per-service profile set in parallel with services. The result
  // map keys on service name; ServiceList renders ProfileSelector when a key
  // exists and falls back to ModelSelector otherwise.
  const loadProfileSets = useCallback(async () => {
    const nid = nodeIdRef.current;
    if (!nid) return;
    // External services (e.g. vLLM) don't have profiles — skip to avoid 502.
    const services = installedServicesRef.current
      .filter(s => !s.external)
      .map(s => s.name);
    const out = {};
    await Promise.all(services.map(async (name) => {
      const set = await fetchProfiles(nid, name);
      if (set && Array.isArray(set.profiles) && set.profiles.length > 0) {
        out[name] = set;
      }
    }));
    setProfileSets(out);
  }, []);

  const handleProfileAdd = (svcName) => {
    const set = profileSets[svcName];
    if (set?.editable === false) return;   // external (vllm) — read-only
    setEditorState({
      service: svcName,
      schema: set?.schema || [],
      editing: null,
      busy: false,
    });
  };

  const handleProfileEdit = (svcName, profile) => {
    const set = profileSets[svcName];
    if (set?.editable === false) return;   // external — read-only
    setEditorState({
      service: svcName,
      schema: set?.schema || [],
      editing: profile,
      busy: false,
    });
  };

  const handleProfileSave = async (payload) => {
    if (!editorState) return;
    const nid = nodeIdRef.current;
    if (!nid) return;
    setEditorState(s => ({ ...s, busy: true }));
    try {
      await upsertProfile(nid, { service: editorState.service, ...payload });
      toast.success(`Profile saved: ${payload.name}`);
      setEditorState(null);
      // 새 schema/list 다시 로드 + active 변경됐으면 서비스 재시작 → inspect 갱신.
      setTimeout(() => {
        loadCatalog().then(loadServices);
        loadProfileSets();
      }, payload.set_active ? 2000 : 500);
    } catch (e) {
      toast.error("Save failed: " + (e.message || e));
      setEditorState(s => ({ ...s, busy: false }));
    }
  };

  const handleProfileDelete = async (svcName, profileName) => {
    const nid = nodeIdRef.current;
    if (!nid) return;
    const set = profileSets[svcName];
    if (set?.editable === false) return;   // external — read-only
    if (!window.confirm(`Delete profile "${profileName}"?`)) return;
    setProfileBusy(b => ({ ...b, [svcName]: true }));
    try {
      await deleteProfile(nid, svcName, profileName);
      toast.success(`Profile deleted: ${profileName}`);
      loadProfileSets();
    } catch (e) {
      toast.error("Delete failed: " + (e.message || e));
    } finally {
      setProfileBusy(b => ({ ...b, [svcName]: false }));
    }
  };

  const handleProfileChange = async (svcName, profileName) => {
    const nid = nodeIdRef.current;
    if (!nid) return;
    setProfileBusy(b => ({ ...b, [svcName]: true }));
    try {
      await setActiveProfile(nid, svcName, profileName);
      toast.success(`Profile changed → restarting ${svcName}`);
      // Optimistic local update so UI reflects the new active immediately.
      setProfileSets(s => ({
        ...s,
        [svcName]: s[svcName] ? { ...s[svcName], active: profileName } : s[svcName],
      }));
      // Pick up the restart / new inspect after a short grace period.
      setTimeout(() => {
        loadCatalog().then(loadServices);
        loadProfileSets();
      }, 2000);
    } catch (e) {
      toast.error("Profile change failed: " + (e.message || e));
    } finally {
      setProfileBusy(b => ({ ...b, [svcName]: false }));
    }
  };

  // Refresh profile sets whenever the service list changes.
  useEffect(() => {
    if (installedServices.length > 0) {
      loadProfileSets();
    }
  }, [installedServices, loadProfileSets]);

  return (
    <div>
      <div className="services-title flex-between-center">
        <span>Services ({installedServices.length})</span>
        <span className="flex-row gap-6">
          <button className="svc-refresh-btn" onClick={() => loadCatalog().then(loadServices)} title={t("my_nodes.refresh")}>↻</button>
          <button
            className="svc-register-btn"
            onClick={async () => {
              const nid = nodeIdRef.current;
              if (!nid) return;
              setRegBusy(true);
              try {
                const r = await fetch(`/node/${encodeURIComponent(nid)}/provider/register`, {
                  method: "POST", headers: { ...getAuthHeaders() },
                });
                if (!r.ok) throw new Error(`HTTP ${r.status}`);
                toast.success("Register queued — RV will receive in ~1s");
                setTimeout(() => loadCatalog().then(loadServices), 1500);
              } catch (e) {
                toast.error("Register failed: " + e.message);
              } finally {
                setRegBusy(false);
              }
            }}
            title={t("my_nodes.push_fullsync")}
            disabled={regBusy}
          >
            {regBusy ? "…" : "⇡"}
          </button>
        </span>
      </div>
      <div className="services-subtitle">
        Installed services on this node. Toggle enable/disable in Settings tab; the runtime state below polls every 5–30s.
      </div>
      {manageLoading && installedServices.length === 0 ? (
        <div className="svc-list p-16">
          <Skeleton.Line width="60%" className="mb-12" />
          <Skeleton.Line width="80%" className="mb-8" />
          <Skeleton.Line width="50%" />
        </div>
      ) : installedServices.length === 0 ? (
        <p className="services-empty">{t("my_nodes.no_services_installed")}</p>
      ) : (
        <ServiceList
          services={installedServices.map(s => {
            // Live override — total_jobs_done / queue_depth 같은 메트릭은
            // node.services 의 최신 heartbeat 데이터로 덮어써서 loadServices
            // 스냅샷과 카드 aggregate 의 시점 불일치를 제거.
            const live = (node.services || []).find(x => x.name === s.name);
            if (!live) return s;
            return {
              ...s,
              healthData: {
                ...(s.healthData || {}),
                queue_depth:    live.queue_depth    ?? s.healthData?.queue_depth    ?? 0,
                total_jobs_done:live.total_jobs_done?? s.healthData?.total_jobs_done?? 0,
                server:         live.server_ready   ?? s.healthData?.server         ?? false,
                server_loading: live.server_loading ?? s.healthData?.server_loading ?? false,
                model:          live.model          ?? s.healthData?.model          ?? "",
              },
            };
          })}
          onStart={handleStart}
          onStop={handleStop}
          onModelChange={handleModelChange}
          actionLoading={actionLoading}
          installedModels={installedModels}
          profileSets={profileSets}
          onProfileChange={handleProfileChange}
          onProfileAdd={handleProfileAdd}
          onProfileEdit={handleProfileEdit}
          onProfileDelete={handleProfileDelete}
          profileBusy={profileBusy}
          t={t}
        />
      )}
      {editorState && (
        <ProfileEditor
          open={!!editorState}
          service={editorState.service}
          schema={editorState.schema}
          editing={editorState.editing}
          existing={profileSets[editorState.service]?.profiles || []}
          busy={editorState.busy}
          onSave={handleProfileSave}
          onClose={() => !editorState.busy && setEditorState(null)}
        />
      )}
    </div>
  );
}

function getSvcStatus(svc) {
  if (svc.server_loading) return "loading";
  if (svc.model) return "running";
  return "stopped";
}

// formatCtxBadge picks the most user-relevant inspect field for a service —
// ctx_size for llama.cpp, max_model_len for vllm — and renders the value as
// a compact "8K" / "16K" badge. Returns null when no relevant field exists.
function formatCtxBadge(svc) {
  const insp = svc?.inspect;
  if (!insp) return null;
  const raw = insp.ctx_size ?? insp.max_model_len;
  if (raw === undefined || raw === null || raw === "") return null;
  const n = parseInt(raw, 10);
  if (!Number.isFinite(n) || n <= 0) return null;
  if (n >= 1024 && n % 1024 === 0) return `${n / 1024}K`;
  return String(n);
}

function getNodePending(svcs) {
  return (svcs || []).reduce((sum, s) => sum + (s.queue_depth || 0), 0);
}

// RV 의 conn_status (alive/stale/offline) 가 있으면 우선시.
// 연결은 살아있지만 running 서비스가 0개면 idle 대신 "standby" — provider
// 프로세스는 떠 있어도 모델이 로드된 서비스가 없는 상태를 의미.
function getDisplayStatus(node) {
  const c = node.conn_status;
  if (c === "offline" || c === "stale") return c;
  const hasRunning = (node.services || []).some((s) => getSvcStatus(s) === "running");
  if (!hasRunning) return "standby";
  return node.status || "offline";
}

// Metrics 집계: queue / done / avg / running.
function getNodeJobStats(svcs) {
  let queue = 0, done = 0, weightedAvg = 0, runningCount = 0;
  for (const s of (svcs || [])) {
    queue += s.queue_depth || 0;
    done += s.total_jobs_done || 0;
    if (s.avg_job_sec > 0 && s.total_jobs_done > 0) {
      weightedAvg += s.avg_job_sec * s.total_jobs_done;
    }
    if (s.running_job_id) runningCount++;
  }
  const avgSec = done > 0 ? weightedAvg / done : 0;
  return { queue, done, avgSec, runningCount };
}

function formatDone(n) {
  if (!n) return "0";
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + "M";
  if (n >= 1_000)     return (n / 1_000).toFixed(1) + "k";
  return String(n);
}

function formatAvgSec(s) {
  if (!s || s <= 0) return "";
  if (s < 1)   return Math.round(s * 1000) + "ms";
  if (s < 60)  return s.toFixed(1) + "s";
  return (s / 60).toFixed(1) + "m";
}

function getNodeGpu(node) {
  return summarizeGpus(node.hardware?.gpus);
}

function truncateId(id) {
  if (!id || id.length <= 16) return id || "";
  return id.slice(0, 10) + "..." + id.slice(-4);
}

function getEmblemUrl(node) {
  const emblem = node.emblem;
  if (!emblem) return null;
  if (emblem.startsWith("http")) return emblem;
  return `/node/${encodeURIComponent(node.id || node.node_id)}/provider/file?path=${encodeURIComponent(emblem)}`;
}

const MAX_VISIBLE_SERVICES = 2;

function MyNodeCard({ node, isSelected, onSelect }) {
  const { t } = useTranslation();
  const nid = node.id || "";
  const allSvcs = node.services || [];
  const svcs = allSvcs.filter(s => getSvcStatus(s) === "running");
  const visibleSvcs = svcs.slice(0, MAX_VISIBLE_SERVICES);
  const extraCount = svcs.length - MAX_VISIBLE_SERVICES;
  const pending = getNodePending(svcs);
  const stats = getNodeJobStats(svcs);
  const gpu = getNodeGpu(node);
  const isOffline = node.conn_status
    ? node.conn_status === "offline"
    : node.status === "offline";

  const cls = ["node-card"];
  if (isSelected) cls.push("selected");
  if (isOffline) cls.push("offline");

  return (
    <div className={cls.join(" ")} onClick={() => onSelect(node)}>
      <div className="node-card-emblem">
        <EmblemImg src={getEmblemUrl(node)} nodeId={nid} />
      </div>
      <div className="node-card-content">
        <div className="node-card-header">
          <span className="node-card-id">{truncateId(nid)}</span>
          <CopyButton value={nid} title={t("my_nodes.copy_node_id")} />
          <StatusTag value={getDisplayStatus(node)} />
          <span className="node-card-hdr-right">
            <span className={`icon-slot ${node.auth_mode === "protected" ? "" : "inactive"}`} title={node.auth_mode === "protected" ? "Protected" : "Not protected"}>
              <svg viewBox="0 0 24 24" width="18" height="18">
                <rect x="6" y="11" width="12" height="9" rx="2" fill="#4a9eff" stroke="#3080d0" strokeWidth="0.8"/>
                <path d="M9 11V8a3 3 0 0 1 6 0v3" fill="none" stroke="#3080d0" strokeWidth="1.5" strokeLinecap="round"/>
                <circle cx="12" cy="15.5" r="1.5" fill="#fff"/>
              </svg>
            </span>
            <span className="icon-slot" title={t("my_nodes.favorited")}><span className="fav-star-icon">{"★"}</span></span>
          </span>
        </div>
        <div className="node-card-label-row">
          <span className="node-card-label">{node.label || "\u00A0"}</span>
          <span className="node-card-gpu">{gpu || "—"}</span>
        </div>

        <div className="node-card-services">
          {visibleSvcs.length === 0 && (
            <div className="svc-row svc-row-empty">{t("my_nodes.no_services_running")}</div>
          )}
          {visibleSvcs.map((s, i) => {
            const st = getSvcStatus(s);
            const ctx = formatCtxBadge(s);
            return (
              <div className="svc-row" key={i}>
                <div className="svc-row-head">
                  <span className={`svc-dot ${st}`} />
                  <span className="svc-name">{s.name}</span>
                  {s.model
                    ? <ModelLabel modelName={s.model} originUrl={s.model_origin_url} hash={s.model_hash} className="svc-model-inline" />
                    : <span className="svc-model-inline">{"\u2014"}</span>}
                  {ctx && <span className="svc-ctx-badge" title={t("my_nodes.context_length")}>{ctx}</span>}
                </div>
              </div>
            );
          })}
          {extraCount > 0 && <div className="svc-more">+{extraCount} more</div>}
        </div>

        <div className="node-card-footer">
          <div className="node-card-footer-row node-card-jobs-row">
            <span className="jobs-left">
              {pending > 0 ? (
                <span className="queue-dot">{pending.toLocaleString()} pending</span>
              ) : (
                <span>{isOffline ? "" : "0 pending"}</span>
              )}
            </span>
            {!isOffline && (stats.done > 0 || stats.avgSec > 0 || stats.runningCount > 0) && (
              <span className="jobs-right node-card-stats">
                {stats.done > 0 && (
                  <span className="stat-item" title={`Total completed jobs: ${stats.done.toLocaleString()}`}>
                    {formatDone(stats.done)} done
                  </span>
                )}
                {stats.avgSec > 0 && (
                  <span className="stat-item" title={t("my_nodes.average_job_duration")}>
                    {"\u00b7"} avg {formatAvgSec(stats.avgSec)}
                  </span>
                )}
                {stats.runningCount > 0 && (
                  <span className="stat-running" title={t("my_nodes.running_jobs")}>
                    running{stats.runningCount > 1 ? ` \u00d7${stats.runningCount}` : ""}
                  </span>
                )}
                {formatUptime(node.started_at) && (
                  <span className="stat-item" title={t("my_nodes.uptime_since_register")}>
                    {"\u00b7"} up {formatUptime(node.started_at)}
                  </span>
                )}
              </span>
            )}
          </div>
          {/* TPM badge below the metrics row. Same two-state convention as
              /nodes list and /search results: green ✓ when fully verified
              (RV challenge passed), gray pending when only the EK cert was
              received but verification didn't complete. */}
          {node.tpm_verified ? (
            <span className="node-card-tpm-badge verified" title="fTPM verified (challenge passed)">
              ✓ TPM: {node.ek_cert_issuer || "verified"}
            </span>
          ) : node.ek_cert_issuer ? (
            <span className="node-card-tpm-badge pending" title={t("my_nodes.ek_cert_pending")}>
              TPM: {node.ek_cert_issuer}
            </span>
          ) : null}
        </div>
      </div>
    </div>
  );
}

function NotAuthorizedNotice({ t, loading }) {
  return (
    <div className="not-authorized-notice">
      <p className="notice-text">
        {loading
          ? (t("my_nodes.verifying") || "Verifying with provider...")
          : (t("my_nodes.not_authorized") || "Logged-in wallet is not authorized for this node.")}
      </p>
    </div>
  );
}

export default function MyNodes() {
  const { t } = useTranslation();
  const { isLoggedIn } = useAuth();
  const [showLogin, setShowLogin] = useState(false);
  const [myNodes, setMyNodes] = useState([]);
  const [allNodes, setAllNodes] = useState([]);
  const [initialLoading, setInitialLoading] = useState(true);
  const [search, setSearch] = useSessionState("mn.search", "");
  const [authOnly, setAuthOnly] = useSessionState("mn.authOnly", false);
  const [serviceFilter, setServiceFilter] = useSessionState("mn.serviceFilter", "");
  const [onlineFilter, setOnlineFilter] = useSessionState("mn.onlineFilter", "");
  const [selectedNodeId, setSelectedNodeId] = useSessionState("mn.selectedNodeId", null);
  const [activeTab, setActiveTab] = useSessionState("mn.activeTab", "detail");
  const [modal, setModal] = useState(null);
  const [deleting, setDeleting] = useState(null);
  const [authLoading, setAuthLoading] = useState(false);
  const [rvList, setRvList] = useState([]);
  const [selectedRv, setSelectedRv] = useSessionState("mn.selectedRv", "");
  const [splitHeight, setSplitHeight] = useSessionState("mn.splitHeight", 250);
  const splitDragging = useRef(false);
  const splitStartY = useRef(0);
  const splitStartH = useRef(0);

  const onSplitterDown = (e) => {
    splitDragging.current = true;
    splitStartY.current = e.clientY;
    splitStartH.current = splitHeight;
    document.body.style.cursor = "row-resize";
    document.body.style.userSelect = "none";
  };

  useEffect(() => {
    const onMove = (e) => {
      if (!splitDragging.current) return;
      const delta = e.clientY - splitStartY.current;
      setSplitHeight(Math.max(200, Math.min(600, splitStartH.current + delta)));
    };
    const onUp = () => {
      splitDragging.current = false;
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    };
    document.addEventListener("mousemove", onMove);
    document.addEventListener("mouseup", onUp);
    return () => { document.removeEventListener("mousemove", onMove); document.removeEventListener("mouseup", onUp); };
  }, []);

  const loadMyNodes = useCallback(async () => {
    const start = Date.now();
    try {
      const data = await fetchMyNodes();
      setMyNodes(Array.isArray(data) ? data : []);
    } catch {}
    const elapsed = Date.now() - start;
    if (elapsed < 400) await new Promise(r => setTimeout(r, 400 - elapsed));
    setInitialLoading(false);
  }, []);

  useEffect(() => { loadMyNodes(); }, [loadMyNodes]);

  // Rendezvous list
  useEffect(() => {
    fetchRendezvousList().then(data => {
      const list = Array.isArray(data) ? data : [];
      setRvList(list);
      if (list.length > 0 && !selectedRv) setSelectedRv(list[0].id);
    }).catch(() => setRvList([]));
  }, []); // eslint-disable-line

  const loadAllNodes = useCallback(async () => {
    // /v1/nodes 는 static 전용이라 queue_depth / status 등 volatile 는 /v1/metrics 에서
    // 가져와 서비스별로 병합해야 실시간 jobs 수치가 반영됨.
    try {
      if (rvList.length > 0 && selectedRv) {
        const rv = rvList.find(r => r.id === selectedRv);
        if (rv?.addr) {
          const [data, metrics] = await Promise.all([
            fetchNodesByRendezvousAddr(rv.addr),
            fetchMetricsByAddr(rv.addr).catch(() => []),
          ]);
          const nodes = Array.isArray(data) ? data : [];
          mergeMetricsIntoNodes(nodes, metrics);
          setAllNodes(nodes);
          return;
        }
      }
      // Fallback: broker 의 /v1/nodes + /v1/metrics 병합
      const [resp, metrics] = await Promise.all([
        fetch("/v1/nodes").then(r => r.json()).catch(() => []),
        fetchMetrics().catch(() => []),
      ]);
      const nodes = Array.isArray(resp) ? resp : [];
      mergeMetricsIntoNodes(nodes, metrics);
      setAllNodes(nodes);
    } catch {
      setAllNodes([]);
    }
  }, [rvList, selectedRv]);

  useEffect(() => {
    loadAllNodes();
    const timer = setInterval(loadAllNodes, 30000);
    return () => clearInterval(timer);
  }, [loadAllNodes]);

  const refreshAll = useCallback(() => {
    loadMyNodes();
    loadAllNodes();
  }, [loadMyNodes, loadAllNodes]);

  const mergedNodes = useMemo(() => {
    return myNodes.map(mn => {
      const live = allNodes.find(n => n.id === mn.id);
      return {
        ...mn,
        addr: live?.addr || "-",
        status: live?.status || "offline",
        online: live?.online || false,
        conn_status: live?.conn_status || "",
        last_seen_ms: live?.last_seen_ms || 0,
        version: live?.version || "",
        bin_hash: live?.bin_hash || "",
        owner_address: live?.owner_address || "",
        hardware: live?.hardware || {},
        services: live?.services || [],
        emblem: live?.emblem || "",
        auth_mode: live?.auth_mode || "",
        tpm_verified: live?.tpm_verified || false,
        ek_cert_issuer: live?.ek_cert_issuer || "",
        // started_at = node's first-register timestamp (RFC3339) — drives
        // the Uptime row on the browse card and Detail tab.
        started_at: live?.started_at || "",
      };
    });
  }, [myNodes, allNodes]);

  const serviceOptions = useMemo(() => {
    const set = new Set();
    mergedNodes.forEach(n => (n.services || []).forEach(s => set.add(s.name)));
    return Array.from(set);
  }, [mergedNodes]);

  const filtered = useMemo(() => {
    return mergedNodes.filter(n => {
      if (search && !n.id.toLowerCase().includes(search.toLowerCase()) && !n.label?.toLowerCase().includes(search.toLowerCase()) && !n.owner_address?.toLowerCase().includes(search.toLowerCase())) return false;
      if (authOnly && !n.auth) return false;
      if (serviceFilter && !(n.services || []).some(s => s.name === serviceFilter)) return false;
      if (onlineFilter === "online" && !n.online) return false;
      if (onlineFilter === "offline" && n.online) return false;
      return true;
    });
  }, [mergedNodes, search, authOnly, serviceFilter, onlineFilter]);

  const selectedData = useMemo(() => {
    return mergedNodes.find(n => n.id === selectedNodeId);
  }, [mergedNodes, selectedNodeId]);

  const handleAdd = (data) => {
    if (!data.id) return;
    addMyNode(data.id, data.label).then(() => {
      loadMyNodes();
      setModal(null);
    });
  };

  const handleDelete = () => {
    deleteMyNode(deleting.id).then(() => {
      loadMyNodes();
      if (selectedNodeId === deleting.id) setSelectedNodeId(null);
      setDeleting(null);
    });
  };

  const handleLabelSave = async (nodeId, label) => {
    await updateMyNode(nodeId, label);
    loadMyNodes();
  };

  const handleAuth = async ({ silent = false } = {}) => {
    if (!selectedNodeId) return;
    setAuthLoading(true);
    try {
      const res = await authMyNode(selectedNodeId);
      if (!silent) {
        if (res.auth) alert(t("my_nodes.auth_success"));
        else alert(t("my_nodes.auth_failed") + (res.message ? ": " + res.message : ""));
      }
      loadMyNodes();
    } catch {
      if (!silent) alert(t("my_nodes.auth_failed"));
    }
    setAuthLoading(false);
  };

  // Auto-handshake when a node is selected (broker login already proves identity).
  useEffect(() => {
    if (!selectedData) return;
    if (!selectedData.auth && !authLoading) {
      handleAuth({ silent: true });
    }
  }, [selectedNodeId, selectedData?.auth]);

  const handleSelect = (node) => {
    // Toggle selection. Keep the active tab so users navigating between
    // nodes in "monitor mode" stay in monitor mode (no jarring reset).
    setSelectedNodeId(selectedNodeId === node.id ? null : node.id);
  };

  const addFields = useMemo(() => [
    { key: "id", label: t("my_nodes.node_id"), required: true },
    { key: "label", label: "Label" },
  ], [t]);

  if (!isLoggedIn) {
    return (
      <div className="page">
        <div className="page-header"><h2>{t("my_nodes.title")}</h2></div>
        <div className="login-required-card">
          <div className="login-required-icon">&#128274;</div>
          <h4 className="login-required-title">
            {t("auth.required_title", "Authentication Required")}
          </h4>
          <p className="login-required-desc">
            {t("auth.required_desc", "Please connect your wallet to manage your nodes")}
          </p>
          <button className="login-required-btn" onClick={() => setShowLogin(true)}>
            {t("auth.connect_wallet", "Connect Wallet")}
          </button>
        </div>
        {showLogin && <LoginModal onClose={() => setShowLogin(false)} />}
      </div>
    );
  }

  return (
    <div className="page">
      <div className="page-header"><h2>{t("my_nodes.title")}</h2></div>

      <div className="page-filters">
        <div className="page-filters-left">
          {rvList.length > 0 && (
            <Dropdown
              value={selectedRv}
              options={rvList.map(rv => ({ value: rv.id, label: `${rv.id} (${rv.region || "-"})` }))}
              onChange={(val) => setSelectedRv(val)}
              placeholder=""
            />
          )}
          <input
            type="text"
            className="filter-input min-w-280"
            placeholder="Node ID or Owner Address"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
          <Dropdown
            value={serviceFilter}
            options={serviceOptions}
            onChange={(val) => setServiceFilter(val)}
            placeholder={t("nodes.all_services")}
          />
          <Dropdown
            value={onlineFilter}
            options={[{ value: "online", label: "Online" }, { value: "offline", label: "Offline" }]}
            onChange={(val) => setOnlineFilter(val)}
            placeholder={t("nodes.all_status")}
          />
          <label className="filter-checkbox">
            <input type="checkbox" checked={authOnly} onChange={(e) => setAuthOnly(e.target.checked)} />
            {" "}{t("my_nodes.auth_only")}
          </label>
        </div>
        <button className="btn btn-add" onClick={() => setModal({ mode: "add" })}>
          {t("my_nodes.add")}
        </button>
      </div>

      <div className="page-body">
        <div>
          <div className="node-cards">
            {initialLoading ? (
              Array.from({ length: 2 }).map((_, i) => (
                <div key={`sk-${i}`} className="node-card">
                  <div className="node-card-emblem">
                    <Skeleton.Block width="100%" height="100%" borderRadius={0} />
                  </div>
                  <div className="node-card-content py-15">
                    <div className="node-card-header">
                      <Skeleton.Line width="60%" height={13} />
                    </div>
                    <div className="node-card-label"><Skeleton.Line width="40%" height={14} /></div>
                    <div className="node-card-services">
                      <div className="svc-row"><Skeleton.Line width="80%" height={12} /></div>
                      <div className="svc-row"><Skeleton.Line width="65%" height={12} /></div>
                    </div>
                    <div className="node-card-footer">
                      <div className="node-card-footer-row">
                        <Skeleton.Line width="50px" height={10} />
                        <Skeleton.Line width="70px" height={10} />
                      </div>
                      <div className="node-card-footer-row">
                        <Skeleton.Block width="60px" height={20} borderRadius={6} />
                        <Skeleton.Block width="70px" height={20} borderRadius={6} />
                      </div>
                    </div>
                  </div>
                </div>
              ))
            ) : (
              filtered.map((node) => (
                <MyNodeCard
                  key={node.id}
                  node={node}
                  isSelected={selectedNodeId === node.id}
                  onSelect={handleSelect}
                />
              ))
            )}
          </div>
        </div>

        {(selectedData || (selectedNodeId && initialLoading)) && (
          <div className="detail-wrap">
            <div className="detail-card">
              {initialLoading || !selectedData ? (
                <div className="detail-skeleton">
                  <div className="detail-skeleton-header">
                    <Skeleton.Line width="30%" height={20} />
                    <Skeleton.Block width="70px" height={22} borderRadius={6} />
                  </div>
                  <div className="detail-skeleton-tabs">
                    <Skeleton.Block width="60px" height={28} borderRadius={6} />
                    <Skeleton.Block width="70px" height={28} borderRadius={6} />
                    <Skeleton.Block width="60px" height={28} borderRadius={6} />
                    <Skeleton.Block width="70px" height={28} borderRadius={6} />
                    <Skeleton.Block width="50px" height={28} borderRadius={6} />
                    <Skeleton.Block width="50px" height={28} borderRadius={6} />
                  </div>
                  <div className="detail-skeleton-section">
                    <Skeleton.Line width="80px" height={12} className="mb-14" />
                    <div className="detail-skeleton-row">
                      <Skeleton.Line width="70px" height={13} />
                      <Skeleton.Line width="60%" height={13} />
                    </div>
                    <div className="detail-skeleton-row">
                      <Skeleton.Line width="50px" height={13} />
                      <Skeleton.Line width="40%" height={13} />
                    </div>
                    <div className="detail-skeleton-row">
                      <Skeleton.Line width="60px" height={13} />
                      <Skeleton.Line width="45%" height={13} />
                    </div>
                    <div className="detail-skeleton-row">
                      <Skeleton.Line width="50px" height={13} />
                      <Skeleton.Block width="60px" height={20} borderRadius={6} />
                    </div>
                    <div className="detail-skeleton-row">
                      <Skeleton.Line width="40px" height={13} />
                      <Skeleton.Line width="35%" height={13} />
                    </div>
                  </div>
                  <div className="detail-skeleton-section">
                    <Skeleton.Line width="90px" height={12} className="mb-14" />
                    <div className="detail-skeleton-row">
                      <Skeleton.Line width="40px" height={13} />
                      <Skeleton.Line width="55%" height={13} />
                    </div>
                    <div className="detail-skeleton-row">
                      <Skeleton.Line width="40px" height={13} />
                      <Skeleton.Line width="40%" height={13} />
                    </div>
                    <div className="detail-skeleton-row">
                      <Skeleton.Line width="40px" height={13} />
                      <Skeleton.Line width="45%" height={13} />
                    </div>
                  </div>
                </div>
              ) : (
              <>
              <div className="detail-card-header">
                <h3>{selectedData.label || truncateId(selectedData.id)}</h3>
              </div>
              <div className="detail-card-body">
                <div className="software-tabs software-tabs-spaced">
                  <button onClick={() => setActiveTab("detail")} className={`software-tab ${activeTab === "detail" ? "active" : ""}`}>
                    {t("my_nodes.tab_detail")}
                  </button>
                  {selectedData.auth && (
                    <>
                      <button onClick={() => setActiveTab("monitor")} className={`software-tab ${activeTab === "monitor" ? "active" : ""}`}>
                        {t("my_nodes.tab_monitor")}
                      </button>
                      <button onClick={() => setActiveTab("deploy")} className={`software-tab ${activeTab === "deploy" ? "active" : ""}`}>
                        Components
                      </button>
                      <button onClick={() => setActiveTab("profiles")} className={`software-tab ${activeTab === "profiles" ? "active" : ""}`}>
                        Profiles
                      </button>
                      <button onClick={() => setActiveTab("settings")} className={`software-tab ${activeTab === "settings" ? "active" : ""}`}>
                        {t("nav.settings")}
                      </button>
                      <button onClick={() => setActiveTab("sync")} className={`software-tab ${activeTab === "sync" ? "active" : ""}`}>
                        Sync
                      </button>
                    </>
                  )}
                </div>
                <div className={`tab-pane ${activeTab === "detail" ? "active" : ""}`}>
                  <NodeDetail node={selectedData} t={t} onAuth={handleAuth} onLabelSave={handleLabelSave} onDelete={(n) => setDeleting(n)} />
                </div>
                <div className={`tab-pane ${activeTab === "monitor" ? "active" : ""}`}>
                  {selectedData.auth ? (
                    <ManageTab node={selectedData} t={t} onAuth={handleAuth} authLoading={authLoading} onRefresh={refreshAll} />
                  ) : (
                    <NotAuthorizedNotice t={t} loading={authLoading} />
                  )}
                </div>
                <div className={`tab-pane ${activeTab === "settings" ? "active" : ""}`}>
                  {selectedData.auth ? (
                    <NodeSettingsTab node={selectedData} t={t} />
                  ) : (
                    <NotAuthorizedNotice t={t} loading={authLoading} />
                  )}
                </div>
                <div className={`tab-pane ${activeTab === "deploy" ? "active" : ""}`}>
                  {selectedData.auth ? (
                    <DeployTab node={selectedData} t={t} />
                  ) : (
                    <NotAuthorizedNotice t={t} loading={authLoading} />
                  )}
                </div>
                <div className={`tab-pane ${activeTab === "profiles" ? "active" : ""}`}>
                  {selectedData.auth ? (
                    <ProfilesTab node={selectedData} t={t} />
                  ) : (
                    <NotAuthorizedNotice t={t} loading={authLoading} />
                  )}
                </div>
                <div className={`tab-pane ${activeTab === "sync" ? "active" : ""}`}>
                  {selectedData.auth ? (
                    <SyncTab node={selectedData} t={t} />
                  ) : (
                    <NotAuthorizedNotice t={t} loading={authLoading} />
                  )}
                </div>
              </div>
              </>
              )}
            </div>
          </div>
        )}
      </div>

      {modal && (
        <FormModal
          title={t("my_nodes.add_title")}
          fields={addFields}
          initial={{}}
          onSave={handleAdd}
          onClose={() => setModal(null)}
        />
      )}

      {deleting && (
        <ConfirmDialog
          message={`${t("my_nodes.delete_confirm")} "${deleting.label || deleting.id}"?`}
          onConfirm={handleDelete}
          onCancel={() => setDeleting(null)}
        />
      )}
    </div>
  );
}
