import React from "react";
import { Handle, Position } from "@xyflow/react";

export default function ProgressNode({ data, selected }) {
  const percent = data.progress || 0;
  const status = data.status || "idle";

  return (
    <div className={`pn-node pn-progress${selected ? " selected" : ""}`}>
      <div className="pn-header pn-header-progress">
        <span className="pn-icon">&#9201;</span>
        <span className="pn-label">{data.label}</span>
        <span className={`pn-status-badge status-${status}`}>{status}</span>
      </div>
      <div className="pn-body">
        <div className="pn-progress-large">
          <div className={`progress-fill ${status === "done" ? "done" : status === "error" ? "error" : ""}`} style={{ width: `${percent}%` }} />
        </div>
        <div className="pn-progress-caption">
          {status === "done" ? "Complete" : status === "error" ? "Failed" : `${Math.round(percent)}%`}
        </div>
      </div>
      <Handle type="target" position={Position.Left} id="input" className="pn-handle pn-handle-in" />
      <Handle type="source" position={Position.Right} id="output" className="pn-handle pn-handle-out" />
    </div>
  );
}
