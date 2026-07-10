import React, { createContext, useContext, useState, useEffect, useCallback } from "react";
import { loadSession, saveSession, clearSession } from "@utils/wallet";

const AuthContext = createContext(null);

export function AuthProvider({ children }) {
  const [auth, setAuth] = useState(() => loadSession());
  const [role, setRole] = useState(() => {
    try {
      const raw = localStorage.getItem("iann_auth_broker_role");
      return raw || null;
    } catch {
      return null;
    }
  });

  // Periodic expiry check
  useEffect(() => {
    if (!auth) return;
    const remaining = auth.expiresAt * 1000 - Date.now();
    if (remaining <= 0) {
      clearSession();
      localStorage.removeItem("iann_auth_broker_role");
      setAuth(null);
      setRole(null);
      return;
    }
    const timer = setTimeout(() => {
      clearSession();
      localStorage.removeItem("iann_auth_broker_role");
      setAuth(null);
      setRole(null);
    }, remaining);
    return () => clearTimeout(timer);
  }, [auth]);

  const login = useCallback((address, message, sig, expiresAt, userRole) => {
    const session = saveSession(address, message, sig, expiresAt);
    if (userRole) {
      localStorage.setItem("iann_auth_broker_role", userRole);
      setRole(userRole);
    }
    setAuth(session);
  }, []);

  const logout = useCallback(() => {
    clearSession();
    localStorage.removeItem("iann_auth_broker_role");
    setAuth(null);
    setRole(null);
  }, []);

  const isLoggedIn = !!auth?.sig;

  return (
    <AuthContext.Provider value={{ auth, role, login, logout, isLoggedIn }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
