import React from "react";
import { useTranslation } from "@i18n";

export default function Footer() {
  const { t } = useTranslation();
  return (
    <footer className="app-footer">
      <div className="app-footer-inner">
        <span>{t("footer.version")}</span>
        <span className="footer-sep">|</span>
        <span>{t("footer.description")}</span>
      </div>
    </footer>
  );
}
