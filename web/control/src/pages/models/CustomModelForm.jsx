import React, { useState } from "react";
import Dropdown from "@components/Dropdown/Dropdown";
import { formatSize } from "@utils/format";
import "./ModelForm.scss";

export default function CustomModelForm({ initial, onSave, onClose, t }) {
  const isEdit = !!initial?.id;
  const [name, setName] = useState(initial?.name || "");
  const [service, setService] = useState(initial?.service || "");
  const [downloadUrl, setDownloadUrl] = useState(initial?.download_url || "");
  const [installPath, setInstallPath] = useState(initial?.install_path || "ai/models");
  const [files, setFiles] = useState(() => {
    if (initial?.files && typeof initial.files === "string") {
      try { return JSON.parse(initial.files); } catch { return []; }
    }
    if (Array.isArray(initial?.files)) return initial.files;
    return [];
  });
  const [architecture, setArchitecture] = useState(initial?.architecture || "");
  const [isPublic, setIsPublic] = useState(initial?.is_public || false);
  const [scanning, setScanning] = useState(false);
  const [scanError, setScanError] = useState("");
  const [saving, setSaving] = useState(false);

  const handleScan = async () => {
    if (!downloadUrl) return;
    setScanning(true);
    setScanError("");
    try {
      const resp = await fetch("/gate/v1/software/scan-url", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url: downloadUrl }),
      });
      if (!resp.ok) {
        const err = await resp.json().catch(() => ({}));
        setScanError(err.error || `HTTP ${resp.status}`);
        setScanning(false);
        return;
      }
      const data = await resp.json();
      const scannedFiles = (data.files || []).map(f => ({
        file_name: f.file_name,
        download_url: f.download_url || "",
        install_path: f.install_path || "",
        hash: f.hash || "",
        size_bytes: f.size_bytes || 0,
        selected: true,
      }));
      setFiles(scannedFiles);
    } catch (e) {
      setScanError(e.message);
    }
    setScanning(false);
  };

  const toggleFile = (idx) => {
    setFiles(prev => prev.map((f, i) => i === idx ? { ...f, selected: !f.selected } : f));
  };


  const handleSave = async () => {
    if (!name.trim()) return;
    setSaving(true);
    const selectedFiles = files.filter(f => f.selected !== false).map(({ selected, ...rest }) => rest);
    const payload = {
      name: name.trim(),
      service,
      architecture,
      download_url: downloadUrl,
      install_path: installPath,
      files: JSON.stringify(selectedFiles),
      is_public: isPublic,
    };
    try {
      await onSave(payload, initial?.id);
    } catch {}
    setSaving(false);
  };

  return (
    <div className="model-form-overlay" onClick={onClose}>
      <div className="model-form-modal" onClick={(e) => e.stopPropagation()}>
        <div className="model-form-header">
          <span>{isEdit ? t("models.edit_model") : t("models.add_model")}</span>
          <span className="model-form-close" onClick={onClose}>&times;</span>
        </div>
        <div className="model-form-body">
          <div className="form-field">
            <label className="form-label">{t("models.model_name")}</label>
            <input className="form-input" value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. my-flux-fp16" disabled={isEdit} />
          </div>
          <div className="form-field">
            <label className="form-label">{t("models.install_path")}</label>
            <input className="form-input" value={installPath} onChange={(e) => setInstallPath(e.target.value)} />
          </div>
          <div className="form-field">
            <label className="form-label">{t("models.download_url")}</label>
            <div className="url-row">
              <input className="form-input flex-fill" value={downloadUrl} onChange={(e) => setDownloadUrl(e.target.value)} placeholder="https://huggingface.co/..." />
              <button className="form-btn primary" onClick={handleScan} disabled={scanning || !downloadUrl}>
                {scanning ? t("models.scanning") : t("models.scan_url")}
              </button>
            </div>
            {scanError && <p className="scan-error">{scanError}</p>}
          </div>
          <div className="form-field">
            <label className="form-label">{t("models.service")}</label>
            <Dropdown
              value={service}
              options={[
                { value: "sd-api", label: "sd-api" },
                { value: "llm-api", label: "llm-api" },
                { value: "voice-api", label: "voice-api" },
              ]}
              onChange={(val) => setService(val)}
              placeholder={t("models.select_service")}
            />
          </div>
          <div className="form-field">
            <label className="form-label">{t("models.architecture")}</label>
            <Dropdown
              value={architecture}
              options={[
                { value: "sd15", label: "SD 1.5" },
                { value: "sd21", label: "SD 2.1" },
                { value: "sdxl", label: "SDXL" },
                { value: "sd3", label: "SD 3" },
                { value: "flux", label: "Flux" },
              ]}
              onChange={(val) => setArchitecture(val)}
              placeholder={t("models.select_architecture")}
              disabled={service !== "sd-api"}
            />
          </div>
          {files.length > 0 && (
            <div>
              <label className="form-label">{t("models.files_found")} ({files.length})</label>
              {files.map((f, i) => (
                <div key={i} className="file-entry">
                  <input type="checkbox" checked={f.selected !== false} onChange={() => toggleFile(i)} />
                  <span className="file-entry-name">{f.file_name}</span>
                  <span className="file-entry-size">{formatSize(f.size_bytes)}</span>
                </div>
              ))}
            </div>
          )}
        </div>
        <div className="model-form-footer">
          <button className="form-btn secondary" onClick={onClose}>{t("common.cancel")}</button>
          <button className="form-btn primary" onClick={handleSave} disabled={saving || !name.trim()}>
            {saving ? "Saving..." : t("common.save")}
          </button>
        </div>
      </div>
    </div>
  );
}
