import React, { useState, useEffect } from "react";
import Dropdown from "@components/Dropdown/Dropdown";
import "./index.scss";

// Profile add/edit modal — manifest schema 기반 동적 form.
//
// Props:
//   open         — boolean
//   onClose      — () => void
//   onSave       — (payload) => Promise   // payload={name,label,values,set_active}
//   service      — 서비스 이름 (모달 제목용)
//   schema       — manifest.inspect.fields 배열 (없으면 form 비워둠)
//                  [{ key, label, type, from, path }]
//   editing      — null이면 "Add", 기존 profile 객체면 "Edit"
//   existing     — 기존 profile 들 (배열). Add 모드에서 name 중복 검증용
//   busy         — Save 중 disable
//
// from="api" 필드는 자동 감지라 form 에서 제외 (입력 의미 없음).
// Architecture vocabulary mirrors profile.NormalizeArchitecture in
// pkg/profile/profile.go. Operators see the canonical IDs (sd15 / sdxl
// / qwen2 / ...) so what's saved exactly matches what LoRA folder
// routing expects. "" = unset (auto-detect / leave blank).
const ARCH_OPTIONS = [
  { value: "",        label: "— auto-detect —" },
  { value: "sd15",    label: "SD 1.5" },
  { value: "sd21",    label: "SD 2.1" },
  { value: "sdxl",    label: "SDXL" },
  { value: "sd3",     label: "SD 3" },
  { value: "flux",    label: "Flux" },
  { value: "qwen2",   label: "Qwen2" },
  { value: "llama3",  label: "Llama 3" },
  { value: "mistral", label: "Mistral" },
  { value: "mixtral", label: "Mixtral" },
  { value: "other",   label: "Other" },
];

export default function ProfileEditor({
  open, onClose, onSave,
  service, schema, editing, existing, busy,
}) {
  const [name, setName] = useState("");
  const [label, setLabel] = useState("");
  const [architecture, setArchitecture] = useState("");
  const [values, setValues] = useState({});
  const [setActive, setSetActive] = useState(true);
  const [errors, setErrors] = useState({});

  // 초기화 — editing 이 바뀌거나 모달이 열릴 때.
  useEffect(() => {
    if (!open) return;
    if (editing) {
      setName(editing.name || "");
      setLabel(editing.label || "");
      setArchitecture(editing.architecture || "");
      setValues({ ...(editing.values || {}) });
      setSetActive(false);   // 편집 시엔 active 변경 의도 없는 경우가 일반적
    } else {
      setName("");
      setLabel("");
      setArchitecture("");
      const initial = {};
      (Array.isArray(schema) ? schema : []).forEach(f => {
        if (f.from === "api") return;
        // 빈 값 placeholder
        initial[f.key] = "";
      });
      setValues(initial);
      setSetActive(true);
    }
    setErrors({});
  }, [open, editing, schema]);

  if (!open) return null;

  const editableFields = (Array.isArray(schema) ? schema : []).filter(f => f.from !== "api");

  const validate = () => {
    const e = {};
    if (!editing) {
      // 추가일 때만 name 검증 (편집은 name 잠금).
      // 모델 패키지 디렉토리 이름과 일치시키기 위해 대소문자 + 숫자 +
      // dash/dot/underscore 허용 (Qwen2.5-14B-Instruct-Q4_K_M 같은 이름).
      if (!name) e.name = "required";
      else if (!/^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$/.test(name)) {
        e.name = "letters, digits, dash/dot/underscore (max 100 chars, no leading punctuation)";
      } else if (Array.isArray(existing) && existing.some(p => p.name === name)) {
        e.name = `Name already in use — pick a different name to avoid overwriting "${name}"`;
      }
    }
    if (!label) e.label = "required";

    editableFields.forEach(f => {
      const raw = values[f.key];
      if (raw === undefined || raw === null || raw === "") return;   // 빈 값은 OK
      if (f.type === "int") {
        if (!/^-?\d+$/.test(String(raw))) e[`v_${f.key}`] = "integer required";
      } else if (f.type === "float") {
        if (isNaN(parseFloat(raw))) e[`v_${f.key}`] = "number required";
      } else if (f.type === "bool") {
        const s = String(raw).toLowerCase();
        if (s !== "true" && s !== "false") e[`v_${f.key}`] = "true or false";
      }
    });
    setErrors(e);
    return Object.keys(e).length === 0;
  };

  const handleSave = async () => {
    if (!validate()) return;
    // 빈 문자열 값은 보내지 않음
    const cleanValues = {};
    Object.entries(values).forEach(([k, v]) => {
      if (v === "" || v === null || v === undefined) return;
      // 숫자/불 type 캐스팅
      const f = editableFields.find(x => x.key === k);
      if (f?.type === "int") cleanValues[k] = parseInt(v, 10);
      else if (f?.type === "float") cleanValues[k] = parseFloat(v);
      else if (f?.type === "bool") cleanValues[k] = String(v).toLowerCase() === "true";
      else cleanValues[k] = v;
    });
    await onSave({
      name, label, architecture,
      values: cleanValues, set_active: setActive,
    });
  };

  return (
    <div className="profile-editor-overlay" onClick={onClose}>
      <div className="profile-editor" onClick={e => e.stopPropagation()}>
        <div className="profile-editor-head">
          <div className="profile-editor-title">
            {editing ? "Edit Profile" : "Add Profile"}
            <span className="profile-editor-svc"> · {service}</span>
          </div>
          <span className="profile-editor-close" onClick={onClose}>✕</span>
        </div>

        <div className="profile-editor-body">
          <div className="form-row">
            <label className="form-label">Name *</label>
            <input
              className={`form-input ${errors.name ? "err" : ""}`}
              value={name}
              onChange={e => setName(e.target.value)}
              disabled={!!editing}
              placeholder="Qwen2.5-14B-Instruct-Q4_K_M"
            />
            <div className="form-hint">
              {errors.name || "letters / digits / dash / dot / underscore. Cannot be changed."}
            </div>
          </div>

          {editing?.package_ref && (
            <div className="form-row">
              <label className="form-label">Package</label>
              <input
                className="form-input"
                value={editing.package_ref}
                disabled
                readOnly
              />
              <div className="form-hint">
                Set at isann install time. To change, re-import the model or switch to a different profile as active.
              </div>
            </div>
          )}

          <div className="form-row">
            <label className="form-label">Architecture</label>
            <Dropdown
              value={architecture}
              options={ARCH_OPTIONS}
              onChange={(v) => setArchitecture(v)}
              placeholder="— auto-detect —"
            />
            <div className="form-hint">
              Base model family — drives LoRA folder routing and compatibility filtering.
              Auto-detected from package metadata when available; override here if needed.
            </div>
          </div>

          <div className="form-row">
            <label className="form-label">Label *</label>
            <input
              className={`form-input ${errors.label ? "err" : ""}`}
              value={label}
              onChange={e => setLabel(e.target.value)}
              placeholder="Qwen 14B · 32K ctx"
            />
            <div className="form-hint">{errors.label || "Name shown in the UI"}</div>
          </div>

          {editableFields.length > 0 && (
            <div className="form-section-title">Values</div>
          )}

          {editableFields.map(f => {
            const err = errors[`v_${f.key}`];
            const cur = values[f.key] ?? "";
            const onChange = (val) => setValues(v => ({ ...v, [f.key]: val }));
            return (
              <div className="form-row" key={f.key}>
                <label className="form-label">
                  {f.label || f.key}
                  <span className="form-type">{f.type ? ` (${f.type})` : ""}</span>
                </label>
                {f.type === "bool" ? (
                  <Dropdown
                    value={String(cur)}
                    options={[
                      { value: "true",  label: "true"  },
                      { value: "false", label: "false" },
                    ]}
                    onChange={(v) => onChange(v)}
                    placeholder="--"
                  />
                ) : Array.isArray(f.options) && f.options.length > 0 ? (
                  <Dropdown
                    value={String(cur)}
                    options={f.options.map(o => typeof o === "string" ? { value: o, label: o } : o)}
                    onChange={(v) => onChange(v)}
                    placeholder=""
                  />
                ) : (f.type === "int" || f.type === "float") ? (
                  <input
                    className={`form-input ${err ? "err" : ""}`}
                    type="number"
                    step={f.type === "float" ? "any" : "1"}
                    value={cur}
                    onChange={e => onChange(e.target.value)}
                  />
                ) : (
                  <input
                    className={`form-input ${err ? "err" : ""}`}
                    value={cur}
                    onChange={e => onChange(e.target.value)}
                    placeholder={f.key}
                  />
                )}
                {err && <div className="form-hint err">{err}</div>}
              </div>
            );
          })}

          <div className="form-row form-checkbox">
            <input
              id="profile-set-active"
              type="checkbox"
              checked={setActive}
              onChange={e => setSetActive(e.target.checked)}
            />
            <label htmlFor="profile-set-active">
              Set as active after save (service auto-restarts)
            </label>
          </div>
        </div>

        <div className="profile-editor-foot">
          <button className="btn btn-secondary" onClick={onClose} disabled={busy}>Cancel</button>
          <button className="btn btn-primary" onClick={handleSave} disabled={busy}>
            {busy ? "Saving…" : "Save"}
          </button>
        </div>
      </div>
    </div>
  );
}
