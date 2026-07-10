import React from "react";
import "./index.scss";

// 서비스 로딩 프로그래스 — 모든 서비스 공통
// status: "starting" | "waiting" | "loading"
// progress: 0-100 (loading일 때만 사용)
// message: 커스텀 메시지 (선택)
export default function LoadingProgress({ status, progress, message }) {
  const isAnim = status === "starting" || status === "waiting";
  const pct = status === "loading" ? (progress || 0) : 30;

  const defaultMsg = {
    starting: "Starting service...",
    waiting: "Waiting for service to respond...",
    loading: `Loading model... ${progress > 0 ? `${progress}%` : ""}`,
  }[status] || "";

  return (
    <div className="loading-progress">
      <div className="loading-progress-bar">
        <div
          className={`loading-progress-fill ${isAnim ? "anim" : ""}`}
          style={{ width: `${pct}%` }}
        />
      </div>
      <span className={`loading-progress-text ${status === "loading" ? "warning" : ""}`}>
        {message || defaultMsg}
      </span>
    </div>
  );
}
