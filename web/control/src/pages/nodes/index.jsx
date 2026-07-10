import React, { useState, useEffect, useCallback, useMemo, useRef } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "@i18n";
import { fetchNodes, fetchRendezvousList, fetchNodesByRendezvousAddr, fetchNodeMetricsByAddr, fetchMetrics, fetchMetricsByAddr, mergeMetricsIntoNodes, fetchMyNodes, addMyNode, deleteMyNode } from "../../api/nodes";
import { useAuth } from "../../context/AuthContext";
import StatusTag from "@components/StatusTag/StatusTag";
import Dropdown from "@components/Dropdown/Dropdown";
import CopyButton from "@components/CopyButton/CopyButton";
import { getEmblemPalette } from "@utils/emblem";
import { summarizeGpus, shortGpuName } from "@utils/gpu";
import { formatUptime } from "@utils/format";
import ModelLabel from "@components/ModelLabel/ModelLabel";
import Skeleton from "@components/Skeleton/Skeleton";

function EmblemPlaceholder({ nodeId }) {
  const { initials, gradient } = getEmblemPalette(nodeId);
  return (
    <div className="emblem-placeholder-base" style={{ background: gradient }}>{initials}</div>
  );
}

function EmblemImg({ src, nodeId }) {
  const [failed, setFailed] = useState(false);
  if (!src || failed) return <EmblemPlaceholder nodeId={nodeId} />;
  return <img src={src} alt="" onError={() => setFailed(true)} />;
}

function parseGateNode(n) {
  let hw = n.hardware || {};
  let svcs = n.services || [];
  if (typeof hw === "string") { try { hw = JSON.parse(hw); } catch { hw = {}; } }
  if (typeof svcs === "string") { try { svcs = JSON.parse(svcs); } catch { svcs = []; } }
  return { ...n, id: n.node_id || n.id, hardware: hw, services: svcs };
}

function getSvcStatus(svc) {
  if (svc.server_loading) return "loading";
  if (svc.server_ready || svc.model) return "running";
  return "stopped";
}

// formatCtxBadge picks the most user-relevant inspect field for a service —
// ctx_size for llama.cpp, max_model_len for vllm — and renders the value as
// a compact "8K" / "16K" badge. Returns null when no relevant field exists,
// so older providers without inspect data simply omit the badge.
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

// RV 의 conn_status (alive/stale/offline) 가 있으면 우선시: stale/offline 은
// 연결 문제 상태라 idle/busy 같은 workload 상태보다 먼저 드러내야 함.
// 연결은 살아있지만 running 서비스가 0개면 idle 대신 "standby" — provider
// 프로세스는 떠 있어도 모델이 로드된 서비스가 없는 상태를 의미.
function getDisplayStatus(node) {
  const c = node.conn_status;
  if (c === "offline" || c === "stale") return c;
  const hasRunning = (node.services || []).some((s) => getSvcStatus(s) === "running");
  if (!hasRunning) return "standby";
  return node.status || "offline";
}

// 카드에 표시할 metrics 집계값 — queue / done / avg / running.
// 각 서비스의 데이터를 합산하고 avg 는 처리량 가중 평균으로.
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

function NodeCard({ node, isSelected, isFav, onSelect, onToggleFav, canFav }) {
  const { t } = useTranslation();
  const nid = node.id || node.node_id || "";
  const allSvcs = node.services || [];
  const svcs = allSvcs.filter(s => getSvcStatus(s) === "running");
  const visibleSvcs = svcs.slice(0, MAX_VISIBLE_SERVICES);
  const extraCount = svcs.length - MAX_VISIBLE_SERVICES;
  const pending = getNodePending(svcs);
  const stats = getNodeJobStats(svcs);
  const gpu = getNodeGpu(node);
  // conn_status 를 우선 판단 (RV 3-tier). 없으면 legacy node.status fallback.
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
          <CopyButton value={nid} title={t("nodes.copy_node_id")} />
          <StatusTag value={getDisplayStatus(node)} />
          <span className="node-card-hdr-right">
            <span className={`icon-slot ${node.auth_mode === "protected" ? "" : "inactive"}`} title={node.auth_mode === "protected" ? "Protected" : "Not protected"}>
              <svg viewBox="0 0 24 24" width="18" height="18">
                <rect x="6" y="11" width="12" height="9" rx="2" fill="#4a9eff" stroke="#3080d0" strokeWidth="0.8"/>
                <path d="M9 11V8a3 3 0 0 1 6 0v3" fill="none" stroke="#3080d0" strokeWidth="1.5" strokeLinecap="round"/>
                <circle cx="12" cy="15.5" r="1.5" fill="#fff"/>
              </svg>
            </span>
            <span
              className={`icon-slot ${isFav ? "" : "inactive"}${canFav ? "" : " disabled"}`}
              style={{ cursor: canFav ? "pointer" : "not-allowed" }}
              title={canFav ? (isFav ? "Remove from My Nodes" : "Add to My Nodes") : "Login required"}
              onClick={(e) => { e.stopPropagation(); if (canFav) onToggleFav(nid); }}
            >
              <span className="fav-star-icon">{isFav ? "\u2605" : "\u2606"}</span>
            </span>
          </span>
        </div>
        <div className="node-card-label-row">
          <span className="node-card-label node-card-owner">{node.owner_address ? truncateId(node.owner_address) : "\u2014"}</span>
          {node.owner_address && <CopyButton value={node.owner_address} title={t("nodes.copy_owner")} />}
          <span className="node-card-gpu">{gpu || "\u2014"}</span>
        </div>
        <div className="node-card-services">
          {visibleSvcs.length === 0 && (
            <div className="svc-row svc-row-muted">{t("nodes.no_services_running")}</div>
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
                  {ctx && <span className="svc-ctx-badge" title={t("nodes.context_length")}>{ctx}</span>}
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
                  <span className="stat-item" title={`Average job duration`}>
                    {"\u00b7"} avg {formatAvgSec(stats.avgSec)}
                  </span>
                )}
                {stats.runningCount > 0 && (
                  <span className="stat-running" title={t("nodes.running_jobs")}>
                    running{stats.runningCount > 1 ? ` \u00d7${stats.runningCount}` : ""}
                  </span>
                )}
                {formatUptime(node.started_at) && (
                  <span className="stat-item" title={t("nodes.uptime_since_register")}>
                    {"\u00b7"} up {formatUptime(node.started_at)}
                  </span>
                )}
              </span>
            )}
          </div>
          {/* TPM badge below the metrics row. Two states:
              - tpm_verified=true \u2192 green "\u2713 TPM: <issuer>"  (RV challenge passed)
              - ek_cert_issuer only \u2192 gray  "TPM: <issuer>"   (cert known, verify pending) */}
          {node.tpm_verified ? (
            <span className="node-card-tpm-badge verified" title="fTPM verified (challenge passed)">
              \u2713 TPM: {node.ek_cert_issuer || "verified"}
            </span>
          ) : node.ek_cert_issuer ? (
            <span className="node-card-tpm-badge pending" title={t("nodes.ek_cert_pending")}>
              TPM: {node.ek_cert_issuer}
            </span>
          ) : null}
        </div>
      </div>
    </div>
  );
}

const PAGE_CHUNK = 20;

export default function Nodes() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { isLoggedIn } = useAuth();
  const [rvList, setRvList] = useState([]);
  const [selectedRv, setSelectedRv] = useState("");
  const [allNodes, setAllNodes] = useState([]);
  const [initialLoading, setInitialLoading] = useState(true);
  const [search, setSearch] = useState("");
  const [serviceFilter, setServiceFilter] = useState("");
  const [gpuFilter, setGpuFilter] = useState("");
  const [myNodeIds, setMyNodeIds] = useState(new Set());
  const [useGate, setUseGate] = useState(false);
  const [visibleCount, setVisibleCount] = useState(PAGE_CHUNK);
  const sentinelRef = useRef(null);

  useEffect(() => {
    fetchRendezvousList().then((data) => {
      const list = Array.isArray(data) ? data : [];
      setRvList(list);
      setUseGate(list.length > 0);
      if (list.length > 0 && !selectedRv) setSelectedRv(list[0].id);
    }).catch(() => setRvList([]));
  }, []);

  useEffect(() => {
    fetchMyNodes().then((data) => {
      const ids = new Set((Array.isArray(data) ? data : []).map((n) => n.id));
      setMyNodeIds(ids);
    }).catch(() => {});
  }, []);

  const loadNodes = useCallback(async () => {
    const start = Date.now();
    try {
      if (useGate && selectedRv) {
        const rv = rvList.find(r => r.id === selectedRv);
        const addr = rv?.addr || "";
        if (addr) {
          // /v1/nodes/metrics 는 /v1/metrics 의 legacy alias —
          // (per-service rows: { node_id, service, status, queue_depth, ... })
          const [data, metrics] = await Promise.all([
            fetchNodesByRendezvousAddr(addr),
            fetchMetricsByAddr(addr).catch(() => []),
          ]);
          const nodes = Array.isArray(data) ? data : [];
          mergeMetricsIntoNodes(nodes, metrics);
          setAllNodes(nodes);
        }
      } else if (!useGate) {
        // 기본 경로: broker 의 /v1/nodes + /v1/metrics 병합
        const [data, metrics] = await Promise.all([
          fetchNodes(),
          fetchMetrics().catch(() => []),
        ]);
        const nodes = Array.isArray(data) ? data : [];
        mergeMetricsIntoNodes(nodes, metrics);
        setAllNodes(nodes);
      }
    } catch {}
    const elapsed = Date.now() - start;
    if (elapsed < 400) await new Promise(r => setTimeout(r, 400 - elapsed));
    setInitialLoading(false);
  }, [useGate, selectedRv, rvList]);

  useEffect(() => {
    loadNodes();
    const timer = setInterval(loadNodes, 30000);
    return () => clearInterval(timer);
  }, [loadNodes]);

  const toggleMyNode = async (nodeId) => {
    if (myNodeIds.has(nodeId)) {
      await deleteMyNode(nodeId);
      setMyNodeIds((prev) => { const s = new Set(prev); s.delete(nodeId); return s; });
    } else {
      await addMyNode(nodeId);
      setMyNodeIds((prev) => new Set(prev).add(nodeId));
    }
  };

  // GPU 종류 옵션 — 모든 노드의 hardware.gpus 에서 unique short name 추출.
  const gpuOptions = useMemo(() => {
    const set = new Set();
    for (const n of allNodes) {
      for (const g of (n.hardware?.gpus || [])) {
        const name = shortGpuName(g.name);
        if (name) set.add(name);
      }
    }
    return Array.from(set).sort().map(name => ({ value: name, label: name }));
  }, [allNodes]);

  const filtered = useMemo(() => {
    return allNodes.filter((n) => {
      const nid = n.id || n.node_id || "";
      const owner = (n.owner_address || "").toLowerCase();
      if (search && !nid.toLowerCase().includes(search.toLowerCase()) && !owner.includes(search.toLowerCase())) return false;
      if (serviceFilter) {
        const svcs = n.services || [];
        if (!svcs.some((s) => s.name === serviceFilter)) return false;
      }
      if (gpuFilter) {
        const gpus = n.hardware?.gpus || [];
        if (!gpus.some(g => shortGpuName(g.name) === gpuFilter)) return false;
      }
      return true;
    });
  }, [allNodes, search, serviceFilter, gpuFilter]);

  // Reset visible count when filters change
  useEffect(() => { setVisibleCount(PAGE_CHUNK); }, [search, serviceFilter, gpuFilter]);

  const visibleNodes = useMemo(() => filtered.slice(0, visibleCount), [filtered, visibleCount]);

  // Infinite scroll with IntersectionObserver
  useEffect(() => {
    const el = sentinelRef.current;
    if (!el) return;
    const obs = new IntersectionObserver((entries) => {
      if (entries[0].isIntersecting) {
        setVisibleCount((prev) => Math.min(prev + PAGE_CHUNK, filtered.length));
      }
    }, { rootMargin: "200px" });
    obs.observe(el);
    return () => obs.disconnect();
  }, [filtered.length]);

  const serviceOptions = useMemo(() => {
    const set = new Set();
    allNodes.forEach((n) => (n.services || []).forEach((s) => set.add(s.name)));
    return Array.from(set);
  }, [allNodes]);

  const handleSelect = (node) => {
    const nid = node.id || node.node_id;
    navigate(`/nodes/${encodeURIComponent(nid)}`);
  };

  return (
    <div className="page page-flex-column">
      <div className="page-header">
        <h2>{t("nodes.title")}{selectedRv ? ` \u2014 ${selectedRv}` : ""}</h2>
      </div>

      <div className="page-filters">
        <div className="page-filters-left">
          {rvList.length > 0 && (
            <Dropdown
              value={selectedRv}
              options={rvList.map((rv) => ({ value: rv.id, label: `${rv.id} (${rv.region || "-"})` }))}
              onChange={(val) => { setSelectedRv(val);}}
              placeholder=""
            />
          )}
          <input
            type="text"
            className="filter-input"
            style={{ minWidth: 280 }}
            placeholder={t("nodes.search_id_or_owner")}
            value={search}
            onChange={(e) => { setSearch(e.target.value);}}
          />
          <Dropdown
            value={serviceFilter}
            options={serviceOptions}
            onChange={(val) => { setServiceFilter(val);}}
            placeholder={t("nodes.all_services")}
          />
          <Dropdown
            value={gpuFilter}
            options={gpuOptions}
            onChange={(val) => { setGpuFilter(val);}}
            placeholder={t("nodes.search_all_gpus")}
          />
        </div>
      </div>

      <div className="page-body">
        <div className="node-cards">
          {initialLoading ? (
            Array.from({ length: 8 }).map((_, i) => (
              <div key={`sk-${i}`} className="node-card">
                <div style={{ width: 90, background: "var(--bg-card-header)", flexShrink: 0 }} />
                <div style={{ flex: 1, padding: "14px 16px", display: "flex", flexDirection: "column", gap: 8 }}>
                  <Skeleton.Line width="60%" />
                  <Skeleton.Line width="80%" height={12} />
                  <Skeleton.Line width="40%" height={12} />
                  <div style={{ marginTop: "auto", display: "flex", gap: 6 }}>
                    <Skeleton.Block width="60px" height={20} borderRadius={6} />
                    <Skeleton.Block width="50px" height={20} borderRadius={6} />
                  </div>
                </div>
              </div>
            ))
          ) : (
            visibleNodes.map((node) => {
              const nid = node.id || node.node_id;
              return (
                <NodeCard
                  key={nid}
                  node={node}
                  isSelected={false}
                  isFav={myNodeIds.has(nid)}
                  onSelect={handleSelect}
                  onToggleFav={toggleMyNode}
                  canFav={isLoggedIn}
                />
              );
            })
          )}
        </div>
        {visibleCount < filtered.length && <div ref={sentinelRef} className="sentinel-1px" />}
      </div>
    </div>
  );
}
