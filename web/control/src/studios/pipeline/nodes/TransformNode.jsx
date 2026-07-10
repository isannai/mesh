import React from "react";
import { Handle, Position } from "@xyflow/react";

export default function TransformNode({ data, selected }) {
  return (
    <div className={`pn-node pn-transform${selected ? " selected" : ""}${data._execStatus ? " exec-" + data._execStatus : ""}`}>
      <div className="pn-header pn-header-transform">
        <span className="pn-icon">&#9881;</span>
        <span className="pn-label">{data.label}</span>
        <span className="pn-badge">{data.transform}</span>
      </div>
      <div className="pn-body">
        {data.params?.path && (
          <div className="pn-field">
            <span className="pn-field-label">path</span>
            <span className="pn-field-value mono">
              {data.params.path}
            </span>
          </div>
        )}
        {data.params?.template && (
          <div className="pn-field">
            <span className="pn-field-label">template</span>
            <span className="pn-field-value">{data.params.template}</span>
          </div>
        )}
        <div className="pn-field">
          <span className="pn-field-label">type</span>
          <span className="pn-field-value">{data.inputType} → {data.outputType}</span>
        </div>
      </div>
      <Handle type="target" position={Position.Left} className="pn-handle pn-handle-in" />
      <Handle type="source" position={Position.Right} className="pn-handle pn-handle-out" />
    </div>
  );
}
