import React from "react";
import { summarizeGpus } from "@utils/gpu";
import { useTranslation } from "@i18n";
import "./NodesView.scss";

function truncateId(id) {
  if (!id || id.length <= 16) return id || "";
  return id.slice(0, 10) + "..." + id.slice(-4);
}

function getSvcStatus(svc) {
  if (svc.server_loading) return "loading";
  if (svc.model) return "running";
  return "stopped";
}

export default function NodesView({ nodes, selectedId, myNodeIds, onToggleFav }) {
  const { t } = useTranslation();
  const node = (nodes || []).find(n => (n.id || n.node_id) === selectedId);

  if (!node) {
    return (
      <div className="studio-placeholder">
        <span className="icon nv-placeholder-icon">&#128421;</span>
        <div>{t("nodes.view_select_hint")}</div>
        <div className="nv-placeholder-count">{(nodes || []).length} {t("nodes.sidebar_title").toLowerCase()}</div>
      </div>
    );
  }

  const nid = node.id || node.node_id || "";
  const isFav = myNodeIds?.has(nid);
  const hw = node.hardware || {};
  const gpus = hw.gpus || [];
  const gpuSummary = summarizeGpus(gpus);
  const cpus = hw.cpus || [];
  const ram = hw.ram || {};
  const svcs = node.services || [];

  return (
    <div className="nodes-view">
      {/* Header */}
      <div className="nv-header">
        <span className={`nv-online-dot ${node.online ? "online" : ""}`} />
        <h2 className="nv-title">{truncateId(nid)}</h2>
        <button
          className={`btn-fav ${isFav ? "favorited" : ""}`}
          onClick={() => onToggleFav(nid)}
        >
          {isFav ? "&#11088; Favorited" : "&#9734; Add to My Nodes"}
        </button>
        <span className={`online-badge ${node.online ? "online" : ""}`}>
          {node.online ? "online" : "offline"}
        </span>
      </div>

      {/* Info Grid */}
      <div className="nv-info-grid">
        <InfoCard title={t("nodes.view_general")}>
          <InfoRow label="ID" value={nid} mono />
          <InfoRow label="Owner" value={truncateId(node.owner_address)} mono />
          <InfoRow label="Version" value={node.version || "—"} />
          <InfoRow label="Status" value={node.conn_status === "offline" || node.conn_status === "stale" ? node.conn_status : (node.status || "—")} />
          <InfoRow label="Started" value={node.started_at ? new Date(node.started_at).toLocaleString() : "—"} />
        </InfoCard>

        <InfoCard title={t("nodes.view_hardware")}>
          {cpus.map((c, i) => (
            <InfoRow key={i} label="CPU" value={c.name || "—"} />
          ))}
          {gpuSummary && <InfoRow label="GPU" value={gpuSummary} />}
          {ram.total_gb && (
            <InfoRow label="RAM" value={`${(ram.total_gb || 0).toFixed(1)} GB`} />
          )}
        </InfoCard>
      </div>

      {/* Services */}
      <div className="nv-card">
        <div className="nv-card-title">
          Services ({svcs.length})
        </div>
        {svcs.length === 0 ? (
          <div className="nv-empty-services">{t("nodes.view_no_services")}</div>
        ) : (
          svcs.map((svc, i) => {
            const status = getSvcStatus(svc);
            const statusColor = status === "running" ? "var(--color-success)" : status === "loading" ? "var(--color-warning)" : "var(--text-muted)";
            return (
              <div key={i} className="nv-service-row">
                <span className="svc-dot" style={{ background: statusColor }} />
                <span className="svc-name">{svc.name || svc.service || "unknown"}</span>
                <span className="svc-model">{svc.model || "—"}</span>
                <span className="svc-status" style={{ color: statusColor }}>{status}</span>
                {svc.queue_depth > 0 && (
                  <span className="svc-queue">queue: {svc.queue_depth}</span>
                )}
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}

function InfoCard({ title, children }) {
  return (
    <div className="nv-card">
      <div className="nv-card-title compact">{title}</div>
      <div className="nv-card-body">{children}</div>
    </div>
  );
}

function InfoRow({ label, value, mono }) {
  return (
    <div className="nv-info-row">
      <span className="nv-info-label">{label}</span>
      <span className={`nv-info-value${mono ? " mono" : ""}`}>{value || "—"}</span>
    </div>
  );
}
