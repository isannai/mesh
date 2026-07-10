import React, { useState, useRef, useEffect, useCallback } from "react";
import { createPortal } from "react-dom";
import "./index.scss";

// 모델 선택 드롭다운 — 모든 has_model 서비스 공통
// models: [{ name, fileName, version, service }]
// currentModel: 현재 선택된 모델 파일명
// onChange: (fileName) => void
// serviceName: 서비스별 필터링 (예: "sd-api")
//
// Portal-based list: the dropdown list is rendered into document.body
// with position:fixed so ancestor `overflow:hidden` (provider config
// rows, modal bodies, scrollable cards) cannot clip it. Trigger rect
// is recomputed on scroll/resize while open so the list stays pinned
// to the trigger.
export default function ModelSelector({ models, currentModel, onChange, serviceName }) {
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [pos, setPos] = useState(null);
  const ref = useRef(null);
  const triggerRef = useRef(null);
  const listRef = useRef(null);

  const recomputePos = useCallback(() => {
    if (!triggerRef.current) return;
    const r = triggerRef.current.getBoundingClientRect();
    const listH = 320;
    const spaceBelow = window.innerHeight - r.bottom;
    const above = spaceBelow < listH && r.top > spaceBelow;
    setPos({
      left: r.left,
      width: Math.max(r.width, 280),
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
      if (!insideTrigger && !insideList) {
        setOpen(false);
        setSearch("");
      }
    };
    document.addEventListener("mousedown", handler);
    return () => document.removeEventListener("mousedown", handler);
  }, []);

  // 서비스별 필터링: service가 비어있거나 일치하는 모델만 표시
  let serviceFiltered = serviceName
    ? models.filter(m => !m.service || m.service === serviceName)
    : models;

  // currentModel이 있는데 목록에 없으면 추가
  if (currentModel && !serviceFiltered.some(m => m.fileName === currentModel)) {
    serviceFiltered = [{ name: currentModel, fileName: currentModel, version: "", service: "" }, ...serviceFiltered];
  }

  const filtered = serviceFiltered.filter(m =>
    m.fileName.toLowerCase().includes(search.toLowerCase()) || m.name.toLowerCase().includes(search.toLowerCase())
  );

  return (
    <div ref={ref} className="model-selector">
      <div className="model-selector-trigger">
        <button
          ref={triggerRef}
          onClick={() => {
            if (!open) recomputePos();
            setOpen(!open);
          }}
          className={`model-selector-btn ${currentModel ? "" : "empty"}`}
        >
          {currentModel || "-- Select model --"} ▼
        </button>
        {!currentModel && <span className="model-selector-warn">⚠</span>}
      </div>
      {open && createPortal(
        <div
          ref={listRef}
          className="model-selector-dropdown"
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
            placeholder="Search models..."
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            autoFocus
            className="model-selector-search"
          />
          <div className="model-selector-list">
            {filtered.length === 0 ? (
              <div className="model-selector-empty">No models found</div>
            ) : filtered.map((m, i) => (
              <div
                key={i}
                onClick={() => { onChange(m.fileName); setOpen(false); setSearch(""); }}
                className={`model-selector-item ${m.fileName === currentModel ? "active" : ""}`}
              >
                {m.fileName}
                {m.fileName === currentModel && " ✓"}
              </div>
            ))}
          </div>
        </div>,
        document.body
      )}
    </div>
  );
}
