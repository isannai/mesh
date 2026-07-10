// Pipeline export/import — local file download/upload helpers.
// File formats:
//   *.iann.json         → single pipeline (format: "iann-pipeline-v1")
//   *.iann-folder.json  → folder with multiple pipelines (format: "iann-folder-v1")
//
// Exported files are directly compatible with `POST /v1/pipeline/execute`
// because Go's json.Unmarshal silently ignores unknown fields (format, name,
// exportedAt, exportedBy). Runtime-dependent data (authHeaders, networkNodes)
// is intentionally excluded — the caller fills those in at execution time.

import { savePipeline, createFolder, getPipeline } from "./pipelineDB";

const FORMAT_PIPELINE = "iann-pipeline-v1";
const FORMAT_FOLDER = "iann-folder-v1";

const KNOWN_NODE_TYPES = [
  "inputNode", "outputNode", "chatInputNode", "optionsNode",
  "transformNode", "nodeSelectorNode", "llmNode", "sdNode",
  "pollerNode", "progressNode",
];

// --- Export builders ---

export function buildPipelineExport(pipeline, exportedBy) {
  const out = {
    format: FORMAT_PIPELINE,
    name: pipeline.name || "Untitled",
    exportedAt: new Date().toISOString(),
    nodes: pipeline.nodes || [],
    edges: pipeline.edges || [],
  };
  if (exportedBy) out.exportedBy = exportedBy;
  return out;
}

export function buildFolderExport(folderName, pipelines, exportedBy) {
  const out = {
    format: FORMAT_FOLDER,
    name: folderName || "Folder",
    exportedAt: new Date().toISOString(),
    pipelines: pipelines.map(p => ({
      name: p.name || "Untitled",
      nodes: p.nodes || [],
      edges: p.edges || [],
    })),
  };
  if (exportedBy) out.exportedBy = exportedBy;
  return out;
}

// --- Security (sign credential handling) ---

// Returns array of nodeSelector nodes that carry a sign credential
// (data.auth.signature + data.auth.message). Empty array means safe to export.
export function detectSignatures(graph) {
  if (!graph || !Array.isArray(graph.nodes)) return [];
  return graph.nodes.filter(n =>
    n.type === "nodeSelectorNode" &&
    n.data?.auth?.signature &&
    n.data?.auth?.message
  );
}

// Returns a graph copy with all nodeSelector data.auth fields removed.
// Original graph is not mutated.
export function stripSignatures(graph) {
  if (!graph || !Array.isArray(graph.nodes)) return graph;
  return {
    ...graph,
    nodes: graph.nodes.map(n => {
      if (n.type !== "nodeSelectorNode" || !n.data?.auth) return n;
      const { auth: _drop, ...rest } = n.data;
      return { ...n, data: rest };
    }),
  };
}

// --- Browser file I/O ---

export function downloadJSON(filename, data) {
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

export function pickFile() {
  return new Promise(resolve => {
    const input = document.createElement("input");
    input.type = "file";
    input.accept = ".json,application/json";
    input.onchange = () => resolve(input.files?.[0] || null);
    input.click();
  });
}

// --- Parse + validate ---

export function parseImportFile(text) {
  let data;
  try {
    data = JSON.parse(text);
  } catch (e) {
    throw new Error("Invalid JSON: " + e.message);
  }
  if (!data || typeof data !== "object" || Array.isArray(data)) {
    throw new Error("Invalid file: not a JSON object");
  }
  return data;
}

export function validateImport(data) {
  const warnings = [];
  if (!data.format) throw new Error("Invalid file: 'format' field missing");
  if (data.format !== FORMAT_PIPELINE && data.format !== FORMAT_FOLDER) {
    throw new Error(`Unsupported format: ${data.format}`);
  }
  if (data.format === FORMAT_PIPELINE) {
    validateGraph(data, warnings, "");
  } else {
    if (!Array.isArray(data.pipelines)) throw new Error("'pipelines' is not an array");
    data.pipelines.forEach((p, i) => validateGraph(p, warnings, `pipelines[${i}].`));
  }
  return { warnings };
}

function validateGraph(p, warnings, prefix) {
  if (!Array.isArray(p.nodes)) throw new Error(`${prefix}nodes is not an array`);
  if (!Array.isArray(p.edges)) throw new Error(`${prefix}edges is not an array`);
  const unknown = [...new Set(p.nodes.map(n => n.type).filter(t => t && !KNOWN_NODE_TYPES.includes(t)))];
  if (unknown.length) warnings.push(`${prefix || "Pipeline: "}unknown node types: ${unknown.join(", ")}`);
}

// --- IndexedDB persist ---

export async function importPipeline(parsed, folderId = "") {
  return await savePipeline({
    name: parsed.name || "Imported",
    folder: folderId,
    nodes: parsed.nodes || [],
    edges: parsed.edges || [],
  });
}

export async function importFolder(parsed) {
  const folderId = await createFolder(parsed.name || "Imported Folder");
  const pipelineIds = [];
  for (const p of parsed.pipelines) {
    const id = await savePipeline({
      name: p.name || "Imported",
      folder: String(folderId),
      nodes: p.nodes || [],
      edges: p.edges || [],
    });
    pipelineIds.push(id);
  }
  return { folderId, pipelineIds };
}

// Loads full graph data for every pipeline in a folder — folder export needs
// the heavy nodes/edges fields that listPipelines() omits.
export async function loadFolderPipelines(pipelineIds) {
  const out = [];
  for (const id of pipelineIds) {
    const data = await getPipeline(id);
    if (data) out.push(data);
  }
  return out;
}

// --- Filename helpers ---

export function safeFilename(name) {
  return (name || "").replace(/[/\\?%*:|"<>]/g, "_").trim() || "untitled";
}

export function pipelineFilename(name) {
  return `${safeFilename(name)}.iann.json`;
}

export function folderFilename(name) {
  return `${safeFilename(name)}.iann-folder.json`;
}

export { FORMAT_PIPELINE, FORMAT_FOLDER };
