import { useState, useCallback } from "react";

export function useSessionState(key, defaultValue) {
  const [state, setState] = useState(() => {
    try {
      const saved = sessionStorage.getItem(key);
      return saved !== null ? JSON.parse(saved) : defaultValue;
    } catch { return defaultValue; }
  });
  const setAndSave = useCallback((val) => {
    setState(prev => {
      const next = typeof val === "function" ? val(prev) : val;
      try { sessionStorage.setItem(key, JSON.stringify(next)); } catch {}
      return next;
    });
  }, [key]);
  return [state, setAndSave];
}
