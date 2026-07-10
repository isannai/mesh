import React, { useCallback } from "react";
import { Handle, Position } from "@xyflow/react";
import usePipelineStore from "../store";

const serviceIcons = {
  "llm-api": "&#129302;",
  "sd-api": "&#127912;",
  "whisper": "&#127908;",
  "tts-api": "&#128266;",
};

export default function AINode({ id, data, selected }) {
  const setSelectedNodeId = usePipelineStore(s => s.setSelectedNodeId);
  const setSelectedAnchor = usePipelineStore(s => s.setSelectedAnchor);
  const selectedAnchor = usePipelineStore(s => s.selectedAnchor);
  const selectedNodeId = usePipelineStore(s => s.selectedNodeId);

  const icon = serviceIcons[data.service] || "&#9881;";
  const isOptionsActive = selected && selectedNodeId === id && selectedAnchor === "options";

  const handleOptionsClick = useCallback((e) => {
    e.stopPropagation();
    setSelectedNodeId(id);
    setSelectedAnchor("options");
  }, [id, setSelectedNodeId, setSelectedAnchor]);

  return (
    <div className={`pn-node pn-ai${selected ? " selected" : ""}${data._execStatus ? " exec-" + data._execStatus : ""}`} data-service={data.service}>
      {/* Top labels for anchors */}
      <div className="pn-top-anchors">
        <span className="pn-anchor-label pn-anchor-node">node</span>
        <span
          className={`pn-anchor-label pn-anchor-options${isOptionsActive ? " active" : ""}`}
          onClick={handleOptionsClick}
          title="Click to edit options"
        >options</span>
      </div>

      {/* Top Handle — Node Selector (left 30%) */}
      <Handle type="target" position={Position.Top} id="node" className="pn-handle pn-handle-node-selector pn-handle-left-30" />
      {/* Top Handle — Options (right 70%) */}
      <Handle type="target" position={Position.Top} id="options" className="pn-handle pn-handle-options pn-handle-left-70" />

      <div className="pn-header pn-header-ai">
        <span className="pn-icon" dangerouslySetInnerHTML={{ __html: icon }} />
        <span className="pn-label">{data.label}</span>
        <span className="pn-badge">{data.service}</span>
      </div>
      <div className="pn-body">
        {data.params?.model && (
          <div className="pn-field">
            <span className="pn-field-label">model</span>
            <span className="pn-field-value">{data.params.model}</span>
          </div>
        )}
        {data.endpoint && (
          <div className="pn-field">
            <span className="pn-field-label">endpoint</span>
            <span className="pn-field-value mono">
              {data.endpoint}
            </span>
          </div>
        )}
        {data.service === "sd-api" && (
          <div className="pn-field">
            <span className="pn-field-label">mode</span>
            <span className="pn-field-value muted">auto</span>
          </div>
        )}
        <div className="pn-field">
          <span className="pn-field-label">node</span>
          <span className={`pn-field-value${data.nodeId ? "" : " auto-placeholder"}`}>
            {data.nodeId || "auto"}
          </span>
        </div>
      </div>
      {/* Left Handles — SD: 3개 (prompt/image/mask), 그 외: 1개 */}
      {data.service === "sd-api" ? (
        <>
          <Handle type="target" position={Position.Left} id="input" className="pn-handle pn-handle-text pn-handle-top-25" />
          <Handle type="target" position={Position.Left} id="image" className="pn-handle pn-handle-image pn-handle-top-50" />
          <Handle type="target" position={Position.Left} id="mask" className="pn-handle pn-handle-image pn-handle-top-75" />
          <div className="pn-left-labels-outer">
            <span className="label-t25">prompt</span>
            <span className="label-t50">image</span>
            <span className="label-t75">mask</span>
          </div>
        </>
      ) : (
        <Handle type="target" position={Position.Left} id="input" className="pn-handle pn-handle-in" />
      )}
      {/* Right Handle — Data output */}
      <Handle type="source" position={Position.Right} id="output" className="pn-handle pn-handle-out" />
    </div>
  );
}
