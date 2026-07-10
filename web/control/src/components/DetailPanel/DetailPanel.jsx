import React from "react";
import { useTranslation } from "../../i18n";
import "./index.scss";

export default function DetailPanel({ data, fields, onClose }) {
  const { t } = useTranslation();
  if (!data) return null;
  return (
    <div className="detail-panel">
      <div className="detail-header">
        <h3>{t("common.detail")}</h3>
        <button className="detail-close" onClick={onClose}>&times;</button>
      </div>
      <div className="detail-grid">
        {fields.map((f) => (
          <div key={f.key} className="detail-item">
            <div className="detail-label">{f.label}</div>
            <div className="detail-value">
              {f.render ? f.render(data[f.key], data) : (data[f.key] || "-")}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
