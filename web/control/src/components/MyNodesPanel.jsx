import React from "react";
import { summarizeGpus } from "@utils/gpu";
import { useTranslation } from "@i18n";
import "./MyNodesPanel.scss";

function truncateId(id) {
  if (!id || id.length <= 16) return id || "";
  return id.slice(0, 10) + "..." + id.slice(-4);
}

function getGpuShort(node) {
  return summarizeGpus(node.hardware?.gpus);
}

export default function MyNodesPanel({ myNodes, onNodeClick, onRemove, selectedNodeId }) {
  const { t } = useTranslation();
  const online = (myNodes || []).filter(n => n.online).length;
  const total = (myNodes || []).length;

  return (
    <div className="bp-right">
      <div className="bp-right-header">
        <span>&#11088; {t("my_nodes.panel_title")}</span>
        <span className="mnp-count">
          {t("my_nodes.panel_online_count", { online, total })}
        </span>
      </div>
      <div className="bp-right-content">
        {(!myNodes || myNodes.length === 0) ? (
          <div className="mnp-empty">
            {t("my_nodes.panel_empty")}
          </div>
        ) : (
          myNodes.map(node => {
            const nid = node.id || node.node_id || "";
            const gpu = getGpuShort(node);
            const svcs = node.services || [];
            const isSelected = selectedNodeId && (selectedNodeId === nid || nid.includes(selectedNodeId) || selectedNodeId.includes(nid));
            return (
              <div
                key={nid}
                className={`mnp-item${isSelected ? " selected" : ""}`}
                onClick={() => onNodeClick?.(nid)}
              >
                <span className={`mnp-dot${node.online ? " online" : ""}`} />
                <div className="mnp-body">
                  <div className="mnp-head-row">
                    <span className={`mnp-label${node.online ? " online" : ""}`}>
                      {node.label || truncateId(nid)}
                    </span>
                    <span
                      className="mnp-remove"
                      onClick={(e) => { e.stopPropagation(); onRemove?.(nid); }}
                      title={t("my_nodes.remove_from_panel")}
                    >&times;</span>
                  </div>
                  {gpu && (
                    <div className="mnp-gpu">{gpu}</div>
                  )}
                  {svcs.length > 0 && (
                    <div className="mnp-svc-row">
                      {svcs.slice(0, 3).map((s, i) => (
                        <span key={i} className="mnp-svc-tag">{s.name || s.service || "?"}</span>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            );
          })
        )}
      </div>
      <div className="mnp-add-wrap">
        <button className="mnp-add-btn">+ Add Node</button>
      </div>
    </div>
  );
}
