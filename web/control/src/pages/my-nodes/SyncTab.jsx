import React, { useState, useRef, useEffect } from "react";
import { getAuthHeaders } from "@utils/wallet";
import Dropdown from "@components/Dropdown/Dropdown";
import "./SyncTab.scss";

export default function SyncTab({ node, t }) {
  const [status, setStatus] = useState("loading"); // loading | idle | creating | done | error
  const [token, setToken] = useState("");
  const [snapshot, setSnapshot] = useState(null);
  const [error, setError] = useState("");
  const [copied, setCopied] = useState(false);
  const [copiedCmd, setCopiedCmd] = useState(false);
  const [ttlHours, setTtlHours] = useState(1);
  const [hashProgress, setHashProgress] = useState({ progress: 0, total: 0, current_file: "" });
  const pollRef = useRef(null);

  const nodeId = node?.id || "";
  const prefix = `/node/${encodeURIComponent(nodeId)}/provider`;

  const stopPolling = () => {
    if (pollRef.current) {
      clearInterval(pollRef.current);
      pollRef.current = null;
    }
  };

  const [peerInfo, setPeerInfo] = useState({ node_id: "", rendezvous_addr: "" });
  const [brokerRdv, setBrokerRdv] = useState("");

  // Broker's own rendezvous address — used as fallback when the master
  // provider hasn't exposed its rendezvous_addr in /sync/status.
  // /info is a public endpoint (no auth required) that returns the
  // broker's configured rendezvous.
  useEffect(() => {
    (async () => {
      try {
        const resp = await fetch("/info");
        if (!resp.ok) return;
        const data = await resp.json();
        const raw = data.rendezvous || data.rendezvous_addr || "";
        if (raw) {
          // Strip scheme if present (e.g. "https://host:port" → "host:port")
          const stripped = raw.replace(/^https?:\/\//, "").replace(/\/$/, "");
          setBrokerRdv(stripped);
        }
      } catch {}
    })();
  }, []);

  const applyStatus = (data) => {
    if (data.node_id || data.rendezvous_addr) {
      setPeerInfo({
        node_id: data.node_id || "",
        rendezvous_addr: data.rendezvous_addr || "",
      });
    }
    if (data.status === "done") {
      setStatus("done");
      setToken(data.token || "");
      setSnapshot({
        files_count: data.files_count,
        total_size: data.total_size,
        expires_at: data.expires_at,
        created_at: data.created_at,
      });
    } else if (data.status === "error") {
      setStatus("error");
      setError(data.error || "Unknown error");
    } else if (data.status === "creating") {
      setStatus("creating");
      setHashProgress({ progress: data.progress || 0, total: data.total || 0, current_file: data.current_file || "" });
    } else {
      setStatus("idle");
    }
  };

  // Load existing snapshot on mount
  useEffect(() => {
    if (!nodeId) return;
    (async () => {
      try {
        const resp = await fetch(`${prefix}/sync/status`, { headers: getAuthHeaders() });
        const text = await resp.text();
        try {
          const data = JSON.parse(text);
          applyStatus(data);
          if (data.status === "creating") pollStatus();
        } catch {
          setStatus("idle");
        }
      } catch {
        setStatus("idle");
      }
    })();
    return () => stopPolling();
  }, [nodeId]);

  const pollStatus = () => {
    stopPolling();
    pollRef.current = setInterval(async () => {
      try {
        const resp = await fetch(`${prefix}/sync/status`, { headers: getAuthHeaders() });
        const text = await resp.text();
        let data;
        try { data = JSON.parse(text); } catch { return; }

        if (data.status === "done") {
          stopPolling();
          applyStatus(data);
        } else if (data.status === "error") {
          stopPolling();
          applyStatus(data);
        } else if (data.status === "creating") {
          setHashProgress({ progress: data.progress || 0, total: data.total || 0, current_file: data.current_file || "" });
        }
      } catch {}
    }, 2000);
  };

  const handleCreate = async () => {
    setStatus("creating");
    setError("");
    setToken("");
    setSnapshot(null);

    try {
      const resp = await fetch(`${prefix}/sync/create`, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...getAuthHeaders() },
        body: JSON.stringify({ ttl_hours: ttlHours }),
      });
      const text = await resp.text();
      let data;
      try { data = JSON.parse(text); } catch {
        setStatus("error");
        setError(`Invalid response: ${text.substring(0, 100)}`);
        return;
      }

      if (!resp.ok) {
        setStatus("error");
        setError(data.errors ? data.errors.join("\n") : (data.error || `HTTP ${resp.status}`));
        return;
      }

      pollStatus();
    } catch (e) {
      setStatus("error");
      setError(e.message);
    }
  };

  const handleCopy = () => {
    if (!token) return;
    navigator.clipboard.writeText(token).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  };

  const formatSize = (bytes) => {
    if (!bytes || bytes === 0) return "0 B";
    if (bytes < 1024) return bytes + " B";
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
    if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + " MB";
    return (bytes / (1024 * 1024 * 1024)).toFixed(2) + " GB";
  };

  const formatTime = (iso) => {
    if (!iso) return "";
    try { return new Date(iso).toLocaleString(); } catch { return iso; }
  };

  const creating = status === "creating";
  const loading = status === "loading";

  return (
    <div className="sync-tab">
      <h3 className="sync-title">
        Sync Snapshot
      </h3>
      <p className="sync-desc">
        Create a snapshot of this node's files. Other nodes can use the generated token to sync their environment.
        All services must be stopped before creating a snapshot.
      </p>

      {/* Create button + TTL */}
      <div className="sync-actions">
        <button
          className="btn-create-snapshot"
          onClick={handleCreate}
          disabled={creating || loading}
        >
          {loading ? "Loading..." : creating ? "Creating Snapshot..." : token ? "Recreate Snapshot" : "Create Sync Snapshot"}
        </button>
        <div className="ttl-row">
          <span className="ttl-label">TTL:</span>
          <Dropdown
            value={ttlHours}
            options={[
              { value: 1, label: "1 hour" },
              { value: 3, label: "3 hours" },
              { value: 6, label: "6 hours" },
              { value: 12, label: "12 hours" },
              { value: 24, label: "24 hours" },
            ]}
            onChange={v => setTtlHours(v)}
            disabled={creating}
            placeholder=""
          />
        </div>
      </div>

      {/* Progress */}
      {creating && (
        <div className="progress-wrap">
          <div className="progress-header">
            <span className="progress-label">
              {hashProgress.total > 0
                ? `Hashing files... ${hashProgress.progress} / ${hashProgress.total}`
                : "Scanning files..."
              }
            </span>
            {hashProgress.total > 0 && (
              <span className="progress-pct">
                {Math.round((hashProgress.progress / hashProgress.total) * 100)}%
              </span>
            )}
          </div>
          <div className="progress-track">
            <div
              className={`progress-fill ${hashProgress.total === 0 ? "progress-fill-pulse" : ""}`}
              style={{ width: hashProgress.total > 0 ? `${(hashProgress.progress / hashProgress.total) * 100}%` : "30%" }}
            />
          </div>
          {hashProgress.current_file && (
            <div className="progress-current-file">
              {hashProgress.current_file}
            </div>
          )}
        </div>
      )}

      {/* Error */}
      {error && (
        <div className="error-box">
          {error}
        </div>
      )}

      {/* Token + Snapshot info — always visible when available */}
      {token && snapshot && (
        <div className="token-box">
          <div className="token-label">
            Sync Token
          </div>
          <div className="token-row">
            <code className="token-code">
              {token}
            </code>
            <button
              className={`btn-copy ${copied ? "copied" : ""}`}
              onClick={handleCopy}
            >
              {copied ? "Copied!" : "Copy"}
            </button>
          </div>

          <div className="snapshot-meta">
            <span>{t("my_nodes.sync_files")} <strong>{snapshot.files_count}</strong></span>
            <span>{t("my_nodes.sync_size")} <strong>{formatSize(snapshot.total_size)}</strong></span>
            <span>{t("my_nodes.sync_created")} <strong>{formatTime(snapshot.created_at)}</strong></span>
            <span>{t("my_nodes.sync_expires")} <strong>{formatTime(snapshot.expires_at)}</strong></span>
          </div>
        </div>
      )}

      {/* CLI usage */}
      {token && (() => {
        const rdv = peerInfo.rendezvous_addr || brokerRdv || "<rdv-host:port>";
        const cmd = `installer sync --from-peer ${peerInfo.node_id || node?.id || "<master-id>"} --rendezvous ${rdv} --token ${token}`;
        const handleCopyCmd = () => {
          navigator.clipboard.writeText(cmd).then(() => {
            setCopiedCmd(true);
            setTimeout(() => setCopiedCmd(false), 2000);
          });
        };
        return (
          <div className="cli-box">
            <div className="cli-label">
              <span>{t("my_nodes.sync_run_on_slave")}</span>
              <button className={`btn-copy ${copiedCmd ? "copied" : ""}`} onClick={handleCopyCmd}>
                {copiedCmd ? "Copied!" : "Copy"}
              </button>
            </div>
            <code className="cli-code">{cmd}</code>
          </div>
        );
      })()}
    </div>
  );
}
