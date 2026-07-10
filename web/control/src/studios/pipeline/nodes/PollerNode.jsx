import React from "react";
import { Handle, Position } from "@xyflow/react";
import { useTranslation } from "@i18n";

export default function PollerNode({ data, selected }) {
  const { t } = useTranslation();
  const status = data._execStatus || "idle";
  const progress = data._execProgress || 0;

  return (
    <div className={`pn-node pn-poller${selected ? " selected" : ""}${status ? " exec-" + status : ""}`}>
      <div className="pn-header pn-header-poller">
        <span className="pn-icon">&#9203;</span>
        <span className="pn-label">{data.label}</span>
      </div>
      <div className="pn-body">
        {status === "running" && (
          <div className="pn-progress-wrap">
            <div className="progress-bar-bg">
              <div className="progress-bar-fill" style={{ width: `${progress}%` }} />
            </div>
            <div className="progress-pct">{Math.round(progress)}%</div>
          </div>
        )}
        {status === "done" && (
          <div className="pn-field">
            <span className="pn-field-value pn-status-text status-done">{t("pipeline.poller_complete")}</span>
          </div>
        )}
        {status === "error" && (
          <div className="pn-field">
            <span className="pn-field-value pn-status-text status-error">{t("pipeline.poller_failed")}</span>
          </div>
        )}
        {status === "idle" && (
          <div className="pn-field">
            <span className="pn-field-value pn-status-text status-idle">{t("pipeline.poller_waiting")}</span>
          </div>
        )}
      </div>
      <Handle type="target" position={Position.Left} id="input" className="pn-handle pn-handle-in" />
      <Handle type="source" position={Position.Right} id="output" className="pn-handle pn-handle-out" />
    </div>
  );
}
