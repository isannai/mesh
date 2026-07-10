import React, { useState, useEffect, useCallback } from "react";
import { useNavigate } from "react-router-dom";
import { ReactFlowProvider } from "@xyflow/react";
import PipelineCanvas from "./PipelineCanvas";
import PipelineSideBar from "./PipelineSideBar";
import PipelineInspectorContent from "./PipelineInspector";
// ActivityBar removed — workspace cards handle navigation
import { fetchNodes, fetchMyNodes, addMyNode, deleteMyNode } from "@api/nodes";
import MyNodesPanel from "@components/MyNodesPanel";
import usePipelineStore from "./store";
import PipelineFiles from "./PipelineFiles";
import { useTranslation } from "@i18n";

function formatLogData(data) {
  if (data == null) return "—";
  if (typeof data === "string") {
    // base64 이미지
    if (data.startsWith("data:image/")) {
      const bytes = Math.round(data.length * 0.75);
      return `(image, ${bytes.toLocaleString()} bytes)`;
    }
    // URL
    if (data.startsWith("http://") || data.startsWith("https://") || data.startsWith("/node/")) {
      return data.length > 100 ? data.slice(0, 100) + "..." : data;
    }
    return data.length > 120 ? data.slice(0, 120) + "..." : data;
  }
  // 바이너리 ArrayBuffer 등
  if (data instanceof ArrayBuffer || data instanceof Blob) {
    return `(binary, ${data.size || data.byteLength} bytes)`;
  }
  // JSON object
  const str = JSON.stringify(data);
  return str.length > 120 ? str.slice(0, 120) + "..." : str;
}
import Dropdown from "@components/Dropdown/Dropdown";
import { debugRunPipeline } from "./runner";
import "./styles.scss";
import "./PipelinePage.scss";

export default function PipelinePage() {
  const { t } = useTranslation();
  const [allNodes, setAllNodes] = useState([]);
  const pipelineName = usePipelineStore(s => s.pipelineName);
  const setNetworkNodes = usePipelineStore(s => s.setNetworkNodes);
  const [myNodeIds, setMyNodeIds] = useState(new Set());
  const [myNodesDetail, setMyNodesDetail] = useState([]);
  const [inspectorOpen, setInspectorOpen] = useState(true);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [bottomH, setBottomH] = useState(180);
  const [bottomCollapsed, setBottomCollapsed] = useState(false);
  const [runMode, setRunMode] = useState("debug"); // "debug" | "release"
  const [runMenuOpen, setRunMenuOpen] = useState(false);
  const [sidebarTab, setSidebarTab] = useState("entities"); // "entities" | "files"
  const [executionLog, setExecutionLog] = useState([]);
  const [expandedLogs, setExpandedLogs] = useState({});

  const loadNodes = useCallback(async () => {
    try {
      const nodes = await fetchNodes();
      const list = (Array.isArray(nodes) ? nodes : []).map(n => {
        let hw = n.hardware || {};
        let svcs = n.services || [];
        if (typeof hw === "string") { try { hw = JSON.parse(hw); } catch { hw = {}; } }
        if (typeof svcs === "string") { try { svcs = JSON.parse(svcs); } catch { svcs = []; } }
        return { ...n, id: n.node_id || n.id, hardware: hw, services: svcs };
      });
      setAllNodes(list);
      setNetworkNodes(list);
    } catch { /* silent */ }
  }, [setNetworkNodes]);

  const loadMyNodes = useCallback(async () => {
    try {
      const result = await fetchMyNodes();
      const list = Array.isArray(result) ? result : result?.items || [];
      const parsed = list.map(n => {
        let hw = n.hardware || {};
        let svcs = n.services || [];
        if (typeof hw === "string") { try { hw = JSON.parse(hw); } catch { hw = {}; } }
        if (typeof svcs === "string") { try { svcs = JSON.parse(svcs); } catch { svcs = []; } }
        return { ...n, id: n.node_id || n.id, hardware: hw, services: svcs };
      });
      setMyNodeIds(new Set(parsed.map(n => n.id)));
      setMyNodesDetail(parsed);
    } catch { /* silent */ }
  }, []);

  useEffect(() => {
    loadNodes();
    loadMyNodes();
    const interval = setInterval(() => { loadNodes(); loadMyNodes(); }, 30000);
    return () => clearInterval(interval);
  }, [loadNodes, loadMyNodes]);

  // Enrich my nodes with allNodes data (services, hardware, online status)
  useEffect(() => {
    if (allNodes.length === 0) return;
    setMyNodesDetail(prev => prev.map(myNode => {
      // Try exact match first, then partial match (ID formats may differ)
      const myId = myNode.id || "";
      const full = allNodes.find(n => n.id === myId) ||
                   allNodes.find(n => myId.includes(n.id) || n.id.includes(myId)) ||
                   allNodes.find(n => {
                     const a = (n.owner_address || "").toLowerCase();
                     const b = (myNode.owner_address || "").toLowerCase();
                     return a && b && a === b;
                   });
      if (!full) return myNode;
      return { ...myNode, ...full, id: myNode.id, label: myNode.label || full.label };
    }));
  }, [allNodes]);

  const toggleFav = useCallback(async (nodeId) => {
    try {
      if (myNodeIds.has(nodeId)) {
        await deleteMyNode(nodeId);
      } else {
        await addMyNode(nodeId);
      }
      loadMyNodes();
    } catch { /* silent */ }
  }, [myNodeIds, loadMyNodes]);

  const navigate = useNavigate();

  const handleRun = useCallback(async () => {
    if (runMode === "debug") {
      try {
        setExecutionLog([]);
        setBottomCollapsed(false);
        const store = usePipelineStore.getState();
        const result = await debugRunPipeline(store, (stepId, update) => {
          if (stepId !== "__pipeline") {
            usePipelineStore.setState(state => ({
              nodes: state.nodes.map(n => n.id === stepId
                ? { ...n, data: { ...n.data, _execStatus: update.status, _execResult: update.result, _execProgress: update.progress } }
                : n
              ),
            }));
          }
          setExecutionLog(prev => [...prev, { stepId, ...update, ts: Date.now() }]);
        });
        if (result?.error) {
          alert("Pipeline error: " + result.error);
        }
      } catch (err) {
        alert("Run failed: " + (err.message || err));
        console.error("Debug Run error:", err);
      }
    } else {
      alert("Release (Server-side) 실행은 아직 구현 중입니다.");
    }
  }, [runMode]);

  const handleStudioChange = useCallback((id) => {
    if (id === "pipeline") return; // already here
    navigate("/");
  }, [navigate]);

  return (
    <div className="pipeline-page-wrap">
      {/* Page Header + Toolbar */}
      <div className="page-header">
        <h2 className="page-title">
          Pipeline Studio
        </h2>
        <div className="pipeline-toolbar">
          <div className="toolbar-left-group">
            <div className="toolbar-runmode">
              <Dropdown
                value={runMode}
                options={[
                  { value: "debug", label: "🔍 Debug Run" },
                  { value: "release", label: "▶ Release Run" },
                ]}
                onChange={setRunMode}
              />
            </div>
            <button className="pl-toolbar-btn pl-btn-run" onClick={handleRun}>
              &#9654; Run
            </button>
          </div>
          <button className="pl-toolbar-btn" title={t("pipeline.toolbar_export_json")} onClick={() => {
            const state = usePipelineStore.getState();
            const data = JSON.stringify({ name: state.pipelineName, nodes: state.nodes, edges: state.edges }, null, 2);
            const blob = new Blob([data], { type: "application/json" });
            const url = URL.createObjectURL(blob);
            const a = document.createElement("a"); a.href = url; a.download = (state.pipelineName || "pipeline") + ".json"; a.click();
            URL.revokeObjectURL(url);
          }}>&#128229; Export</button>
          <label className="pl-toolbar-btn import-label" title={t("pipeline.toolbar_import_json")}>
            &#128228; Import
            <input type="file" accept=".json" className="file-input-hidden" onChange={(e) => {
              const file = e.target.files?.[0];
              if (!file) return;
              const reader = new FileReader();
              reader.onload = (ev) => {
                try {
                  const data = JSON.parse(ev.target.result);
                  const store = usePipelineStore.getState();
                  if (data.nodes) store.nodes = data.nodes;
                  if (data.edges) store.edges = data.edges;
                  if (data.name) store.pipelineName = data.name;
                  usePipelineStore.setState({ nodes: data.nodes || [], edges: data.edges || [], pipelineName: data.name || "imported" });
                } catch { alert("Invalid JSON"); }
              };
              reader.readAsText(file);
              e.target.value = "";
            }} />
          </label>
        </div>
      </div>
      <div className="pipeline-page">
      {/* Side Bar + toggle tab */}
      {sidebarOpen && (
        <div className="pipeline-sidebar">
          <div className="ps-tabs">
            <div className={`ps-tab ${sidebarTab === "entities" ? "active" : ""}`} onClick={() => setSidebarTab("entities")}><span className="ps-tab-icon">&#9881;</span> Entities</div>
            <div className={`ps-tab ${sidebarTab === "files" ? "active" : ""}`} onClick={() => setSidebarTab("files")}><span className="ps-tab-icon">&#128194;</span> Files</div>
          </div>
          <div className="ps-tab-content">
            {sidebarTab === "entities" ? <PipelineSideBar /> : <PipelineFiles />}
          </div>
        </div>
      )}

      {/* Center: Canvas + Bottom Panel */}
      <div className="pipeline-center">
        <div className="pipeline-canvas-area">
          <div className="sidebar-toggle-tab" onClick={() => setSidebarOpen(o => !o)}>
            <span className="sidebar-toggle-arrow">{sidebarOpen ? "\u25C0" : "\u25B6"}</span>
          </div>
          <div className="inspector-toggle-tab" onClick={() => setInspectorOpen(o => !o)}>
            <span className="inspector-toggle-arrow">{inspectorOpen ? "\u25B6" : "\u25C0"}</span>
          </div>
          <div className="pf-canvas-filename">{pipelineName || "Untitled"}</div>
          <ReactFlowProvider>
            <PipelineCanvas />
          </ReactFlowProvider>
        </div>

        {!bottomCollapsed && (
          <>
            <div
              className="pipeline-splitter"
              onMouseDown={(e) => {
                e.preventDefault();
                const onMove = (ev) => {
                  const parent = e.target.parentElement;
                  const rect = parent.getBoundingClientRect();
                  let h = rect.bottom - ev.clientY;
                  if (h < 80) h = 80;
                  if (h > rect.height * 0.7) h = rect.height * 0.7;
                  setBottomH(h);
                };
                const onUp = () => {
                  document.removeEventListener("mousemove", onMove);
                  document.removeEventListener("mouseup", onUp);
                  document.body.style.cursor = "";
                };
                document.body.style.cursor = "ns-resize";
                document.addEventListener("mousemove", onMove);
                document.addEventListener("mouseup", onUp);
              }}
              onDoubleClick={() => setBottomCollapsed(c => !c)}
            />
            <div className="pipeline-bottom" style={{ height: bottomH }}>
              <div className="bp-left">
                <div className="bp-tabs">
                  <div className="bp-tab active">{t("pipeline.tab_output")}</div>
                  <div className="bp-tab">{t("pipeline.tab_errors")}</div>
                </div>
                <div className="bp-content">
                  {executionLog.length === 0 ? (
                    <div className="bp-empty-message">
                      No output yet. Run a pipeline to see results here.
                    </div>
                  ) : (
                    (() => {
                      const filtered = executionLog.filter(l =>
                        l.stepId !== "__pipeline" && (l.status === "done" || l.status === "error" || (l.status === "running" && l.nodeType === "pollerNode"))
                      );
                      return filtered.map((log, i) => {
                        const logKey = `${log.stepId}_${i}`;
                        const expanded = expandedLogs[logKey];
                        return (
                          <div key={i} className="log-row">
                            <div className="log-header">
                              <span className={`log-status status-${log.status}`}>
                                {log.status === "done" ? "✓" : log.status === "running" ? "●" : "✗"}
                              </span>
                              <span className="log-step-id">{log.stepId}</span>
                              <span className="log-duration">
                                {log.duration ? `${log.duration}ms` : ""}
                              </span>
                              {log.node && <span className="log-node">→ {log.node.length > 16 ? log.node.slice(0, 8) + "..." + log.node.slice(-4) : log.node}</span>}
                              <span
                                className="log-expand-btn"
                                onClick={() => setExpandedLogs(prev => ({ ...prev, [logKey]: !prev[logKey] }))}
                              >{expanded ? "▲" : "▼"}</span>
                            </div>
                            {log.inputData != null && (
                              <div className={`log-data input-data ${expanded ? "expanded" : ""}`}>
                                <span className="tag input-tag">[INPUT] </span>
                                {expanded
                                  ? (typeof log.inputData === "string" ? log.inputData : JSON.stringify(log.inputData, null, 2))
                                  : formatLogData(log.inputData)
                                }
                              </div>
                            )}
                            {log.result != null && log.nodeType !== "outputNode" && (
                              <div className={`log-data output-data ${expanded ? "expanded" : ""}`}>
                                <span className="tag output-tag">[OUTPUT] </span>
                                {expanded
                                  ? (typeof log.result === "string" ? log.result : JSON.stringify(log.result, null, 2))
                                  : formatLogData(log.result)
                                }
                              </div>
                            )}
                            {log.error && (
                              <div className="log-error">
                                <span className="error-tag">[ERROR] </span>{log.error}
                              </div>
                            )}
                          </div>
                        );
                      });
                    })()
                  )}
                </div>
              </div>
              <MyNodesPanel myNodes={myNodesDetail} onNodeClick={() => {}} onRemove={toggleFav} />
            </div>
          </>
        )}
      </div>

      {/* Inspector */}
      {inspectorOpen && (
        <div className="pipeline-inspector-content">
          <PipelineInspectorContent />
        </div>
      )}
    </div>

    </div>
  );
}
