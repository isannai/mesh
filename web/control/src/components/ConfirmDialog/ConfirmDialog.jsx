import React from "react";
import { useTranslation } from "../../i18n";
import "./index.scss";

export default function ConfirmDialog({ message, onConfirm, onCancel }) {
  const { t } = useTranslation();
  return (
    <div className="modal-overlay" onClick={onCancel}>
      <div className="confirm-dialog" onClick={(e) => e.stopPropagation()}>
        <p>{message}</p>
        <div className="confirm-actions">
          <button className="btn btn-cancel" onClick={onCancel}>{t("common.cancel")}</button>
          <button className="btn btn-delete" onClick={onConfirm}>{t("common.delete")}</button>
        </div>
      </div>
    </div>
  );
}
