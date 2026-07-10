import React, { useState, useRef, useEffect, useCallback } from "react";
import { createPortal } from "react-dom";
import "./index.scss";

// archDisplayName converts the canonical architecture enum (sd15, sdxl,
// qwen2, …) into a short operator-facing label for the badge. Mirrors
// the vocabulary in pkg/profile/profile.go's NormalizeArchitecture. Any
// unknown value passes through verbatim so legacy data still renders.
const ARCH_LABELS = {
  sd15:    "SD 1.5",
  sd21:    "SD 2.1",
  sdxl:    "SDXL",
  sd3:     "SD 3",
  flux:    "Flux",
  qwen2:   "Qwen2",
  llama3:  "Llama 3",
  mistral: "Mistral",
  mixtral: "Mixtral",
  other:   "Other",
};
function archDisplayName(s) {
  return ARCH_LABELS[s] || s;
}

// 프로필 선택 드롭다운 — 서비스 카드의 모델 선택 자리에 위치.
// profiles: [{ name, label, values: { ctx_size, gpu_layers, ... } }]
// activeName: 현재 active 프로필의 name
// editable: false 면 CRUD UI 모두 숨김 (vllm 처럼 외부 관리 서비스용)
// onChange: (name) => void                 — active 변경 (editable=false 일때도 가능)
// onAdd: () => void                        — 새 프로필 추가 (모달 트리거)
// onEdit: (profile) => void                — 기존 프로필 편집 (모달 트리거)
// onDelete: (name) => void                 — 삭제
// busy / deleteBusy: 버튼 비활성 상태
//
// Portal-based list: dropdown is rendered into document.body with
// position:fixed so ancestor `overflow:hidden` (service card rows,
// scrollable monitors, modals) cannot clip it.
export default function ProfileSelector({
  profiles, activeName, editable = true,
  onChange, onAdd, onEdit, onDelete,
  busy, deleteBusy,
}) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [pos, setPos] = useState(null);
  const ref = useRef(null);
  const triggerRef = useRef(null);
  const listRef = useRef(null);

  const recomputePos = useCallback(() => {
    if (!triggerRef.current) return;
    const r = triggerRef.current.getBoundingClientRect();
    const listH = 280;
    const spaceBelow = window.innerHeight - r.bottom;
    const above = spaceBelow < listH && r.top > spaceBelow;
    setPos({
      left: r.left,
      width: Math.max(r.width, 360),
      top: above ? undefined : r.bottom + 4,
      bottom: above ? window.innerHeight - r.top + 4 : undefined,
    });
  }, []);

  useEffect(() => {
    if (!open) return;
    window.addEventListener("scroll", recomputePos, true);
    window.addEventListener("resize", recomputePos);
    return () => {
      window.removeEventListener("scroll", recomputePos, true);
      window.removeEventListener("resize", recomputePos);
    };
  }, [open, recomputePos]);

  useEffect(() => {
    const handler = (e) => {
      const insideTrigger = ref.current && ref.current.contains(e.target);
      const insideList = listRef.current && listRef.current.contains(e.target);
      if (!insideTrigger && !insideList) setOpen(false);
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, []);

  const list = Array.isArray(profiles) ? profiles : [];
  const active = list.find((p) => p.name === activeName) || null;
  const filtered = list.filter((p) => {
    const q = search.toLowerCase();
    return (
      p.name.toLowerCase().includes(q) ||
      (p.label || "").toLowerCase().includes(q)
    );
  });

  // values 미리보기 — engine 별로 키가 달라서 generic 하게.
  // 우선순위: 잘 알려진 키들 먼저, 나머지는 뒤에. 의사결정에 영향 적은
  // 키 (model_dir 같은 경로) 는 hidden 으로 둬서 미리보기 노이즈 줄임.
  const HIDDEN_KEYS = new Set(["model_dir"]);
  const preview = (p) => {
    const v = p.values || {};
    const known = new Set(["model_default", "ctx_size", "max_model_len", "gpu_layers"]);
    const parts = [];
    // 1) Known keys with friendly labels
    if (v.model_default) parts.push(String(v.model_default));
    const ctx = v.ctx_size ?? v.max_model_len;
    if (ctx !== undefined && ctx !== "") parts.push(`ctx ${ctx}`);
    if (v.gpu_layers !== undefined && v.gpu_layers !== "") parts.push(`ngl ${v.gpu_layers}`);
    // 2) 그 외 키 = 그대로 key=value (vllm 의 quantization 등). hidden 키는 제외.
    Object.entries(v).forEach(([k, val]) => {
      if (known.has(k) || HIDDEN_KEYS.has(k) || val === "" || val === null || val === undefined) return;
      parts.push(`${k}=${val}`);
    });
    return parts.join(" · ");
  };

  const isLast = list.length <= 1;

  return (
    <div ref={ref} className="profile-selector">
      <div className="profile-selector-trigger">
        <button
          ref={triggerRef}
          onClick={() => {
            if (!open) recomputePos();
            setOpen(!open);
          }}
          className={`profile-selector-btn ${active ? "" : "empty"}`}
          disabled={busy}
          title={active ? preview(active) : "Select profile"}
        >
          {busy ? "…" : (active?.label || active?.name || "-- Select profile --")}
          {active?.needs_config && (
            <span className="profile-selector-trigger-warn" title="Active profile needs review — click an entry to open the editor and Save once">
              ⚠ needs review
            </span>
          )} ▼
        </button>
        {!editable && (
          <span className="profile-selector-readonly" title="Externally managed — values are display-only">
            read-only
          </span>
        )}
      </div>
      {open && createPortal(
        <div
          ref={listRef}
          className="profile-selector-dropdown"
          style={pos ? {
            position: "fixed",
            left: pos.left, right: "auto",
            width: pos.width,
            top: pos.top ?? "auto",
            bottom: pos.bottom ?? "auto",
            margin: 0,
            zIndex: 9999,
          } : { visibility: "hidden", position: "fixed", right: "auto", zIndex: 9999 }}
        >
          <input
            type="text"
            placeholder="Search profiles..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            autoFocus
            className="profile-selector-search"
          />
          <div className="profile-selector-list">
            {filtered.length === 0 ? (
              <div className="profile-selector-empty">No profiles</div>
            ) : (
              filtered.map((p) => {
                const isActive = p.name === activeName;
                // Auto-seeded entries (fresh `isann install -model`) are
                // locked from being selected as active until the operator
                // opens the editor and saves once. Save clears NeedsConfig
                // server-side, which removes the lock on next refresh.
                // Clicking the row force-opens the editor instead of
                // changing active, so the only path forward is review.
                const needsReview = !!p.needs_config;
                return (
                  <div
                    key={p.name}
                    className={`profile-selector-item ${isActive ? "active" : ""} ${needsReview ? "needs-review" : ""}`}
                  >
                    <div
                      className="profile-selector-item-main"
                      onClick={() => {
                        if (needsReview) {
                          // Force review — open the editor instead of
                          // activating the profile.
                          if (editable && onEdit) onEdit(p);
                          setOpen(false);
                          setSearch("");
                          return;
                        }
                        onChange(p.name);
                        setOpen(false);
                        setSearch("");
                      }}
                      title={needsReview ? "Profile needs review — open editor and Save to unlock" : undefined}
                    >
                      <div className="profile-selector-item-label">
                        {p.label || p.name}
                        {isActive && " ✓"}
                        {p.architecture && (
                          <span className="profile-selector-item-arch" title={`Base architecture: ${p.architecture}`}>
                            {archDisplayName(p.architecture)}
                          </span>
                        )}
                        {needsReview && (
                          <span className="profile-selector-item-warn">needs review</span>
                        )}
                      </div>
                      <div className="profile-selector-item-preview">{preview(p)}</div>
                    </div>
                  </div>
                );
              })
            )}
          </div>
          {/* Footer hint — CRUD lives in the Profiles tab now. The selector
              is the quick-swap surface (active toggle only); add/edit/delete
              happens in Profiles tab. */}
          <div className="profile-selector-foot">
            Add · edit · delete in the <b>Profiles</b> tab
          </div>
        </div>,
        document.body
      )}
    </div>
  );
}
