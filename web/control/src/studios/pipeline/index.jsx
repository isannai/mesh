import React from "react";
import { ReactFlowProvider } from "@xyflow/react";
import PipelineCanvas from "./PipelineCanvas";
import PipelineSideBar from "./PipelineSideBar";
import PipelineInspector from "./PipelineInspector";
import "./styles.scss";

export default function PipelineStudio() {
  return (
    <ReactFlowProvider>
      <PipelineCanvas />
    </ReactFlowProvider>
  );
}

export { PipelineSideBar };
export { default as PipelineInspectorComponent } from "./PipelineInspector";
