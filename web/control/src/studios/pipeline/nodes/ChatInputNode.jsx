import React from "react";
import { Handle, Position } from "@xyflow/react";

export default function ChatInputNode({ data, selected }) {
  const messages = data.messages || [];
  const preview = messages.slice(0, 3).map(m => `${m.role}: ${(m.content || "").slice(0, 25)}`).join("\n");
  const more = messages.length > 3 ? `\n+${messages.length - 3} more` : "";

  return (
    <div className={`pn-node pn-input${selected ? " selected" : ""}${data._execStatus ? " exec-" + data._execStatus : ""}`}>
      <div className="pn-header">
        <span className="pn-icon">&#128172;</span>
        <span className="pn-label">{data.label}</span>
      </div>
      <div className="pn-body">
        <div className="pn-field">
          <span className="pn-field-label">messages</span>
          <span className="pn-field-value count-badge">{messages.length}</span>
        </div>
        {messages.length > 0 && (
          <div className="pn-field">
            <span className="pn-field-value tiny-preformat">
              {(preview + more).slice(0, 80)}
            </span>
          </div>
        )}
      </div>
      <Handle
        type="source"
        position={Position.Right}
        className="pn-handle pn-handle-json"
      />
    </div>
  );
}
