import React, { useState } from "react";
import { useTranslation } from "@i18n";
import { connectMetaMask, buildMessage, signWithMetaMask } from "@utils/wallet";
import { useAuth } from "../../context/AuthContext";
import "./index.scss";

export default function LoginModal({ onClose }) {
  const { t } = useTranslation();
  const { login } = useAuth();
  const [step, setStep] = useState("select"); // select | signing | success | error
  const [address, setAddress] = useState("");
  const [error, setError] = useState("");
  const [sigInfo, setSigInfo] = useState(null);

  const handleMetaMask = async () => {
    try {
      setStep("signing");
      setError("");
      const addr = await connectMetaMask();
      setAddress(addr);
      const { message, expiresAt } = buildMessage("owner", "broker", 3600);
      const sig = await signWithMetaMask(addr, message);

      // Verify with server
      const res = await fetch("/v1/auth/verify", {
        method: "POST",
        headers: {
          "Authorization": "ISANN " + sig,
          "X-ISANN-Message": message,
        },
      });
      const data = await res.json();
      if (!res.ok || !data.role) {
        setError(data.error || "Access denied: not authorized");
        setStep("error");
        return;
      }

      setSigInfo({ address: data.address || addr, message, sig, expiresAt, role: data.role });
      setStep("success");
    } catch (e) {
      setError(e.message || "Connection failed");
      setStep("error");
    }
  };

  const handleSuccess = () => {
    if (sigInfo) {
      login(sigInfo.address, sigInfo.message, sigInfo.sig, sigInfo.expiresAt, sigInfo.role);
    }
    if (onClose) onClose();
  };

  return (
    <div className="login-overlay" onClick={(e) => { if (e.target === e.currentTarget && onClose) onClose(); }}>
      <div className="login-modal">
        <button className="login-modal-close" onClick={onClose}>&times;</button>
        <div className="login-modal-header">
          <h3>{t("auth.connect_wallet", "Connect Wallet")}</h3>
          <p>{t("auth.broker_requires", "Broker requires Owner or Admin authentication")}</p>
        </div>
        <div className="login-modal-body">

          {step === "select" && (
            <div className="wallet-options">
              <div className="wallet-option" onClick={handleMetaMask}>
                <span className="wallet-icon">&#129418;</span>
                <div className="wallet-info">
                  <div className="wallet-name">MetaMask</div>
                  <div className="wallet-desc">{t("auth.metamask_desc", "Connect using browser extension")}</div>
                </div>
                <span className="wallet-arrow">&#8250;</span>
              </div>
              <div className="wallet-option disabled">
                <span className="wallet-icon">&#128279;</span>
                <div className="wallet-info">
                  <div className="wallet-name">WalletConnect</div>
                  <div className="wallet-desc">{t("auth.walletconnect_desc", "Scan with mobile wallet")}</div>
                </div>
                <span className="wallet-coming">{t("auth.coming_soon")}</span>
              </div>
            </div>
          )}

          {step === "signing" && (
            <div className="signing-state">
              <div className="signing-spinner" />
              <div className="signing-text">{t("auth.waiting_sig", "Waiting for signature...")}</div>
              <div className="signing-sub">{t("auth.confirm_wallet", "Please confirm the signing request in your wallet")}</div>
            </div>
          )}

          {step === "success" && sigInfo && (
            <div className="success-state">
              <div className="success-icon">&#10003;</div>
              <div className="success-text">{t("auth.connected", "Connected Successfully")}</div>
              <div className="success-addr">{sigInfo.address}</div>
              <div className="success-role">{t("auth.role_label")} <strong style={{ color: sigInfo.role === "owner" ? "var(--color-success)" : "var(--color-primary)" }}>{sigInfo.role}</strong></div>
              <div className="success-expires">{t("auth.session_expires", "Session expires in 1 hour")}</div>
              <button className="btn-continue" onClick={handleSuccess}>
                {t("auth.continue", "Continue")}
              </button>
            </div>
          )}

          {step === "error" && (
            <div className="error-state">
              <div className="error-icon">&#10060;</div>
              <div className="error-text">{error || t("auth.connection_failed", "Connection Failed")}</div>
              <button className="btn-retry" onClick={() => setStep("select")}>
                {t("auth.try_again", "Try Another Wallet")}
              </button>
            </div>
          )}

        </div>
      </div>
    </div>
  );
}
