import React, { useState, useEffect, useCallback } from "react";
import { getAuthHeaders } from "@utils/wallet";
import { useTranslation } from "@i18n";
import "./LogsTab.scss";

export default function LogsTab({ nodeId }) {
  const { t } = useTranslation();
  const [files, setFiles] = useState([]);
  const [selectedFile, setSelectedFile] = useState(null);
  const [lines, setLines] = useState([]);
  const [tail, setTail] = useState(100);
  const [search, setSearch] = useState("");
  const [loading, setLoading] = useState(false);

  // Reset on node change
  useEffect(() => {
    setFiles([]);
    setSelectedFile(null);
    setLines([]);
  }, [nodeId]);

  // Load file list. Uses an AbortController scoped per (nodeId) so that
  // switching nodes mid-flight discards the in-flight response — otherwise
  // a late reply from the previous node would set selectedFile to a file
  // that doesn't exist on the newly-selected node.
  const loadFiles = useCallback(async (signal) => {
    if (!nodeId) return;
    try {
      const resp = await fetch(
        `/node/${encodeURIComponent(nodeId)}/provider/logs`,
        { headers: getAuthHeaders(), signal }
      );
      if (signal?.aborted) return;
      if (resp.ok) {
        const data = await resp.json();
        if (signal?.aborted) return;
        const list = Array.isArray(data) ? data : [];
        setFiles(list);
        setSelectedFile(prev => {
          if (prev && list.some(f => f.name === prev)) return prev;
          if (list.length === 0) return null;
          const firstLog = list.find(f => f.category === "log") || list[0];
          return firstLog.name;
        });
      } else {
        setFiles([]);
        setSelectedFile(null);
      }
    } catch (e) {
      if (e?.name === "AbortError") return;
      setFiles([]);
      setSelectedFile(null);
    }
  }, [nodeId]);

  useEffect(() => {
    const ctrl = new AbortController();
    loadFiles(ctrl.signal);
    return () => ctrl.abort();
  }, [loadFiles]);

  // Load log content. 404 = file vanished between listing and tail; in
  // that case we refetch the listing so the sidebar snaps to a valid file.
  const loadLogs = useCallback(async (signal) => {
    if (!nodeId || !selectedFile) return;
    // Only tail files that are in the CURRENT listing. Guards against a
    // stale selectedFile lingering from a previous node — without this,
    // we would fetch e.g. installer.log from a node that doesn't have it
    // because state updates from the nodeId-reset effect hadn't committed
    // yet when this effect fired.
    if (files.length > 0 && !files.some(f => f.name === selectedFile)) return;
    setLoading(true);
    try {
      const params = new URLSearchParams({ file: selectedFile, tail });
      if (search) params.set("q", search);
      const resp = await fetch(
        `/node/${encodeURIComponent(nodeId)}/provider/logs?${params}`,
        { headers: getAuthHeaders(), signal }
      );
      if (signal?.aborted) return;
      if (resp.ok) {
        const data = await resp.json();
        if (signal?.aborted) return;
        setLines(data.lines || []);
      } else if (resp.status === 404) {
        setLines([]);
        loadFiles();
      } else {
        setLines([]);
      }
    } catch (e) {
      if (e?.name === "AbortError") return;
      setLines([]);
    }
    if (!signal?.aborted) setLoading(false);
  }, [nodeId, selectedFile, tail, search, loadFiles, files]);

  useEffect(() => {
    const ctrl = new AbortController();
    loadLogs(ctrl.signal);
    return () => ctrl.abort();
  }, [loadLogs]);

  const handleDownload = () => {
    const blob = new Blob([lines.join("")], { type: "text/plain" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = selectedFile || "log.txt";
    a.click();
    URL.revokeObjectURL(url);
  };

  const formatSize = (bytes) => {
    if (bytes < 1024) return bytes + " B";
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(0) + " KB";
    return (bytes / (1024 * 1024)).toFixed(1) + " MB";
  };

  const logFiles = files.filter(f => f.category === "log");
  const eventFiles = files.filter(f => f.category === "event");

  return (
    <div className="logs-tab">
      <div className="logs-layout">
        {/* File List */}
        <div className="logs-filelist">
          {logFiles.length > 0 && (
            <>
              <div className="logs-filelist-title">Logs</div>
              {logFiles.map(f => (
                <div
                  key={f.name}
                  className={`logs-file ${selectedFile === f.name ? "active" : ""}`}
                  onClick={() => setSelectedFile(f.name)}
                >
                  <span className="logs-file-name">{f.name}</span>
                  <span className="logs-file-size">{formatSize(f.size)}</span>
                </div>
              ))}
            </>
          )}
          {eventFiles.length > 0 && (
            <>
              <div className="logs-filelist-title">{t("my_nodes.logs_events")}</div>
              {eventFiles.map(f => (
                <div
                  key={f.name}
                  className={`logs-file ${selectedFile === f.name ? "active" : ""}`}
                  onClick={() => setSelectedFile(f.name)}
                >
                  <span className="logs-file-name">{f.name.replace("events/", "")}</span>
                  <span className="logs-file-size">{formatSize(f.size)}</span>
                </div>
              ))}
            </>
          )}
          {files.length === 0 && (
            <div className="logs-filelist-empty">{t("my_nodes.logs_no_files")}</div>
          )}
        </div>

        {/* Main viewer */}
        <div className="logs-main">
          <div className="logs-toolbar">
            <input
              className="logs-search"
              type="text"
              placeholder={t("my_nodes.logs_search")}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              onKeyDown={(e) => e.key === "Enter" && loadLogs()}
            />
            <span className="logs-tail-label">{t("my_nodes.logs_lines")}</span>
            <input
              className="logs-tail-input"
              type="number"
              value={tail}
              onChange={(e) => setTail(Math.max(1, Math.min(1000, parseInt(e.target.value) || 100)))}
            />
            <button className="btn btn-primary" onClick={loadLogs} disabled={loading}>
              {loading ? "..." : "Refresh"}
            </button>
            <button className="btn" onClick={handleDownload} disabled={lines.length === 0}>
              Download
            </button>
          </div>

          <div className="logs-viewer">
            <div className="logs-viewer-header">
              <span className="logs-viewer-title">{selectedFile || "No file selected"}</span>
              <span className="logs-viewer-info">{lines.length} lines</span>
            </div>
            <div className="logs-content">
              {!selectedFile ? (
                <div className="logs-empty">{t("my_nodes.logs_select_file")}</div>
              ) : lines.length === 0 ? (
                <div className="logs-empty">{loading ? "Loading..." : "No logs available"}</div>
              ) : isEventFile(selectedFile) ? (
                <EventTable lines={lines} />
              ) : (
                lines.map((line, i) => (
                  <LogLine key={i} num={i + 1} text={line} />
                ))
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

function isEventFile(name) {
  return name && name.endsWith(".jsonl");
}

function EventTable({ lines }) {
  const events = lines.map(line => {
    try { return JSON.parse(line.trim()); } catch { return null; }
  }).filter(Boolean);

  if (events.length === 0) return <div className="logs-empty">No events</div>;

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
          const ts = evt.ts ? new Date(evt.ts).toLocaleTimeString() : "-";
          const details = Object.entries(evt)
            .filter(([k]) => k !== "ts" && k !== "event" && k !== "service")
            .map(([k, v]) => `${k}=${typeof v === "object" ? JSON.stringify(v) : v}`)
            .join("  ");
          const evtClass = (evt.event || "").includes("error") || (evt.event || "").includes("fail") ? "event-err"
            : (evt.event || "").includes("start") ? "event-ok"
            : (evt.event || "").includes("stop") || (evt.event || "").includes("kill") ? "event-warn"
            : "";
          return (
            <tr key={i} className={evtClass}>
              <td className="event-ts">{ts}</td>
              <td className="event-name">{evt.event || "-"}</td>
              <td className="event-svc">{evt.service || "-"}</td>
              <td className="event-details">{details || "-"}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}

function LogLine({ num, text }) {
  let tagClass = "";
  if (text.includes("[signal") || text.includes("error") || text.includes("failed") || text.includes("ERROR")) {
    tagClass = "log-err";
  } else if (text.includes("warn") || text.includes("WARN")) {
    tagClass = "log-warn";
  } else if (text.includes("connected") || text.includes("registered OK") || text.includes("punch")) {
    tagClass = "log-conn";
  }

  return (
    <div className={`log-line ${tagClass}`}>
      <span className="log-line-num">{num}</span>
      <span className="log-text">{text}</span>
    </div>
  );
}
