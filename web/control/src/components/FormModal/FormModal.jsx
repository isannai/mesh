import React, { useState, useEffect, useRef } from "react";
import { useTranslation } from "../../i18n";
import { computeFileHash } from "@utils/hash";
import { hashFromURL } from "@api/tools";
import Dropdown from "@components/Dropdown/Dropdown";
import "./index.scss";

export default function FormModal({ title, fields, initial, onSave, onClose }) {
  const { t } = useTranslation();
  const [form, setForm] = useState({});
  const fileInputRef = useRef(null);

  useEffect(() => {
    const init = {};
    fields.forEach((f) => {
      init[f.key] = initial?.[f.key] ?? f.defaultValue ?? (f.type === "checkbox" ? false : "");
    });
    setForm(init);
  }, [initial, fields]);

  const handleChange = (key, value, field) => {
    setForm((prev) => {
      const next = { ...prev, [key]: value };
      if (field?.onChange) {
        const extra = field.onChange(value, next);
        if (extra) Object.assign(next, extra);
      }
      return next;
    });
  };

  const handleSubmit = (e) => {
    e.preventDefault();
    onSave(form);
  };

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal-content" onClick={(e) => e.stopPropagation()}>
        <h3>{title}</h3>
        <form onSubmit={handleSubmit}>
          {fields.filter((f) => !f.visibleWhen || f.visibleWhen(form)).map((f) => (
            <div key={f.key} className="form-group">
              <label>{f.label}{f.required && <span className="required"> *</span>}</label>
              {f.type === "checkbox" ? (
                <div
                  className={`toggle-switch ${form[f.key] ? "on" : ""}`}
                  onClick={() => handleChange(f.key, !form[f.key], f)}
                >
                  <div className="toggle-knob" />
                  <span className="toggle-label">{form[f.key] ? t("common.on") : t("common.off")}</span>
                </div>
              ) : f.type === "textarea" ? (
                <textarea
                  value={form[f.key] || ""}
                  onChange={(e) => handleChange(f.key, e.target.value, f)}
                  required={f.required}
                />
              ) : f.type === "select" ? (
                <Dropdown
                  value={form[f.key] || ""}
                  options={f.options || []}
                  onChange={(val) => handleChange(f.key, val, f)}
                  placeholder={t("common.select")}
                  disabled={f.readOnly}
                />
              ) : f.type === "hash" ? (
                <div className="hash-field">
                  <input
                    type="text"
                    value={form[f.key] || ""}
                    onChange={(e) => handleChange(f.key, e.target.value)}
                    placeholder="e.g. 961841e9fb7c"
                    readOnly={f.readOnly}
                  />
                  <button type="button" className="btn btn-hash" onClick={() => fileInputRef.current?.click()}>
                    {t("common.file")}
                  </button>
                  <button type="button" className="btn btn-hash" onClick={async () => {
                    const url = prompt(t("common.enter_url"));
                    if (!url) return;
                    try {
                      const res = await hashFromURL(url);
                      handleChange(f.key, res.hash);
                    } catch (e) {
                      alert("Failed: " + e.message);
                    }
                  }}>
                    URL
                  </button>
                  <input
                    type="file"
                    ref={fileInputRef}
                    className="fm-file-hidden"
                    onChange={async (e) => {
                      const file = e.target.files[0];
                      if (!file) return;
                      const hash = await computeFileHash(file);
                      handleChange(f.key, hash);
                      e.target.value = "";
                    }}
                  />
                </div>
              ) : f.prefix ? (
                <div className="fm-prefix-row">
                  <span className="fm-prefix-label">{f.prefix}</span>
                  <input
                    type="text"
                    className="fm-prefix-input"
                    value={(form[f.key] || "").startsWith(f.prefix) ? (form[f.key] || "").slice(f.prefix.length) : (form[f.key] || "")}
                    onChange={(e) => handleChange(f.key, f.prefix + e.target.value, f)}
                    required={f.required}
                    readOnly={f.readOnly}
                    placeholder={f.placeholder ? f.placeholder.replace(f.prefix, "") : ""}
                  />
                </div>
              ) : (
                <input
                  type={f.type || "text"}
                  value={form[f.key] || ""}
                  onChange={(e) => handleChange(f.key, e.target.value, f)}
                  required={f.required}
                  readOnly={f.readOnly}
                  placeholder={f.placeholder || ""}
                />
              )}
            </div>
          ))}
          <div className="modal-actions">
            <button type="button" className="btn btn-cancel" onClick={onClose}>{t("common.cancel")}</button>
            <button type="submit" className="btn btn-primary">{t("common.save")}</button>
          </div>
        </form>
      </div>
    </div>
  );
}
