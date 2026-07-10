import React from "react";
import StatusTag from "@components/StatusTag/StatusTag";
import ModelSelector from "@components/ModelSelector";
import ProfileSelector from "@components/ProfileSelector";
import "./ServiceList.scss";

const IconLLM = () => (
  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
    <path d="M12 2a4 4 0 0 1 4 4v2a4 4 0 0 1-8 0V6a4 4 0 0 1 4-4z" />
    <path d="M8 14h8" /><path d="M10 18h4" />
    <circle cx="9" cy="6" r="0.5" fill="currentColor" /><circle cx="15" cy="6" r="0.5" fill="currentColor" />
    <path d="M6 10c-2 1-3 3-3 5a7 7 0 0 0 14 0c0-2-1-4-3-5" />
  </svg>
);

const IconSD = () => (
  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
    <rect x="3" y="3" width="18" height="18" rx="3" />
    <circle cx="8.5" cy="8.5" r="1.5" />
    <path d="M21 15l-5-5L5 21" />
  </svg>
);

const IconService = () => (
  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
    <rect x="4" y="4" width="16" height="16" rx="2" />
    <path d="M9 9h6M9 12h6M9 15h4" />
  </svg>
);

function getServiceIcon(name) {
  if (name.includes("llm")) return <IconLLM />;
  if (name.includes("sd") || name.includes("image") || name.includes("stable")) return <IconSD />;
  return <IconService />;
}

export default function ServiceList({
  services, onStart, onStop, onModelChange,
  actionLoading, installedModels, t,
  profileSets, onProfileChange, profileBusy,
  onProfileAdd, onProfileEdit, onProfileDelete,
}) {
  const anyRunning = services.some(s => s.running);
  // An external engine (vllm etc.) holding the GPU/network slot prevents
  // local managed services from being started — IANN can't stop the external
  // process to free resources, so Switch/Start must be disabled.
  const externalActive = services.some(s => s.external && s.running);
  // Running services float to the top; rest keep original order.
  const sorted = [...services].sort((a, b) => {
    const ar = a.running || a.external ? 1 : 0;
    const br = b.running || b.external ? 1 : 0;
    return br - ar;
  });

  return (
    <div className="sl-list">
      {sorted.map((svc) => {
        // docker launcher 는 image 안에 engine 이 내장 — gate 의 engine
        // 카탈로그 매칭 불필요. 옛 wrapped 패턴 (host 에 sd-server 바이너리
        // 별도 설치) 만 depEngine 매칭이 의미 있었음.
        const isDocker = svc.kind === "docker";
        const engineMissing = !svc.external && !isDocker && !svc.depEngine;
        const hd = svc.healthData || {};
        const isStarting = svc.running && !hd.status;
        const isModelLoading = svc.running && hd.status && (hd.server_loading === true || hd.server === false);
        const isLoading = isStarting || isModelLoading;
        const isThisAction = actionLoading?.name === svc.name;
        const isPendingStart = isThisAction && actionLoading?.action === "starting";
        const isPendingStop = isThisAction && actionLoading?.action === "stopping";
        const isActive = svc.running || isPendingStart;

        // Row state — drives borderLeftColor (sl-row), color (sl-icon),
        // background (sl-dot) via shared modifier.
        const rowState = isPendingStop ? "is-stopping"
          : svc.running ? (isLoading ? "is-loading" : "is-running")
          : isPendingStart ? "is-starting"
          : "";

        return (
          <div key={svc.name} className={"sl-row " + rowState + (isActive ? " sl-row-active" : "")}>

            {/* Line 1: icon + name + version + status */}
            <div className="sl-header">
              <span className={`sl-icon ${rowState}`}>{getServiceIcon(svc.name)}</span>
              <span className={`sl-dot ${rowState}`} />
              <span className="sl-name">{svc.name}</span>
              {svc.version && <span className="tag tag-version">v{svc.version}</span>}
              <span className="sl-spacer" />
              {isPendingStop ? <span className="sl-status sl-warn">Stopping...</span>
               : isPendingStart && !svc.running ? <span className="sl-status sl-warn">Starting...</span>
               : svc.running && isLoading ? <span className="sl-status sl-warn">{isModelLoading ? "Loading..." : "Starting..."}</span>
               : svc.running ? <StatusTag value={(hd.queue_depth > 0) ? "busy" : "idle"} />
               : <StatusTag value="stopped" />}
            </div>

            {/* Engine missing — dep-line slot, mirrors the installed shape */}
            {engineMissing && (
              <div className="sl-dep-line">
                <span className="sl-dep sl-warn">└─ engine not installed</span>
              </div>
            )}

            {/* Engine row — local-only, external services show their own
                (engine, external) row below. The active model is rendered
                separately via the existing ModelLabel surface, so we don't
                duplicate "model: <name>" inline here. */}
            {svc.depEngine && !svc.external && (
              <div className="sl-dep-line">
                <span className="sl-dep">└─ {svc.depEngine.name} (engine) ✓{svc.depEngine.version ? ` v${svc.depEngine.version}` : ""}</span>
                {svc.hasModel && !svc.running && !isPendingStart && !profileSets?.[svc.name] && (
                  <span className="sl-model-inline" onClick={e => e.stopPropagation()}>
                    <ModelSelector models={installedModels} currentModel={svc.currentModel}
                      onChange={(fileName) => onModelChange(svc.name, fileName)} serviceName={""} />
                  </span>
                )}
              </div>
            )}

            {/* External engine (vllm etc.): same row shape, no hash. */}
            {svc.external && (
              <div className="sl-dep-line">
                <span className="sl-dep">└─ {svc.kind || "external"} (engine, external)</span>
              </div>
            )}

            {/* Profile selector — only for managed services (sd/llm). External
                engines (vllm) skip the profile concept since IANN can't push
                changes into a docker container the user manages themselves. */}
            {profileSets?.[svc.name]?.editable && (
              <div className="sl-dep-line" onClick={e => e.stopPropagation()}>
                <span className="sl-dep">└─ profile</span>
                <ProfileSelector
                  profiles={profileSets[svc.name].profiles}
                  activeName={profileSets[svc.name].active}
                  editable={true}
                  onChange={(name) => onProfileChange?.(svc.name, name)}
                  busy={!!profileBusy?.[svc.name]}
                />
              </div>
            )}

            {/* Loading — stop 중일 때도 표시. 사용자 입장에선 컨테이너가
                실제로 멈출 때까지 시간이 걸리니까 (docker stop -t 10) 진행
                중인 신호가 필요. */}
            {(isPendingStart || isPendingStop || (svc.running && isLoading)) && (
              <div className="sl-loading">
                <div className="sl-loading-bar"><div className="sl-loading-fill" /></div>
                <span className="sl-loading-text">{
                  isPendingStop ? "Stopping engine..."
                  : isModelLoading ? "Loading model..."
                  : "Preparing engine..."
                }</span>
              </div>
            )}

            {/* Footer: Queue/Jobs + Button — 오른쪽 아래 */}
            <div className="sl-footer">
              {(svc.running || svc.external) && !isLoading && (
                <div className="sl-stats">
                  <span>Pending <strong className={(hd.queue_depth || 0) > 0 ? "text-danger" : "text-primary"}>{hd.queue_depth || 0}</strong></span>
                  <span>Done <strong>{(hd.total_jobs_done || 0).toLocaleString()}</strong></span>
                </div>
              )}
              <span className="sl-spacer" />
              {svc.running ? (
                <button className={"btn btn-sm sl-btn-stop" + (actionLoading ? " btn-disabled-dim" : "")}
                  onClick={() => onStop(svc.name)}
                  disabled={!!actionLoading}>
                  {isPendingStop ? "Stopping..." : t("common.stop")}
                </button>
              ) : engineMissing ? (
                <span className="sl-status sl-warn">Engine not installed</span>
              ) : (
                <button className={"btn btn-sm sl-btn-switch" + (actionLoading || externalActive ? " btn-disabled-dim" : "")}
                  onClick={() => onStart(svc.name)}
                  disabled={!!actionLoading || externalActive || (svc.hasModel && !svc.currentModel && !profileSets?.[svc.name]?.active)}
                  title={externalActive ? "External engine is running — stop it first" : ""}>
                  {anyRunning ? "Switch" : t("common.start")}
                </button>
              )}
            </div>
          </div>
        );
      })}
    </div>
  );
}
