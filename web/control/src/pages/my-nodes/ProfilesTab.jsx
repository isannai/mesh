import React, { useState, useEffect, useCallback } from "react";
import { fetchProfiles, setActiveProfile, upsertProfile, deleteProfile } from "../../api/profiles";
import ProfileEditor from "@components/ProfileEditor/ProfileEditor";
import { useToast } from "@components/Toast/ToastContext";
import { getAuthHeaders } from "@utils/wallet";
import "./ProfilesTab.scss";

// ProfilesTab — single surface for managing per-service profiles.
//
// Replaces the scattered profile UX (Monitor's ProfileSelector dropdown
// + ProfileEditor modal triggered from elsewhere + Components tab partial
// info). Self-contained: fetches profile sets and service list directly,
// keeps local state, talks to the same APIs the Monitor handlers used.
//
// LoRA section is rendered per-service inline (PR #4 wires up the actual
// LoRA list / picker). For now it surfaces a placeholder so the layout
// shape is final and the LoRA wiring drops in cleanly.

// Display map for normalized architecture enums — keep in sync with
// pkg/profile/profile.go NormalizeArchitecture and the search page's
// normalizeArchClient. SDXL-family finetunes (Pony / Illustrious / NoobAI)
// stay distinct from vanilla SDXL so the LoRA folder routing matches.
const ARCH_LABELS = {
  sd15: "SD 1.5",
  sd21: "SD 2.1",
  sd3:  "SD 3",
  sd35: "SD 3.5",
  sdxl: "SDXL",
  pony: "Pony",
  illustrious: "Illustrious",
  noobai: "NoobAI",
  flux: "Flux",
  "flux-d": "Flux.1 D",
  "flux-s": "Flux.1 S",
  "hunyuan-video": "Hunyuan Video",
  qwen2: "Qwen2",
  qwen25: "Qwen 2.5",
  qwen3: "Qwen 3",
  llama3:  "Llama 3",
  llama31: "Llama 3.1",
  mistral: "Mistral",
  mixtral: "Mixtral",
  other:   "Other",
};
function archDisplay(s) { return ARCH_LABELS[s] || s; }

// Image-gen UNet families → sd-api shows multi Default LoRAs panel.
const SD_ARCHS = new Set([
  "sd15", "sd21", "sd3", "sd35",
  "sdxl", "pony", "illustrious", "noobai",
  "flux", "flux-d", "flux-s",
]);
// LLM families → llm-api shows single Active LoRA picker.
const LLM_ARCHS = new Set(["qwen2", "qwen25", "qwen3", "llama3", "llama31", "mistral", "mixtral"]);

function svcIcon(name) {
  if (name === "sd-api") return "🎨";
  if (name === "llm-api") return "💬";
  if (name === "vllm-api") return "⚡";
  return "▣";
}

function KV({ k, v, mono, small }) {
  return (
    <div className="pt-kv">
      <span className="pt-kv-key">{k}</span>
      <span className={`pt-kv-val ${mono ? "mono" : ""} ${small ? "small" : ""}`}>{v}</span>
    </div>
  );
}

function humanBytes(n) {
  if (!n || n <= 0) return "—";
  if (n > 1073741824) return (n / 1073741824).toFixed(2) + " GB";
  if (n > 1048576) return (n / 1048576).toFixed(1) + " MB";
  if (n > 1024) return (n / 1024).toFixed(0) + " KB";
  return n + " B";
}

function detectLoraSource(pkg) {
  // Civitai / HF / file:// — derive from the first download's URL or
  // hash_source. Mirrors the broker's ModelLabel prefix logic.
  const dl = (pkg.downloads || [])[0] || {};
  const url = (dl.download_url || "").toLowerCase();
  if (url.includes("civitai.com")) return "civitai";
  if (url.includes("huggingface.co") || url.includes("hf.co")) return "huggingface";
  if (pkg.hash_source === "civitai") return "civitai";
  if (pkg.hash_source === "huggingface") return "huggingface";
  if (pkg.mode === "reference" || dl.ref_path) return "imported";
  return "";
}

// LoraDefaultsPanelSD — sd-api 의 multi-default LoRA 편집 패널.
//
// 각 LoRA 행: ☐/⭐ Default 체크박스 + 이름 + 출처 배지 + 크기 + weight slider + Copy tag.
// 우상단: "Disable LoRA on this profile" 토글 — ON 이면 모든 LoRA 차단 (engine spawn
// 에서 --lora-model-dir 빠짐). 토글이 ON 일 때 default 설정은 보존되지만 비활성.
//
// onSaveLoras 는 그 profile 의 loras 전체 (defaults + disabled) 를 받아 upsert 호출.
// auto-save: 체크박스 토글 / 슬라이더 떼는 시점에 즉시 저장.
function LoraDefaultsPanelSD({ arch, loras, profileLoras, busy, onCopyTag, onSaveLoras }) {
  const disabled = !!profileLoras?.disabled;
  const defaultsByRef = new Map();
  for (const d of (profileLoras?.defaults || [])) {
    defaultsByRef.set(d.package_ref, d);
  }
  const compatible = (loras || []).filter((l) => l.architecture === arch);
  const defaultCount = (profileLoras?.defaults || []).length;

  // Build the next loras settings from a mutator on the current state.
  const mutate = (fn) => {
    const next = {
      disabled,
      defaults: (profileLoras?.defaults || []).slice(),
    };
    fn(next);
    onSaveLoras(next);
  };

  const toggleDefault = (lora) => {
    const ref = `loras/${lora.architecture}/${lora.name}`;
    mutate((next) => {
      const idx = next.defaults.findIndex((d) => d.package_ref === ref);
      if (idx >= 0) next.defaults.splice(idx, 1);
      else next.defaults.push({ package_ref: ref, weight: 0.7 });
    });
  };

  const setWeight = (lora, weight) => {
    const ref = `loras/${lora.architecture}/${lora.name}`;
    mutate((next) => {
      const idx = next.defaults.findIndex((d) => d.package_ref === ref);
      if (idx >= 0) next.defaults[idx] = { ...next.defaults[idx], weight };
    });
  };

  const toggleDisabled = () => {
    mutate((next) => { next.disabled = !disabled; });
  };

  return (
    <>
      <div className="pt-lora-head-row">
        <div className="pt-section-hint flex-1">
          sd.cpp applies LoRAs per request. Checking <b>Default</b> auto-prepends them to every
          sd-api call — an API client passing <code>loras: [...]</code> takes precedence
          (default ignored). Architecture-compatible (<code>{arch}</code>) only.
        </div>
        <button
          type="button"
          className={`pt-lora-disable-toggle ${disabled ? "on" : ""}`}
          disabled={busy}
          onClick={toggleDisabled}
          title={disabled ? "Click to re-enable LoRA on this profile" : "Click to disable all LoRAs on this profile"}
        >
          <span className="pt-lora-disable-track" />
          <span className="pt-lora-disable-label">
            {disabled ? "LoRA disabled" : "Disable LoRA"}
          </span>
        </button>
      </div>

      {disabled && (
        <div className="pt-lora-disabled-banner">
          🚫 LoRAs are fully disabled while this profile is active — the engine spawns without
          <code>--lora-model-dir</code> and any <code>&lt;lora:&gt;</code> token in the prompt is ignored.
          Defaults are preserved but stay inert until you turn this toggle off.
        </div>
      )}

      {compatible.length === 0 ? (
        <div className="pt-detail-empty">
          No LoRAs installed in <code>packages/loras/{arch}/</code>.
          <div className="pt-detail-link">→ Find {arch} LoRAs in Search</div>
        </div>
      ) : (
        <div className={`pt-lora-list ${disabled ? "pt-lora-list-muted" : ""}`}>
          {compatible.map((lora) => {
            const ref = `loras/${lora.architecture}/${lora.name}`;
            const cur = defaultsByRef.get(ref);
            const isDef = !!cur;
            const weight = cur?.weight ?? 0.7;
            const source = detectLoraSource(lora);
            const dl = (lora.downloads || [])[0] || {};
            return (
              <div key={lora.name} className={`pt-lora-row ${isDef ? "is-default" : ""}`}>
                <label className="pt-lora-default-toggle">
                  <input
                    type="checkbox"
                    checked={isDef}
                    disabled={busy || disabled}
                    onChange={() => toggleDefault(lora)}
                  />
                  <span className={`pt-lora-default-label ${isDef ? "on" : "off"}`}>
                    {isDef ? "⭐ DEFAULT" : "SET DEFAULT"}
                  </span>
                </label>
                <span className="pt-lora-name">{lora.name}</span>
                {source && (
                  <span className={`pt-lora-source pt-lora-source-${source}`}>
                    {source === "huggingface" ? "HuggingFace" : source === "civitai" ? "Civitai" : "Imported"}
                  </span>
                )}
                <span className="pt-lora-size">{humanBytes(dl.size_bytes)}</span>
                <div className={`pt-lora-weight-wrap ${isDef && !disabled ? "" : "muted"}`}>
                  <span className="pt-lora-weight-label">weight</span>
                  <input
                    type="range"
                    min="0"
                    max="1.5"
                    step="0.05"
                    value={isDef ? weight : 0.7}
                    disabled={busy || !isDef || disabled}
                    onChange={(e) => setWeight(lora, parseFloat(e.target.value))}
                    className="pt-lora-weight-slider"
                  />
                  <span className="pt-lora-weight-val">{isDef ? weight.toFixed(2) : "—"}</span>
                </div>
                <div className="pt-lora-actions">
                  <button
                    className="btn btn-icon-sm"
                    title="Copy <lora:name:weight> tag for prompt"
                    onClick={() => onCopyTag(lora.name)}
                  >📋</button>
                </div>
              </div>
            );
          })}
        </div>
      )}

      <div className="pt-lora-list-foot">
        <span><b>{defaultCount}</b> default · <b>{compatible.length - defaultCount}</b> available</span>
        <span className="pt-lora-foot-link">
          Install more from <a href="#/search">Search →</a> ({arch} auto-filtered)
        </span>
      </div>
    </>
  );
}

// LoraActivePanelLLM — llm-api 의 single-active LoRA 편집 패널.
//
// llama.cpp 는 부팅 시 단일 LoRA 머지 — 변경하면 engine 재기동 필요.
// loras.active 가 없거나 None 이면 LoRA 미적용. Disable 토글이 ON 이면
// 선택 자체가 무효화되고 engine 이 --lora-scaled 없이 spawn.
function LoraActivePanelLLM({ arch, loras, profileLoras, busy, onSaveLoras }) {
  const disabled = !!profileLoras?.disabled;
  const active = profileLoras?.active;
  const currentRef = active?.package_ref || "";
  const currentScale = active?.scale != null ? Number(active.scale) : 1.0;
  const compatible = (loras || []).filter((l) => l.architecture === arch);

  const setActive = (ref) => {
    const next = { disabled, defaults: profileLoras?.defaults || [] };
    if (ref) {
      next.active = { package_ref: ref, scale: currentRef ? currentScale : 1.0 };
    } else {
      next.active = null;
    }
    onSaveLoras(next);
  };

  const setScale = (scale) => {
    if (!currentRef) return;
    const next = {
      disabled,
      defaults: profileLoras?.defaults || [],
      active: { package_ref: currentRef, scale },
    };
    onSaveLoras(next);
  };

  const toggleDisabled = () => {
    onSaveLoras({
      disabled: !disabled,
      defaults: profileLoras?.defaults || [],
      active: profileLoras?.active || null,
    });
  };

  return (
    <>
      <div className="pt-lora-head-row">
        <div className="pt-section-hint flex-1">
          llama.cpp merges a single LoRA at boot — changing it auto-restarts the engine.
          Architecture-compatible (<code>{arch}</code>) candidates only.
        </div>
        <button
          type="button"
          className={`pt-lora-disable-toggle ${disabled ? "on" : ""}`}
          disabled={busy}
          onClick={toggleDisabled}
        >
          <span className="pt-lora-disable-track" />
          <span className="pt-lora-disable-label">
            {disabled ? "LoRA disabled" : "Disable LoRA"}
          </span>
        </button>
      </div>

      {disabled && (
        <div className="pt-lora-disabled-banner">
          🚫 No LoRA applied while this profile is active — engine spawns without <code>--lora-scaled</code>.
        </div>
      )}

      {compatible.length === 0 ? (
        <div className="pt-detail-empty">
          No {arch} LoRAs installed.
          <div className="pt-detail-link">→ Find {arch} LoRAs in Search</div>
        </div>
      ) : (
        <div className={`pt-lora-picker-block ${disabled ? "pt-lora-list-muted" : ""}`}>
          <select
            className="pt-lora-picker-select"
            value={currentRef}
            disabled={busy || disabled}
            onChange={(e) => setActive(e.target.value)}
          >
            <option value="">— None (no LoRA applied) —</option>
            {compatible.map((lora) => {
              const ref = `loras/${lora.architecture}/${lora.name}`;
              const dl = (lora.downloads || [])[0] || {};
              return (
                <option key={ref} value={ref}>
                  {lora.name} ({humanBytes(dl.size_bytes)})
                </option>
              );
            })}
          </select>
          <label className="pt-lora-scale-label">
            scale
            <input
              type="number"
              step="0.05"
              min="0"
              max="2"
              value={currentScale}
              disabled={busy || disabled || !currentRef}
              onChange={(e) => setScale(parseFloat(e.target.value) || 0)}
              className="pt-lora-scale-input"
            />
          </label>
          {currentRef && !disabled && (
            <div className="pt-lora-restart-warn">
              ⚠ Engine restarts on save.
            </div>
          )}
        </div>
      )}
    </>
  );
}

function ServiceGroup({
  service, set, busy, loras,
  onAdd, onEdit, onDelete, onChangeActive,
  onCopyTag, onSaveLoras,
}) {
  const editable = set?.editable !== false;
  const profiles = Array.isArray(set?.profiles) ? set.profiles : [];
  const activeName = set?.active || (profiles[0]?.name);
  const [expandedId, setExpandedId] = useState(null);

  return (
    <div className="pt-svc-group">
      <div className="pt-svc-head">
        <span className="pt-svc-icon">{svcIcon(service)}</span>
        <span className="pt-svc-name">{service}</span>
        <span className="pt-svc-active">
          Active: {activeName
            ? <span className="pt-active-pill">{activeName}</span>
            : <span className="pt-svc-empty">(none)</span>}
          {!editable && (
            <span className="pt-svc-readonly" title="External service — values are display-only">
              read-only · external
            </span>
          )}
        </span>
        {editable && (
          <button className="btn btn-add-sm" onClick={() => onAdd(service)}>
            + Add Profile
          </button>
        )}
      </div>

      {profiles.length === 0 ? (
        <div className="pt-svc-empty-row">
          No profiles registered. Install a model via Search to seed one.
        </div>
      ) : (
        <div className="pt-row-list">
          {profiles.map((p) => {
            const isActive = p.name === activeName;
            const expanded = expandedId === p.name;
            const arch = p.architecture || "";
            const showLora = editable && SD_ARCHS.has(arch);
            const showLlmLora = editable && LLM_ARCHS.has(arch);
            return (
              <div key={p.name} className={`pt-row ${expanded ? "expanded" : ""}`}>
                <div className="pt-row-head" onClick={() => setExpandedId(expanded ? null : p.name)}>
                  <span className={`pt-arrow ${expanded ? "open" : ""}`}>▶</span>
                  <span className="pt-row-name">{p.name}</span>
                  <span className="pt-row-label">{p.label || ""}</span>
                  <span className="pt-row-tags">
                    {arch && <span className="pt-tag pt-tag-arch">{archDisplay(arch)}</span>}
                    {isActive && <span className="pt-tag pt-tag-active">✓ active</span>}
                    {p.needs_config && <span className="pt-tag pt-tag-warn">needs review</span>}
                  </span>
                </div>
                {expanded && (
                  <div className="pt-row-detail">
                    <div className="pt-detail-grid">
                      <div className="pt-detail-card">
                        <h4>Basic</h4>
                        <KV k="Name" v={p.name} mono />
                        <KV k="Label" v={p.label || "—"} />
                        <KV k="Architecture" v={arch ? archDisplay(arch) : "(unset)"} />
                        <KV k="Package" v={p.package_ref || "—"} mono small />
                      </div>
                      <div className="pt-detail-card">
                        <h4>Engine Values</h4>
                        {Object.entries(p.values || {}).filter(([k, v]) => v !== "" && v != null && k !== "model_dir").length === 0 ? (
                          <div className="pt-detail-empty">(defaults)</div>
                        ) : (
                          Object.entries(p.values || {})
                            .filter(([k, v]) => v !== "" && v != null && k !== "model_dir")
                            .map(([k, v]) => <KV key={k} k={k} v={String(v)} mono small />)
                        )}
                      </div>

                      {showLora && (
                        <div className="pt-detail-card pt-detail-card-full">
                          <h4>🎨 Default LoRAs — multi-select</h4>
                          <LoraDefaultsPanelSD
                            arch={arch}
                            loras={loras}
                            profileLoras={p.loras}
                            busy={busy}
                            onCopyTag={onCopyTag}
                            onSaveLoras={(next) => onSaveLoras(service, p, next)}
                          />
                        </div>
                      )}

                      {showLlmLora && (
                        <div className="pt-detail-card pt-detail-card-full">
                          <h4>💬 Active LoRA — boot-time merge</h4>
                          <LoraActivePanelLLM
                            arch={arch}
                            loras={loras}
                            profileLoras={p.loras}
                            busy={busy}
                            onSaveLoras={(next) => onSaveLoras(service, p, next)}
                          />
                        </div>
                      )}
                    </div>

                    <div className="pt-row-actions">
                      {editable && !isActive && (
                        <button
                          className="btn btn-secondary btn-sm"
                          disabled={busy}
                          onClick={() => onChangeActive(service, p.name)}
                        >
                          Make active (restart engine)
                        </button>
                      )}
                      {editable && (
                        <button
                          className="btn btn-secondary btn-sm"
                          onClick={() => onEdit(service, p)}
                        >✎ Edit values</button>
                      )}
                      {editable && !isActive && profiles.length > 1 && (
                        <button
                          className="btn btn-delete btn-sm pt-row-actions-end"
                          disabled={busy}
                          onClick={() => onDelete(service, p.name)}
                        >Delete profile</button>
                      )}
                      {isActive && (
                        <span className="pt-active-note pt-row-actions-end">
                          Active profile — switch to another before deleting
                        </span>
                      )}
                    </div>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

export default function ProfilesTab({ node, t }) {
  const toast = useToast();
  const nodeId = node?.id || "";
  // Heartbeat 의 node.services 는 RV 가 받은 서비스만 포함 — 즉 부팅됐다가
  // 등록까지 마친 서비스다. 설치만 됐고 한 번도 안 띄운 서비스 (예: 막
  // 설치한 llm-api) 는 여기서 빠진다. /provider/packages 의 type=service
  // 가 disk 상의 진짜 source-of-truth — Components 탭과 같은 source.
  // 외부 서비스 (vllm-api 같은) 는 installer 가 registry 안 하므로
  // heartbeat 에서 따로 끌어와 합친다.
  const [installedSvcs, setInstalledSvcs] = useState([]);
  const [profileSets, setProfileSets] = useState({});
  const [profileBusy, setProfileBusy] = useState({});
  const [editorState, setEditorState] = useState(null);
  const [refreshKey, setRefreshKey] = useState(0);
  const [loras, setLoras] = useState([]); // installed LoRA packages on this node

  // Service list comes from node.services (rendezvous payload — single
  // source of truth via provider.json). LoRAs still come from the node's
  // installed-package catalog.
  useEffect(() => {
    setInstalledSvcs((node?.services || [])
      .filter((s) => s && s.name)
      .map((s) => ({ name: s.name, kind: s.type, external: s.type !== "" && s.type !== "local" })));
  }, [node?.services]);

  useEffect(() => {
    if (!nodeId) return;
    let cancelled = false;
    fetch(`/node/${encodeURIComponent(nodeId)}/provider/packages?type=lora`, {
      headers: { ...getAuthHeaders() },
    })
      .then((r) => r.ok ? r.json() : [])
      .then((arr) => {
        if (cancelled) return;
        setLoras(Array.isArray(arr) ? arr : []);
      })
      .catch(() => {
        if (cancelled) return;
        setLoras([]);
      });
    return () => { cancelled = true; };
  }, [nodeId, refreshKey]);

  const services = installedSvcs;

  const handleCopyTag = (loraName) => {
    const tag = `<lora:${loraName}:0.7>`;
    if (navigator.clipboard?.writeText) {
      navigator.clipboard.writeText(tag).catch(() => {});
    }
    toast.success(`Copied: ${tag}`);
  };

  // Save a profile's loras section (defaults / active / disabled).
  // Calls upsertProfile with just the loras field — values is omitted so
  // the backend's sticky preserve keeps engine_values intact. For sd-api
  // changes the backend may still restart (active profile + structure
  // change). For llm-api any change requires restart since llama.cpp
  // merges LoRA at boot.
  const handleSaveLoras = async (svcName, prof, nextLoras) => {
    if (!nodeId) return;
    const set = profileSets[svcName];
    if (set?.editable === false) return;
    const isActive = prof.name === set?.active;
    setProfileBusy((b) => ({ ...b, [svcName]: true }));
    // Optimistic local update so checkbox / slider feel responsive.
    setProfileSets((prev) => {
      const cur = prev[svcName];
      if (!cur) return prev;
      return {
        ...prev,
        [svcName]: {
          ...cur,
          profiles: (cur.profiles || []).map((q) =>
            q.name === prof.name ? { ...q, loras: nextLoras } : q
          ),
        },
      };
    });
    try {
      await upsertProfile(nodeId, {
        service: svcName,
        name: prof.name,
        label: prof.label || "",
        architecture: prof.architecture || "",
        values: prof.values || {},
        loras: nextLoras,
        set_active: isActive,
      });
      // llm-api 의 active 변경은 engine 재기동 — 잠시 후 reload.
      const willRestart = isActive && svcName === "llm-api";
      toast.success(willRestart ? "LoRA saved (engine restarting)" : "LoRA saved");
      setTimeout(() => setRefreshKey((k) => k + 1), willRestart ? 2000 : 600);
    } catch (e) {
      toast.error("LoRA save failed: " + (e.message || e));
      // Rollback by reloading.
      setRefreshKey((k) => k + 1);
    } finally {
      setProfileBusy((b) => ({ ...b, [svcName]: false }));
    }
  };

  // Load profile sets per managed service. External (vLLM) is skipped
  // because there's nothing to edit — its values are externally managed.
  const loadProfileSets = useCallback(async () => {
    if (!nodeId) return;
    const managed = (services || []).filter((s) => !s.external);
    const out = {};
    await Promise.all(managed.map(async (s) => {
      const set = await fetchProfiles(nodeId, s.name);
      if (set && Array.isArray(set.profiles) && set.profiles.length > 0) {
        out[s.name] = set;
      } else if (set) {
        // Empty set still recorded so UI shows "no profiles" placeholder
        // instead of hiding the service group entirely.
        out[s.name] = set;
      }
    }));
    setProfileSets(out);
  }, [nodeId, services]);

  useEffect(() => {
    if (services.length > 0) loadProfileSets();
  }, [services, loadProfileSets, refreshKey]);

  const handleAdd = (svcName) => {
    const set = profileSets[svcName];
    if (set?.editable === false) return;
    setEditorState({
      service: svcName,
      schema: set?.schema || [],
      editing: null,
      busy: false,
    });
  };

  const handleEdit = (svcName, p) => {
    const set = profileSets[svcName];
    if (set?.editable === false) return;
    setEditorState({
      service: svcName,
      schema: set?.schema || [],
      editing: p,
      busy: false,
    });
  };

  const handleSave = async (payload) => {
    if (!editorState) return;
    setEditorState((s) => ({ ...s, busy: true }));
    try {
      await upsertProfile(nodeId, { service: editorState.service, ...payload });
      toast.success(`Profile saved: ${payload.name}`);
      setEditorState(null);
      // 활성 변경 시 엔진이 재시작 — inspect 갱신을 기다린 후 재로드.
      setTimeout(() => setRefreshKey((k) => k + 1), payload.set_active ? 2000 : 500);
    } catch (e) {
      toast.error("Save failed: " + (e.message || e));
      setEditorState((s) => ({ ...s, busy: false }));
    }
  };

  const handleDelete = async (svcName, profileName) => {
    if (!nodeId) return;
    const set = profileSets[svcName];
    if (set?.editable === false) return;
    if (!window.confirm(`Delete profile "${profileName}"?`)) return;
    setProfileBusy((b) => ({ ...b, [svcName]: true }));
    try {
      await deleteProfile(nodeId, svcName, profileName);
      toast.success(`Profile deleted: ${profileName}`);
      setRefreshKey((k) => k + 1);
    } catch (e) {
      toast.error("Delete failed: " + (e.message || e));
    } finally {
      setProfileBusy((b) => ({ ...b, [svcName]: false }));
    }
  };

  const handleChangeActive = async (svcName, profileName) => {
    if (!nodeId) return;
    setProfileBusy((b) => ({ ...b, [svcName]: true }));
    try {
      await setActiveProfile(nodeId, svcName, profileName);
      toast.success(`Active profile: ${profileName} (restarting engine)`);
      setTimeout(() => setRefreshKey((k) => k + 1), 2000);
    } catch (e) {
      toast.error("Set active failed: " + (e.message || e));
    } finally {
      setProfileBusy((b) => ({ ...b, [svcName]: false }));
    }
  };

  // Order services so managed (sd-api/llm-api) come first, external
  // (vllm-api) at the end. Operators usually iterate on managed services.
  const ordered = (services || [])
    .slice()
    .sort((a, b) => Number(!!a.external) - Number(!!b.external));

  if (ordered.length === 0) {
    return (
      <div className="pt-empty">
        No services installed on this node yet.
        <br />Install <code>sd-api</code> / <code>llm-api</code> in Components, then return here to manage profiles.
      </div>
    );
  }

  return (
    <div className="pt-root">
      <div className="pt-intro-row">
        <div className="pt-intro">
          Per-service profiles. Each profile bundles model package + engine values.
          Switching active profile auto-restarts the engine.
          <br />
          <span className="pt-intro-note">
            Only services with <code>enable: true</code> in <code>conf/provider.json</code> appear here —
            disabled services are hidden until you turn them on and restart the provider.
          </span>
        </div>
        <button
          className="pt-refresh-btn"
          onClick={() => setRefreshKey((k) => k + 1)}
          title="Refresh"
        >↻</button>
      </div>
      {ordered.map((svc) => (
        <ServiceGroup
          key={svc.name}
          service={svc.name}
          set={profileSets?.[svc.name]}
          busy={!!profileBusy?.[svc.name]}
          loras={loras}
          onAdd={handleAdd}
          onEdit={handleEdit}
          onDelete={handleDelete}
          onChangeActive={handleChangeActive}
          onCopyTag={handleCopyTag}
          onSaveLoras={handleSaveLoras}
        />
      ))}

      {editorState && (
        <ProfileEditor
          open={!!editorState}
          onClose={() => setEditorState(null)}
          onSave={handleSave}
          service={editorState.service}
          schema={editorState.schema}
          editing={editorState.editing}
          existing={profileSets[editorState.service]?.profiles || []}
          busy={editorState.busy}
        />
      )}
    </div>
  );
}
