import React, { useState, useEffect, useCallback } from "react";
import { useTranslation } from "@i18n";
import { RestartBadge } from "@components/Toggle";
import Dropdown from "@components/Dropdown/Dropdown";
import { getAuthHeaders } from "@utils/wallet";
import "./ProviderAuthTab.scss";

const isValidAddress = (addr) => /^0x[a-fA-F0-9]{40}$/.test(addr || "");

function AddressListEditor({ label, hint, items, onAdd, onRemove, addressError, setAddressError }) {
  const [draft, setDraft] = useState("");

  const handleAdd = () => {
    const v = draft.trim();
    if (!v) return;
    if (!isValidAddress(v)) {
      setAddressError(label);
      return;
    }
    if ((items || []).some(a => a.toLowerCase() === v.toLowerCase())) {
      setDraft("");
      return;
    }
    onAdd(v);
    setDraft("");
    setAddressError(null);
  };

  return (
    <div className="detail-card-group">
      <div className="detail-card-group-title">{label}</div>
      {hint && (
        <div className="address-hint">{hint}</div>
      )}
      <table className="address-table">
        <tbody>
          {(items || []).map((addr, i) => (
            <tr key={i} className="row-divider">
              <td className="col-address">{addr}</td>
              <td className="col-action">
                <button className="btn-remove-addr" onClick={() => onRemove(i)}>✕</button>
              </td>
            </tr>
          ))}
          <tr>
            <td className="cell-input">
              <input
                type="text"
                className="addr-input"
                placeholder="0x..."
                value={draft}
                onChange={e => { setDraft(e.target.value); if (addressError === label) setAddressError(null); }}
                onKeyDown={e => { if (e.key === "Enter") { e.preventDefault(); handleAdd(); } }}
              />
            </td>
            <td className="cell-input-action">
              <button className="btn-add-addr" onClick={handleAdd}>+</button>
            </td>
          </tr>
        </tbody>
      </table>
      {addressError === label && (
        <div className="invalid-address-msg">Invalid Ethereum address</div>
      )}
    </div>
  );
}

export default function ProviderAuthTab({ nodeId }) {
  const { t } = useTranslation();
  const [auth, setAuth] = useState(null);
  const [original, setOriginal] = useState(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState(null);
  const [addressError, setAddressError] = useState(null);
  const [dirty, setDirty] = useState(false);
  const [savedAt, setSavedAt] = useState(0);

  const load = useCallback(async () => {
    if (!nodeId) return;
    setLoading(true);
    setError(null);
    try {
      const res = await fetch(`/node/${encodeURIComponent(nodeId)}/provider/auth`, {
        headers: { ...getAuthHeaders() },
      });
      if (!res.ok) {
        if (res.status === 403) throw new Error(t("settings.auth_owner_only"));
        throw new Error(`Load failed (${res.status})`);
      }
      const data = await res.json();
      setAuth(data);
      setOriginal(JSON.parse(JSON.stringify(data)));
      setDirty(false);
    } catch (e) {
      setError(e.message);
      setAuth(null);
    }
    setLoading(false);
  }, [nodeId, t]);

  useEffect(() => { load(); }, [load]);

  const update = (patch) => {
    setAuth(prev => ({ ...prev, ...patch }));
    setDirty(true);
  };

  const modeChanged = dirty && auth && original && auth.mode !== original.mode;

  const handleSave = async () => {
    setSaving(true);
    setError(null);
    try {
      const res = await fetch(`/node/${encodeURIComponent(nodeId)}/provider/auth`, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...getAuthHeaders() },
        body: JSON.stringify(auth),
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error || `Save failed (${res.status})`);
      }
      setOriginal(JSON.parse(JSON.stringify(auth)));
      setDirty(false);
      setSavedAt(Date.now());
    } catch (e) {
      setError(e.message);
    }
    setSaving(false);
  };

  if (loading) {
    return <div className="detail-card"><div className="detail-card-body"><p className="auth-loading">{t("settings.loading")}</p></div></div>;
  }
  if (error && !auth) {
    return <div className="detail-card"><div className="detail-card-body"><p className="auth-error-inline">{error}</p></div></div>;
  }
  if (!auth) return null;

  return (
    <div className="detail-card">
      <div className="detail-card-body">
        {error && (
          <div className="auth-error-banner">
            {error}
          </div>
        )}

        <div className="detail-card-group">
          <div className="detail-card-group-title">{t("settings.auth_mode")}</div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.auth_mode")}</span>
            <span className="detail-card-value auth-mode-value">
              <Dropdown
                value={auth.mode || "open"}
                options={[
                  { value: "open", label: t("settings.auth_mode_open") },
                  { value: "protected", label: t("settings.auth_mode_protected") },
                ]}
                onChange={v => update({ mode: v })}
                placeholder=""
              />
            </span>
          </div>
        </div>

        <div className="detail-card-group">
          <div className="detail-card-group-title">{t("settings.auth_owner")}</div>
          <div className="address-hint">{t("settings.auth_owner_hint")}</div>
          <div className="detail-card-row">
            <span className="detail-card-label">{t("settings.auth_owner")}</span>
            <span className="detail-card-value auth-owner-display">
              {auth.owner || "—"}
            </span>
          </div>
        </div>

        <div className="detail-card-group">
          <div className="detail-card-group-title">{t("settings.auth_issuer")}</div>
          <div className="address-hint">{t("settings.auth_issuer_hint")}</div>
          <input
            type="text"
            className="auth-issuer-input"
            value={auth.issuer || ""}
            onChange={e => update({ issuer: e.target.value })}
            placeholder="0x..."
          />
          {auth.issuer && !isValidAddress(auth.issuer) && (
            <div className="invalid-address-msg">{t("settings.auth_invalid_address")}</div>
          )}
        </div>

        <AddressListEditor
          label={t("settings.auth_admins")}
          items={auth.admins}
          addressError={addressError}
          setAddressError={setAddressError}
          onAdd={(addr) => update({ admins: [...(auth.admins || []), addr] })}
          onRemove={(i) => update({ admins: (auth.admins || []).filter((_, idx) => idx !== i) })}
        />

        <AddressListEditor
          label={t("settings.auth_users")}
          items={auth.users}
          addressError={addressError}
          setAddressError={setAddressError}
          onAdd={(addr) => update({ users: [...(auth.users || []), addr] })}
          onRemove={(i) => update({ users: (auth.users || []).filter((_, idx) => idx !== i) })}
        />

        <div className="auth-save-bar">
          <button className="btn btn-primary" onClick={handleSave} disabled={!dirty || saving || (auth.issuer && !isValidAddress(auth.issuer))}>
            {saving ? t("settings.saving") : t("common.save")}
          </button>
          {savedAt > 0 && !dirty && Date.now() - savedAt < 3000 && (
            <span className="saved-tick">✓</span>
          )}
        </div>
      </div>
    </div>
  );
}
