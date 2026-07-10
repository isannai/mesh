import React, { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "@i18n";
import { useAuth } from "../context/AuthContext";
import { truncateAddress } from "@utils/wallet";
import LoginModal from "@components/LoginModal/LoginModal";
import "./header.scss";

export default function Header() {
  const { t } = useTranslation();
  const { auth, role, isLoggedIn, logout } = useAuth();
  const [showLogin, setShowLogin] = useState(false);
  const navigate = useNavigate();

  const roleColor = role === "owner"
    ? "var(--color-success)"
    : role === "admin"
      ? "var(--color-primary)"
      : "var(--color-warning)";

  return (
    <header className="top-header">
      <div className="top-header-inner">
        <div className="header-left">
          <span
            className="header-title"
            onClick={() => navigate("/")}
            title="Home"
          >
            {t("header.title")}
          </span>
          <button
            type="button"
            className="header-search-btn"
            onClick={() => navigate("/search")}
            title="Marketplace search"
          >
            <svg viewBox="0 0 24 24" width="14" height="14" aria-hidden="true">
              <circle cx="10.5" cy="10.5" r="6" fill="none" stroke="currentColor" strokeWidth="2" />
              <line x1="15" y1="15" x2="20" y2="20" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
            </svg>
            <span>Search</span>
          </button>
        </div>
        <div className="header-right">
          {isLoggedIn ? (
            <div className="header-auth-wrap">
              <div className="header-auth-card">
                <span
                  className="header-addr"
                  title={auth?.address}
                >
                  {truncateAddress(auth?.address)}
                </span>
                {role && (
                  <span className="header-role-badge" style={{ color: roleColor }}>{role}</span>
                )}
              </div>
              <button
                className="header-logout-btn"
                onClick={logout}
              >
                {t("auth.logout", "Logout")}
              </button>
            </div>
          ) : (
            <button
              className="header-login-btn"
              onClick={() => setShowLogin(true)}
            >
              {t("auth.connect_wallet", "Connect Wallet")}
            </button>
          )}
        </div>
      </div>
      {showLogin && <LoginModal onClose={() => setShowLogin(false)} />}
    </header>
  );
}
