import React from "react";
import { Handle, Position } from "@xyflow/react";

const serviceLabels = {
  "sd-api": "SD",
  "llm-api": "LLM",
  "whisper": "STT",
  "tts-api": "TTS",
};

export default function OptionsNode({ data, selected }) {
  const svcLabel = serviceLabels[data.service] || data.service || "?";
  const opts = data.options || {};
  const previewKeys = Object.keys(opts).slice(0, 3);

  return (
    <div className={`pn-node pn-options${selected ? " selected" : ""}`}>
      <div className="pn-header pn-header-options">
        <span className="pn-icon">&#9881;</span>
        <span className="pn-label">{data.label}</span>
        <span className="pn-badge pn-badge-options">{svcLabel}</span>
      </div>
      <div className="pn-body">
        {previewKeys.map(key => (
          <div key={key} className="pn-field">
            <span className="pn-field-label">{key}</span>
            <span className="pn-field-value">{String(opts[key])}</span>
          </div>
        ))}
        {Object.keys(opts).length > 3 && (
          <div className="pn-field">
            <span className="pn-field-value tiny-muted">
              +{Object.keys(opts).length - 3} more
            </span>
          </div>
        )}
      </div>
      <Handle type="source" position={Position.Bottom} id="output" className="pn-handle pn-handle-options-out" />
    </div>
  );
}
