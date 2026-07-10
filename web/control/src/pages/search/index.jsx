// Unified marketplace search — Etherscan-style.
//
// Layout follows docs/TODO/search page/search-results-mockup.html:
//   - Search meta block: detection chip + confidence + order/sources/filters
//   - Running Nodes (single unified section, "You" badge for own nodes)
//   - Catalog section (HF + Civitai merged, "Use" for already-installed)
//
// On Enter / Search submission we run both APIs in parallel:
//   - broker GET /v1/search/nodes (Phase 2) — running nodes
//   - HF + Civitai catalog APIs (Phase 3)   — installable models
//
// Catalog calls are skipped for queries that have no catalog meaning
// (address / node ID / GPU / engine), so a wallet address doesn't return
// random model fuzzy matches.

import React, { useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { searchNodes, getCachedSearch } from "../../api/search";
import { searchCatalogs, fetchCivitaiByHash, getCachedCatalog, fetchHFById } from "../../api/catalog";
import { fetchMyNodes, addMyNode, authMyNode, fetchNodes, fetchMetrics, mergeMetricsIntoNodes } from "../../api/nodes";
import { getAuthHeaders } from "@utils/wallet";
import { detectQueryType } from "@utils/searchDetect";
import { getEmblemPalette } from "@utils/emblem";
import { formatSize, formatUptime } from "@utils/format";
import ModelLabel from "@components/ModelLabel/ModelLabel";
import { summarizeGpus, shortGpuName } from "@utils/gpu";
import StatusTag from "@components/StatusTag/StatusTag";
import { useTranslation } from "@i18n";
import { useToast } from "@components/Toast/ToastContext";
import "./index.scss";

// EmblemBadge — reuses the same .node-card-emblem styling as the Nodes /
// my-nodes pages so the avatar shape (2:3 portrait, gradient initials
// fallback) stays consistent across the site.
function EmblemBadge({ match }) {
  const nodeId = match.node_id || match.owner_address || "";
  const emblem = match.emblem;
  const emblemUrl = (() => {
    if (!emblem) return null;
    if (emblem.startsWith("http")) return emblem;
    return `/node/${encodeURIComponent(nodeId)}/provider/file?path=${encodeURIComponent(emblem)}`;
  })();
  const [failed, setFailed] = useState(false);
  const { initials, gradient } = getEmblemPalette(nodeId);
  return (
    <div className="node-card-emblem">
      {emblemUrl && !failed
        ? <img src={emblemUrl} alt="" onError={() => setFailed(true)} />
        : <div className="emblem-placeholder-base" style={{ background: gradient }}>{initials}</div>}
    </div>
  );
}

const ORDER_OPTIONS = [
  { value: "",          label: "⚡ Nodes first"  },
  { value: "catalog",   label: "📦 Catalog first" },
  { value: "mixed",     label: "⇆ Mixed"         },
];

const SOURCE_OPTIONS = [
  { id: "running",      label: "Running",     defaultOn: true },
  { id: "huggingface",  label: "HuggingFace", defaultOn: true },
  { id: "civitai",      label: "Civitai",     defaultOn: true },
  { id: "modelscope",   label: "ModelScope",  defaultOn: false, disabled: true },
];

export default function SearchPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const initialQ = params.get("q") || "";

  const [input, setInput] = useState(initialQ);
  const [submitted, setSubmitted] = useState(initialQ);
  const [order, setOrder] = useState(params.get("order") || "");
  const [sources, setSources] = useState(() => {
    const init = {};
    for (const s of SOURCE_OPTIONS) init[s.id] = s.defaultOn;
    return init;
  });
  const [hideNsfw, setHideNsfw] = useState(true);

  // Lazy initializer — read in-memory cache synchronously so back-navigation
  // paints results without any network flash. The useEffect below still
  // revalidates so the data stays fresh.
  const [nodeRes, setNodeRes] = useState(() => {
    if (!initialQ) return null;
    const det = detectQueryType(initialQ);
    return getCachedSearch(det.normalized || initialQ, {
      type: det.type,
      order: params.get("order") || undefined,
    });
  });
  const [catalogRes, setCatalogRes] = useState(() => {
    if (!initialQ) return null;
    const det = detectQueryType(initialQ);
    if (det.type !== "hash" && det.type !== "text") return null;
    const hf = getCachedCatalog("hf", det.normalized || initialQ, {});
    const civ = getCachedCatalog("civitai", det.normalized || initialQ, {});
    if (!hf && !civ) return null;
    return {
      items: [...(hf?.items || []), ...(civ?.items || [])],
      errors: {},
    };
  });
  const [running, setRunning] = useState(false);
  const [byHashItem, setByHashItem] = useState(null);
  const [errors, setErrors] = useState({});

  // myNodes: IndexedDB-stored records ({id, label, auth, ...}) merged with
  // online + GPU info from /v1/nodes. The Install modal needs the full record;
  // the legacy "You" badge only needs the ID Set, which we derive.
  const [myNodes, setMyNodes] = useState([]);
  const myNodeIds = useMemo(() => new Set(myNodes.map((n) => n.id)), [myNodes]);

  const refreshMyNodes = React.useCallback(async () => {
    try {
      const local = await fetchMyNodes();
      const localList = Array.isArray(local) ? local : [];
      // /v1/nodes is intentionally static (ETag-stable) and omits liveness
      // fields — RV moved `online`/`conn_status`/`status` to /v1/metrics.
      // We must call both and merge with mergeMetricsIntoNodes() to get
      // anything other than perpetual "offline". See pkg/rendezvous/server.go
      // line 1022-1024 for the design note.
      let serverById = new Map();
      try {
        const [server, metrics] = await Promise.all([fetchNodes(), fetchMetrics()]);
        const serverList = Array.isArray(server) ? server : [];
        const merged = mergeMetricsIntoNodes(serverList, metrics);
        for (const s of merged) {
          for (const k of [s.id, s.node_id]) {
            if (k) serverById.set(String(k).toLowerCase(), s);
          }
        }
      } catch { /* non-fatal */ }

      // Build the merged my-node list (without packages first so the UI
      // paints fast; package hashes get folded in async below).
      const merged = localList.map((n) => {
        const s = serverById.get(String(n.id || "").toLowerCase()) || {};
        const gpus = Array.isArray(s.hardware?.gpus) ? s.hardware.gpus : [];
        let online = false;
        if (typeof s.online === "boolean") online = s.online;
        if (!online && s.conn_status)      online = s.conn_status !== "offline";
        if (!online && s.status)           online = s.status !== "offline";
        return {
          ...n,
          online,
          gpus,
          services: Array.isArray(s.services) ? s.services : [],
          role: s.role || "",
          name: s.name || n.label || "",
          // RV-reported public address ("ip:port"). Used in the install
          // modal's target list when no operator-set label exists, so
          // the row reads as a recognizable network endpoint instead
          // of a 0x… proxy ID.
          addr: s.addr || "",
          // Per-package install records (filled by Phase 2 fan-out below).
          // Keeping each package's hashes/repos/name together — instead of
          // flattening into per-node Sets — preserves the package boundary so
          // a file:// import (no repos) doesn't get judged against another
          // package's repo info on the same node.
          installedPackages: [],
        };
      });
      setMyNodes(merged);

      // Phase 2: enrich each authed+online node with the actual on-disk
      // packages list via /node/<id>/provider/packages. Services[] only
      // covers models attached to a service — a downloaded but unassigned
      // model wouldn't show up otherwise. Failures are non-fatal: that
      // node just keeps the empty installedPackageHashes from phase 1.
      const targets = merged.filter((n) => n.auth && n.online);
      await Promise.all(targets.map(async (n) => {
        try {
          const resp = await fetch(`/node/${encodeURIComponent(n.id)}/provider/packages`, {
            headers: { ...getAuthHeaders() },
          });
          if (!resp.ok) return;
          const data = await resp.json();
          const arr = Array.isArray(data) ? data : (Array.isArray(data?.versions) ? data.versions : []);
          const installedPackages = packagesFromVersions(arr);
          if (installedPackages.length === 0) return;
          setMyNodes((prev) => prev.map((m) =>
            m.id === n.id ? { ...m, installedPackages } : m
          ));
        } catch { /* non-fatal — keep the empty list */ }
      }));
    } catch { /* non-fatal */ }
  }, []);

  const toast = useToast();
  // Active installs panel state — each entry is one ongoing install. SSE
  // progress events update `percent` and `status`; entry is dropped a few
  // seconds after done/error so the user has time to see the final state.
  const [activeInstalls, setActiveInstalls] = useState({});

  const updateInstall = useCallback((id, patch) => {
    setActiveInstalls((prev) => {
      const cur = prev[id];
      if (!cur && !patch) return prev;
      return { ...prev, [id]: { ...(cur || {}), ...patch } };
    });
  }, []);
  const dropInstall = useCallback((id, delayMs = 4000) => {
    setTimeout(() => {
      setActiveInstalls((prev) => {
        if (!prev[id]) return prev;
        const next = { ...prev };
        delete next[id];
        return next;
      });
    }, delayMs);
  }, []);

  // POST /node/<id>/installer/install returns SSE. If we don't drain the
  // body the broker→provider connection collapses, and pkg/provider/stream.go
  // detects client disconnect and KILLS the installer child process. We
  // therefore launch the request at page scope (lives past the modal close)
  // and consume the SSE stream — progress events drive the active-installs
  // panel; final done/error becomes a toast.
  const startBackgroundInstall = useCallback(async (nodeId, body, modelLabel) => {
    const jobId = `${nodeId}:${modelLabel}:${Date.now()}`;
    updateInstall(jobId, { id: jobId, label: modelLabel, nodeId, percent: 0, status: "starting", file: "" });
    toast.success(`Install started: ${modelLabel}`);
    let resp;
    try {
      resp = await fetch(`/node/${encodeURIComponent(nodeId)}/installer/install`, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...getAuthHeaders() },
        body: JSON.stringify(body),
      });
      if (!resp.ok) throw new Error(`install rejected: ${resp.status}`);
    } catch (err) {
      const msg = friendlyInstallError(err);
      updateInstall(jobId, { status: "error", error: msg });
      dropInstall(jobId);
      toast.error(`Install failed (${modelLabel}): ${msg}`);
      return;
    }
    // Drain SSE — translate event types into panel updates.
    try {
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();
      let buf = "";
      let errMsg = "";
      let sawError = false;
      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        buf += decoder.decode(value, { stream: true });
        const lines = buf.split("\n");
        buf = lines.pop();
        for (const line of lines) {
          if (!line.startsWith("data: ")) continue;
          try {
            const evt = JSON.parse(line.slice(6));
            switch (evt.event) {
              case "progress":
                updateInstall(jobId, {
                  status: "downloading",
                  percent: typeof evt.percent === "number" ? evt.percent : undefined,
                  file: evt.file_name || "",
                });
                break;
              case "file_start":
                updateInstall(jobId, { status: "downloading", file: evt.file_name || "", percent: 0 });
                break;
              case "file_done":
                updateInstall(jobId, { status: "downloading", file: evt.file_name || "", percent: 100 });
                break;
              case "checking":
                updateInstall(jobId, { status: "verifying", file: evt.file_name || "" });
                break;
              case "skip":
                updateInstall(jobId, { status: "skipped", file: evt.file_name || "" });
                break;
              case "error":
                sawError = true;
                errMsg = evt.message || "";
                break;
              default:
                break;
            }
          } catch { /* malformed event line — ignore */ }
        }
      }
      if (sawError) {
        const msg = friendlyInstallError(new Error(errMsg));
        updateInstall(jobId, { status: "error", error: msg, percent: 0 });
        dropInstall(jobId, 6000);
        toast.error(`Install failed (${modelLabel}): ${msg}`);
      } else {
        updateInstall(jobId, { status: "done", percent: 100 });
        dropInstall(jobId);
        toast.success(`Install complete: ${modelLabel}`);
        // Refresh just this node's installed package hashes so the
        // catalog card flips to "✓ Installed" without requiring a full
        // page refresh. Best-effort; failure is silent.
        try {
          const vresp = await fetch(`/node/${encodeURIComponent(nodeId)}/provider/packages`, {
            headers: { ...getAuthHeaders() },
          });
          if (vresp.ok) {
            const vdata = await vresp.json();
            const arr = Array.isArray(vdata) ? vdata : (Array.isArray(vdata?.versions) ? vdata.versions : []);
            const installedPackages = packagesFromVersions(arr);
            setMyNodes((prev) => prev.map((m) =>
              m.id === nodeId ? { ...m, installedPackages } : m
            ));
          }
        } catch { /* non-fatal — next refreshMyNodes will pick it up */ }
      }
    } catch (err) {
      const msg = friendlyInstallError(err);
      updateInstall(jobId, { status: "error", error: msg });
      dropInstall(jobId, 6000);
      toast.error(`Install stream error (${modelLabel}): ${msg}`);
    }
  }, [toast, updateInstall, dropInstall]);

  const detected = useMemo(() => detectQueryType(input), [input]);

  // My-nodes lookup so we can stamp the 👤 You badge AND drive the Install
  // modal's per-node target selection. Failure is non-fatal — a logged-out
  // user just sees zero "You" badges and an empty install target list.
  useEffect(() => { refreshMyNodes(); }, [refreshMyNodes]);

  // Run the search whenever submitted/order/sources change. We don't fire on
  // every keystroke — typing only updates the detection chip preview.
  useEffect(() => {
    if (!submitted) {
      setNodeRes(null);
      setCatalogRes(null);
      setByHashItem(null);
      setErrors({});
      return;
    }
    const det = detectQueryType(submitted);
    let cancelled = false;
    const ctl = new AbortController();
    setRunning(true);
    setErrors({});

    const tasks = [];

    if (sources.running) {
      tasks.push(
        searchNodes(det.normalized || submitted, {
          type: det.type,
          order: order || undefined,
          signal: ctl.signal,
        }).then(
          (r) => { if (!cancelled) setNodeRes(r); },
          (err) => { if (!cancelled) setErrors((e) => ({ ...e, broker: String(err) })); },
        ),
      );
    } else {
      setNodeRes({ matches: [] });
    }

    // Catalog text search hits HF only. Civitai's API search is
    // model-name substring matching, much weaker than its web UI's
    // Meilisearch — confusing zero-hit results for queries that show
    // thousands on the website. Operators search Civitai web directly,
    // copy a sha256, and paste it here for a precise by-hash lookup.
    if (det.type === "text" && sources.huggingface) {
      tasks.push(
        searchCatalogs(det.normalized || submitted, {}).then(
          (r) => {
            if (cancelled) return;
            setCatalogRes(r);
            if (r.errors) setErrors((e) => ({ ...e, ...r.errors }));
          },
          (err) => { if (!cancelled) setErrors((e) => ({ ...e, catalog: String(err) })); },
        ),
      );
    } else {
      setCatalogRes({ items: [] });
    }

    if (det.type === "hash" && det.normalized && sources.civitai) {
      tasks.push(
        fetchCivitaiByHash(det.normalized).then(
          (r) => { if (!cancelled && r) setByHashItem(r); },
          () => {/* non-fatal */},
        ),
      );
    } else {
      setByHashItem(null);
    }

    Promise.allSettled(tasks).finally(() => {
      if (!cancelled) setRunning(false);
    });

    return () => { cancelled = true; ctl.abort(); };
  }, [submitted, order, sources]);

  const onSubmit = (e) => {
    e?.preventDefault?.();
    const q = input.trim();
    setSubmitted(q);
    const next = new URLSearchParams();
    if (q) next.set("q", q);
    if (order) next.set("order", order);
    setParams(next, { replace: true });
  };

  const onClearInput = () => {
    setInput("");
    setSubmitted("");
    setParams(new URLSearchParams(), { replace: true });
  };

  // Installed-on-my-node lookup: builds two Sets so the catalog card can
  // stamp `✓ Installed` reliably regardless of which side of the data
  // path has metadata available.
  //
  // installedHashes — hash-based match (most precise):
  //   1) services[].model_hash — models attached to a service profile.
  //   2) installedPackageHashes — every package.json hash from
  //      /node/<id>/provider/packages.
  //
  // installedNames — name-based fallback for when one side has no hash:
  //   1) services[].model — runtime-loaded model name
  //   2) installedPackageNames — package.json `name` (= the local install
  //      name broker passed via --name, which is item.name||item.id).
  //
  // Deliberately NOT sourced from nodeRes.matches (search-query-filtered,
  // would miss installs on nodes whose loaded models don't match input).
  // Per-package install records keyed by node id. Each package object is
  // {name, hashes:Set, repos:Set}; matching iterates packages so the
  // package boundary is respected (a file:// import with empty `repos`
  // doesn't get judged against a different package's repo info).
  const installedPackagesByNode = useMemo(() => {
    const m = new Map();
    for (const n of myNodes) {
      if (Array.isArray(n.installedPackages) && n.installedPackages.length > 0) {
        m.set(n.id, n.installedPackages);
      }
    }
    return m;
  }, [myNodes]);
  // Service-side fallbacks. /v1/nodes services[] gives us the model_hash
  // + model name for currently-attached profiles, but no download_url —
  // so they can't cross-check repos. Used only when the per-package
  // matcher misses (e.g. a model loaded by a service but for some reason
  // not present in /provider/packages).
  const installedServiceHashesByNode = useMemo(() => {
    const m = new Map();
    for (const n of myNodes) {
      const s = new Set();
      for (const svc of (n.services || [])) {
        const h = svc.model_hash || svc.modelHash;
        if (h) s.add(String(h).toLowerCase());
      }
      if (s.size > 0) m.set(n.id, s);
    }
    return m;
  }, [myNodes]);
  const installedServiceNamesByNode = useMemo(() => {
    const m = new Map();
    for (const n of myNodes) {
      const s = new Set();
      for (const svc of (n.services || [])) {
        if (svc.model) s.add(String(svc.model).toLowerCase());
      }
      if (s.size > 0) m.set(n.id, s);
    }
    return m;
  }, [myNodes]);
  const authedNodeIds = useMemo(() =>
    new Set(myNodes.filter((n) => n.auth).map((n) => n.id)),
    [myNodes]);

  // Order rendering: nodes-first (default), catalog-first, or mixed.
  // "Mixed" still keeps two sections — a true interleave doesn't help when
  // the relevance score isn't comparable across kinds.
  const sectionsOrder = order === "catalog" ? ["catalog", "nodes"] : ["nodes", "catalog"];

  return (
    <div className="search-page">
      {/* Mockup-style mini search bar at top of content (page-level). The
          global header search button still works for navigation. */}
      <div className="search-mini-row">
        <form className="search-mini" onSubmit={onSubmit}>
          <input
            className="search-mini-input"
            placeholder="0xabc… · sha256:… · node-… · RTX 4090 · sdxl …"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            autoFocus
            spellCheck={false}
          />
          {input && (
            <button
              type="button"
              className="search-mini-clear"
              title={t("search.clear")}
              onClick={onClearInput}
            >
              ✕
            </button>
          )}
          <button type="submit" className="search-mini-submit" disabled={!input.trim()} title={t("search.submit")} aria-label={t("search.submit")}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
              <circle cx="11" cy="11" r="7" />
              <line x1="21" y1="21" x2="16.65" y2="16.65" />
            </svg>
          </button>
        </form>
        {/* Civitai hint — public API only matches model names, much
            weaker than civitai.com's Meilisearch. Operators search
            Civitai's website, copy a sha256 from the file row, and
            paste it here for a precise by-hash match. */}
        <div className="search-civitai-hint">
          ⓘ For Civitai models — search at{" "}
          <a href="https://civitai.com/search/models" target="_blank" rel="noopener noreferrer">
            civitai.com
          </a>
          , copy the file's SHA-256, paste it here for a precise lookup.
        </div>
      </div>

      {submitted ? (
        <>
          <div className="search-meta">
            <DetectionRow detected={detected} input={input} />
            <FilterRow label="Order">
              {ORDER_OPTIONS.map((o) => (
                <ChipButton
                  key={o.value || "default"}
                  active={order === o.value}
                  mark
                  onClick={() => setOrder(o.value)}
                >
                  {o.label}
                </ChipButton>
              ))}
            </FilterRow>
            <FilterRow label="Sources">
              {SOURCE_OPTIONS.map((s) => (
                <ChipButton
                  key={s.id}
                  active={!!sources[s.id]}
                  disabled={s.disabled}
                  mark
                  onClick={() =>
                    setSources((prev) => ({ ...prev, [s.id]: !prev[s.id] }))
                  }
                >
                  {s.label}
                </ChipButton>
              ))}
            </FilterRow>
            <FilterRow label="Filters">
              <ChipButton disabled>GPU: Any ▾</ChipButton>
              <ChipButton disabled>VRAM: Any ▾</ChipButton>
              <ChipButton disabled>{t("search.engine_any")}</ChipButton>
              <ChipButton active={hideNsfw} mark onClick={() => setHideNsfw((v) => !v)}>
                Hide NSFW
              </ChipButton>
            </FilterRow>
          </div>

          <div className="search-results">
            {sectionsOrder.map((kind) =>
              kind === "nodes" ? (
                <NodesSection
                  key="nodes"
                  data={nodeRes}
                  running={running}
                  myNodeIds={myNodeIds}
                  onPickNode={(match) => {
                    if (match.node_id) {
                      navigate(`/nodes/${encodeURIComponent(match.node_id)}`);
                    } else if (match.owner_address) {
                      // node_id missing (broker projection bug or older
                      // binary) — fall back to the owner's nodes list so
                      // the click isn't a dead-end.
                      navigate(`/nodes?owner=${encodeURIComponent(match.owner_address)}`);
                    } else {
                      navigate("/nodes");
                    }
                  }}
                  error={errors.broker}
                  query={submitted}
                />
              ) : (
                <CatalogSection
                  key="catalog"
                  data={catalogRes}
                  byHashItem={byHashItem}
                  running={running}
                  installedPackagesByNode={installedPackagesByNode}
                  installedServiceHashesByNode={installedServiceHashesByNode}
                  installedServiceNamesByNode={installedServiceNamesByNode}
                  authedNodeIds={authedNodeIds}
                  errors={errors}
                  myNodes={myNodes}
                  nodeMatches={nodeRes?.matches || []}
                  onMyNodesChanged={refreshMyNodes}
                  startBackgroundInstall={startBackgroundInstall}
                  activeInstalls={activeInstalls}
                />
              ),
            )}
          </div>
        </>
      ) : (
        <SearchHints />
      )}
    </div>
  );
}

function DetectionRow({ detected, input }) {
  const { t } = useTranslation();
  if (!input.trim()) {
    return (
      <div className="detection-row">
        <span className="detect-chip ghost">{t("search.detected_none")}</span>
      </div>
    );
  }
  if (detected.type === "text") {
    return (
      <div className="detection-row">
        <span className="detect-chip">{t("search.detected_free_text")}</span>
        <span className="detect-confidence">fuzzy match</span>
      </div>
    );
  }
  return (
    <div className="detection-row">
      <span className={`detect-chip detect-${detected.type}`}>
        Detected: {detected.label}
      </span>
      <span className="detect-confidence">routed to {detected.type}</span>
      {detected.normalized && (
        <span className="detect-norm">{abbreviateMid(detected.normalized)}</span>
      )}
    </div>
  );
}

function FilterRow({ label, children }) {
  return (
    <div className="filter-group">
      <span className="filter-group-label">{label}:</span>
      {children}
    </div>
  );
}

function ChipButton({ active, disabled, onClick, mark, children }) {
  const cls = ["filter-chip"];
  if (active) cls.push("filter-chip-active");
  if (disabled) cls.push("filter-chip-disabled");
  // mark is always rendered into a fixed-width slot when the chip is a
  // toggle (mark prop set). CSS controls opacity by active state so the
  // chip width never changes — the ✓ glyph is just dimmed when inactive.
  return (
    <button
      type="button"
      className={cls.join(" ")}
      disabled={disabled}
      onClick={disabled ? undefined : onClick}
    >
      {mark && <span className="filter-chip-mark">✓</span>}
      {children}
    </button>
  );
}

function SearchHints() {
  const { t } = useTranslation();
  const hints = ["hint_sha256", "hint_evm", "hint_node_id", "hint_gpu", "hint_engine", "hint_free_text"];
  return (
    <div className="search-hints">
      <h3>{t("search.try_search_title")}</h3>
      <ul>
        {hints.map((k) => (
          <li key={k} dangerouslySetInnerHTML={{ __html: t(`search.${k}`) }} />
        ))}
      </ul>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────
// Running Nodes section — single unified list, You badge stamped inline
function NodesSection({ data, running, myNodeIds, onPickNode, error, query }) {
  const { t } = useTranslation();
  const matchesRaw = data?.matches || [];
  // Surface user's own nodes first (mockup convention "본인 노드 우선").
  const matches = useMemo(() => {
    const own = [];
    const rest = [];
    for (const m of matchesRaw) {
      if (myNodeIds.has(m.node_id)) own.push(m);
      else rest.push(m);
    }
    return [...own, ...rest];
  }, [matchesRaw, myNodeIds]);

  return (
    <section className="result-section">
      <div className="result-header">
        <div className="result-title">
          ⚡ Running Nodes
          <span className="count-badge">{matches.length}</span>
        </div>
        <div className="result-sort-hint">
          {data?.detected_type ? `Type: ${data.detected_type}` : ""}
          {myNodeIds.size > 0 ? " · Your nodes first" : ""}
        </div>
      </div>
      {error && <div className="result-error">{t("search.error_prefix")} {error}</div>}
      {!running && matches.length === 0 && !error && (
        <div className="result-empty">{t("search.no_matching_nodes", { q: query })}</div>
      )}
      {running && matches.length === 0 && !error && (
        <>
          <NodeSkeletonCard />
          <NodeSkeletonCard />
        </>
      )}
      {matches.map((m, i) => (
        <NodeCard
          key={m.node_id || `${m.owner_address}-${i}`}
          match={m}
          isYou={myNodeIds.has(m.node_id)}
          onClick={() => onPickNode(m)}
        />
      ))}
    </section>
  );
}

// Skeleton placeholder rendered while a search is in flight and no results
// have arrived yet. Layout mirrors NodeCard so the swap-in is positionally
// stable — the page doesn't reflow when real cards appear.
function NodeSkeletonCard() {
  return (
    <div className="result-card skeleton-card">
      <div className="card-emblem skeleton-block" />
      <div className="card-body">
        <div className="card-title-row">
          <span className="skeleton-bar w-55p" />
        </div>
        <div className="card-info-row">
          <span className="skeleton-bar w-65p" />
        </div>
        <div className="card-meta">
          <span className="skeleton-bar w-60" />
          <span className="skeleton-bar w-80" />
          <span className="skeleton-bar w-70" />
        </div>
      </div>
      <div className="card-metrics">
        <div className="card-metrics-row">
          <span className="skeleton-bar w-40" />
          <span className="skeleton-bar w-30" />
        </div>
        <div className="card-metrics-row">
          <span className="skeleton-bar w-40" />
          <span className="skeleton-bar w-30" />
        </div>
        <div className="card-metrics-row">
          <span className="skeleton-bar w-40" />
          <span className="skeleton-bar w-30" />
        </div>
      </div>
    </div>
  );
}

function NodeCard({ match, isYou, onClick }) {
  const { t } = useTranslation();
  const gpu = match.gpu || {};
  const queue = match.queue || {};
  const stats = match.stats || {};
  const cls = ["result-card"];
  if (isYou) cls.push("you");
  // Status: prefer conn_status (RV's 3-tier), fall back to status, default
  // green for live nodes. queue.pending pushes us to yellow when work is
  // already piling up.
  const connDown = match.conn_status === "offline" || match.conn_status === "stale";
  const status = (connDown || match.status === "offline")
    ? "⚪"
    : (queue.pending > 0 ? "🟡" : "🟢");
  // Pick the first service that actually has a model loaded — otherwise
  // a stopped service (sd-api with empty model) at index 0 would mask a
  // running one (vllm-api with model) further down the list. Falls back
  // to plain [0] when none has a name (preserves "—" rendering).
  const pickModel = (arr) => {
    if (!Array.isArray(arr) || arr.length === 0) return null;
    return arr.find((m) => m && m.name) || arr[0];
  };
  const primaryModel = pickModel(match.matched_models) || pickModel(match.loaded_models) || null;
  const matchedModelName = primaryModel?.name || "—";
  // Service / engine label should track the primaryModel — otherwise we
  // can end up with "sd-api · qwen2.5-1.5b" (sd-api's name + vllm-api's
  // model). Prefer primaryModel.service (service name), then its engine,
  // then fall back to top-level match.engine for legacy responses.
  const primaryServiceLabel = primaryModel?.service || primaryModel?.engine || match.engine || "";
  // ctx badge: only meaningful for LLM-style services. llama.cpp uses
  // ctx_size, vllm uses max_model_len. Format as "8K" / "16K" when the
  // value is a multiple of 1024.
  const ctxBadge = (() => {
    // Search every loaded_model / matched_model entry's inspect for an
    // LLM context value. ctx_size (llama.cpp) or max_model_len (vllm).
    const candidates = [
      ...(match.matched_models || []),
      ...(match.loaded_models || []),
    ];
    let raw;
    for (const m of candidates) {
      const insp = m?.inspect;
      if (!insp) continue;
      raw = insp.ctx_size ?? insp.max_model_len;
      if (raw !== undefined && raw !== null && raw !== "") break;
      raw = undefined;
    }
    if (raw === undefined) return null;
    const n = parseInt(raw, 10);
    if (!Number.isFinite(n) || n <= 0) return null;
    return n >= 1024 && n % 1024 === 0 ? `${n / 1024}K` : String(n);
  })();
  // Display label: full node_id if present, else owner-derived placeholder
  // so the card never collapses to "—" when the broker forgot to populate it.
  const idLabel = match.node_id
    ? match.node_id
    : (match.owner_address ? `owner:${match.owner_address}` : "(unknown node)");
  // Average job duration formatter: render in ms when sub-second so
  // "596ms" reads more useful than "0.6s", switch to seconds above 1s.
  const avgLabel = (() => {
    if (!(stats.done > 0 && stats.avg_sec > 0)) return null;
    return stats.avg_sec < 1
      ? `${Math.round(stats.avg_sec * 1000)}ms`
      : `${stats.avg_sec.toFixed(1)}s`;
  })();
  // Latency: search response may carry it as ms. Show at >0 only.
  const latencyLabel = (typeof match.latency_ms === "number" && match.latency_ms > 0)
    ? `${(match.latency_ms / 1000).toFixed(1)}s`
    : null;
  // Display status: search results only contain online providers (RV filters
  // offline/stale upstream), so "online" is implicit and redundant. Only the
  // workload state (idle/busy) is informative here — leave it blank otherwise
  // so the row stays clean.
  const displayStatus = (() => {
    if (match.status === "idle" || match.status === "busy") return match.status;
    return null;
  })();
  const pendingCount = stats.pending ?? queue.pending ?? 0;

  return (
    <div className={cls.join(" ")} onClick={onClick}>
      <div className="card-emblem">
        <EmblemBadge match={match} />
      </div>
      {/* Left column (2): static identity — id / owner / model / badge row.
          "What is this node?" answered top-to-bottom. */}
      <div className="card-static">
        <div className="card-id-row">
          <span className="card-id">{idLabel}</span>
          {displayStatus && <StatusTag value={displayStatus} />}
        </div>
        {match.owner_address && (
          <div className="card-owner-row">{match.owner_address}</div>
        )}
        <div className="card-info-row">
          {primaryServiceLabel && <span className="engine-label">{primaryServiceLabel}</span>}
          {primaryServiceLabel && " · "}
          {primaryModel?.name
            ? <ModelLabel modelName={primaryModel.name} originUrl={primaryModel.model_origin_url} hash={primaryModel.hash} className="model-label" />
            : <span className="model-label">{matchedModelName}</span>}
        </div>
        <div className="card-badges">
          {gpu.model && (
            <span className="card-chip">
              {shortGPU(gpu.model)} X {gpu.count || 1}
            </span>
          )}
          {ctxBadge && (
            <span className="card-chip card-chip-ctx" title="Context length">
              {ctxBadge}
            </span>
          )}
          {isYou && <span className="match-reason you">👤 You</span>}
        </div>
      </div>
      {/* Right column (1): runtime/metrics — Queue / Done / Avg / Latency.
          "How busy is it?" stacked vertically so the eye snaps to live numbers. */}
      <div className="card-metrics">
        <div className="card-metrics-row">
          <span className="card-metrics-label">{t("search.metrics_pending")}</span>
          <span className="card-metrics-value">
            {pendingCount}
            {queue.max ? ` / ${queue.max}` : ""}
          </span>
        </div>
        <div className="card-metrics-row">
          <span className="card-metrics-label">{t("search.metrics_done")}</span>
          <span className="card-metrics-value">{stats.done || 0}</span>
        </div>
        {avgLabel && (
          <div className="card-metrics-row">
            <span className="card-metrics-label">{t("search.metrics_avg")}</span>
            <span className="card-metrics-value">{avgLabel}</span>
          </div>
        )}
        {latencyLabel && (
          <div className="card-metrics-row">
            <span className="card-metrics-label">{t("search.metrics_latency")}</span>
            <span className="card-metrics-value">{latencyLabel}</span>
          </div>
        )}
        {/* Node uptime — based on rendezvous's started_at (first-register
            timestamp). "How long has this node been online?" — useful
            signal alongside the live runtime numbers. */}
        {formatUptime(match.started_at) && (
          <div className="card-metrics-row">
            <span className="card-metrics-label">Up</span>
            <span className="card-metrics-value">{formatUptime(match.started_at)}</span>
          </div>
        )}
        {/* TPM badge anchored at the bottom of the metrics column —
            "is this hardware trustworthy?" reads naturally next to
            the live runtime numbers, and frees the title row to focus
            on identity (node id + status). */}
        {(match.tpm_verified || match.ek_cert_issuer) && (
          <div className="card-metrics-tpm">
            {match.tpm_verified ? (
              <span className="match-reason tpm" title="fTPM verified (challenge passed)">
                ✓ TPM: {match.ek_cert_issuer || "verified"}
              </span>
            ) : (
              <span className="match-reason tpm-pending" title={t("search.ek_cert_pending")}>
                TPM: {match.ek_cert_issuer}
              </span>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

// ─────────────────────────────────────────────────────────────────────
// Catalog section — HF + Civitai merged
function CatalogSection({ data, byHashItem, running, installedPackagesByNode, installedServiceHashesByNode, installedServiceNamesByNode, authedNodeIds, errors, myNodes, nodeMatches, onMyNodesChanged, startBackgroundInstall, activeInstalls }) {
  const { t } = useTranslation();
  const arr = data?.items || [];
  // by-hash exact match (Civitai only) gets pinned to the top.
  const items = byHashItem ? [byHashItem, ...arr.filter((x) => x !== byHashItem)] : arr;
  const sourceErrors = data?.errors || {};
  return (
    <section className="result-section">
      <div className="result-header">
        <div className="result-title">
          📦 Catalog{byHashItem ? " (HuggingFace + Civitai by hash)" : " (HuggingFace)"}
          <span className="count-badge">{items.length}</span>
        </div>
        <div className="result-sort-hint">
          Sort: Match · Downloads ↓
        </div>
      </div>

      {Object.entries(sourceErrors).map(([src, err]) => (
        <div key={src} className="result-warning">
          {src} catalog error: {err}
        </div>
      ))}

      {!running && items.length === 0 && (
        <div className="result-empty">{t("search.no_catalog_matches")}</div>
      )}
      {running && items.length === 0 && (
        <>
          <CatalogSkeletonCard />
          <CatalogSkeletonCard />
          <CatalogSkeletonCard />
        </>
      )}

      {items.map((it) => (
        <CatalogCard
          key={`${it.source}-${it.id}`}
          item={it}
          installedPackagesByNode={installedPackagesByNode}
          installedServiceHashesByNode={installedServiceHashesByNode}
          installedServiceNamesByNode={installedServiceNamesByNode}
          authedNodeIds={authedNodeIds}
          myNodes={myNodes}
          nodeMatches={nodeMatches}
          onMyNodesChanged={onMyNodesChanged}
          startBackgroundInstall={startBackgroundInstall}
          activeInstalls={activeInstalls}
        />
      ))}
    </section>
  );
}

// Skeleton placeholder for catalog cards while the HF/Civitai list
// requests are in flight. Mirrors CatalogCard so the page doesn't jump
// when real cards swap in.
function CatalogSkeletonCard() {
  return (
    <div className="result-card catalog skeleton-card">
      <div className="install-btn-square skeleton-block" />
      <div className="card-body">
        <div className="card-title-row">
          <span className="skeleton-bar w-55p" />
          <span className="skeleton-bar w-70" />
        </div>
        <div className="card-badges-row">
          <span className="skeleton-bar w-60" />
          <span className="skeleton-bar w-80" />
        </div>
        <div className="card-info-row">
          <span className="skeleton-bar w-45p" />
        </div>
        <div className="card-meta">
          <span className="skeleton-bar w-40" />
          <span className="skeleton-bar w-50" />
          <span className="skeleton-bar w-60" />
          <span className="skeleton-bar w-100" />
        </div>
      </div>
      <div className="card-actions">
        <span className="skeleton-bar sq-32" />
        <span className="skeleton-bar sq-32" />
      </div>
    </div>
  );
}

function CatalogCard({ item, installedPackagesByNode, installedServiceHashesByNode, installedServiceNamesByNode, authedNodeIds, myNodes, nodeMatches, onMyNodesChanged, startBackgroundInstall, activeInstalls }) {
  const { t } = useTranslation();
  // HF list endpoint sometimes returns siblings without lfs metadata (no
  // sha256, no file size) and may omit license / downloadsAllTime. When the
  // card renders with missing data we lazy-fetch the detail endpoint and
  // merge — only the cards the user actually scrolls past pay this cost.
  const [enriched, setEnriched] = React.useState(null);
  const [inspectOpen, setInspectOpen] = React.useState(false);
  // "inspect" by default; install-btn-square opens directly into "install"
  // phase so the user lands on the node-picker without an extra Inspect step.
  const [inspectInitialPhase, setInspectInitialPhase] = React.useState("inspect");
  React.useEffect(() => {
    if (item.source !== "huggingface") return;
    const needsEnrich =
      (item.primarySize || 0) === 0 ||
      !item.license ||
      !item.primaryHash;
    if (!needsEnrich) return;
    let cancelled = false;
    fetchHFById(item.id).then(
      (detail) => { if (!cancelled && detail) setEnriched(detail); },
      () => {/* non-fatal — card just shows what it has */},
    );
    return () => { cancelled = true; };
  }, [item.id, item.source, item.primarySize, item.license, item.primaryHash]);

  // Effective item: enriched detail wins, fall back to list summary fields
  // when detail hasn't arrived yet (or for non-HF sources).
  const eff = enriched || item;

  // Per-card installed count, computed against the ENRICHED item.
  // Match priority:
  //   1) catalog has hashes → require hash overlap AND (when both sides
  //      have repo info) repo match. This rules out "same content
  //      re-uploaded under a different repo" false matches — same SHA256
  //      across jc-builds vs Joshua65535 reuploads of identical GGUF.
  //   2) no catalog hash → name fallback (same loose matching as before).
  //
  // Sharded models still match correctly: ANY shard hash overlap counts,
  // and the repo extracted from any download_url applies to the whole
  // package (all shards live under the same HF repo).
  const { installedCount, brokenCount, authedTotal, installState, installed } = (() => {
    const itemKey = String(eff.name || eff.id || "").toLowerCase();
    // Build the set of acceptable repo keys for the catalog side, in the
    // same shape extractRepoKeyFromUrl produces on the node side. Civitai
    // keeps both versionId and modelId because disk download_url carries
    // versionId only, but page URL carries modelId — depending on how the
    // download was kicked off either may end up as the saved url.
    const catalogRepos = new Set();
    if (eff.source === "huggingface" && eff.id) {
      catalogRepos.add(("hf:" + String(eff.id)).toLowerCase());
    } else if (eff.source === "civitai") {
      if (eff.versionId) catalogRepos.add(("civitai:v:" + String(eff.versionId)).toLowerCase());
      if (eff.id)        catalogRepos.add(("civitai:m:" + String(eff.id)).toLowerCase());
    }
    const catalogHashes = new Set();
    if (eff.primaryHash) catalogHashes.add(String(eff.primaryHash).toLowerCase());
    for (const f of (eff.hashes || [])) {
      if (f?.hash) catalogHashes.add(String(f.hash).toLowerCase());
    }
    const total = authedNodeIds ? authedNodeIds.size : 0;
    let installedN = 0;
    let brokenN    = 0;
    if (authedNodeIds) {
      for (const nid of authedNodeIds) {
        const s = classifyPackageNode({
          nid,
          catalogHashes,
          catalogRepos,
          itemKey,
          installedPackagesByNode,
          installedServiceHashesByNode,
          installedServiceNamesByNode,
        });
        if (s === "installed") installedN += 1;
        else if (s === "broken") brokenN += 1;
      }
    }
    let state;
    if (total === 0 || installedN === 0) state = "none";
    else if (installedN >= total)        state = "full";
    else                                  state = "partial";
    return { installedCount: installedN, brokenCount: brokenN, authedTotal: total, installState: state, installed: state === "full" };
  })();

  const cls = ["result-card", "catalog"];
  if (installed) cls.push("installed");
  const onOpen = () => window.open(eff.url, "_blank", "noopener,noreferrer");
  const stop = (e) => e.stopPropagation();

  // Icon depends on kind/baseModel — gives the big square button some
  // identity per model type. Falls back to 📦 for everything else.
  const icon = pickCatalogIcon(eff);

  // Shard count — sharded safetensors / bin (e.g. "model-00003-of-00015.safetensors")
  // are common for 13B+ models. Show a chip so the user knows it's multi-file.
  const shardCount = countShards(eff.hashes);

  return (
    <div className={cls.join(" ")} onClick={onOpen}>
      <button
        className={`install-btn-square ${installed ? "installed" : ""}`}
        onClick={(e) => { stop(e); setInspectInitialPhase("install"); setInspectOpen(true); }}
        title={installed ? "Use this model" : "Install (download + register)"}
      >
        <span className="install-btn-icon">{installed ? "✓" : icon}</span>
      </button>

      <div className="card-body">
        <div className="card-title-row">
          {/* HF: eff.id is "owner/repo" — path-style. Civitai: mirror
              the same shape with "id/name" (no surrounding spaces) so
              both sources render the slash identically. */}
          <span className="card-id">
            {eff.source === "huggingface"
              ? (eff.id || eff.name || "")
              : (eff.id
                  ? `${eff.id}${eff.name ? `/${eff.name}` : ""}`
                  : (eff.name || ""))}
          </span>
          <span className={`brand-tag brand-tag-${eff.source}`}>
            {eff.source === "huggingface" ? "HuggingFace" : "Civitai"}
          </span>
        </div>
        {(() => {
          // "other" (kind) and "Other" (Civitai baseModel) carry no useful
          // signal — they are catch-all fallbacks. Drop them so the badges
          // row only surfaces meaningful tags.
          const showKind = eff.kind && eff.kind !== "base" && eff.kind.toLowerCase() !== "other";
          const showBase = eff.baseModel && eff.baseModel.toLowerCase() !== "other";
          const showLicense = !!eff.license;
          const showInstall = installState !== "none";
          const showBroken  = brokenCount > 0;
          if (!showKind && !showBase && shardCount <= 1 && !showInstall && !showBroken && !showLicense) return null;
          return (
            <div className="card-badges-row">
              {showLicense && <span className="card-chip license">{eff.license}</span>}
              {showKind && <span className="card-chip kind">{eff.kind}</span>}
              {showBase && <span className="card-chip kind">{eff.baseModel}</span>}
              {shardCount > 1 && (
                <span className="card-chip shard">{shardCount} shards</span>
              )}
              {installState === "full" && (
                <span className="card-chip installed-tag">
                  {authedTotal > 1
                    ? `✓ Installed on all ${authedTotal} nodes`
                    : "✓ Installed"}
                </span>
              )}
              {installState === "partial" && (
                <span className="card-chip installed-partial-tag">
                  ✓ {installedCount}/{authedTotal} nodes
                </span>
              )}
              {showBroken && (
                <span className="card-chip broken-tag">
                  ⚠ Broken on {brokenCount}
                </span>
              )}
            </div>
          );
        })()}
        <div className="card-meta">
          <span title={t("search.likes_tooltip")}>
            <strong>★</strong> {typeof eff.likes === "number" && eff.likes > 0 ? abbreviateNum(eff.likes) : "—"}
          </span>
          <span title={t("search.downloads_tooltip")}>
            <strong>📥</strong> {typeof eff.downloads === "number" && eff.downloads > 0 ? abbreviateNum(eff.downloads) : "—"}
          </span>
          <span>
            <strong>💾</strong> {eff.primarySize > 0 ? formatSize(eff.primarySize) : "—"}
          </span>
          <HashCell hash={eff.primaryHash} onCardClickStop={stop} />
        </div>
      </div>

      <div className="card-actions">
        <button
          className="icon-btn"
          onClick={(e) => { stop(e); setInspectInitialPhase("inspect"); setInspectOpen(true); }}
          title={t("search.inspect_tooltip")}
          aria-label={t("search.inspect_label")}
        >
          ⓘ
        </button>
      </div>
      {inspectOpen && (
        <InspectModal
          item={eff}
          installed={installed}
          initialPhase={inspectInitialPhase}
          myNodes={myNodes || []}
          nodeMatches={nodeMatches || []}
          onMyNodesChanged={onMyNodesChanged}
          startBackgroundInstall={startBackgroundInstall}
          activeInstalls={activeInstalls || {}}
          onClose={() => setInspectOpen(false)}
        />
      )}
    </div>
  );
}

// InspectModal — frontend-only detail view shown when ⓘ is clicked.
// Reuses the enriched item already fetched by the card's lazy useEffect, so
// opening costs zero additional network. Backend-dependent fields (provider
// compatibility, profile state) are intentionally omitted; a future
// /v1/installer/inspect endpoint can be folded in without restructuring
// this component.
function InspectModal({ item, installed, initialPhase = "inspect", myNodes = [], nodeMatches = [], onMyNodesChanged, startBackgroundInstall, activeInstalls = {}, onClose }) {
  const { t } = useTranslation();
  // Two-phase modal: "inspect" shows metadata; "install" shows the per-node
  // target picker. The progress phase is a planned follow-up — for now the
  // modal closes after firing the install request and the user watches the
  // existing install-panel (or the dedicated Deploy tab).
  const [phase, setPhase] = React.useState(initialPhase);
  const [selectedNodeId, setSelectedNodeId] = React.useState("");
  const [installing, setInstalling] = React.useState(false);
  const [installError, setInstallError] = React.useState("");
  const [authBusyId, setAuthBusyId] = React.useState("");
  // partialsByNode: {nodeId -> {percent, downloaded, total, fileName}} for
  // THIS catalog item, populated by /provider/partials. Drives the
  // "47% saved — Resume" hint on each node row + seeds the in-modal
  // progress bar before any new install starts.
  const [partialsByNode, setPartialsByNode] = React.useState({});

  // ESC closes the modal. Body scroll locked while open so background
  // search results don't scroll behind the overlay.
  React.useEffect(() => {
    const onKey = (e) => { if (e.key === "Escape") onClose(); };
    document.addEventListener("keydown", onKey);
    const prevOverflow = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = prevOverflow;
    };
  }, [onClose]);

  const stopProp = (e) => e.stopPropagation();

  // LoRA short-circuit — Civitai catalog flags Type:LORA via item.kind.
  // LoRAs flow through a different install path (type=lora, architecture
  // category) so we skip service inference entirely. Architecture comes
  // from baseModel ("SD 1.5" → sd15, ...). Mirror of pkg/profile NormalizeArchitecture.
  const isLora = item.kind === "lora";
  const loraArch = isLora ? normalizeArchClient(item.baseModel) : "";

  // Frontend service inference — simple rules. Not authoritative; the
  // operator-side `isann install` re-runs full inference (including
  // provider.json checks) before actually installing.
  const inference = isLora
    ? { service: "", engine: "", source: "" }
    : inferServiceClient(item);
  const installPath = isLora
    ? (loraArch ? `packages/loras/${loraArch}/${item.name || item.id}/` : null)
    : (inference.service ? `packages/models/${inference.service}/${item.name || item.id}/` : null);
  const profilePath = isLora
    ? null
    : (inference.service ? `conf/profiles/${inference.service}.json` : null);

  const files = Array.isArray(item.hashes) ? item.hashes : [];
  const totalSize = files.reduce((sum, f) => sum + (f.size || 0), 0);

  // Primary file = the largest LFS file, mirrors the card's primaryHash logic.
  let primaryName = "";
  let primarySize = 0;
  for (const f of files) {
    if ((f.size || 0) > primarySize) { primarySize = f.size; primaryName = f.file; }
  }

  // Format guess from primary filename extension.
  const format = (() => {
    if (!primaryName) return "—";
    const m = String(primaryName).match(/\.([a-z0-9]+)$/i);
    return m ? m[1].toLowerCase() : "—";
  })();

  // Warnings derived purely from frontend data.
  const warnings = [];
  if (totalSize > 5 * 1024 * 1024 * 1024) {
    warnings.push({ level: "warn", msg: `Large download (${formatSize(totalSize)}) — ensure disk space and stable connection.` });
  }
  if (!item.license) {
    warnings.push({ level: "warn", msg: "License not declared by author — verify usage terms before deploy." });
  }
  // service vs architecture validation depends on install path. LoRAs are
  // partitioned by architecture (independent of service inference); models
  // need a service to land in the right packages/models/{service}/ subdir.
  if (isLora) {
    if (!loraArch) {
      warnings.push({ level: "error", msg: "Civitai didn't surface a baseModel for this LoRA — install will fail without --architecture." });
    }
  } else if (!inference.service) {
    warnings.push({ level: "error", msg: "Could not auto-infer service. Pick one manually before installing." });
  }

  // Per-node install candidates. installedHashesByNode is built from the
  // current search response (nodeRes.matches): each match's loaded_models /
  // matched_models gives us the hashes that node currently has on disk.
  // Combined with myNodes (IndexedDB + /v1/nodes online merge) we can render
  // a 3-state radio list: authed-online (selectable), authed-offline, and
  // not-authed (must authenticate first).
  const myNodeIds = React.useMemo(() => new Set(myNodes.map((n) => n.id)), [myNodes]);
  const nodeRows = React.useMemo(() => {
    // Per-node lookup into the current search response. /v1/search/nodes
    // ships richer per-node data than /v1/nodes (it carries match.gpu =
    // {model, count, vram_gb}) so we prefer it when the node appears in
    // results. /v1/nodes-derived myNodes.gpus[] is the fallback for nodes
    // that aren't in the current search.
    const matchById = new Map();
    for (const m of (nodeMatches || [])) {
      if (m.node_id) matchById.set(m.node_id, m);
    }
    // Per-node lookup tables mirror the page-level shape — per-package
    // record (hashes/repos/name) plus service-level hash/name fallbacks.
    // Per-package preserves the boundary that file:// imports rely on
    // (no repos → cross-check skipped for that specific package).
    const installedPackagesByNode = new Map();
    const installedServiceHashesByNode = new Map();
    const installedServiceNamesByNode = new Map();
    for (const n of myNodes) {
      if (Array.isArray(n.installedPackages) && n.installedPackages.length > 0) {
        installedPackagesByNode.set(n.id, n.installedPackages);
      }
      const sh = new Set();
      const sn = new Set();
      for (const svc of (n.services || [])) {
        const h = svc.model_hash || svc.modelHash;
        if (h) sh.add(String(h).toLowerCase());
        if (svc.model) sn.add(String(svc.model).toLowerCase());
      }
      if (sh.size > 0) installedServiceHashesByNode.set(n.id, sh);
      if (sn.size > 0) installedServiceNamesByNode.set(n.id, sn);
    }
    const catalogHashes = (() => {
      const s = new Set();
      if (item.primaryHash) s.add(String(item.primaryHash).toLowerCase());
      for (const f of (item.hashes || [])) {
        if (f?.hash) s.add(String(f.hash).toLowerCase());
      }
      return s;
    })();
    const catalogRepos = new Set();
    if (item.source === "huggingface" && item.id) {
      catalogRepos.add(("hf:" + String(item.id)).toLowerCase());
    } else if (item.source === "civitai") {
      if (item.versionId) catalogRepos.add(("civitai:v:" + String(item.versionId)).toLowerCase());
      if (item.id)        catalogRepos.add(("civitai:m:" + String(item.id)).toLowerCase());
    }
    const matchKey = String(item.name || item.id || "").toLowerCase();
    return myNodes.map((n) => {
      const authed = !!n.auth;
      const online = !!n.online;
      let disabled = false;
      let reason = "";
      if (!authed)      { disabled = true; reason = "not-authed"; }
      else if (!online) { disabled = true; reason = "offline"; }
      // GPU summary, preferring nodeMatches.match.gpu (richer per-node data
      // from the search index) and falling back to /v1/nodes hardware.gpus.
      const m = matchById.get(n.id);
      let gpuLine = "";
      if (m?.gpu?.model) {
        const count = m.gpu.count || 1;
        const short = shortGpuName(m.gpu.model);
        gpuLine = count > 1 ? `${short} X ${count}` : short;
      } else {
        gpuLine = summarizeGpus(n.gpus) || "";
      }
      const installState = classifyPackageNode({
        nid: n.id,
        catalogHashes,
        catalogRepos,
        itemKey: matchKey,
        installedPackagesByNode,
        installedServiceHashesByNode,
        installedServiceNamesByNode,
      });
      return {
        id: n.id,
        name: n.id,
        // subLabel: rendered below the proxy ID — operator-set label
        // first (clearest), else RV-reported "ip:port" so the row
        // identifies as a recognizable network endpoint when no
        // alias has been set.
        subLabel: n.label || n.addr || "",
        authed,
        online,
        disabled,
        reason,
        installState,
        alreadyInstalled: installState === "installed",
        metaLine: gpuLine,
      };
    });
  }, [myNodes, myNodeIds, nodeMatches, item.primaryHash, item.name, item.id, item.versionId, item.source, item.hashes]);

  const authedTotal = nodeRows.filter((r) => r.authed).length;
  const installedNodeCount = nodeRows.filter((r) => r.authed && r.alreadyInstalled).length;

  // Default selection priority: clean (none) > broken (worth reinstalling)
  // > anything authed+online (includes already-installed for re-runs).
  // "" means no valid target (Install button stays disabled).
  React.useEffect(() => {
    if (selectedNodeId && nodeRows.find((r) => r.id === selectedNodeId && !r.disabled)) return;
    const fresh = nodeRows.find((r) => r.authed && r.online && r.installState === "none")
      || nodeRows.find((r) => r.authed && r.online && r.installState === "broken")
      || nodeRows.find((r) => r.authed && r.online);
    setSelectedNodeId(fresh ? fresh.id : "");
  }, [nodeRows, selectedNodeId]);

  const selectedNodeName = (() => {
    const r = nodeRows.find((x) => x.id === selectedNodeId);
    return r ? r.name : "";
  })();

  const handleAuthenticate = async (nodeId) => {
    setAuthBusyId(nodeId);
    setInstallError("");
    try {
      const res = await authMyNode(nodeId);
      if (res?.auth) {
        // IndexedDB row is updated by authMyNode; ask parent to refresh
        // myNodes so this row flips to authed+online.
        if (onMyNodesChanged) await onMyNodesChanged();
      } else {
        setInstallError("인증 실패: provider 측 owner 검증을 통과하지 못했습니다");
      }
    } catch (err) {
      setInstallError(friendlyInstallError(err));
    } finally {
      setAuthBusyId("");
    }
  };

  const triggerInstall = () => {
    const row = nodeRows.find((r) => r.id === selectedNodeId);
    if (!row || !row.authed || !row.online) {
      setInstallError("Selected node is not authenticated or is offline.");
      return;
    }
    if (isLora) {
      if (!loraArch) {
        setInstallError("Architecture inference failed — Civitai didn't surface a baseModel for this LoRA.");
        return;
      }
    } else if (!inference.service) {
      setInstallError("Service inference failed — manual-pick UI not yet available.");
      return;
    }
    if (typeof startBackgroundInstall !== "function") {
      setInstallError("Internal: install handler not provided");
      return;
    }
    // Civitai install uses the canonical downloadUrl that Civitai
    // returned in the by-hash response (already exact for this file —
    // no need to reconstruct with synthetic ?type/format/size/fp).
    // Falls back to the bare versionId-based endpoint when the API
    // didn't surface it (rare).
    const civitaiSrc = (item.source === "civitai")
      ? (item.primaryDownloadUrl ||
         (item.versionId ? `https://civitai.com/api/download/models/${item.versionId}` : ""))
      : "";
    // LoRA install diverges from model install: backend filesystem
    // partitions LoRAs by architecture (packages/loras/{arch}/) instead
    // of by service (packages/models/{service}/), and the strict CLI
    // refuses a LoRA install without --architecture.
    const body = isLora
      ? {
          type: "lora",
          name: item.name || item.id,
          version: "latest",
          architecture: loraArch,
          ...(civitaiSrc ? { src: civitaiSrc } : { repo: item.url }),
        }
      : {
          type: "model",
          name: item.name || item.id,
          version: "latest",
          service: inference.service,
          ...(civitaiSrc ? { src: civitaiSrc } : { repo: item.url }),
        };
    // Fire-and-forget at the PAGE scope. fetch lives past the modal so
    // closing the modal does NOT abort it — but we deliberately keep the
    // modal open here so the in-modal progress section can show the
    // install rolling forward. User can close any time with × / Cancel.
    startBackgroundInstall(row.id, body, item.name || item.id);
  };

  // Fetch partial-download state from each authed online node, filtered
  // to THIS catalog item, so the user sees "47% saved" before kicking
  // off install (and so the progress bar seeds at the right starting
  // value when they hit Install). Refreshed on a 3s timer so the UI
  // doesn't get stuck on stale percent when an install elsewhere (other
  // window, CLI) is making progress on the same partial.
  const itemKey = item.name || item.id;
  React.useEffect(() => {
    if (phase !== "install") return;
    let cancelled = false;
    const fetchOnce = async () => {
      const targets = (myNodes || []).filter((n) => n.auth && n.online);
      const entries = await Promise.all(targets.map(async (n) => {
        try {
          const resp = await fetch(`/node/${encodeURIComponent(n.id)}/provider/partials`, {
            headers: { ...getAuthHeaders() },
          });
          if (!resp.ok) return [n.id, null];
          const data = await resp.json();
          const arr = Array.isArray(data) ? data : [];
          // Pick the highest-percent file for this package — multi-file
          // models report per-file partials; the most-progressed one is
          // the most useful "where am I" indicator.
          let best = null;
          for (const p of arr) {
            if (p?.package_name !== itemKey) continue;
            if (!best || (p.percent || 0) > (best.percent || 0)) best = p;
          }
          return [n.id, best];
        } catch {
          return [n.id, null];
        }
      }));
      if (cancelled) return;
      const next = {};
      for (const [id, val] of entries) {
        if (val) next[id] = val;
      }
      setPartialsByNode(next);
    };
    fetchOnce();
    const handle = setInterval(fetchOnce, 3000);
    return () => { cancelled = true; clearInterval(handle); };
  }, [phase, itemKey, myNodes]);

  // Find the install entry (if any) targeting selectedNode + this catalog
  // item. We split by "in flight" vs "terminal" — both should render in
  // the progress section so the user sees error/done states for their
  // 4-6s grace, but only the in-flight one should disable the Install
  // button. Otherwise an errored job blocks retry until its drop timer
  // expires, which feels broken.
  const matchingJob = React.useMemo(() => {
    if (!selectedNodeId) return null;
    for (const j of Object.values(activeInstalls || {})) {
      if (j.nodeId === selectedNodeId && j.label === itemKey) return j;
    }
    return null;
  }, [activeInstalls, selectedNodeId, itemKey]);
  const isJobInFlight = matchingJob &&
    matchingJob.status !== "error" &&
    matchingJob.status !== "done" &&
    matchingJob.status !== "skipped";
  const activeJob = matchingJob; // for progress rendering (any state)

  // While ANY install for this model is in flight (regardless of which
  // node it's on) we want to lock the radio list — switching nodes mid-
  // download would just lose the user's view of the running progress.
  // Find the in-flight job's target node so other rows get disabled and
  // the selection auto-pins back if the user tries to flip.
  const inFlightLockNodeId = React.useMemo(() => {
    for (const j of Object.values(activeInstalls || {})) {
      if (j.label !== itemKey) continue;
      if (j.status === "error" || j.status === "done" || j.status === "skipped") continue;
      return j.nodeId;
    }
    return "";
  }, [activeInstalls, itemKey]);

  // Auto-pin the radio to the locked node so the bottom button + progress
  // section stay coherent with the running install.
  React.useEffect(() => {
    if (inFlightLockNodeId && selectedNodeId !== inFlightLockNodeId) {
      setSelectedNodeId(inFlightLockNodeId);
    }
  }, [inFlightLockNodeId, selectedNodeId]);

  const partialForSelected = selectedNodeId ? partialsByNode[selectedNodeId] : null;

  // Effective progress to render: live job percent wins over partial-on-
  // disk percent. null = nothing to show.
  const progressView = (() => {
    if (activeJob) {
      const pct = Math.max(0, Math.min(100, Math.round(activeJob.percent || 0)));
      return {
        percent: pct,
        statusLabel: (() => {
          if (activeJob.status === "done") return "✓ done";
          if (activeJob.status === "error") return "✗ error";
          if (activeJob.status === "verifying") return "verifying…";
          if (activeJob.status === "skipped") return "skipped";
          if (activeJob.status === "starting") return "starting…";
          return `downloading ${pct}%`;
        })(),
        file: activeJob.file || "",
        error: activeJob.error || "",
        kind: "live",
      };
    }
    if (partialForSelected) {
      return {
        percent: partialForSelected.percent || 0,
        statusLabel: `partial — ${partialForSelected.percent || 0}% saved (resume on Install)`,
        file: partialForSelected.file_name || "",
        error: "",
        kind: "partial",
      };
    }
    return null;
  })();

  return (
    <div className="modal-backdrop" onClick={(e) => { e.stopPropagation(); onClose(); }}>
      <div className="modal" onClick={stopProp}>

        <div className="modal-header">
          <div className="modal-title">
            <span className="modal-title-icon">{installed ? "✓" : "📦"}</span>
            <span>{phase === "install" ? "Install" : "Inspection"} — {item.name || item.id}</span>
          </div>
          <button className="modal-close" onClick={onClose} title={t("search.modal_close")}>×</button>
        </div>

        {phase === "inspect" && (
        <div className="modal-body">

          {/* Overview */}
          <div className="section-title">{t("search.inspect_overview")}</div>
          <div className="kv-row"><div className="kv-key">{t("search.inspect_name")}</div><div className="kv-val"><strong>{item.name || item.id}</strong></div></div>
          <div className="kv-row">
            <div className="kv-key">{t("search.inspect_source")}</div>
            <div className="kv-val">
              <span className={`badge ${item.source === "civitai" ? "badge-civitai" : "badge-hf"}`}>
                {item.source === "civitai" ? "Civitai" : "HuggingFace"}
              </span>
              {" "}
              <a href={item.url} target="_blank" rel="noopener noreferrer">{item.id} ↗</a>
            </div>
          </div>
          <div className="kv-row">
            <div className="kv-key">{t("search.inspect_license")}</div>
            <div className="kv-val">
              {item.license
                ? <span className="badge badge-license">{item.license}</span>
                : <span className="kv-muted">— not provided</span>}
            </div>
          </div>
          {item.pipelineTag && (
            <div className="kv-row"><div className="kv-key">{t("search.inspect_modality")}</div><div className="kv-val">{item.pipelineTag}</div></div>
          )}
          <div className="kv-row"><div className="kv-key">{t("search.inspect_format")}</div><div className="kv-val">{format}</div></div>

          {/* Service (or Architecture for LoRAs) — frontend inference only */}
          {isLora ? (
            <>
              <div className="section-title">{t("search.inspect_lora_target")}</div>
              <div className="kv-row">
                <div className="kv-key">{t("search.inspect_architecture")}</div>
                <div className="kv-val">
                  {loraArch ? (
                    <>
                      <span className="badge badge-info">{loraArch}</span>
                      <span className="kv-hint"> [from: baseModel={item.baseModel || "?"}]</span>
                    </>
                  ) : (
                    <>
                      <span className="badge badge-warn">✗ baseModel missing</span>
                      <span className="kv-hint"> — Civitai didn't surface a baseModel; install will fail</span>
                    </>
                  )}
                </div>
              </div>
              <div className="kv-row">
                <div className="kv-key">{t("search.inspect_compatible_with")}</div>
                <div className="kv-val">{archDisplay(loraArch)} checkpoints</div>
              </div>
            </>
          ) : (
            <>
              <div className="section-title">{t("search.inspect_service_section")}</div>
              {inference.service ? (
                <>
                  <div className="kv-row">
                    <div className="kv-key">{t("search.inspect_service")}</div>
                    <div className="kv-val">
                      <span className="badge badge-info">{inference.service}</span>
                      <span className="kv-hint"> [from: {inference.source}]</span>
                    </div>
                  </div>
                  <div className="kv-row"><div className="kv-key">{t("search.inspect_engine")}</div><div className="kv-val">{inference.engine || "—"}</div></div>
                </>
              ) : (
                <div className="kv-row">
                  <div className="kv-key">{t("search.inspect_service")}</div>
                  <div className="kv-val">
                    <span className="badge badge-warn">✗ inference failed</span>
                    <span className="kv-hint"> — pick manually at install time</span>
                  </div>
                </div>
              )}
            </>
          )}

          {/* Files */}
          <div className="section-title">{t("search.inspect_files", { n: files.length })}</div>
          {files.length === 0 ? (
            <div className="kv-muted" style={{ fontSize: 12 }}>{t("search.no_file_metadata")}</div>
          ) : (
            <div className="file-list">
              <div className="file-row is-header">
                <div>FILE</div>
                <div style={{ textAlign: "right" }}>SIZE</div>
                <div>SHA256</div>
                <div></div>
              </div>
              {files.map((f, i) => {
                const isPrimary = f.file === primaryName;
                return (
                  <div key={i} className={`file-row ${isPrimary ? "is-primary" : ""}`}>
                    <div className="file-name">
                      {f.file}
                      {isPrimary && <span className="primary-marker">PRIMARY</span>}
                    </div>
                    <div className="file-size">{f.size > 0 ? formatSize(f.size) : "—"}</div>
                    <div className="file-hash" title={f.hash || ""}>{f.hash ? `${f.hash.slice(0, 10)}…` : "—"}</div>
                    {f.hash ? (
                      <button
                        className="file-copy"
                        title={t("search.copy_sha256")}
                        onClick={() => navigator.clipboard?.writeText(f.hash)}
                      >📋</button>
                    ) : <div />}
                  </div>
                );
              })}
            </div>
          )}

          {/* Install Target */}
          {installPath && (
            <>
              <div className="section-title">{t("search.inspect_install_target")}</div>
              <div className="kv-row"><div className="kv-key">Path</div><div className="kv-val"><code>{installPath}</code></div></div>
              <div className="kv-row"><div className="kv-key">{t("search.inspect_disk_required")}</div><div className="kv-val"><strong>{totalSize > 0 ? formatSize(totalSize) : "—"}</strong></div></div>
              <div className="kv-row">
                <div className="kv-key">{t("search.inspect_already_on_disk")}</div>
                <div className="kv-val">
                  {authedTotal === 0
                    ? <span className="kv-muted">— no authenticated nodes</span>
                    : installedNodeCount > 0
                      ? <span className="badge badge-ok">✓ on {installedNodeCount} of {authedTotal} authed node{authedTotal > 1 ? "s" : ""}</span>
                      : <span className="badge badge-warn">✗ Not installed</span>}
                </div>
              </div>
              {profilePath && (
                <div className="kv-row"><div className="kv-key">{t("search.inspect_profile_file")}</div><div className="kv-val"><code>{profilePath}</code> <span className="kv-hint">{t("search.inspect_state_check")}</span></div></div>
              )}
            </>
          )}

          {/* Warnings */}
          {warnings.length > 0 && (
            <>
              <div className="section-title">{t("search.warnings")}</div>
              <div className="warn-list">
                {warnings.map((w, i) => (
                  <div key={i} className={`warn-item ${w.level === "error" ? "error" : ""}`}>
                    {w.level === "error" ? "✗" : "⚠"} {w.msg}
                  </div>
                ))}
              </div>
            </>
          )}

        </div>
        )}

        {phase === "install" && (
        <div className="modal-body">
          <div className="section-title">Choose target node</div>
          {nodeRows.length === 0 ? (
            <div className="kv-muted">
              등록된 내 노드가 없습니다 — Nodes 페이지에서 먼저 노드를 추가하세요.
            </div>
          ) : (
            <div className="target-options">
              {nodeRows.map((row) => {
                const isSelected = row.id === selectedNodeId;
                // While an install is in flight, lock every row that isn't
                // the in-flight target. Switching nodes mid-download would
                // hide running progress + confuse the action button.
                const lockedByInFlight = !!inFlightLockNodeId && row.id !== inFlightLockNodeId;
                const rowDisabled = row.disabled || lockedByInFlight;
                const cls = ["target-option"];
                if (rowDisabled) cls.push("target-option-disabled");
                if (isSelected) cls.push("target-option-selected");
                return (
                  <label key={row.id} className={cls.join(" ")}>
                    <input
                      type="radio"
                      name="install-target"
                      disabled={rowDisabled}
                      checked={isSelected}
                      onChange={() => setSelectedNodeId(row.id)}
                    />
                    <div className="target-option-body">
                      {/* Row 1: proxy ID alone, monospaced. */}
                      <div className="target-option-title">
                        <span className="card-id">{row.name}</span>
                      </div>
                      {/* Row 2: operator-set label or "ip:port". */}
                      {row.subLabel && (
                        <div className="target-option-sublabel">{row.subLabel}</div>
                      )}
                      {/* Row 3: badges + GPU chip together. */}
                      <div className="target-option-meta">
                        {row.metaLine && (
                          <span className="card-chip">{row.metaLine}</span>
                        )}
                        {row.installState === "installed" && (
                          <span className="badge badge-ok">✓ Already installed</span>
                        )}
                        {row.installState === "broken" && (
                          <span className="badge badge-broken">⚠ Broken — reinstall</span>
                        )}
                        {!row.authed && (
                          <span className="badge badge-warn">🔒 Authenticate first</span>
                        )}
                        {row.authed && !row.online && (
                          <span className="badge badge-warn">offline</span>
                        )}
                        {!row.authed && (
                          <button
                            type="button"
                            className="btn-link"
                            disabled={authBusyId === row.id}
                            onClick={(e) => { e.preventDefault(); handleAuthenticate(row.id); }}
                          >
                            {authBusyId === row.id ? "Authenticating…" : "Authenticate"}
                          </button>
                        )}
                      </div>
                    </div>
                  </label>
                );
              })}
            </div>
          )}

          <div className="compat-box">
            <div className="compat-box-title">Pre-flight checks</div>
            <div>
              ✓ SHA256 verified against {item.source === "huggingface" ? "HuggingFace" : "Civitai"} metadata after download
            </div>
            <div className="compat-box-note">
              ⓘ Install status for imported models (via <code>--src file://</code> or arbitrary URLs) is verified for single-file models only. Sharded imports are not cross-checked against this catalog because shared shard hashes can match unrelated models.
            </div>
          </div>

          {progressView && (
            <div className={`install-modal-progress install-modal-progress-${progressView.kind}`}>
              <div className="install-modal-progress-head">
                <span className="install-modal-progress-title">Progress</span>
                <span className="install-modal-progress-status">{progressView.statusLabel}</span>
              </div>
              <div className="install-modal-progress-bar">
                <div
                  className="install-modal-progress-fill"
                  style={{ width: `${progressView.percent}%` }}
                />
              </div>
              {progressView.file && (
                <div className="install-modal-progress-file" title={progressView.file}>
                  {progressView.file}
                </div>
              )}
              {progressView.error && (
                <div className="install-modal-progress-error">{progressView.error}</div>
              )}
            </div>
          )}

          {installError && (
            <div className="warn-list">
              <div className="warn-item error">✗ {installError}</div>
            </div>
          )}
        </div>
        )}

        <div className="modal-footer">
          {phase === "inspect" ? (
            <>
              <button className="btn btn-secondary" onClick={onClose}>Cancel</button>
              <button
                className="btn btn-primary"
                disabled={isLora ? !loraArch : !inference.service}
                title={
                  isLora
                    ? (loraArch ? "Install (download + register)" : "baseModel unknown — Civitai didn't surface architecture")
                    : (inference.service ? "Install (download + register)" : "Service unknown — pick one first")
                }
                onClick={() => setPhase("install")}
              >
                {installed ? "Use →" : "Install ↓"}
              </button>
            </>
          ) : (
            <>
              <button
                className="btn btn-secondary"
                disabled={installing}
                onClick={() => setPhase("inspect")}
              >
                ← Back
              </button>
              <button
                className="btn btn-primary"
                disabled={!selectedNodeId || (isLora ? !loraArch : !inference.service) || isJobInFlight}
                onClick={triggerInstall}
              >
                {isJobInFlight
                  ? `Installing… ${Math.round(activeJob?.percent || 0)}%`
                  : !selectedNodeId
                    ? "Pick a node →"
                    : matchingJob?.status === "error"
                      ? `Retry on ${selectedNodeName} →`
                      : partialForSelected
                        ? `Resume on ${selectedNodeName} →`
                        : `Install on ${selectedNodeName} →`}
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

// friendlyInstallError — short Korean rewrite of common install backend
// errors. Mirrors the mapping in hooks/useInstallManager.js:35 so the user
// sees the same wording across pages. Falls through to the raw message
// when no rule matches.
// extractRepoKeyFromUrl pulls a source-prefixed identifier from a
// download URL so the matching layer can tell apart same-content
// uploads across different sources / accounts. Returns "" for URLs we
// don't recognize (matching falls back to hash-only).
//
// HuggingFace:
//   https://huggingface.co/<owner>/<repo>/resolve/...  →  hf:<owner>/<repo>
// Civitai:
//   https://civitai.com/api/download/models/<versionId>  →  civitai:v:<versionId>
//   https://civitai.com/models/<modelId>                  →  civitai:m:<modelId>
//
// We keep version-id and model-id in distinct namespaces so a Civitai
// modelId never collides with a versionId of the same numeric value.
function extractRepoKeyFromUrl(url) {
  if (!url || typeof url !== "string") return "";
  let m = url.match(/^https?:\/\/huggingface\.co\/([^/]+)\/([^/]+)/i);
  if (m) return ("hf:" + m[1] + "/" + m[2]).toLowerCase();
  m = url.match(/^https?:\/\/civitai\.com\/api\/download\/models\/(\d+)/i);
  if (m) return ("civitai:v:" + m[1]).toLowerCase();
  m = url.match(/^https?:\/\/civitai\.com\/models\/(\d+)/i);
  if (m) return ("civitai:m:" + m[1]).toLowerCase();
  return "";
}

// Backwards-compatible alias used by older call sites in this file.
const extractHFRepoFromUrl = extractRepoKeyFromUrl;

// classifyPackageNode returns whether a node has the catalog item
// "installed", "broken" (some shard hashes present but not all), or
// "none". Iterates per-package so package boundaries are preserved.
//
// Import packages (file:// or arbitrary URL — `pkg.repos` empty) get a
// nuanced treatment:
//   - single-file catalogs (catalogHashes.size === 1): import matching is
//     allowed. SHA-256 collisions across distinct models are negligible,
//     so a hash hit is trustworthy even without repo cross-check.
//   - sharded catalogs (size > 1): import matching is skipped. Different
//     models can share base/shard weights byte-identically (fine-tunes,
//     mirrors), so partial overlap on a sharded catalog can fuse an
//     import to the wrong card.
//
// Falls back to service-level hashes (model_hash) when no package
// matches; service fallback has no shard granularity so it can only
// return "installed" or "none", never "broken".
function classifyPackageNode({
  nid,
  catalogHashes,
  catalogRepos,
  itemKey,
  installedPackagesByNode,
  installedServiceHashesByNode,
  installedServiceNamesByNode,
}) {
  // No catalog hash → name-only fallback (legacy/imperfect, boolean-like).
  if (!catalogHashes || catalogHashes.size === 0) {
    if (!itemKey) return "none";
    const svcNames = installedServiceNamesByNode?.get(nid);
    if (svcNames && svcNames.has(itemKey)) return "installed";
    const pkgs = installedPackagesByNode?.get(nid);
    if (pkgs) {
      for (const p of pkgs) {
        if (p.name && p.name === itemKey) return "installed";
      }
    }
    return "none";
  }

  const pkgs = installedPackagesByNode?.get(nid) || [];
  let sawBroken = false;

  for (const pkg of pkgs) {
    // Imports (no repo info) only match against single-file catalogs —
    // sharded imports are skipped to avoid cross-model shard fusion.
    if (pkg.repos.size === 0 && catalogHashes.size > 1) continue;

    let hits = 0;
    for (const h of catalogHashes) {
      if (pkg.hashes.has(h)) hits += 1;
    }
    if (hits === 0) continue;

    // Repos cross-check rules out same-bytes-different-uploader.
    // Only run it when both sides have repo info; imports (empty repos)
    // skip the check and trust the hash hit (single-file only path).
    if (pkg.repos.size > 0 && catalogRepos && catalogRepos.size > 0) {
      let repoHit = false;
      for (const r of catalogRepos) {
        if (pkg.repos.has(r)) { repoHit = true; break; }
      }
      if (!repoHit) continue;
    }

    if (hits === catalogHashes.size) return "installed";
    sawBroken = true;
    // Continue — another package on this node may be complete.
  }

  if (sawBroken) return "broken";

  // Service fallback (hash-only; no shard granularity).
  const svcHashes = installedServiceHashesByNode?.get(nid);
  if (svcHashes) {
    for (const h of catalogHashes) {
      if (svcHashes.has(h)) return "installed";
    }
  }
  return "none";
}

// packagesFromVersions converts a /provider/packages response array into
// the per-package shape consumed by the matching layer. Each package
// keeps its hashes + repos together so the matcher can reason about
// packages independently. Whether a package is eligible for catalog
// matching (e.g. file:// imports vs HF/Civitai downloads) is decided
// later in classifyPackageNode based on `repos` and the catalog's shard
// count — kept here intentionally lossless.
function packagesFromVersions(arr) {
  if (!Array.isArray(arr)) return [];
  const out = [];
  for (const pkg of arr) {
    const hashes = new Set();
    const repos = new Set();
    if (pkg?.hash) hashes.add(String(pkg.hash).toLowerCase());
    for (const d of (pkg?.downloads || [])) {
      if (d?.hash) hashes.add(String(d.hash).toLowerCase());
      const repo = extractRepoKeyFromUrl(d?.download_url);
      if (repo) repos.add(repo);
    }
    if (hashes.size === 0 && !pkg?.name) continue;
    out.push({
      name: String(pkg?.name || "").toLowerCase(),
      hashes,
      repos,
    });
  }
  return out;
}

function friendlyInstallError(err) {
  // Rules out the empty-Error case where err.message is "" — String(err)
  // would yield "Error" with no detail, which is useless to the user.
  const raw = err?.message;
  let msg;
  if (typeof raw === "string" && raw.length > 0) {
    msg = raw;
  } else {
    const s = String(err || "").trim();
    msg = (s && s !== "Error") ? s : "";
  }
  if (!msg) return "Install failed (no detail provided by installer — check node logs/isann.log).";
  if (msg.includes("install already in progress")) return "Already installing this package in another tab.";
  if (msg.includes("hash mismatch"))                return "Hash verification failed — the corrupt file was removed. Please retry.";
  if (msg.includes("ready_check timeout"))          return "Install finished but the service did not become ready in time.";
  if (msg.includes("install rejected: 401") || msg.includes("install rejected: 403")) {
    return "Permission denied — node auth expired or you are not the owner. Re-authenticate this node and retry.";
  }
  return msg;
}

// normalizeArchClient — frontend mirror of pkg/profile NormalizeArchitecture.
// Order matters: SDXL-family finetunes (Pony / Illustrious / NoobAI) match
// before generic "sdxl" because they share UNet shape but different feature
// spaces — collapsing them under sdxl produced unusable LoRA cross-loads.
// Returns "" when nothing matches so the modal can prompt for explicit
// --architecture. Keep in sync with pkg/profile/profile.go NormalizeArchitecture.
function normalizeArchClient(s) {
  const lc = String(s || "").toLowerCase().replace(/\s+/g, "").replace(/-/g, "").replace(/_/g, "");
  if (!lc) return "";
  // SDXL-family finetunes — must precede the generic "sdxl" check.
  if (lc.includes("noobai") || lc.includes("noob")) return "noobai";
  if (lc.includes("illustrious") || lc.includes("illust")) return "illustrious";
  if (lc.includes("pony")) return "pony";
  // SD versions — most-specific first.
  if (lc.includes("sd3.5") || lc.includes("sd35") || lc.includes("stablediffusion3.5")) return "sd35";
  if (lc.includes("sd3") || lc.includes("stablediffusionv3") || lc.includes("stablediffusion3")) return "sd3";
  if (lc.includes("sd2.1") || lc.includes("sd21") || lc.includes("stablediffusionv2")) return "sd21";
  if (lc.includes("sd1.5") || lc.includes("sd15") || lc.includes("stablediffusionv1")) return "sd15";
  if (lc.includes("sdxl") || lc.includes("stablediffusionxl")) return "sdxl";
  // Flux — D / S labels first, then generic.
  if (lc.includes("flux.1d") || lc.includes("flux1d") || lc.includes("fluxdev")) return "flux-d";
  if (lc.includes("flux.1s") || lc.includes("flux1s") || lc.includes("fluxschnell")) return "flux-s";
  if (lc.includes("flux")) return "flux";
  // Video.
  if (lc.includes("hunyuan")) return "hunyuan-video";
  // LLM family — version-specific labels first.
  if (lc.includes("qwen2.5") || lc.includes("qwen25")) return "qwen25";
  if (lc.includes("qwen3")) return "qwen3";
  if (lc.includes("qwen2") || lc.includes("qwen")) return "qwen2";
  if (lc.includes("llama3.1") || lc.includes("llama31")) return "llama31";
  if (lc.includes("llama")) return "llama3";
  if (lc.includes("mixtral")) return "mixtral";
  if (lc.includes("mistral")) return "mistral";
  return "";
}

const ARCH_DISPLAY = {
  sd15: "SD 1.5", sd21: "SD 2.1", sd3: "SD 3", sd35: "SD 3.5",
  sdxl: "SDXL", pony: "Pony", illustrious: "Illustrious", noobai: "NoobAI",
  flux: "Flux", "flux-d": "Flux.1 D", "flux-s": "Flux.1 S",
  "hunyuan-video": "Hunyuan Video",
  qwen2: "Qwen2", qwen25: "Qwen 2.5", qwen3: "Qwen 3",
  llama3: "Llama 3", llama31: "Llama 3.1",
  mistral: "Mistral", mixtral: "Mixtral",
};
function archDisplay(s) { return ARCH_DISPLAY[s] || s || "?"; }

// inferServiceClient — minimal frontend service inference using only data
// already on `item`. Mirrors a subset of the Go-side inferService rules
// in pkg/installer/service_infer.go but stays simple; ambiguous cases
// return service="" so the modal can show "inference failed" + suggest
// manual selection at install time.
function inferServiceClient(item) {
  const tags = (item.tags || []).map((t) => String(t).toLowerCase());
  const pipeline = (item.pipelineTag || "").toLowerCase();
  const lib = (item.libraryName || "").toLowerCase();
  const base = (item.baseModel || "").toLowerCase();
  const files = Array.isArray(item.hashes) ? item.hashes.map((h) => String(h.file || "").toLowerCase()) : [];
  const hasGGUF = files.some((f) => f.endsWith(".gguf"));
  const hasSafetensors = files.some((f) => f.endsWith(".safetensors"));

  // GGUF → llama.cpp regardless of pipeline tag.
  if (hasGGUF) return { service: "llm-api", engine: "llama.cpp", source: "gguf extension" };

  // Image generation (text-to-image / SD / SDXL / Flux variants).
  if (pipeline === "text-to-image" || base.includes("sd") || base.includes("sdxl") || base.includes("flux") ||
      tags.some((t) => t === "stable-diffusion" || t === "sdxl" || t.includes("flux"))) {
    return { service: "sd-api", engine: "sd.cpp", source: pipeline ? `pipeline_tag=${pipeline}` : "tags" };
  }

  // Speech in/out → voice-api (placeholder for future engine wiring).
  if (pipeline === "automatic-speech-recognition" || pipeline === "text-to-speech") {
    return { service: "voice-api", engine: pipeline === "text-to-speech" ? "xtts" : "whisper", source: `pipeline_tag=${pipeline}` };
  }

  // Transformers safetensors text-generation → vllm-api (external launcher).
  if (hasSafetensors && (pipeline === "text-generation" || lib === "transformers")) {
    return { service: "vllm-api", engine: "vllm", source: pipeline ? `pipeline_tag=${pipeline}` : "library_name=transformers" };
  }

  return { service: "", engine: "", source: "" };
}

// HashCell renders the truncated sha256 plus a copy-to-clipboard button.
// Operators commonly need the full hash (64 hex chars) to verify downloads
// outside the install flow, so a one-click copy is faster than expanding
// the title tooltip and selecting the text.
function HashCell({ hash, onCardClickStop }) {
  const [copied, setCopied] = React.useState(false);
  const copy = (e) => {
    onCardClickStop(e);
    if (!hash) return;
    const done = () => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1200);
    };
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(hash).then(done, () => {/* swallow */});
    } else {
      // Fallback for older browsers / non-secure contexts.
      const ta = document.createElement("textarea");
      ta.value = hash;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand("copy"); done(); } catch { /* noop */ }
      document.body.removeChild(ta);
    }
  };
  return (
    <span title={hash || ""}>
      <strong>Hash:</strong> {hash ? `${hash.slice(0, 8)}…` : "—"}
      {hash && (
        <button
          className="hash-copy-btn"
          onClick={copy}
          title={copied ? "Copied!" : "Copy full sha256"}
          aria-label="Copy sha256"
        >
          {copied ? "✓" : "📋"}
        </button>
      )}
    </span>
  );
}

// pickCatalogIcon returns a model-type appropriate emoji for the big install
// button. Helps users scan results visually — LLMs / image models / audio
// look different at a glance.
function pickCatalogIcon(item) {
  const kind = (item.kind || "").toLowerCase();
  const base = (item.baseModel || "").toLowerCase();
  const tags = (item.tags || []).map((t) => String(t).toLowerCase());
  const pipeline = (item.pipelineTag || "").toLowerCase();
  if (kind === "lora" || kind === "embedding") return "🔧";
  if (kind === "vae") return "🎨";
  if (kind === "controlnet") return "🎯";
  if (pipeline === "automatic-speech-recognition" || pipeline === "text-to-speech") return "🎤";
  if (pipeline === "text-to-image" || base.includes("sdxl") || base.includes("sd 1") || tags.includes("stable-diffusion")) {
    return base.includes("sdxl") ? "🎨" : "🖼️";
  }
  return "📦"; // LLM / default
}

// countShards returns the number of hash entries that look like sharded
// safetensors / bin files (e.g. "model-00003-of-00015.safetensors").
// Returns 0 if not sharded so callers can do `> 1` checks.
function countShards(hashes) {
  if (!Array.isArray(hashes) || hashes.length === 0) return 0;
  let n = 0;
  for (const h of hashes) {
    if (/-\d{5}-of-\d{5}\.(safetensors|bin)$/i.test(h.file || "")) n++;
  }
  return n;
}

// ─────────────────────────────────────────────────────────────────────
// Helpers
function shortId(s) {
  if (!s) return "—";
  if (s.length <= 16) return s;
  return s.slice(0, 8) + "…" + s.slice(-4);
}

// shortGPU strips vendor / brand prefixes from a GPU model string so the
// chip stays compact. "NVIDIA GeForce RTX 3060" → "RTX 3060".
function shortGPU(s) {
  if (!s) return "";
  return String(s)
    .replace(/^NVIDIA\s+/i, "")
    .replace(/^GeForce\s+/i, "")
    .replace(/^AMD\s+/i, "")
    .replace(/^Radeon\s+/i, "")
    .replace(/^Intel\s+/i, "")
    .trim();
}

function abbreviateMid(s) {
  if (!s) return "";
  if (s.length <= 16) return s;
  return s.slice(0, 6) + "…" + s.slice(-4);
}

function humanMatchType(t) {
  switch (t) {
    case "hash_exact": return "hash exact";
    case "owner": return "owner";
    case "gpu": return "GPU match";
    case "engine": return "engine match";
    case "node": return "node";
    case "text": return "text match";
    default: return t || "match";
  }
}

function abbreviateNum(n) {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + "M";
  if (n >= 1_000) return (n / 1_000).toFixed(1) + "k";
  return String(n);
}

function ratingStars(r) {
  const full = Math.round(r);
  return "★".repeat(Math.max(0, Math.min(5, full))) + "☆".repeat(5 - Math.max(0, Math.min(5, full)));
}
