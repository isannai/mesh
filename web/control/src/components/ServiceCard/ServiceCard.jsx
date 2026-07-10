import React, { useEffect, useRef, useState } from "react";
import StatusTag from "@components/StatusTag/StatusTag";
import LoadingProgress from "@components/LoadingProgress";
import { useTranslation } from "@i18n";
import "./index.scss";

// 서비스 상태 카드 — 모든 서비스 공통
export function ServiceCard({
  name,
  version,
  running,
  healthData,        // { status, server, server_loading, model, bin_hash, queue_depth, total_jobs_done }
  engine,            // { name, version } or null
  engineMissing,     // boolean
  engineName,        // required engine name (when missing)
  hasModel,
  currentModel,
  modelSelector,     // React.ReactNode — ModelSelector 컴포넌트
  isPendingStart,
  isPendingStop,
  hasActiveJob,      // boolean — 작업 진행 중 여부
  onStart,
  onStop,
  canStart,
  actionLoading,
  t,
}) {
  const hd = healthData || {};
  const isStarting = running && !hd.status;
  const isModelLoading = running && hd.status && (hd.server_loading === true || hd.server === false);

  // Elapsed seconds since service started running
  const [loadElapsed, setLoadElapsed] = useState(0);
  useEffect(() => {
    if (!running) {
      setLoadElapsed(0);
      return;
    }
    const start = Date.now();
    setLoadElapsed(0);
    const id = setInterval(() => {
      setLoadElapsed(Math.floor((Date.now() - start) / 1000));
    }, 1000);
    return () => clearInterval(id);
  }, [running]);
  const showWarning = isStarting || isPendingStart || isPendingStop;
  const info = hd;

  // Card state — drives both border-color (svc-card) and dot background
  // (svc-dot) via the same modifier class. Order matters: pendingStop
  // wins over running, loading wins over plain running.
  const cardState = isPendingStop ? "is-stopping"
    : running ? ((isStarting || isModelLoading) ? "is-loading" : "is-running")
    : showWarning ? "is-starting"
    : "";

  return (
    <div className={`svc-card ${cardState}${actionLoading ? " svc-card-disabled" : ""}`}>
      {/* Header */}
      <div className="svc-card-header">
        <div className="svc-card-title">
          <div className={`svc-dot ${cardState}`} />
          <span className="svc-name">{name}</span>
          <span className="tag tag-version">v{version || "?"}</span>
        </div>
        {isPendingStop ? (
          <span className="svc-status-text warning">{t("services.stopping")}</span>
        ) : showWarning ? (
          <span className="svc-status-text warning">{t("services.starting")}</span>
        ) : running && isModelLoading ? (
          <span className="svc-status-text warning">{t("services.loading")}</span>
        ) : running ? (
          <StatusTag value={(info.queue_depth > 0 || hasActiveJob) ? "busy" : "idle"} />
        ) : (
          <StatusTag value="stopped" />
        )}
      </div>

      {/* Dependencies */}
      {engine && (
        <div className="svc-dep success">└─ {engine.name} ({t("services.engine_label")}) ✓ v{engine.version || "?"}</div>
      )}
      {engineMissing && (
        <div className="svc-dep danger clickable" onClick={() => window.location.href = "/deploy"} title={t("services.go_to_deploy")}>
          └─ {engineName} ({t("services.engine_label")}) ✗ {t("services.engine_not_installed")}
        </div>
      )}

      {/* Model info */}
      {hasModel && running && !isModelLoading && hd.model && (
        <div className="svc-dep primary">└─ {t("services.model_prefix")} {hd.model}</div>
      )}
      {hasModel && !running && !isPendingStart && modelSelector}

      {/* Loading states */}
      {isPendingStart && !running && <LoadingProgress status="starting" />}
      {running && isStarting && (
        <LoadingProgress status="waiting" message={t("services.preparing_engine")} />
      )}
      {running && isModelLoading && (
        <LoadingProgress
          status="loading"
          message={t("services.loading_model")}
        />
      )}

      {/* Running details */}
      {running && !isStarting && !isModelLoading && (
        <div className="svc-details">
          {hd.model && (<>
            <span className="svc-detail-label">{t("services.model_label")}</span>
            <span className="svc-detail-value">{hd.model}</span>
          </>)}
          {hd.bin_hash && (<>
            <span className="svc-detail-label hash-gap">{t("services.hash_label")}</span>
            <span className="svc-detail-hash">{hd.bin_hash}</span>
          </>)}
        </div>
      )}

      {/* Footer */}
      <div className="svc-card-footer">
        <div className="svc-stats">
          <span>{t("services.stat_pending")} <strong className={(info.queue_depth || 0) > 0 ? "text-danger" : "text-primary"}>{info.queue_depth || 0}</strong></span>
          <span>{t("services.stat_done")} <strong>{(info.total_jobs_done || 0).toLocaleString()}</strong></span>
        </div>
        {running ? (
          <button className="btn btn-sm btn-danger-outline" onClick={onStop}>
            {isPendingStop ? t("services.stopping") : t("common.stop")}
          </button>
        ) : (
          <button className={`btn btn-sm btn-primary${(!canStart || !!actionLoading) ? " btn-disabled-dim" : ""}`} onClick={onStart} disabled={!canStart || !!actionLoading}>
            {isPendingStart ? t("services.starting") : t("common.start")}
          </button>
        )}
      </div>
    </div>
  );
}

// ProcessCard (PID-based per-process list, zombie badge, Kill PID buttons)
// was removed along with engine-runner. Container lifecycle is reflected
// by ServiceCard's running/loading state alone.

// 작업 카드 — 서비스별 확장 가능
export function JobCard({ job, onCancel }) {
  const { t } = useTranslation();
  if (!job) return null;
  const isActive = job.status === "running" || job.status === "preparing" || job.status === "queued";
  // Status class — drives border, job-id color, job-pct color, and bar fill.
  // is-done / is-failed override is-active when both apply (terminal states).
  const stateCls = job.status === "done" ? "is-done"
    : job.status === "failed" ? "is-failed"
    : "is-active";
  const cardCls = isActive ? `is-active ${stateCls}` : stateCls;
  const pct = job.status === "done" ? 100 : job.progress || 0;

  return (
    <div className={`job-card ${cardCls}`}>
      <div className="job-card-title">{t("services.job_title")}</div>
      <div className="job-row">
        <span className={`job-id ${stateCls}`}>{job.job_id?.slice(0, 8)}</span>
        <StatusTag value={job.status} />
        {pct > 0 && (
          <div className="job-progress">
            <div className="job-progress-bar">
              <div className={`job-progress-fill ${stateCls}`} style={{ width: `${pct}%` }} />
            </div>
            <span className={`job-pct ${stateCls}`}>{pct}%</span>
          </div>
        )}
        {isActive && onCancel && (
          <button className="job-cancel" onClick={onCancel}>✕</button>
        )}
      </div>
    </div>
  );
}

// 카드 간 연결선
export function Connector({ color }) {
  return (
    <div className="card-connector">
      <div className="card-connector-line" style={{ background: color || "var(--border-dropdown)" }} />
    </div>
  );
}
