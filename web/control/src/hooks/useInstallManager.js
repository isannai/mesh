import { useState, useCallback } from "react";
import { nodeLabel } from "@utils/format";
import { getAuthHeaders } from "@utils/wallet";
import { createCustomModel, deleteCustomModel } from "@utils/customStore";

const MAX_CONCURRENT = 5;

// Deploy Install/Uninstall/Remove/Import/SaveToDB 공통 훅
export default function useInstallManager({ selectedNodeIds, onlineNodes, activeType, myNodes, setRefreshKey, setConfirmAction }) {
  const [installJobs, setInstallJobs] = useState([]);

  const runWithWorkerPool = (tasks) => {
    let idx = 0, running = 0;
    const next = () => { while (running < MAX_CONCURRENT && idx < tasks.length) { const task = tasks[idx++]; running++; task().finally(() => { running--; next(); }); } };
    next();
  };

  // 패널 제거 지연: 작은 파일/hash skip 등으로 install 이 순식간에 끝나도
  // 사용자가 최종 상태를 볼 수 있게 done 을 2.5초 뒤에 마킹.
  const DONE_RETENTION_MS = 2500;
  // 에러 메시지를 사용자에게 보여줄 시간. 이후 자동으로 done 처리되어 패널에서 사라짐.
  const ERROR_RETENTION_MS = 5000;
  // 첫 progress 이벤트가 이 percent 이상이면 resume 으로 간주하고 ↻ 배지 표시
  const RESUMED_THRESHOLD = 5;

  // 에러 마킹 + 일정 시간 뒤 자동 제거 (done 으로 마킹해서 activeJobs 에서 빠지게 함)
  const markError = (jobId, message) => {
    setInstallJobs(prev => prev.map(j => j.id !== jobId ? j : { ...j, error: message }));
    setTimeout(() => {
      setInstallJobs(prev => prev.map(j => j.id !== jobId ? j : { ...j, done: true }));
    }, ERROR_RETENTION_MS);
  };

  // Map raw installer-backend error strings to operator-facing English.
  const friendlyError = (msg) => {
    if (!msg) return msg;
    if (msg.includes("install already in progress")) {
      return "Already installing this package in another tab.";
    }
    if (msg.includes("hash mismatch")) {
      return "Hash verification failed — the bad file was removed. Please retry.";
    }
    if (msg.includes("ready_check timeout")) {
      return "Install finished but the service did not become ready in time.";
    }
    return msg;
  };

  const processSSE = async (resp, jobId) => {
    const reader = resp.body.getReader(); const decoder = new TextDecoder(); let buf = "";
    while (true) {
      const { done, value } = await reader.read(); if (done) break;
      buf += decoder.decode(value, { stream: true }); const lines = buf.split("\n"); buf = lines.pop();
      for (const line of lines) {
        if (!line.startsWith("data: ")) continue;
        try {
          const evt = JSON.parse(line.slice(6));
          if (["progress", "file_done", "file_start", "checking", "skip"].includes(evt.event)) {
            setInstallJobs(prev => prev.map(j => {
              if (j.id !== jobId) return j;
              const existing = j.progress[evt.file_name];
              // 첫 progress 이벤트가 0 이 아닌 percent 면 resume 으로 마킹
              const isFirstProgress = !existing && evt.event === "progress" && (evt.percent ?? 0) > RESUMED_THRESHOLD;
              return {
                ...j,
                progress: {
                  ...j.progress,
                  [evt.file_name]: {
                    percent: evt.percent ?? 0,
                    status: evt.event,
                    resumed: existing?.resumed || isFirstProgress,
                  },
                },
              };
            }));
          }
          else if (evt.event === "done") {
            setTimeout(() => {
              setInstallJobs(prev => prev.map(j => j.id !== jobId ? j : { ...j, done: true }));
            }, DONE_RETENTION_MS);
          }
          else if (evt.event === "error") {
            markError(jobId, friendlyError(evt.message));
          }
        } catch {}
      }
    }
  };

  const installToNode = useCallback((nodeId, body, swName) => {
    const jobId = Date.now() + Math.random();
    const ctrl = new AbortController();
    setInstallJobs(prev => [...prev, {
      id: jobId, nodeId, nodeLabel: nodeLabel(nodeId, myNodes), swName,
      progress: {}, error: null, done: false,
      // Cancel 버튼이 호출하는 함수. AbortController 를 통해 fetch 를 끊으면
      // Provider 가 disconnect 감지 → installer child kill → partial 보존 → 다음 시도 resume
      cancel: () => ctrl.abort(),
    }]);
    return (async () => {
      try {
        const resp = await fetch(`/node/${encodeURIComponent(nodeId)}/installer/install`, {
          method: "POST",
          headers: { "Content-Type": "application/json", ...getAuthHeaders() },
          body: JSON.stringify(body),
          signal: ctrl.signal,
        });
        if (!resp.ok) {
          let msg = `HTTP ${resp.status}`;
          try { const d = await resp.json(); msg = d.error || msg; } catch {}
          markError(jobId, friendlyError(msg));
          return;
        }
        if (!resp.body) {
          setInstallJobs(prev => prev.map(j => j.id !== jobId ? j : { ...j, done: true }));
          return;
        }
        await processSSE(resp, jobId);
        setInstallJobs(prev => prev.map(j => j.id !== jobId ? j : (j.done || j.error ? j : { ...j, done: true })));
      } catch (e) {
        if (e.name === "AbortError") {
          markError(jobId, "Cancelled by user (resume available)");
        } else {
          markError(jobId, friendlyError(e.message) || "Connection lost");
        }
      }
      finally { setRefreshKey(k => k + 1); }
    })();
  }, [myNodes, setRefreshKey]);

  // Gate 소프트웨어 설치
  const handleInstall = useCallback((model) => {
    const onlineIds = new Set(onlineNodes.map(n => n.id));
    const targets = selectedNodeIds.filter(nid => !model.nodeStatus[nid] && onlineIds.has(nid));
    if (!targets.length) return;
    setInstallJobs(prev => prev.filter(j => !j.done && !j.error));
    const body = { type: activeType, name: model.name, version: "latest" };
    runWithWorkerPool(targets.map(nid => () => installToNode(nid, body, model.name)));
  }, [selectedNodeIds, onlineNodes, activeType, installToNode]);

  // 커스텀 모델 설치
  const handleInstallCustom = useCallback((model) => {
    const cm = model._customModel;
    let cmFiles; try { cmFiles = JSON.parse(cm.files || "[]"); } catch { cmFiles = []; }
    const packageJson = {
      name: cm.name, type: "model", version: "custom", platform: "All", latest: true,
      install_root: cm.install_path || "ai/models",
      service: cm.service || "",
      architecture: cm.architecture || "",
      files: cmFiles.map(f => ({ file_name: f.file_name, download_url: f.download_url, install_path: f.install_path || "", hash: f.hash || "", size_bytes: f.size_bytes || 0, required: true })),
    };
    const onlineIds = new Set(onlineNodes.map(n => n.id));
    const targets = selectedNodeIds.filter(nid => !model.nodeStatus[nid] && onlineIds.has(nid));
    if (!targets.length) return;
    setInstallJobs(prev => prev.filter(j => !j.done && !j.error));
    runWithWorkerPool(targets.map(nid => () => installToNode(nid, { type: "model", name: cm.name, version: "custom", package_json: packageJson }, cm.name)));
  }, [selectedNodeIds, onlineNodes, installToNode]);

  // 파일 basename 추출: "v1-5-pruned-emaonly-fp16.safetensors" -> "v1-5-pruned-emaonly-fp16"
  const fileBaseName = (fileName) => {
    const dot = fileName.lastIndexOf(".");
    return dot > 0 ? fileName.substring(0, dot) : fileName;
  };

  // Gate 카테고리 내 개별 파일 install — Models 탭 Level 2에서 사용.
  // Package name = 파일 basename, manifest는 packages/models/{category}/{basename}.json에 저장됨.
  const handleInstallFile = useCallback((category, file) => {
    const mf = category._manifest;
    if (!mf) return;
    const baseName = fileBaseName(file.file_name);
    const verifyEntry = (mf.verify || []).find(v => v.file_name === file.file_name);
    const packageJson = {
      name: baseName,
      type: "model",
      version: mf.version,
      platform: mf.platform,
      latest: true,
      install_root: mf.install_root,
      service: mf.service || "",
      architecture: mf.architecture || "",
      category: mf.name,                         // ← Gate 카테고리 이름 = subdir
      downloads: [file],
      verify: verifyEntry ? [verifyEntry] : [],
    };
    const onlineIds = new Set(onlineNodes.map(n => n.id));
    const targets = selectedNodeIds.filter(nid => onlineIds.has(nid));
    if (!targets.length) return;
    setInstallJobs(prev => prev.filter(j => !j.done && !j.error));
    runWithWorkerPool(targets.map(nid => () =>
      installToNode(
        nid,
        { type: "model", name: baseName, version: mf.version, package_json: packageJson },
        `${mf.name}/${file.file_name}`,
      )
    ));
  }, [selectedNodeIds, onlineNodes, installToNode]);

  // 개별 파일 uninstall — 기존 /installer/uninstall 엔드포인트 재사용.
  // name = 파일 basename, category = Gate 카테고리 이름.
  const handleUninstallFile = useCallback((category, file) => {
    setConfirmAction({
      message: `Uninstall file "${file.file_name}" from "${category.name}"?`,
      onConfirm: async () => {
        setConfirmAction(null);
        const baseName = fileBaseName(file.file_name);
        for (const nid of selectedNodeIds) {
          try {
            await fetch(`/node/${encodeURIComponent(nid)}/installer/uninstall`, {
              method: "POST",
              headers: { "Content-Type": "application/json", ...getAuthHeaders() },
              body: JSON.stringify({ type: "model", name: baseName, category: category.name }),
            });
          } catch {}
        }
        setRefreshKey(k => k + 1);
      },
    });
  }, [selectedNodeIds, setConfirmAction, setRefreshKey]);

  // Uninstall (확인 다이얼로그)
  // category 는 model 일 때 service_name (sd-api/llm-api/...), lora 일 때 architecture
  // (sd15/sdxl/...) 을 전달해야 새 CLI 의 엄격 검증을 통과한다. 없으면
  // installer 가 packages/{type}/{category}/{name}/ subdir 를 식별 못해 실패.
  const handleUninstall = useCallback((model) => {
    setConfirmAction({
      message: `Uninstall "${model.name}"? Files and manifest will be removed from nodes.`,
      onConfirm: async () => {
        setConfirmAction(null);
        const targets = selectedNodeIds.filter(nid => model.nodeStatus[nid]);
        const category = activeType === "model"
          ? (model.service_name || model._installed?.service || model._nodeManifest?.service || "")
          : activeType === "lora"
          ? (model.architecture || model._installed?.architecture || model._nodeManifest?.architecture || "")
          : "";
        const body = { type: activeType, name: model.name };
        if (category) body.category = category;
        for (const nid of targets) {
          try { await fetch(`/node/${encodeURIComponent(nid)}/installer/uninstall`, { method: "POST", headers: { "Content-Type": "application/json", ...getAuthHeaders() }, body: JSON.stringify(body) }); } catch {}
        }
        setRefreshKey(k => k + 1);
      },
    });
  }, [selectedNodeIds, activeType, setConfirmAction, setRefreshKey]);

  // Remove: DB 레코드만 삭제
  const handleRemove = useCallback((model) => {
    if (!model._customModel?.id) return;
    setConfirmAction({
      message: `Remove "${model.name}" from DB? Files and manifest will not be affected.`,
      onConfirm: async () => {
        setConfirmAction(null);
        try { await deleteCustomModel(model._customModel.id); } catch {}
        setRefreshKey(k => k + 1);
      },
    });
  }, [setConfirmAction, setRefreshKey]);

  // Save to DB: 노드에만 있는 모델을 DB에 저장
  const handleSaveToDB = useCallback(async (model) => {
    const manifest = model._nodeManifest;
    if (!manifest) return;
    let files = "[]";
    try { files = JSON.stringify(manifest.files || []); } catch {}
    const payload = {
      name: manifest.name,
      service: manifest.service || "",
      architecture: manifest.architecture || "",
      download_url: "",
      install_path: manifest.files?.[0]?.install_path || "",
      files,
      is_public: false,
    };
    try {
      await createCustomModel(payload);
    } catch (e) {
      alert(e.message || "save failed");
      return;
    }
    setRefreshKey(k => k + 1);
  }, [setRefreshKey]);

  const isJobRunning = (name) => installJobs.some(j => j.swName === name && !j.done && !j.error);

  return {
    installJobs, setInstallJobs,
    handleInstall, handleInstallCustom,
    handleInstallFile, handleUninstallFile,
    handleUninstall, handleRemove, handleSaveToDB,
    isJobRunning,
  };
}
