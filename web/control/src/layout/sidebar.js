import React, { useState, useEffect } from "react";
import { NavLink, useLocation, useNavigate } from "react-router-dom";
import "@styles/layout/index.scss";
import { useTranslation } from "@i18n";
import { useAuth } from "../context/AuthContext";
import { fetchCards, isCardEnabled } from "../api/cards";
import "./sidebar.scss";

const workspaceItems = [
  { path: "/nodes", labelKey: "nav.nodes", cardId: "nodes" },
];

const managementItems = [
  { path: "/my-nodes", labelKey: "nav.my_nodes", cardId: "my-nodes" },
];


const resourcesItems = [
  { path: "/docs/api", labelKey: "nav.api_reference", cardId: "api" },
  { path: "/docs", labelKey: "nav.docs", cardId: "resources" },
];

export default function Sidebar() {
  const { t } = useTranslation();
  const { role } = useAuth();
  const isAdmin = role === "owner" || role === "admin";
  const location = useLocation();
  const navigate = useNavigate();
  const isApiDocs = location.pathname === "/docs/api";

  const [cardsConfig, setCardsConfig] = useState({});
  useEffect(() => { fetchCards().then(setCardsConfig); }, []);
  const cardEnabled = (id) => !id || isCardEnabled(id, cardsConfig);

  const [collapsed, setCollapsed] = useState(() => {
    try { return JSON.parse(sessionStorage.getItem("sidebar.collapsed") || "{}"); } catch { return {}; }
  });

  const toggle = (key) => {
    setCollapsed(prev => {
      const next = { ...prev, [key]: !prev[key] };
      sessionStorage.setItem("sidebar.collapsed", JSON.stringify(next));
      return next;
    });
  };

  const renderGroup = (key, label, items) => {
    const visible = items.filter(i => cardEnabled(i.cardId));
    if (visible.length === 0) return null;
    return (
      <>
        <div className="nav-group-label" onClick={() => toggle(key)}>
          <span>{label}</span>
          <span className={`nav-group-arrow${collapsed[key] ? " collapsed" : ""}`}>▼</span>
        </div>
        <div className={`nav-group-items${collapsed[key] ? " collapsed" : ""}`}>
          {visible.map((item) => (
            <NavLink
              key={item.path}
              to={item.path}
              end={item.path === "/"}
              className={({ isActive }) => `nav-item${isActive ? " active" : ""}`}
            >
              {t(item.labelKey)}
            </NavLink>
          ))}
        </div>
      </>
    );
  };

  // API Reference 모드: 로고 유지 + 카테고리별 페이지 전환
  const [apiCollapsed, setApiCollapsed] = useState({});
  const toggleApi = (key) => setApiCollapsed(prev => ({ ...prev, [key]: !prev[key] }));

  if (isApiDocs) {
    const cats = [
      { key: "image", label: "Image Generation", items: ["Health / Status", "Queue Stats", "Models", "txt2img (Async)", "img2img / inpaint (Async)", "Job Tracking", "Delete Job", "Job Result", "Result Download"] },
      { key: "llm", label: "LLM", items: ["Health Check", "Queue Stats", "Models", "Chat Completion (Sync)", "Text Completion (Sync)", "Embeddings (Async)", "Chat Completion (Async)", "Job Tracking", "Delete Job", "Job Result", "Result Download"] },
      { key: "vllm", label: "vLLM", items: ["Health Check", "Queue Stats", "Models", "Chat Completion (Sync)", "Text Completion (Sync)", "Embeddings (Async)", "Chat Completion (Async)", "Job Tracking", "Delete Job", "Job Result", "Result Download"] },
      { key: "pipeline", label: "Pipeline", items: ["Pipeline Execute", "List Jobs", "Job Status", "Job Result", "Cancel Job", "Entity Types"] },
      { key: "node", label: "Node", items: [
        "Provider Versions", "Provider Processes",
        "Log Files", "Log Tail",
        "Profiles", "Sync Status",
        "Provider Queue Stats", "Submit Job", "Provider Job by ID", "Provider Job Result",
        "Provider Output File",
        "Get Provider Config", "Update Provider Config",
        "Set Active Profile", "Create Profile", "Delete Profile",
        "Set Emblem", "Delete Emblem",
        "Scan Local",
        "Force Re-register", "Sync Create",
        "Install Software", "Uninstall Software",
        "Start Service", "Stop Service", "Kill Process",
      ] },
      { key: "admin", label: "Broker", items: [
        "Health Check", "Info", "Node ID",
        "Node Discovery", "Metrics", "Search Nodes",
        "Cards", "API Policy",
        "Auth Verify", "Node Auth",
      ] },
      { key: "broker-admin", label: "Broker Admin", items: [
        "Admin Status",
        "Get Config", "Update Config",
        "Get Logs", "Logs Stream",
        "Log Files (Broker)", "Log Tail (Broker)",
        "Update Cards", "Update API Features", "API Features Preset",
      ] },
      { key: "gate", label: "Gate", items: [
        "List Rendezvous", "All Nodes Catalog", "Curated Models",
        "Software Catalog", "Software Package",
        "Scan URL", "Scan File",
      ] },
      { key: "rendezvous", label: "Rendezvous", items: ["Rendezvous Health", "List Nodes", "Node by ID", "List Metrics", "Metrics by Node ID"] },
      { key: "voice", label: "Voice", items: [], soon: true },
    ];
    return (
      <div className="sidebar">
        <nav className="sidebar-nav">
          <div className="nav-group-label nav-api-title">API Reference</div>
          {cats.map(cat => (
            <div key={cat.key}>
              <div className={`nav-group-label ${cat.soon ? "nav-group-label-static" : "nav-group-label-clickable"}`} onClick={() => { if (!cat.soon) { toggleApi(cat.key); window.__apiSetCategory?.(cat.key); } }}>
                <span>{cat.label} {cat.soon ? <span className="nav-soon-text">(soon)</span> : ""}</span>
                {!cat.soon && cat.items.length > 0 && <span className={`nav-group-arrow${apiCollapsed[cat.key] ? " collapsed" : ""}`}>▼</span>}
              </div>
              {!cat.soon && !apiCollapsed[cat.key] && (
                <div className="nav-group-items">
                  {cat.items.map(item => {
                    const m = item.match(/^(.*?)\s*\((Sync|Async|SSE)\)\s*$/i);
                    const label = m ? m[1] : item;
                    const tag = m ? m[2] : null;
                    return (
                      <a key={item} className="nav-item nav-api-item" onClick={() => {
                        window.__apiSetCategory?.(cat.key);
                        const targetId = "api-" + item.toLowerCase().replace(/[^a-z0-9]+/g, "-");
                        const tryScroll = (attempt = 0) => {
                          const el = document.getElementById(targetId);
                          if (el) { el.scrollIntoView({ behavior: "smooth", block: "start" }); return; }
                          if (attempt < 20) requestAnimationFrame(() => tryScroll(attempt + 1));
                        };
                        requestAnimationFrame(() => tryScroll());
                      }}>
                        <span className="nav-api-item-label">{label}</span>
                        {tag && <span className={`nav-api-item-tag tag-${tag.toLowerCase()}`}>{tag}</span>}
                      </a>
                    );
                  })}
                </div>
              )}
            </div>
          ))}
        </nav>
      </div>
    );
  }

  // 일반 모드
  return (
    <div className="sidebar">
      <div className="sidebar-logo">
        <span className="sidebar-logo-text">IANN</span>
      </div>
      <nav>
        {renderGroup("workspace", t("nav.workspace"), workspaceItems)}
        <div className="nav-separator" />
        {renderGroup("management", t("nav.management"), managementItems)}
        <div className="nav-separator" />
        {renderGroup("resources", t("nav.resources"), resourcesItems)}
        <div className="nav-separator" />
        {cardEnabled("logs") && (
          <NavLink
            to="/system/logs"
            className={({ isActive }) => `nav-item${isActive ? " active" : ""}`}
          >
            {t("nav.logs")}
          </NavLink>
        )}
        {isAdmin && cardEnabled("settings") && (
          <NavLink
            to="/settings"
            className={({ isActive }) => `nav-item${isActive ? " active" : ""}`}
          >
            {t("nav.settings")}
          </NavLink>
        )}
      </nav>
    </div>
  );
}
