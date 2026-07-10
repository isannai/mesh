import React, { useState, useEffect, useRef } from "react";
import { listPipelines, deletePipeline } from "./pipelineDB";
import { useTranslation } from "@i18n";
import "./pipelineFileDialog.scss";

// mode: "save" | "load"
export default function PipelineFileDialog({ mode, currentName, onConfirm, onClose }) {
  const { t } = useTranslation();
  const [items, setItems] = useState([]);
  const [name, setName] = useState(currentName || "");
  const [selectedId, setSelectedId] = useState(null);
  const inputRef = useRef(null);

  useEffect(() => {
    listPipelines().then(list => {
      list.sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0));
      setItems(list);
    });
  }, []);

  useEffect(() => {
    if (mode === "save" && inputRef.current) inputRef.current.focus();
  }, [mode]);

  const handleSelect = (item) => {
    setSelectedId(item.id);
    setName(item.name);
  };

  const handleConfirm = () => {
    if (!name.trim()) return;
    onConfirm({ id: selectedId, name: name.trim() });
  };

  const handleKeyDown = (e) => {
    if (e.key === "Enter") handleConfirm();
    if (e.key === "Escape") onClose();
  };

  const isSave = mode === "save";
  const title = isSave ? t("pipeline.save_pipeline") : t("pipeline.load_pipeline");
  const confirmLabel = isSave ? t("pipeline.file_save") : t("pipeline.load");
  const canConfirm = isSave ? name.trim().length > 0 : selectedId != null;

  return (
    <div className="pfd-overlay" onClick={onClose}>
      <div className="pfd-dialog" onClick={e => e.stopPropagation()} onKeyDown={handleKeyDown}>
        <div className="pfd-title">{title}</div>

        <div className="pfd-list">
          {items.length === 0 ? (
            <div className="pfd-empty">{t("pipeline.file_no_saved")}</div>
          ) : items.map(item => (
            <div
              key={item.id}
              className={`pfd-item ${selectedId === item.id ? "selected" : ""}`}
              onClick={() => handleSelect(item)}
              onDoubleClick={() => { handleSelect(item); onConfirm({ id: item.id, name: item.name }); }}
            >
              <span className="pfd-item-icon">&#128196;</span>
              <span className="pfd-item-name">{item.name}</span>
              <span className="pfd-item-date">
                {item.updatedAt ? new Date(item.updatedAt).toLocaleDateString() : ""}
              </span>
              <span
                className="pfd-item-delete"
                title={t("pipeline.file_delete")}
                onClick={async (e) => {
                  e.stopPropagation();
                  await deletePipeline(item.id);
                  const updated = await listPipelines();
                  updated.sort((a, b) => (b.updatedAt || 0) - (a.updatedAt || 0));
                  setItems(updated);
                  if (selectedId === item.id) { setSelectedId(null); setName(""); }
                }}
              >&times;</span>
            </div>
          ))}
        </div>

        {isSave && (
          <div className="pfd-name-row">
            <label className="pfd-name-label">{t("pipeline.file_name_label")}</label>
            <input
              ref={inputRef}
              className="pfd-name-input"
              value={name}
              onChange={e => { setName(e.target.value); setSelectedId(null); }}
              placeholder={t("pipeline.name_placeholder")}
            />
          </div>
        )}

        <div className="pfd-actions">
          <button className="pfd-btn pfd-btn-cancel" onClick={onClose}>{t("common.cancel")}</button>
          <button className="pfd-btn pfd-btn-confirm" disabled={!canConfirm} onClick={handleConfirm}>
            {confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
