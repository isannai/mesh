export function nodeLabel(nid, nodes) {
  if (!nid) return "";
  const n = Array.isArray(nodes) ? nodes.find(n => n.id === nid) : null;
  return n?.label || nid.slice(0, 8) + "..";
}

// 설치 상태 데이터: 단일 노드(✓/—), 다중 노드(count/total + pct)
// TODO: Deploy 테이블의 Installed 컬럼에서 이 함수를 사용하도록 교체
//   { key: "installedCount", label: "Installed", align: "center",
//     render: (v, row) => <InstalledBadge count={v} total={totalSelected} /> }
export function getInstalledInfo(count, total) {
  if (total <= 1) {
    return { mode: "single", installed: count > 0 };
  }
  return { mode: "cluster", count, total, pct: total > 0 ? Math.round(count / total * 100) : 0 };
}
// 사용 예:
// const info = getInstalledInfo(count, total);
// if (info.mode === "single") → ✓ / —
// if (info.mode === "cluster") → 프로그래스 바 + count/total

export function formatSize(bytes) {
  if (!bytes || bytes <= 0) return "-";
  if (bytes >= 1024 * 1024 * 1024) return (bytes / (1024 * 1024 * 1024)).toFixed(1) + " GB";
  if (bytes >= 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + " MB";
  if (bytes >= 1024) return (bytes / 1024).toFixed(1) + " KB";
  return bytes + " B";
}

// formatUptime renders an RFC3339 timestamp as a human-friendly elapsed
// duration since that point — e.g. "23s", "5m", "2h 14m", "3d 8h".
// Returns "" for empty / unparseable inputs so callers can `&&`-guard.
// "now" parameter is exposed for tests; defaults to Date.now() at call time.
export function formatUptime(startedAt, now = Date.now()) {
  if (!startedAt) return "";
  const t = new Date(startedAt).getTime();
  if (!Number.isFinite(t)) return "";
  let secs = Math.floor((now - t) / 1000);
  if (secs < 0) secs = 0;
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ${mins % 60}m`;
  const days = Math.floor(hrs / 24);
  return `${days}d ${hrs % 24}h`;
}
