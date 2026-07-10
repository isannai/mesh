import React from "react";
import { useTranslation } from "../../i18n";
import "./index.scss";

const colorMap = {
  alive:   { bg: "rgba(0,184,148,0.15)", color: "#00b894", border: "rgba(0,184,148,0.3)" },
  online:  { bg: "rgba(0,184,148,0.15)", color: "#00b894", border: "rgba(0,184,148,0.3)" },
  stale:   { bg: "rgba(210,153,34,0.15)", color: "#d29922", border: "rgba(210,153,34,0.4)" },
  offline: { bg: "rgba(99,110,114,0.15)", color: "#8a9199", border: "rgba(99,110,114,0.3)" },
  idle:    { bg: "rgba(9,132,227,0.15)", color: "#4a9eff", border: "rgba(9,132,227,0.3)" },
  busy:    { bg: "rgba(225,112,85,0.15)", color: "#e17055", border: "rgba(225,112,85,0.3)" },
  standby: { bg: "rgba(108,92,231,0.15)", color: "#a29bfe", border: "rgba(108,92,231,0.3)" },
  stopped: { bg: "rgba(99,110,114,0.15)", color: "#8a9199", border: "rgba(99,110,114,0.3)" },
  running: { bg: "rgba(0,184,148,0.15)", color: "#00b894", border: "rgba(0,184,148,0.3)" },
  queued:  { bg: "rgba(108,117,125,0.15)", color: "#8a9199", border: "rgba(108,117,125,0.3)" },
  done:    { bg: "rgba(63,185,80,0.15)", color: "#3fb950", border: "rgba(63,185,80,0.3)" },
  failed:  { bg: "rgba(248,81,73,0.15)", color: "#f85149", border: "rgba(248,81,73,0.3)" },
};

const defaultColor = { bg: "rgba(99,110,114,0.15)", color: "#8a9199", border: "rgba(99,110,114,0.3)" };

// 연결 상태 (conn_status) 가 있으면 stale/offline 을 우선 표시, 그 외엔 node.status (idle/busy) 표시.
// StatusTag 는 value 만 받으므로 호출 측이 우선순위를 결정해 전달해야 함.
export default function StatusTag({ value }) {
  const { t } = useTranslation();
  const c = colorMap[value] || defaultColor;
  const className = `status-tag${value === "stale" ? " status-tag-pulse" : ""}`;
  return (
    <span className={className} style={{ background: c.bg, color: c.color, border: `1px solid ${c.border}` }}>
      {value || t("common.unknown")}
    </span>
  );
}
