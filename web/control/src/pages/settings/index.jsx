import React, { useState } from "react";
import { useTranslation } from "@i18n";
import { useAuth } from "../../context/AuthContext";
import GeneralTab from "./GeneralTab";
import BrokerTab from "./BrokerTab";
import CardsTab from "./CardsTab";
import ApiTab from "./ApiTab";

export default function Settings() {
  const { t } = useTranslation();
  const { role } = useAuth();
  const isOwner = role === "owner";
  const tabs = isOwner ? ["general", "broker", "cards", "api"] : ["general"];
  const [activeTab, setActiveTab] = useState("general");

  const tabLabel = (tab) => {
    if (tab === "general") return t("settings.tab_general");
    if (tab === "broker") return t("settings.tab_broker");
    if (tab === "cards") return "Cards";
    if (tab === "api") return "API";
    return tab;
  };

  return (
    <div className="page">
      <div className="page-header"><h2>{t("settings.title")}</h2></div>
      <div className="page-body">
        <div className="software-tabs mb-16">
          {tabs.map(tab => (
            <button
              key={tab}
              onClick={() => setActiveTab(tab)}
              className={`software-tab ${activeTab === tab ? "active" : ""}`}
            >
              {tabLabel(tab)}
            </button>
          ))}
        </div>
        {activeTab === "general" && <GeneralTab />}
        {activeTab === "broker" && <BrokerTab />}
        {activeTab === "cards" && <CardsTab />}
        {activeTab === "api" && <ApiTab />}
      </div>
    </div>
  );
}
