import React from "react";
import { Handle, Position } from "@xyflow/react";

export default function NodeSelectorNode({ data, selected }) {
  const isFixed = data.strategy === "fixed";

  return (
    <div className={`pn-node pn-node-selector${selected ? " selected" : ""}`}>
      <div className="pn-header pn-header-selector">
        <span className="pn-icon">{isFixed ? "\uD83D\uDCCC" : "\uD83D\uDD0D"}</span>
        <span className="pn-label">{data.label}</span>
      </div>
      <div className="pn-body">
        {isFixed ? (
          <>
            <div className="pn-field">
              <span className="pn-field-label">node</span>
              <span className="pn-field-value tiny">
                {data.nodeId ? (data.nodeId.length > 16 ? data.nodeId.slice(0, 10) + "..." + data.nodeId.slice(-4) : data.nodeId) : "—"}
              </span>
            </div>
          </>
        ) : (
          <>
            {data.service && (
              <div className="pn-field">
                <span className="pn-field-label">service</span>
                <span className="pn-field-value">{data.service}</span>
              </div>
            )}
            {data.model && (
              <div className="pn-field">
                <span className="pn-field-label">model</span>
                <span className="pn-field-value">{data.model}</span>
              </div>
            )}
            {data.gpu && (
              <div className="pn-field">
                <span className="pn-field-label">gpu</span>
                <span className="pn-field-value">{data.gpu}</span>
              </div>
            )}
            {!data.service && !data.model && !data.gpu && (
              <div className="pn-field">
                <span className="pn-field-value tiny-muted">any online node</span>
              </div>
            )}
          </>
        )}
      </div>
      <Handle type="source" position={Position.Bottom} id="output" className="pn-handle pn-handle-node-selector" />
    </div>
  );
}
