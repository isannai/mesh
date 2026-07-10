import { create } from "zustand";

// Data type colors (matching pipeline-design.md §6.3)
export const DATA_TYPE_COLORS = {
  text:  "#3fb950",
  image: "#58a6ff",
  audio: "#bc8cff",
  video: "#f0883e",
  json:  "#d29922",
  file:  "#8b949e",
};

function edgeMarker(color) {
  return { type: "arrowclosed", width: 12, height: 12, color };
}

// Derive short prefix from node type + data (matches template ID style: sd1, llm1, poll1, ...)
function nodePrefix(type, data) {
  if (type === "llmNode") return "llm";
  if (type === "sdNode") return "sd";
  if (type === "ttsNode") return "tts";
  if (type === "sttNode") return "stt";
  if (type === "transformNode") return data?.transform || "tf";
  if (type === "pollerNode") return "poll";
  if (type === "inputNode") return "input";
  if (type === "chatInputNode") return "chat";
  if (type === "outputNode") return "out";
  if (type === "nodeSelectorNode") return "node";
  if (type === "optionsNode") return "opt";
  return type.replace(/Node$/, "").toLowerCase() || "n";
}

function generateNodeId(type, data, existingNodes) {
  const prefix = nodePrefix(type, data);
  const re = new RegExp(`^${prefix}(\\d+)$`);
  let max = 0;
  for (const n of existingNodes) {
    const m = re.exec(n.id);
    if (m) max = Math.max(max, parseInt(m[1], 10));
  }
  return `${prefix}${max + 1}`;
}

// ── Pipeline Templates ──

const TEMPLATES = {
  sd: {
    name: "SD — Image Generation",
    nodes: [
      { id: "input1", type: "inputNode", position: { x: 30, y: 220 },
        data: { label: "Text Input", outputType: "text", params: { value: "a cute cat holding a magic staff, fantasy art, 8k" } } },
      { id: "node1", type: "nodeSelectorNode", position: { x: 180, y: 30 },
        data: { label: "Fixed Node", strategy: "fixed", nodeId: "" } },
      { id: "opt1", type: "optionsNode", position: { x: 430, y: 30 },
        data: { label: "SD Options", service: "sd-api", options: { negative_prompt: "blurry, low quality", steps: 20, width: 512, height: 512, cfg_scale: 7.0, sampler: "euler_a", seed: -1 } } },
      { id: "sd1", type: "sdNode", position: { x: 300, y: 200 },
        data: { label: "SD", service: "sd-api", waitMode: "async", nodeId: null, endpoint: "/v1/images/generations", method: "POST", outputType: "image", params: {}, options: {} } },
      { id: "poll1", type: "pollerNode", position: { x: 580, y: 200 },
        data: { label: "Poller" } },
      { id: "out1", type: "outputNode", position: { x: 830, y: 200 },
        data: { label: "Image Viewer", viewer: "image", inputType: "image" } },
    ],
    edges: [
      { id: "e-input-sd", source: "input1", target: "sd1", targetHandle: "input", type: "default", style: { stroke: DATA_TYPE_COLORS.text, strokeWidth: 2 }, markerEnd: edgeMarker(DATA_TYPE_COLORS.text) },
      { id: "e-node-sd", source: "node1", target: "sd1", targetHandle: "node", type: "default", style: { stroke: "#bc8cff", strokeWidth: 2 }, markerEnd: edgeMarker("#bc8cff") },
      { id: "e-opt-sd", source: "opt1", target: "sd1", targetHandle: "options", type: "default", style: { stroke: "#d29922", strokeWidth: 2 }, markerEnd: edgeMarker("#d29922") },
      { id: "e-sd-poll", source: "sd1", sourceHandle: "output", target: "poll1", type: "default", style: { stroke: DATA_TYPE_COLORS.image, strokeWidth: 2 }, markerEnd: edgeMarker(DATA_TYPE_COLORS.image) },
      { id: "e-poll-out", source: "poll1", sourceHandle: "output", target: "out1", type: "default", style: { stroke: "#f0883e", strokeWidth: 2 }, markerEnd: edgeMarker("#f0883e") },
    ],
  },

  llm: {
    name: "LLM — Chat Completion",
    nodes: [
      { id: "chat1", type: "chatInputNode", position: { x: 30, y: 180 },
        data: { label: "Chat Input", outputType: "json", messages: [
          { role: "system", content: "You are a helpful assistant. Answer in Korean." },
          { role: "user", content: "서울 날씨 알려줘" },
        ] } },
      { id: "node1", type: "nodeSelectorNode", position: { x: 250, y: 20 },
        data: { label: "Fixed Node", strategy: "fixed", nodeId: "" } },
      { id: "opt1", type: "optionsNode", position: { x: 500, y: 20 },
        data: { label: "LLM Options", service: "llm-api", options: { temperature: 0.7, max_tokens: 2048, top_p: 0.9, top_k: 40, repeat_penalty: 1.1, frequency_penalty: 0, presence_penalty: 0, seed: -1, stop: "" } } },
      { id: "llm1", type: "llmNode", position: { x: 350, y: 180 },
        data: { label: "LLM", service: "llm-api", waitMode: "sync", nodeId: null, endpoint: "/v1/chat/completions", method: "POST", outputType: "json", params: {}, options: {} } },
      { id: "ext1", type: "transformNode", position: { x: 630, y: 180 },
        data: { label: "Extract", transform: "extract", inputType: "json", outputType: "text", params: { path: "choices[0].message.content" } } },
      { id: "out1", type: "outputNode", position: { x: 870, y: 180 },
        data: { label: "Text Viewer", viewer: "text", inputType: "text" } },
    ],
    edges: [
      { id: "e-chat-llm", source: "chat1", target: "llm1", targetHandle: "input", type: "default", style: { stroke: DATA_TYPE_COLORS.text, strokeWidth: 2 }, markerEnd: edgeMarker(DATA_TYPE_COLORS.text) },
      { id: "e-node-llm", source: "node1", target: "llm1", targetHandle: "node", type: "default", style: { stroke: "#bc8cff", strokeWidth: 2 }, markerEnd: edgeMarker("#bc8cff") },
      { id: "e-opt-llm", source: "opt1", target: "llm1", targetHandle: "options", type: "default", style: { stroke: "#d29922", strokeWidth: 2 }, markerEnd: edgeMarker("#d29922") },
      { id: "e-llm-ext", source: "llm1", sourceHandle: "output", target: "ext1", type: "default", style: { stroke: DATA_TYPE_COLORS.json, strokeWidth: 2 }, markerEnd: edgeMarker(DATA_TYPE_COLORS.json) },
      { id: "e-ext-out", source: "ext1", target: "out1", type: "default", style: { stroke: DATA_TYPE_COLORS.text, strokeWidth: 2 }, markerEnd: edgeMarker(DATA_TYPE_COLORS.text) },
    ],
  },
};

const defaultNodes = TEMPLATES.sd.nodes;
const defaultEdges = TEMPLATES.sd.edges;

const usePipelineStore = create((set, get) => ({
  // Pipeline data
  nodes: defaultNodes,
  edges: defaultEdges,
  pipelineName: "cat-with-staff",
  pipelineId: null, // IndexedDB record ID (null = unsaved)
  setPipelineId: (id) => set({ pipelineId: id }),

  // Files tab state (persisted across tab switches)
  fileExpandedFolders: {},
  setFileExpandedFolders: (v) => set({ fileExpandedFolders: typeof v === "function" ? v(get().fileExpandedFolders) : v }),

  // Selection
  selectedNodeId: null,
  selectedAnchor: null, // null = general, "options" = options form
  setSelectedNodeId: (id) => set({ selectedNodeId: id, selectedAnchor: null }),
  setSelectedAnchor: (anchor) => set({ selectedAnchor: anchor }),

  // React Flow callbacks
  onNodesChange: (changes) => {
    set((state) => ({
      nodes: applyNodeChanges(state.nodes, changes),
    }));
  },
  onEdgesChange: (changes) => {
    set((state) => ({
      edges: applyEdgeChanges(state.edges, changes),
    }));
  },
  onConnect: (connection) => {
    set((state) => {
      // Determine color from source node type
      const sourceNode = state.nodes.find(n => n.id === connection.source);
      let color;
      if (sourceNode?.type === "nodeSelectorNode") {
        color = "#bc8cff"; // 보라색
      } else if (sourceNode?.type === "optionsNode") {
        color = "#d29922"; // 노란색
      } else if (sourceNode?.type === "pollerNode") {
        color = "#f0883e"; // 주황색
      } else {
        const outType = sourceNode?.data?.outputType || "text";
        color = DATA_TYPE_COLORS[outType] || DATA_TYPE_COLORS.text;
      }
      return {
        edges: [
          ...state.edges,
          {
            ...connection,
            id: `e-${connection.source}-${connection.target}`,
            type: "default",
            animated: false,
            style: { stroke: color, strokeWidth: 2 },
            markerEnd: edgeMarker(color),
          },
        ],
      };
    });
  },

  // Add node (from palette drag-drop)
  addNode: (type, position, data) => {
    const id = generateNodeId(type, data, get().nodes);
    set((state) => ({
      nodes: [
        ...state.nodes,
        { id, type, position, data: { ...data, label: data.label || type } },
      ],
    }));
    return id;
  },

  // Delete selected node
  deleteNode: (id) => {
    set((state) => ({
      nodes: state.nodes.filter(n => n.id !== id),
      edges: state.edges.filter(e => e.source !== id && e.target !== id),
      selectedNodeId: state.selectedNodeId === id ? null : state.selectedNodeId,
    }));
  },

  // Update node data
  updateNodeData: (id, data) => {
    set((state) => ({
      nodes: state.nodes.map(n => n.id === id ? { ...n, data: { ...n.data, ...data } } : n),
    }));
  },

  // Clear all nodes and edges
  clearCanvas: () => {
    set({ nodes: [], edges: [], selectedNodeId: null });
  },

  // Load a template pipeline
  loadTemplate: (key) => {
    const tpl = TEMPLATES[key];
    if (!tpl) return;
    set({ nodes: tpl.nodes, edges: tpl.edges, selectedNodeId: null, pipelineName: tpl.name, pipelineId: null });
  },

  // Available templates
  templates: Object.entries(TEMPLATES).map(([key, tpl]) => ({ key, name: tpl.name })),

  // Network nodes (from /v1/nodes, for Node Finder dropdowns)
  networkNodes: [],
  setNetworkNodes: (nodes) => set({ networkNodes: nodes }),

  // Execution state
  execution: {
    status: "idle", // idle | running | done | error
    runId: null,
    steps: {},
    startedAt: null,
  },
  setExecution: (exec) => set({ execution: exec }),
}));

// Simple node/edge change appliers (without importing from @xyflow/react to keep store pure)
function applyNodeChanges(nodes, changes) {
  let result = [...nodes];
  for (const change of changes) {
    if (change.type === "position" && change.position) {
      result = result.map(n => n.id === change.id ? { ...n, position: change.position } : n);
    } else if (change.type === "remove") {
      result = result.filter(n => n.id !== change.id);
    } else if (change.type === "select") {
      // handled separately
    }
  }
  return result;
}

function applyEdgeChanges(edges, changes) {
  let result = [...edges];
  for (const change of changes) {
    if (change.type === "remove") {
      result = result.filter(e => e.id !== change.id);
    }
  }
  return result;
}

export default usePipelineStore;
