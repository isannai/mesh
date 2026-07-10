import React, { useState } from "react";
import { summarizeGpus } from "@utils/gpu";
import { useTranslation } from "@i18n";
import "./NodesSideBar.scss";

function truncateId(id) {
  if (!id || id.length <= 16) return id || "";
  return id.slice(0, 10) + "..." + id.slice(-4);
}

function getGpuShort(node) {
  return summarizeGpus(node.hardware?.gpus);
}

export default function NodesSideBar({ nodes, selectedId, myNodeIds, onSelect, onToggleFav }) {
  const { t } = useTranslation();
  const [search, setSearch] = useState("");
  const [statusFilter, setStatusFilter] = useState("all");

  const filtered = (nodes || []).filter(n => {
    const nid = n.id || n.node_id || "";
    if (search && !nid.toLowerCase().includes(search.toLowerCase())) return false;
    if (statusFilter === "online" && !n.online) return false;
    if (statusFilter === "offline" && n.online) return false;
    return true;
  });

  return (
    <>
      <div className="sb-header">{t("nodes.sidebar_title")}</div>
      <div className="ns-search-wrap">
        <input
          type="text"
          className="ns-search-input"
          placeholder={t("nodes.sidebar_search")}
          value={search}
          onChange={e => setSearch(e.target.value)}
        />
      </div>
      <div className="ns-filter-row">
        {["all", "online", "offline"].map(f => (
          <button
            key={f}
            className={`ns-filter-btn ${statusFilter === f ? "active" : ""}`}
            onClick={() => setStatusFilter(f)}
          >{t(`nodes.filter_${f}`)}</button>
        ))}
      </div>
      <div className="ns-count-text">
        {t("nodes.count_text", { n: filtered.length, s: filtered.length !== 1 ? "s" : "" })}
      </div>
      {filtered.map(node => {
        const nid = node.id || node.node_id || "";
        const isFav = myNodeIds?.has(nid);
        const gpu = getGpuShort(node);
        const svcs = node.services || [];
        return (
          <div
            key={nid}
            className={`sb-item ns-node-card${selectedId === nid ? " active" : ""}`}
            onClick={() => onSelect(nid)}
          >
            <div className="ns-node-header">
              <span className={`ns-dot ${node.online ? "online" : ""}`} />
              <span className="ns-node-id">{truncateId(nid)}</span>
              <span
                className={`ns-fav-star ${isFav ? "favorited" : ""}`}
                onClick={(e) => { e.stopPropagation(); onToggleFav?.(nid); }}
                title={isFav ? t("my_nodes.remove_from_panel") : t("nodes.add_to_my_nodes")}
              >{isFav ? "\u2605" : "\u2606"}</span>
            </div>
            {gpu && (
              <span className="ns-gpu-text">{gpu}</span>
            )}
            {svcs.length > 0 && (
              <div className="ns-svc-row">
                {svcs.slice(0, 3).map((s, i) => (
                  <span key={i} className="ns-svc-tag">{s.name || s.service || "?"}</span>
                ))}
                {svcs.length > 3 && (
                  <span className="ns-svc-more">+{svcs.length - 3}</span>
                )}
              </div>
            )}
          </div>
        );
      })}
    </>
  );
}
