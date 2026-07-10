import React from "react";
import { Handle, Position } from "@xyflow/react";

const typeConfig = {
  text:  { color: "#3fb950", icon: "\u270F", placeholder: "Enter text..." },
  image: { color: "#58a6ff", icon: "\uD83D\uDDBC", placeholder: "Image URL or file..." },
};

export default function InputNode({ data, selected }) {
  const cfg = typeConfig[data.outputType] || typeConfig.text;

  return (
    <div className={`pn-node pn-input${selected ? " selected" : ""}${data._execStatus ? " exec-" + data._execStatus : ""}`}>
      <div className="pn-header">
        <span className="pn-icon">{cfg.icon}</span>
        <span className="pn-label">{data.label}</span>
      </div>
      <div className="pn-body">
        <div className="pn-field">
          <span className="pn-field-label">type</span>
          <span className="pn-field-value" style={{ color: cfg.color }}>{data.outputType}</span>
        </div>
        {data.outputType === "text" && data.params?.value && (
          <div className="pn-field">
            <span className="pn-field-value tiny">
              {data.params.value.length > 30 ? data.params.value.slice(0, 30) + "..." : data.params.value}
            </span>
          </div>
        )}
      </div>
      <Handle
        type="source"
        position={Position.Right}
        className={`pn-handle ${data.outputType === "image" ? "pn-handle-image" : "pn-handle-text"}`}
      />
    </div>
  );
}
