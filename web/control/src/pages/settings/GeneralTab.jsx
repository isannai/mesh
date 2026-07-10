import React from "react";
import { useTranslation } from "@i18n";
import { useTheme } from "@theme";
import { usePipelineLayout } from "../../context/PipelineLayoutContext";
import Dropdown from "@components/Dropdown/Dropdown";
import "./settings-form.scss";

const LANGUAGES = [
  { value: "en", label: "English", flag: "US" },
  { value: "ko", label: "한국어", flag: "KR" },
];

export default function GeneralTab() {
  const { t, lang, setLang } = useTranslation();
  const { theme, setTheme } = useTheme();
  const { layout: pipelineLayout, setLayout: setPipelineLayout } = usePipelineLayout();

  const THEMES = [
    { value: "dark", label: t("settings.theme_dark") },
    { value: "light", label: t("settings.theme_light") },
  ];

  const PIPELINE_LAYOUTS = [
    { value: "fullwidth", label: t("settings.pipeline_fullwidth") },
    { value: "centered", label: t("settings.pipeline_centered") },
  ];

  return (
    <div className="detail-card">
      <div className="detail-card-body">
        <div className="detail-card-group">
          <div className="detail-card-group-title">{t("settings.language")}</div>
          <div className="detail-card-row settings-flex-row">
            <span className="detail-card-label">{t("settings.language_desc")}</span>
            <span className="detail-card-value settings-value-wide">
              <Dropdown
                value={lang}
                options={LANGUAGES.map(l => ({ value: l.value, label: `${l.flag}  ${l.label}` }))}
                onChange={(val) => setLang(val)}
                placeholder=""
              />
            </span>
          </div>
        </div>
        <div className="detail-card-group">
          <div className="detail-card-group-title">{t("settings.theme")}</div>
          <div className="detail-card-row settings-flex-row">
            <span className="detail-card-label">{t("settings.theme_desc")}</span>
            <span className="detail-card-value settings-value-wide">
              <Dropdown
                value={theme}
                options={THEMES}
                onChange={(val) => setTheme(val)}
                placeholder=""
              />
            </span>
          </div>
        </div>
        <div className="detail-card-group">
          <div className="detail-card-group-title">{t("settings.pipeline_layout")}</div>
          <div className="detail-card-row settings-flex-row">
            <span className="detail-card-label">{t("settings.pipeline_layout_desc")}</span>
            <span className="detail-card-value settings-value-wide">
              <Dropdown
                value={pipelineLayout}
                options={PIPELINE_LAYOUTS}
                onChange={(val) => setPipelineLayout(val)}
                placeholder=""
              />
            </span>
          </div>
        </div>
      </div>
    </div>
  );
}
