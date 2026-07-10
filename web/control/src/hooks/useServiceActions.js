import { useState, useRef, useCallback } from "react";
import { useToast } from "@components/Toast/ToastContext";
import { getAuthHeaders } from "@utils/wallet";

// 서비스 Start/Stop/Kill 공통 훅
// servicesRef: { current: [{ name, running }] } — 현재 서비스 목록 참조
export default function useServiceActions(nodeId, loadServices, onRefresh, servicesRef) {
  const toast = useToast();
  const [actionLoading, _setActionLoading] = useState(null);
  const actionLoadingRef = useRef(null);
  const setActionLoading = (v) => { actionLoadingRef.current = v; _setActionLoading(v); };

  // 다른 실행 중인 서비스를 Stop한 뒤 대상 서비스를 Start
  const handleStart = useCallback(async (name) => {
    if (actionLoadingRef.current) return;
    setActionLoading({ name, action: "starting" });

    // GPU 공유 — 한 번에 하나만 실행: 다른 실행 중인 서비스 Stop
    const others = (servicesRef?.current || []).filter(s => s.running && s.name !== name);
    for (const s of others) {
      try {
        await fetch(`/node/${encodeURIComponent(nodeId)}/service/${encodeURIComponent(s.name)}/stop`, {
          method: "POST", headers: { "Content-Type": "application/json", ...getAuthHeaders() },
        });
      } catch {}
    }
    // Stop 후 컨테이너 종료 대기
    if (others.length > 0) await new Promise(r => setTimeout(r, 2000));

    fetch(`/node/${encodeURIComponent(nodeId)}/service/${encodeURIComponent(name)}/start`, {
      method: "POST", headers: { "Content-Type": "application/json", ...getAuthHeaders() },
    }).then(r => r.json()).then(d => {
      if (d.error) { toast.error(d.error); setActionLoading(null); }
    }).catch(() => {});
    toast.success(`${name} start requested`);
    setTimeout(loadServices, 1000);
    setTimeout(() => { if (actionLoadingRef.current?.action === "starting") setActionLoading(null); }, 300000);
  }, [nodeId, loadServices]);

  const handleStop = useCallback((name) => {
    // 진행 중인 액션이 있으면 무시 — 중복 클릭 / 자동 재시도 방지. isannd
    // 도 idempotent 응답 ("already stopped") 를 주지만 굳이 wire 까지 보낼
    // 필요는 없으니 UI 단에서 컷.
    if (actionLoadingRef.current) return;
    setActionLoading({ name, action: "stopping" });
    fetch(`/node/${encodeURIComponent(nodeId)}/service/${encodeURIComponent(name)}/stop`, {
      method: "POST", headers: { "Content-Type": "application/json", ...getAuthHeaders() },
    }).catch(() => {});
    toast.success(`${name} stop requested`);
    setTimeout(loadServices, 1000);
    if (onRefresh) setTimeout(onRefresh, 5000);
    // Stop 도 handleStart 와 동일하게 안전망 timeout — actionLoading 이
    // 영원히 안 풀려서 Start 버튼까지 disabled 상태로 굳는 걸 방지. 정상
    // 케이스에선 svc.running=false 로 전환되며 버튼이 Start 로 바뀌는데,
    // 그 사이 actionLoading 가 살아있으면 새 Start 클릭이 막힘.
    setTimeout(() => { if (actionLoadingRef.current?.action === "stopping") setActionLoading(null); }, 30000);
  }, [nodeId, loadServices, onRefresh]);

  return { handleStart, handleStop, actionLoading, actionLoadingRef, setActionLoading };
}
