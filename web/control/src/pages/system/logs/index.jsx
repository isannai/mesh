import React, { useState, useEffect, useCallback, useMemo, useRef } from "react";
import { fetchMyNodes, fetchNodes } from "../../../api/nodes";
import { getAuthHeaders } from "@utils/wallet";
import { useAuth } from "../../../context/AuthContext";
import { useTranslation } from "@i18n";
import "./index.scss";

const BROKER_ID = "__broker__";
const LIVE_FILE = "__live__";
const LIVE_POLL_MS = 2000;

function truncateId(id) {
  if (!id || id.length <= 16) return id || "";
  return id.slice(0, 8) + "..." + id.slice(-4);
}

function formatSize(bytes) {
  if (!bytes) return "0 B";
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(0) + " KB";
  return (bytes / (1024 * 1024)).toFixed(1) + " MB";
}

function isServiceRunning(svc) {
  if (svc.server_loading) return false;
  return !!svc.model || svc.server_ready;
}

export default function SystemLogs() {
  const { t } = useTranslation();
  const { auth, role, isLoggedIn } = useAuth();
  const ownerAddress = (auth?.address || "").toLowerCase();
  const isAdmin = role === "owner" || role === "admin";

  const [myNodes, setMyNodes] = useState([]);   // [{id, label}]
  const [allNodes, setAllNodes] = useState([]); // RV /v1/nodes — for services info
  const [selectedNode, setSelectedNode] = useState(isAdmin ? BROKER_ID : null);
  const [files, setFiles] = useState([]);
  const [selectedFile, setSelectedFile] = useState(LIVE_FILE);
  const [lines, setLines] = useState([]);
  const [tail, setTail] = useState(200);
  const [search, setSearch] = useState("");
  const [loading, setLoading] = useState(false);
  const viewerRef = useRef(null);

  const loadMy = useCallback(async () => {
    try {
      const list = await fetchMyNodes();
      setMyNodes(Array.isArray(list) ? list : []);
    } catch {
      setMyNodes([]);
    }
  }, []);

  const loadAll = useCallback(async () => {
    try {
      const list = await fetchNodes();
      setAllNodes(Array.isArray(list) ? list : []);
    } catch {
      setAllNodes([]);
    }
  }, []);

  useEffect(() => {
    // My Nodes are gated by login — without a wallet session, the IndexedDB
    // store should not be exposed even though it's local-only data. Same
    // policy as elsewhere in the app: my-nodes management requires auth.
    if (!isLoggedIn) {
      setMyNodes([]);
      setAllNodes([]);
      return;
    }
    loadMy();
    loadAll();
    const t = setInterval(loadAll, 30000);
    return () => clearInterval(t);
  }, [isLoggedIn, loadMy, loadAll]);

  // Merge my_nodes (local store) with /v1/nodes (RV) for services + status.
  const nodes = useMemo(() => {
    const byId = {};
    for (const n of allNodes) {
      const nid = n.id || n.node_id;
      if (nid) byId[nid] = n;
    }
    return myNodes.map(mn => {
      const live = byId[mn.id] || {};
      const services = (live.services || []).filter(isServiceRunning);
      const truncated = truncateId(mn.id);
      return {
        id: mn.id,
        label: mn.label || truncated,
        addr: live.addr || "—",
        auth: !!mn.auth,
        online: !!live.online,
        services,
      };
    });
  }, [myNodes, allNodes]);

  // ── File list ────────────────────────────────────────────────────────────
  const loadFiles = useCallback(async () => {
    if (selectedNode === BROKER_ID) {
      try {
        const resp = await fetch("/v1/admin/logs/files", { headers: getAuthHeaders() });
        if (resp.ok) {
          const data = await resp.json();
          setFiles(Array.isArray(data) ? data : []);
        } else {
          setFiles([]);
        }
      } catch {
        setFiles([]);
      }
      return;
    }
    // Provider node
    try {
      const resp = await fetch(`/node/${encodeURIComponent(selectedNode)}/provider/logs`, {
        headers: getAuthHeaders(),
      });
      if (resp.ok) {
        const data = await resp.json();
        setFiles(Array.isArray(data) ? data : []);
      } else {
        setFiles([]);
      }
    } catch {
      setFiles([]);
    }
  }, [selectedNode]);

  useEffect(() => {
    if (!selectedNode) {
      setFiles([]);
      setLines([]);
      setSelectedFile(null);
      return;
    }
    setFiles([]);
    setLines([]);
    setSelectedFile(selectedNode === BROKER_ID ? LIVE_FILE : null);
    loadFiles();
  }, [selectedNode, loadFiles]);

  // Auto-select first my-node for non-admin users (broker self isn't visible).
  useEffect(() => {
    if (selectedNode || isAdmin) return;
    if (nodes.length > 0) setSelectedNode(nodes[0].id);
  }, [nodes, selectedNode, isAdmin]);

  // ── Log content ──────────────────────────────────────────────────────────
  const loadLines = useCallback(async (signal) => {
    if (!selectedFile) {
      setLines([]);
      return;
    }
    setLoading(true);
    try {
      let url;
      if (selectedNode === BROKER_ID) {
        if (selectedFile === LIVE_FILE) {
          url = `/v1/admin/logs?n=${tail}`;
        } else {
          const params = new URLSearchParams({ name: selectedFile, tail: String(tail) });
          if (search) params.set("q", search);
          url = `/v1/admin/logs/file?${params}`;
        }
      } else {
        const params = new URLSearchParams({ file: selectedFile, tail: String(tail) });
        if (search) params.set("q", search);
        url = `/node/${encodeURIComponent(selectedNode)}/provider/logs?${params}`;
      }
      const resp = await fetch(url, { headers: getAuthHeaders(), signal });
      if (signal?.aborted) return;
      if (resp.ok) {
        const data = await resp.json();
        if (signal?.aborted) return;
        const list = Array.isArray(data.lines) ? data.lines : [];
        let next = list.map(l => l.replace(/\n$/, ""));
        // Live mode: server returns RingBuffer unfiltered, do client search.
        if (selectedNode === BROKER_ID && selectedFile === LIVE_FILE && search) {
          const q = search.toLowerCase();
          next = next.filter(l => l.toLowerCase().includes(q));
        }
        setLines(next);
      } else {
        setLines([]);
      }
    } catch (e) {
      if (e?.name !== "AbortError") setLines([]);
    }
    if (!signal?.aborted) setLoading(false);
  }, [selectedNode, selectedFile, tail, search]);

  useEffect(() => {
    const ctrl = new AbortController();
    loadLines(ctrl.signal);
    let timer;
    const isLive = selectedNode === BROKER_ID && selectedFile === LIVE_FILE;
    if (isLive) {
      timer = setInterval(() => loadLines(ctrl.signal), LIVE_POLL_MS);
    }
    return () => {
      ctrl.abort();
      if (timer) clearInterval(timer);
    };
  }, [loadLines, selectedNode, selectedFile]);

  // Auto-scroll on Live
  useEffect(() => {
    const isLive = selectedNode === BROKER_ID && selectedFile === LIVE_FILE;
    if (!isLive || !viewerRef.current) return;
    viewerRef.current.scrollTop = viewerRef.current.scrollHeight;
  }, [lines, selectedNode, selectedFile]);

  const handleDownload = () => {
    const blob = new Blob([lines.join("\n")], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    const name = selectedNode === BROKER_ID
      ? `broker-${selectedFile === LIVE_FILE ? "live" : selectedFile}`
      : `${truncateId(selectedNode)}-${selectedFile}`;
    a.href = url;
    a.download = name + ".log";
    a.click();
    URL.revokeObjectURL(url);
  };

  const isLive = selectedNode === BROKER_ID && selectedFile === LIVE_FILE;
  const logFiles = files.filter(f => f.category !== "event");
  const eventFiles = files.filter(f => f.category === "event");

  return (
    <div className="page system-logs-page">
      <div className="page-header">
        <h2>Logs</h2>
      </div>
      <div className="page-body">
        <div className="logs-layout">

          {/* ── Col 1: Nodes ── */}
          <div className="col">
            <div className="col-header">
              <span>{t("logs.col_nodes")}</span>
              <span className="col-badge">{nodes.length + (isAdmin ? 1 : 0)}</span>
            </div>
            <div className="col-body">

              {isAdmin && (
                <>
                  <div className="node-section-label">{t("logs.section_self")}</div>
                  <div
                    className={`node-card broker${selectedNode === BROKER_ID ? " active" : ""}`}
                    onClick={() => setSelectedNode(BROKER_ID)}
                  >
                    <div className="node-head">
                      <span className="node-name">{t("logs.broker_name")}</span>
                      <span className="node-status online">self</span>
                    </div>
                    <div className="node-addr">{typeof window !== "undefined" ? window.location.host : "—"}</div>
                    <div className="node-services-empty">&nbsp;</div>
                  </div>
                </>
              )}

              {!isLoggedIn ? (
                <>
                  <div className="node-section-label">{t("logs.section_my_nodes")}</div>
                  <div className="empty-hint">{t("logs.empty_hint")}</div>
                </>
              ) : (
                <>
                  {nodes.length > 0 && <div className="node-section-label">{t("logs.section_my_nodes")}</div>}
                </>
              )}
              {isLoggedIn && nodes.map(n => (
                <div
                  key={n.id}
                  className={`node-card${selectedNode === n.id ? " active" : ""}`}
                  onClick={() => setSelectedNode(n.id)}
                >
                  <div className="node-head">
                    <span className="node-name">{n.label}</span>
                    {!n.auth ? (
                      <span className="node-status no-auth">no auth</span>
                    ) : !n.online ? (
                      <span className="node-status offline">offline</span>
                    ) : (
                      <span className="node-status online">online</span>
                    )}
                  </div>
                  <div className="node-addr">{n.addr}</div>
                  <div className="node-services">
                    {n.services.length === 0 ? (
                      <span className="node-services-empty">&nbsp;</span>
                    ) : (
                      <>
                        <span className="svc-chip">{n.services[0].name}</span>
                        {n.services.length > 1 && (
                          <span className="svc-chip-more">+{n.services.length - 1}</span>
                        )}
                      </>
                    )}
                  </div>
                </div>
              ))}

              {isLoggedIn && nodes.length === 0 && (
                <div className="empty-hint">No saved nodes. Add nodes from /my-nodes.</div>
              )}
            </div>
          </div>

          {/* ── Col 2: Files ── */}
          <div className="col">
            <div className="col-header">
              <span>Files</span>
              <span className="col-icon-btn" onClick={loadFiles} title="Refresh">↻</span>
            </div>
            <div className="col-body">
              {selectedNode === BROKER_ID && (
                <>
                  <div className="file-section-label">Live</div>
                  <div
                    className={`file-item${selectedFile === LIVE_FILE ? " active" : ""}`}
                    onClick={() => setSelectedFile(LIVE_FILE)}
                  >
                    <span className="file-name">RingBuffer (memory)</span>
                    <span className="file-live-dot">●</span>
                  </div>
                </>
              )}

              {logFiles.length > 0 && <div className="file-section-label">Files</div>}
              {logFiles.map(f => (
                <div
                  key={f.name}
                  className={`file-item${selectedFile === f.name ? " active" : ""}`}
                  onClick={() => setSelectedFile(f.name)}
                >
                  <span className="file-name">{f.name}</span>
                  <span className="file-size">{formatSize(f.size)}</span>
                </div>
              ))}

              {eventFiles.length > 0 && <div className="file-section-label">Events</div>}
              {eventFiles.map(f => (
                <div
                  key={f.name}
                  className={`file-item${selectedFile === f.name ? " active" : ""}`}
                  onClick={() => setSelectedFile(f.name)}
                >
                  <span className="file-name">{f.name.replace(/^events\//, "")}</span>
                  <span className="file-size">{formatSize(f.size)}</span>
                </div>
              ))}

              {selectedNode !== BROKER_ID && logFiles.length === 0 && eventFiles.length === 0 && (
                <div className="empty-hint">
                  {(() => {
                    const n = nodes.find(x => x.id === selectedNode);
                    if (!n) return "Node not found";
                    if (!n.auth) return "Authenticate this node in /my-nodes to view its logs.";
                    if (!n.online) return "Node is offline";
                    return "No log files";
                  })()}
                </div>
              )}
            </div>
          </div>

          {/* ── Col 3: Viewer ── */}
          <div className="col col-viewer">
            <div className="viewer-toolbar">
              <input
                className="toolbar-input search"
                placeholder="Search..."
                value={search}
                onChange={e => setSearch(e.target.value)}
                onKeyDown={e => e.key === "Enter" && loadLines()}
              />
              <span className="toolbar-label">Lines</span>
              <input
                className="toolbar-input tail"
                type="number"
                value={tail}
                onChange={e => setTail(Math.max(1, Math.min(2000, parseInt(e.target.value) || 200)))}
              />
              <button className="btn btn-primary" onClick={() => loadLines()} disabled={loading}>
                {loading ? "..." : "Refresh"}
              </button>
              <span className="toolbar-spacer" />
              <span className="viewer-info">
                {lines.length} lines{isLive ? " · live" : ""}
              </span>
              <button className="btn" onClick={handleDownload} disabled={lines.length === 0}>
                Download
              </button>
            </div>
            <div className="viewer-content" ref={viewerRef}>
              {!selectedNode ? (
                <div className="viewer-empty">Select a node from the left</div>
              ) : !selectedFile ? (
                <div className="viewer-empty">Select a file</div>
              ) : lines.length === 0 ? (
                <div className="viewer-empty">{loading ? "Loading..." : "No logs"}</div>
              ) : isEventFile(selectedFile) ? (
                <EventTable lines={lines} />
              ) : (
                lines.map((line, i) => <LogLine key={i} num={i + 1} text={line} />)
              )}
            </div>
          </div>

        </div>
      </div>
    </div>
  );
}

function isEventFile(name) {
  return !!name && name.endsWith(".jsonl");
}

function EventTable({ lines }) {
  const events = lines
    .map(line => {
      try { return JSON.parse(line.trim()); } catch { return null; }
    })
    .filter(Boolean);

  if (events.length === 0) {
    return <div className="viewer-empty">No events</div>;
  }

  return (
    <table className="event-table">
      <thead>
        <tr>
          <th>Time</th>
          <th>Event</th>
          <th>Service</th>
          <th>Details</th>
        </tr>
      </thead>
      <tbody>
        {events.map((evt, i) => {
          const ts = evt.ts ? new Date(evt.ts).toLocaleTimeString() : "—";
          const details = Object.entries(evt)
            .filter(([k]) => k !== "ts" && k !== "event" && k !== "service")
            .map(([k, v]) => `${k}=${typeof v === "object" ? JSON.stringify(v) : v}`)
            .join("  ");
          const name = String(evt.event || "");
          let cls = "";
          if (name.includes("error") || name.includes("fail")) cls = "event-err";
          else if (name.includes("start")) cls = "event-ok";
          else if (name.includes("stop") || name.includes("kill")) cls = "event-warn";
          return (
            <tr key={i} className={cls}>
              <td className="event-ts">{ts}</td>
              <td className="event-name">{evt.event || "—"}</td>
              <td className="event-svc">{evt.service || "—"}</td>
              <td className="event-details">{details || "—"}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function LogLine({ num, text }) {
  let cls = "";
  if (text.includes("[Error]") || text.includes("error") || text.includes("failed") || text.includes("ERROR")) {
    cls = "err";
  } else if (text.includes("warn") || text.includes("WARN")) {
    cls = "warn";
  } else if (text.includes("connected") || text.includes("registered OK") || text.includes("punch")) {
    cls = "conn";
  }
  return (
    <div className={`log-line ${cls}`}>
      <span className="num">{num}</span>
      <span className="text">{text}</span>
    </div>
  );
}
