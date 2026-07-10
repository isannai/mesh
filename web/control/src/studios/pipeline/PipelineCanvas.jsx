import React, { useCallback, useRef } from "react";
import {
  ReactFlow,
  Background,
  Controls,
  ControlButton,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";

import { useTranslation } from "@i18n";
import InputNode from "./nodes/InputNode";
import AINode from "./nodes/AINode";
import TransformNode from "./nodes/TransformNode";
import OutputNode from "./nodes/OutputNode";
import NodeSelectorNode from "./nodes/NodeSelectorNode";
import OptionsNode from "./nodes/OptionsNode";
import ProgressNode from "./nodes/ProgressNode";
import PollerNode from "./nodes/PollerNode";
import ChatInputNode from "./nodes/ChatInputNode";
import usePipelineStore from "./store";

const nodeTypes = {
  inputNode: InputNode,
  llmNode: AINode,
  sdNode: AINode,
  transformNode: TransformNode,
  outputNode: OutputNode,
  nodeSelectorNode: NodeSelectorNode,
  optionsNode: OptionsNode,
  progressNode: ProgressNode,
  pollerNode: PollerNode,
  chatInputNode: ChatInputNode,
};

// Types that represent an AI service invocation (LLM / SD / future TTS-STT).
// Used by edge validation to share rules across all AI entities.
const AI_NODE_TYPES = new Set(["llmNode", "sdNode", "ttsNode", "sttNode"]);

export default function PipelineCanvas() {
  const { t } = useTranslation();
  const nodes = usePipelineStore(s => s.nodes);
  const edges = usePipelineStore(s => s.edges);
  const onNodesChange = usePipelineStore(s => s.onNodesChange);
  const onEdgesChange = usePipelineStore(s => s.onEdgesChange);
  const onConnect = usePipelineStore(s => s.onConnect);
  const setSelectedNodeId = usePipelineStore(s => s.setSelectedNodeId);
  const addNode = usePipelineStore(s => s.addNode);
  const clearCanvas = usePipelineStore(s => s.clearCanvas);
  const loadTemplate = usePipelineStore(s => s.loadTemplate);
  const templates = usePipelineStore(s => s.templates);
  const reactFlowWrapper = useRef(null);

  const onNodeClick = useCallback((_event, node) => {
    setSelectedNodeId(node.id);
  }, [setSelectedNodeId]);

  const onPaneClick = useCallback(() => {
    setSelectedNodeId(null);
  }, [setSelectedNodeId]);

  // Handle drop from palette
  const onDragOver = useCallback((event) => {
    event.preventDefault();
    event.dataTransfer.dropEffect = "move";
  }, []);

  const onDrop = useCallback((event) => {
    event.preventDefault();
    const raw = event.dataTransfer.getData("application/pipeline-node");
    if (!raw) return;

    try {
      const nodeData = JSON.parse(raw);
      const bounds = reactFlowWrapper.current?.getBoundingClientRect();
      if (!bounds) return;

      const position = {
        x: event.clientX - bounds.left - 80,
        y: event.clientY - bounds.top - 20,
      };

      addNode(nodeData.type, position, nodeData.data);
    } catch { /* ignore */ }
  }, [addNode]);

  // Validate connections by type
  const isValidConnection = useCallback((connection) => {
    const sourceNode = nodes.find(n => n.id === connection.source);
    const targetNode = nodes.find(n => n.id === connection.target);
    if (!sourceNode) return true;
    const targetHandle = connection.targetHandle;
    const sourceType = sourceNode.type;
    const sourceOutputType = sourceNode.data?.outputType;

    // Config anchors — strict type
    if (targetHandle === "node") return sourceType === "nodeSelectorNode";
    if (targetHandle === "options") {
      if (sourceType !== "optionsNode") return false;
      // Options 의 service 와 AI 노드의 service 가 일치해야 함
      const optService = sourceNode.data?.service;
      const aiService = targetNode?.data?.service;
      if (optService && aiService && optService !== aiService) return false;
      return true;
    }

    // Image/mask anchors — only accept image output
    if (targetHandle === "image" || targetHandle === "mask") {
      return sourceOutputType === "image";
    }

    // Input anchor on AI node — check expected input type
    if (targetHandle === "input" && AI_NODE_TYPES.has(targetNode?.type)) {
      // LLM/SD both accept text (prompt) or json (chat messages)
      return sourceOutputType === "text" || sourceOutputType === "json";
    }

    return true;
  }, [nodes]);

  return (
    <div ref={reactFlowWrapper} className="rf-wrapper">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onConnect={onConnect}
        onNodeClick={onNodeClick}
        onPaneClick={onPaneClick}
        isValidConnection={isValidConnection}
        onDragOver={onDragOver}
        onDrop={onDrop}
        nodeTypes={nodeTypes}
        defaultEdgeOptions={{ type: "default", style: { strokeWidth: 2 } }}
        fitView
        fitViewOptions={{ padding: 0.3 }}
        deleteKeyCode="Delete"
        proOptions={{ hideAttribution: true }}
        colorMode="dark"
      >
        <Background color="var(--border-default)" gap={20} size={1} />
        <Controls position="bottom-left" className="rf-controls">
          <ControlButton onClick={() => { if (window.confirm(t("pipeline.clear_canvas_confirm"))) clearCanvas(); }} title={t("pipeline.clear_canvas")}>
            <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="2">
              <polyline points="3 6 5 6 21 6" /><path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" />
            </svg>
          </ControlButton>
          {templates.map(t => (
            <ControlButton key={t.key} onClick={() => { if (window.confirm(`Load "${t.name}" template?`)) loadTemplate(t.key); }} title={t.name}>
              <span className="rf-template-label">{t.key.toUpperCase()}</span>
            </ControlButton>
          ))}
        </Controls>
      </ReactFlow>
    </div>
  );
}
