import React, { createContext, useContext, useState } from "react";
import en from "./en.json";
import ko from "./ko.json";

const langs = { en, ko };
const LangContext = createContext();

export function LanguageProvider({ children }) {
  const [lang, setLang] = useState(localStorage.getItem("iann_lang") || "en");
  const changeLang = (l) => { setLang(l); localStorage.setItem("iann_lang", l); };
  const t = (key, vars) => {
    const keys = key.split(".");
    let val = langs[lang];
    for (const k of keys) { val = val?.[k]; }
    if (val == null) return key;
    if (vars && typeof val === "string") {
      return val.replace(/\{(\w+)\}/g, (_, k) => (vars[k] != null ? String(vars[k]) : `{${k}}`));
    }
    return val;
  };
  return <LangContext.Provider value={{ t, lang, setLang: changeLang }}>{children}</LangContext.Provider>;
}

export function useTranslation() { return useContext(LangContext); }
