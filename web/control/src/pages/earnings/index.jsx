import React from "react";
import { useTranslation } from "@i18n";

export default function Earnings() {
  const { t } = useTranslation();
  return (
    <div className="page">
      <div className="page-header"><h2>{t("earnings.title")}</h2></div>
      <div className="page-body">
        <p className="text-muted">{t("dashboard.coming_soon")}</p>
      </div>
    </div>
  );
}
