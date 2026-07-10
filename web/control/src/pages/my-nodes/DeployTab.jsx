import React, { useState, useEffect, useMemo, useRef } from "react";
import { useSessionState } from "@utils/hooks";
import { getAuthHeaders } from "@utils/wallet";
import { formatSize } from "@utils/format";
import DataTable from "@components/DataTable";
import ConfirmDialog from "@components/ConfirmDialog";
import useInstallManager from "@hooks/useInstallManager";
import { fetchGateSoftware, fetchGatePackage } from "@api/software";
import "./DeployTab.scss";

const TYPE_TABS = [
  {
    key: "model",
    label: "Models",
    desc: "Model packages installed on disk. Add via isann CLI or the catalog Search page.",
    cli: [
      "# Install from a HuggingFace repo (sharded weights handled automatically)",
      "isann install -model --repo https://huggingface.co/<owner>/<repo>",
      "",
      "# Pick a specific quantization from a GGUF repo",
      "isann install -model --repo https://huggingface.co/<owner>/<repo> --path <file>.gguf --for-service llm-api",
      "",
      "# Install from a direct file URL (HF resolve URL, mirror, etc.)",
      "isann install -model --src https://huggingface.co/<owner>/<repo>/resolve/main/<file>.gguf --for-service llm-api",
      "",
      "# Import a local file",
      "isann install -model --for-service llm-api --src file:///abs/path/to/model.gguf",
      "",
      "# Import a sharded local directory (all shards in one folder)",
      "isann install -model --for-service llm-api --src file:///abs/path/to/model-dir/",
    ],
  },
  {
    key: "lora",
    label: "LoRAs",
    desc: "LoRA adapters installed on disk, grouped by base architecture (sd15/sdxl/qwen2/...). Apply via Profiles tab or sd-api inference request.",
    cli: [
      "# Install a LoRA from Civitai (architecture auto-derived from baseModel)",
      "isann install -lora --src https://civitai.com/models/<id>/<slug>",
      "",
      "# Install a LoRA from a direct file URL — architecture must be specified",
      "isann install -lora --src https://example.com/anime-style.safetensors --architecture sd15",
      "",
      "# Import a local LoRA file (hardlink-first, zero disk overhead on same FS)",
      "isann install -lora --src file:///abs/path/to/lora.safetensors --architecture sd15",
    ],
  },
];

export default function DeployTab({ node, t }) {
  const nodeId = node?.id || "";
  const [activeType, setActiveType] = useSessionState("dpt.type", "model");
  const isModelTab = activeType === "model";
  const isLoraTab = activeType === "lora";
  // Both Models and LoRAs use /provider/packages as source of truth (no
  // gate catalog) — the only difference is the row's category source
  // (service vs architecture). Treat them together as "node-sourced".
  const isNodeSourcedTab = isModelTab || isLoraTab;

  const [gateSoftware, setGateSoftware] = useState({});
  const [nodeVersions, setNodeVersions] = useState([]);
  const [gateManifests, setGateManifests] = useState({});
  const [loading, setLoading] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [confirmAction, setConfirmAction] = useState(null);
  // CLI cheat-sheet visibility — per-subtab so collapsing on Models
  // doesn't hide the LoRA examples the operator hasn't seen yet.
  // Stored as `{core:true, service:false, ...}` under one key so we
  // don't sprinkle 5 separate keys across localStorage.
  const [cliVisibleMap, setCliVisibleMap] = useState(() => {
    try {
      const raw = localStorage.getItem("iann.dpt.cliVisibleMap");
      if (raw) return JSON.parse(raw) || {};
    } catch {}
    return {};
  });
  // Default to true when a subtab hasn't been touched yet — first-time
  // visitors should see the examples; explicit hide is what gets remembered.
  const cliVisible = cliVisibleMap[activeType] !== false;
  const setCliVisible = (next) => {
    setCliVisibleMap((prev) => {
      const value = typeof next === "function" ? next(prev[activeType] !== false) : next;
      const merged = { ...prev, [activeType]: value };
      try { localStorage.setItem("iann.dpt.cliVisibleMap", JSON.stringify(merged)); } catch {}
      return merged;
    });
  };

  const gateSoftwareRef = useRef({});
  const selectedNodeIds = useMemo(() => nodeId ? [nodeId] : [], [nodeId]);
  const onlineNodes = useMemo(() => node ? [node] : [], [node]);
  const myNodes = onlineNodes;

  // 1) Gate software (catalog of installable infra components — core/services/engines).
  // Models tab no longer pulls a catalog list: the source of truth is the
  // node itself via /provider/packages, so we render whatever's actually
  // present on disk and skip any broker-side IndexedDB / gate model index.
  useEffect(() => {
    (async () => {
      try {
        const typeResults = await Promise.all(TYPE_TABS.map(async ({ key }) => {
          try { return [key, await fetchGateSoftware(key)]; }
          catch { return [key, []]; }
        }));
        const sw = Object.fromEntries(typeResults.map(([k, v]) => [k, Array.isArray(v) ? v : []]));
        gateSoftwareRef.current = sw;
        setGateSoftware(sw);
      } catch {}
    })();
  }, [refreshKey]);

  // 2) Installed versions for this node + partial downloads (resume 후보)
  useEffect(() => {
    if (!nodeId) return;
    (async () => {
      try {
        const r = await fetch(`/node/${encodeURIComponent(nodeId)}/provider/packages`, { headers: getAuthHeaders() });
        setNodeVersions(r.ok ? await r.json() : []);
      } catch { setNodeVersions([]); }
    })();
  }, [nodeId, refreshKey]);

  // 3) Manifests for active type
  // 의존성: activeType, gateSoftware 상태. refreshKey 는 빠진 것처럼 보이지만
  // refreshKey 변경 → Effect 1 이 gateSoftware 업데이트 → 이 effect 재실행으로 자연 전파됨.
  // 이렇게 해야 F5 직후 race condition (Effect 1 의 fetch 가 끝나기 전에 Effect 3 이
  // 빈 ref 로 실행돼서 manifest 가 비는 문제) 이 사라짐.
  useEffect(() => {
    const swList = gateSoftware[activeType] || [];
    if (swList.length === 0) {
      // gateSoftware 가 아직 안 채워졌거나 해당 타입에 등록된 게 없음
      setGateManifests({});
      setLoading(false);
      return;
    }
    setLoading(true);
    (async () => {
      const mf = {};
      await Promise.all(swList.map(async (s) => {
        try { const data = await fetchGatePackage(activeType, s.name); if (data) mf[s.name] = data; } catch {}
      }));
      setGateManifests(mf);
      setLoading(false);
    })();
  }, [activeType, gateSoftware]);

  // Build list
  const currentGateSw = gateSoftware[activeType] || [];
  const nodeVersionMap = useMemo(() => {
    const m = {};
    (Array.isArray(nodeVersions) ? nodeVersions : []).forEach(v => { m[v.name + ":" + v.type] = v; });
    return m;
  }, [nodeVersions]);

  const combinedList = useMemo(() => {
    // Models / LoRAs: source of truth is the node — render whatever
    // /provider/packages reports as installed. No gate catalog, no
    // broker-local DB. Models group under service (sd-api/llm-api/...),
    // LoRAs group under architecture (sd15/sdxl/...) — both flow through
    // the same card visuals; only the secondary badge text differs.
    if (isNodeSourcedTab) {
      return (Array.isArray(nodeVersions) ? nodeVersions : [])
        .filter(i => i.type === activeType)
        .map(i => ({
          name: i.name,
          service_name: i.service || "",
          architecture: i.architecture || "",
          latestVersion: i.version || "",
          installed: true,
          installedVersion: i.version || "",
          _source: "node",
          _nodeManifest: i,
          _installed: i,
          _status: "installed",
        }));
    }

    // Core / Services / Engines — gate-distributed catalog drives the
    // list (with installed-state derived from nodeVersionMap).
    //
    // Provider's /provider/packages response intentionally drops real
    // type=service package entries and re-injects synthesized stubs
    // for each enabled service in conf/provider.json (just name + type
    // + optional kind, no version / no downloads / no installed_at).
    // A bare existence check would mark every conf-listed service as
    // "Installed" even when the actual package isn't on disk. Require
    // a real install marker so synthesized stubs don't pass.
    const isRealInstall = (v) => !!v && (!!v.installed_at || !!v.version || (Array.isArray(v.downloads) && v.downloads.length > 0));
    return currentGateSw.map(sw => {
      const mf = gateManifests[sw.name];
      const inst = nodeVersionMap[sw.name + ":" + activeType];
      const isInstalled = isRealInstall(inst);
      return {
        name: sw.name, service_name: sw.service_name || "", latestVersion: mf?.version || "",
        installed: isInstalled, installedVersion: inst?.version || "",
        _source: "supported",
        _manifest: mf || null,
        _installed: isInstalled ? inst : null,
        _status: isInstalled ? "installed" : "not_installed",
      };
    });
  }, [currentGateSw, gateManifests, nodeVersionMap, nodeVersions, activeType, isNodeSourcedTab]);

  const filteredList = combinedList;

  // Install manager — adapted for single node using nodeStatus format
  const singleNodeVersions = useMemo(() => ({ [nodeId]: nodeVersions }), [nodeId, nodeVersions]);
  const installManagerList = useMemo(() => combinedList.map(item => ({
    ...item,
    nodeStatus: { [nodeId]: item.installed ? item.installedVersion : null },
    installedCount: item.installed ? 1 : 0,
  })), [combinedList, nodeId]);

  const {
    installJobs, setInstallJobs,
    handleInstall: _handleInstall,
    handleUninstall: _handleUninstall,
    isJobRunning,
  } = useInstallManager({ selectedNodeIds, onlineNodes, activeType, myNodes, setRefreshKey, setConfirmAction });

  const handleInstall = (row) => {
    const adapted = { ...row, nodeStatus: { [nodeId]: row.installed ? row.installedVersion : null } };
    _handleInstall(adapted);
  };
  const handleUninstall = (row) => {
    const adapted = { ...row, nodeStatus: { [nodeId]: row.installed ? row.installedVersion : null } };
    _handleUninstall(adapted);
  };

  // Progress helpers
  // Status class — drives progress-bar-fill background + status-label color.
  // CSS rules in DeployTab.scss (or models/index.scss) define the colors per
  // state. Keep `barColor` exported for places that still need the raw value.
  const statusCls = (s) => s === "file_done" ? "is-done"
    : s === "checking" ? "is-checking"
    : s === "error" ? "is-error"
    : s === "skip" ? "is-skip"
    : "is-default";
  const statusLbl = (s, p) => s === "file_done" ? "done" : s === "checking" ? "checking" : s === "skip" ? "skip" : s === "error" ? "error" : `${p ?? 0}%`;
  // 에러도 일시적으로 보여줘서 사용자 피드백 제공 (자동 제거는 useInstallManager 에서 setTimeout)
  const activeJobs = installJobs.filter(j => !j.done);

  const activeTab = TYPE_TABS.find(t => t.key === activeType) || TYPE_TABS[0];

  return (
    <div>
      {/* Type filter — refresh button sits at the right edge of the
          tab row so the tab strip + reload action read as one unit. */}
      <div className="deploy-type-tabs">
        <div className="deploy-type-tabs-left">
          {TYPE_TABS.map(tab => (
            <button
              key={tab.key}
              className={`deploy-type-tab ${activeType === tab.key ? "active" : ""}`}
              onClick={() => setActiveType(tab.key)}
            >{tab.label}</button>
          ))}
        </div>
        <button
          className="deploy-refresh-btn"
          onClick={() => setRefreshKey(k => k + 1)}
          disabled={loading}
          title="Refresh"
        >{loading ? "..." : "↻"}</button>
      </div>

      {/* Per-tab one-line description — gives the empty space below
          the tabs a purpose by stating what category the user is
          looking at. The optional `cli` block follows with
          copy-pasteable isann CLI examples for that category, and
          can be collapsed once the operator has memorized them. */}
      <div className="deploy-type-desc">
        <span>{activeTab.desc}</span>
        {Array.isArray(activeTab.cli) && activeTab.cli.length > 0 && (
          <button
            type="button"
            className="deploy-cli-toggle"
            onClick={() => setCliVisible(v => !v)}
            title={cliVisible ? "Hide CLI examples" : "Show CLI examples"}
          >
            {cliVisible ? "Hide CLI" : "Show CLI"}
          </button>
        )}
      </div>
      {cliVisible && Array.isArray(activeTab.cli) && activeTab.cli.length > 0 && (
        <pre className="deploy-type-cli">
          <code>{activeTab.cli.join("\n")}</code>
        </pre>
      )}

      {/* Table */}
      {loading ? <DataTable columns={[
          { key: "name", label: "Name" },
          { key: "version", label: "Version" },
          { key: "status", label: "Status" },
        ]} data={[]} loading={true} loadingRows={5} />
      : filteredList.length === 0 ? (
        <div className="deploy-empty">
          <p>No software found</p>
        </div>
      ) : (
        <div className="sw-card-grid">
          {filteredList.map((row, i) => (
            <NodeSoftwareCard
              key={row.name + i}
              row={row}
              type={activeType}
              isModelTab={isModelTab}
              isLoraTab={isLoraTab}
              running={isJobRunning(row.name)}
              onInstall={() => handleInstall(row)}
              onUninstall={() => handleUninstall(row)}
            />
          ))}
        </div>
      )}

      {/* Install progress */}
      {activeJobs.length > 0 && (
        <div className="install-panel">
          <div className="install-panel-title">Installing...</div>
          {activeJobs.map(job => (
            <div key={job.id} className="install-job">
              <div className="job-header">
                <span className={`job-name ${job.error ? "has-error" : ""}`}>{job.swName}</span>
                {!job.error && job.cancel && (
                  <button className="btn btn-sm btn-delete btn-cancel-job" onClick={() => job.cancel()}>
                    Cancel
                  </button>
                )}
              </div>
              {job.error && (
                <div className="job-error">
                  {job.error}
                </div>
              )}
              {Object.entries(job.progress).map(([file, p]) => (
                <div key={file} className="file-progress">
                  {/* File name sits ABOVE the bar so long basenames
                      (e.g. multi-tag GGUF / llama-bNNNN-bin-win-cuda-...
                      .zip) don't squeeze the bar into a thin sliver.
                      The status label stays on the bar row, right-aligned. */}
                  <div className="file-progress-name">
                    {file}
                    {p.resumed && (
                      <span className="resume-badge" title="Resumed from partial download">↻</span>
                    )}
                  </div>
                  <div className="file-progress-bar-row">
                    <div className="progress-bar-bg">
                      <div className={`progress-bar-fill ${statusCls(p.status)}`} style={{ width: `${p.status === "file_done" || p.status === "skip" ? 100 : p.percent ?? 0}%` }} />
                    </div>
                    <span className={`status-label ${statusCls(p.status)}`}>{statusLbl(p.status, p.percent)}</span>
                  </div>
                </div>
              ))}
            </div>
          ))}
        </div>
      )}

      {confirmAction && <ConfirmDialog message={confirmAction.message} onConfirm={confirmAction.onConfirm} onCancel={() => setConfirmAction(null)} />}
    </div>
  );
}

// ── NodeSoftwareCard ─────────────────────────────────────────────
// Mirrors the card used in pages/models (Deploy), but with this page's
// single-node row shape (row.installed / row.installedVersion instead of
// the multi-node nodeStatus map).

const ICON_MAP = {
  "sd-api":      "🎨",
  "diffusion":   "🎨",
  "llm-api":     "💬",
  "llm":         "💬",
  "voice-api":   "🎤",
  "voice":       "🎤",
  "tts":         "🎤",
  "stt":         "🎤",
  "terminal":    "💻",
  "shell":       "💻",
  "core":        "🔧",
  "provider":    "🔧",
  "broker":      "📡",
  "engine-runner": "⚙️",
  "installer":   "📥",
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

// modelOriginCandidates returns URLs from each known location on a row
// — preserves probe order so callers (modelPathPrefix, detectModelOrigin)
// look at the same sources in the same order.
function modelOriginCandidates(row) {
  return [
    row?._nodeManifest?.downloads?.[0]?.download_url,
    row?._installed?.downloads?.[0]?.download_url,
    row?._customModel?.download_url,
  ];
}

// modelPathPrefix returns the single-segment prefix to lead the card
// title with — HF owner (e.g. "jc-builds"), Civitai modelId (e.g.
// "2607063"), or "imported" when the package has no recoverable
// catalog URL (file:// or unknown hosts). Caller renders
// "{prefix}/{row.name}" so the title shape is consistent regardless
// of source.
function modelPathPrefix(row) {
  for (const url of modelOriginCandidates(row)) {
    if (!url) continue;
    let m;
    m = url.match(/^https?:\/\/(?:huggingface\.co|hf\.co)\/([^/]+)\//i);
    if (m) return m[1];
    m = url.match(/^https?:\/\/civitai\.com\/api\/download\/models\/(\d+)/i);
    if (m) return m[1];
    m = url.match(/^https?:\/\/civitai\.com\/models\/(\d+)/i);
    if (m) return m[1];
  }
  return "imported";
}

// detectModelOrigin classifies a row's source by URL pattern.
// Returns "huggingface" | "civitai" | "import" — drives the source
// badge next to the model title.
function detectModelOrigin(row) {
  for (const url of modelOriginCandidates(row)) {
    if (!url) continue;
    if (/^https?:\/\/(?:huggingface\.co|hf\.co)\//i.test(url)) return "huggingface";
    if (/^https?:\/\/civitai\.com\//i.test(url)) return "civitai";
  }
  return "import";
}

function describeRow(row, type) {
  const parts = [];
  if (row.service_name) parts.push(row.service_name);
  else parts.push(type.charAt(0).toUpperCase() + type.slice(1));
  // Models surface only the service name in the desc — version stamps
  // for imported / unknown packages are timestamps that read as noise
  // ("v20260508-165355") and provide no actionable signal.
  if (type !== "model") {
    const v = row.installedVersion || row.latestVersion;
    if (v && v !== "custom" && v !== "import") parts.push("v" + v);
  }
  return parts.join(" ");
}

function NodeSoftwareCard({
  row, type, isModelTab, isLoraTab, running,
  onInstall, onUninstall,
}) {
  const src = row._source;
  const installed = !!row.installed;
  // Models and LoRAs share the node-sourced flow (path prefix + brand
  // badge). The secondary tag differs: models tag by service, LoRAs tag
  // by base architecture.
  const isNodeSourced = isModelTab || isLoraTab;

  let primary = null;
  if (installed) {
    primary = <span className="sw-badge installed">✓ Installed</span>;
  } else if (src === "supported" && !isNodeSourced) {
    primary = (
      <button className="btn btn-sm btn-primary"
        onClick={(e) => { e.stopPropagation(); onInstall(); }} disabled={running}>
        {running ? "..." : "Install"}
      </button>
    );
  }

  return (
    <div className={`sw-card ${installed ? "installed" : ""}`}>
      <div className="sw-card-ico">{pickIcon(row.name, type)}</div>

      <div className="sw-card-body">
        <div className="sw-card-title">
          {(() => {
            const prefix = isNodeSourced ? modelPathPrefix(row) : "";
            if (!prefix) return row.name;
            // "imported" is a placeholder prefix (no real namespace
            // recoverable) — dim it so it reads as supplementary
            // context, not as authoritative provenance like a real
            // HF owner or Civitai modelId would. Wrapped in a single
            // span so the parent flex `gap` doesn't insert visible
            // whitespace between prefix / "/" / name.
            const muted = prefix === "imported";
            return (
              <span className="sw-card-title-text">
                <span className={muted ? "sw-card-prefix-muted" : "sw-card-prefix"}>
                  {prefix}
                </span>
                <span className="sw-card-prefix-sep">/</span>
                <span>{row.name}</span>
              </span>
            );
          })()}
        </div>
        <div className="sw-card-desc sw-card-tags">
          {isModelTab && row.service_name && (
            <span className="sw-service-tag">{row.service_name}</span>
          )}
          {isLoraTab && row.architecture && (
            <span className="sw-arch-tag">{row.architecture}</span>
          )}
          {isNodeSourced && (() => {
            const origin = detectModelOrigin(row);
            const labels = { huggingface: "HuggingFace", civitai: "Civitai", import: "Import" };
            return <span className={`brand-tag brand-tag-${origin}`}>{labels[origin]}</span>;
          })()}
          {!isNodeSourced && describeRow(row, type)}
        </div>
      </div>

      <div className="sw-card-actions">
        {primary}
        {installed && (
          <button className="btn btn-sm btn-delete-ghost"
            onClick={(e) => { e.stopPropagation(); onUninstall(); }}>
            Uninstall
          </button>
        )}
      </div>
    </div>
  );
}
