import React from "react";
import { Handle, Position } from "@xyflow/react";
import { useTranslation } from "@i18n";

export default function OutputNode({ data, selected }) {
  const { t } = useTranslation();
  const viewer = data.viewer || "auto";
  const result = data._execResult || data.result || null;

  const viewerWidth = data.viewerWidth || (viewer === "image" ? 180 : 160);
  const viewerHeight = data.viewerHeight || (viewer === "image" ? 100 : 40);

  return (
    <div className={`pn-node pn-output${selected ? " selected" : ""}${data._execStatus ? " exec-" + data._execStatus : ""}`} style={{ minWidth: viewerWidth }}>
      <div className="pn-header">
        <span className="pn-icon">{viewer === "image" ? "\uD83D\uDDBC" : viewer === "audio" ? "\uD83D\uDD0A" : "\u25A0"}</span>
        <span className="pn-label">{data.label}</span>
      </div>
      <div className="pn-body">
        {/* Image Viewer */}
        {viewer === "image" && (
          <div className="pn-viewer-frame image-frame" style={{ height: viewerHeight }}>
            {result ? (
              <img src={result} alt="output" />
            ) : (
              <span className="viewer-empty">{t("pipeline.output_no_image")}</span>
            )}
          </div>
        )}

        {/* Text Viewer */}
        {viewer === "text" && (
          <div className="pn-viewer-frame text-frame" style={{ minHeight: viewerHeight, maxHeight: viewerHeight * 2 }}>
            {result ? (typeof result === "string" ? result : JSON.stringify(result, null, 2)) : "No output yet"}
          </div>
        )}

        {/* Audio Viewer */}
        {viewer === "audio" && (
          <div className="pn-viewer-frame audio-frame">
            {result ? (
              <audio controls src={result} />
            ) : (
              <span className="viewer-empty">{t("pipeline.output_no_audio")}</span>
            )}
          </div>
        )}

        {/* Auto / unknown */}
        {viewer !== "image" && viewer !== "text" && viewer !== "audio" && (
          <div className="pn-field">
            <span className="pn-field-label">viewer</span>
            <span className="pn-field-value">{viewer}</span>
          </div>
        )}
      </div>
      <Handle type="target" position={Position.Left} id="input" className="pn-handle pn-handle-in" />
    </div>
  );
}
