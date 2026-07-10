// Home / workspace landing page — based on
// docs/TODO/search page/home-mockup.html.
//
// Layout:
//   ① Hero — title + tagline + subtitle + big search bar + filter chips
//   ② Trending Models — auto-aggregated from currently-loaded service models
//   ③ Workspace — existing tool cards (Search card intentionally absent —
//      hero search bar is the entry point)

import React, { useState, useEffect } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "@i18n";
import { fetchNodes, fetchCuratedModels } from "../../api/nodes";
import { fetchCards, isCardEnabled } from "../../api/cards";
import { useAuth } from "../../context/AuthContext";
import { detectQueryType } from "@utils/searchDetect";
import ModelLabel from "@components/ModelLabel/ModelLabel";
import Skeleton from "@components/Skeleton/Skeleton";
import { modelSearchQuery } from "@utils/modelPath";

const ICONS = {
  install: (
    <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
      <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4" />
      <polyline points="7 10 12 15 17 10" />
      <line x1="12" y1="15" x2="12" y2="3" />
    </svg>
  ),
  setup: (
    <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
      <path d="M12 2l3.09 6.26L22 9.27l-5 4.87 1.18 6.88L12 17.77l-6.18 3.25L7 14.14 2 9.27l6.91-1.01L12 2z" />
    </svg>
  ),
  nodes: (
    <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
      <rect x="2" y="2" width="20" height="8" rx="2" />
      <rect x="2" y="14" width="20" height="8" rx="2" />
      <circle cx="6" cy="6" r="1" fill="currentColor" />
      <circle cx="6" cy="18" r="1" fill="currentColor" />
    </svg>
  ),
  management: (
    <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
      <path d="M14.7 6.3a1 1 0 000 1.4l1.6 1.6a1 1 0 001.4 0l3.77-3.77a6 6 0 01-7.94 7.94l-6.91 6.91a2.12 2.12 0 01-3-3l6.91-6.91a6 6 0 017.94-7.94l-3.76 3.76z" />
    </svg>
  ),
  pipeline: (
    <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
      <circle cx="5" cy="12" r="2" /><circle cx="12" cy="5" r="2" />
      <circle cx="12" cy="19" r="2" /><circle cx="19" cy="12" r="2" />
      <path d="M7 12h4M14 12h3M12 7v4M12 15v2" />
    </svg>
  ),
  resources: (
    <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
      <path d="M4 19.5A2.5 2.5 0 016.5 17H20" />
      <path d="M6.5 2H20v20H6.5A2.5 2.5 0 014 19.5v-15A2.5 2.5 0 016.5 2z" />
      <line x1="9" y1="7" x2="17" y2="7" />
      <line x1="9" y1="11" x2="15" y2="11" />
    </svg>
  ),
  api: (
    <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
      <polyline points="16 18 22 12 16 6" />
      <polyline points="8 6 2 12 8 18" />
      <line x1="14" y1="4" x2="10" y2="20" />
    </svg>
  ),
  settings: (
    <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
      <path d="M12.22 2h-.44a2 2 0 00-2 2v.18a2 2 0 01-1 1.73l-.43.25a2 2 0 01-2 0l-.15-.08a2 2 0 00-2.73.73l-.22.38a2 2 0 00.73 2.73l.15.1a2 2 0 011 1.72v.51a2 2 0 01-1 1.74l-.15.09a2 2 0 00-.73 2.73l.22.38a2 2 0 002.73.73l.15-.08a2 2 0 012 0l.43.25a2 2 0 011 1.73V20a2 2 0 002 2h.44a2 2 0 002-2v-.18a2 2 0 011-1.73l.43-.25a2 2 0 012 0l.15.08a2 2 0 002.73-.73l.22-.39a2 2 0 00-.73-2.73l-.15-.08a2 2 0 01-1-1.74v-.5a2 2 0 011-1.74l.15-.09a2 2 0 00.73-2.73l-.22-.38a2 2 0 00-2.73-.73l-.15.08a2 2 0 01-2 0l-.43-.25a2 2 0 01-1-1.73V4a2 2 0 00-2-2z" />
      <circle cx="12" cy="12" r="3" />
    </svg>
  ),
  logs: (
    <svg width="36" height="36" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5">
      <path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z" />
      <polyline points="14 2 14 8 20 8" />
      <line x1="8" y1="13" x2="16" y2="13" />
      <line x1="8" y1="17" x2="16" y2="17" />
      <line x1="8" y1="9" x2="11" y2="9" />
    </svg>
  ),
};

function buildCards(t) {
  return [
    {
      id: "nodes", icon: ICONS.nodes, path: "/nodes", color: "var(--color-primary)",
      title: t("workspace.card_nodes_title"),
      description: t("workspace.card_nodes_desc"),
    },
    {
      id: "management", icon: ICONS.management, path: "/my-nodes", color: "var(--color-success)",
      title: t("workspace.card_management_title"),
      description: t("workspace.card_management_desc"),
      sub: [t("workspace.card_management_sub_my_nodes"), t("workspace.card_management_sub_models")],
    },
    {
      id: "pipeline", icon: ICONS.pipeline, path: "/studios/pipeline", color: "var(--color-purple)",
      title: t("workspace.card_pipeline_title"),
      description: t("workspace.card_pipeline_desc"),
      sub: ["React Flow", "LLM", "SD", "Transform"],
    },
    {
      id: "resources", icon: ICONS.resources, path: "/docs", color: "var(--color-primary)",
      title: t("workspace.card_resources_title"),
      description: t("workspace.card_resources_desc"),
      sub: [t("workspace.card_resources_sub_docs"), t("workspace.card_resources_sub_guides")],
    },
    {
      id: "api", icon: ICONS.api, path: "/docs/api", color: "var(--color-success)",
      title: t("workspace.card_api_title"),
      description: t("workspace.card_api_desc"),
      sub: [t("workspace.card_api_sub_image"), t("workspace.card_api_sub_llm"), t("workspace.card_api_sub_node")],
    },
    {
      id: "install", icon: ICONS.install, path: "/welcome", color: "var(--color-purple)",
      title: t("workspace.card_install_title"),
      description: t("workspace.card_install_desc"),
      sub: [t("workspace.card_install_sub_installer"), t("workspace.card_install_sub_wallet"), t("workspace.card_install_sub_onboarding")],
    },
    {
      id: "logs", icon: ICONS.logs, path: "/system/logs", color: "var(--color-danger)",
      title: t("workspace.card_logs_title"),
      description: t("workspace.card_logs_desc"),
      sub: [t("workspace.card_logs_sub_live"), t("workspace.card_logs_sub_search")],
    },
    {
      id: "settings", icon: ICONS.settings, path: "/settings", color: "var(--color-warning)",
      title: t("workspace.card_settings_title"),
      description: t("workspace.card_settings_desc"),
      sub: [t("workspace.card_settings_sub_language"), t("workspace.card_settings_sub_theme"), t("workspace.card_settings_sub_services"), t("workspace.card_settings_sub_auth")],
    },
  ];
}

// services may arrive as array or JSON string (RV vs gate paths). Returns array.
function parseSvcs(svcs) {
  if (Array.isArray(svcs)) return svcs;
  if (typeof svcs === "string") {
    try { return JSON.parse(svcs) || []; } catch { return []; }
  }
  return [];
}

function FilterRow({ label, children }) {
  return (
    <div className="ws-filter-group">
      <span className="ws-filter-group-label">{label}:</span>
      {children}
    </div>
  );
}

function Chip({ active, disabled, onClick, mark, children }) {
  const cls = ["ws-filter-chip"];
  if (active) cls.push("ws-filter-chip-active");
  if (disabled) cls.push("ws-filter-chip-disabled");
  return (
    <button
      type="button"
      className={cls.join(" ")}
      disabled={disabled}
      onClick={disabled ? undefined : onClick}
    >
      {mark && <span className="ws-filter-chip-mark">✓</span>}
      {children}
    </button>
  );
}

// Skeleton placeholder shaped like a trending-card. Layout mirrors the
// real card so the swap-in doesn't reflow when fetched data arrives.
function TrendingCardSkeleton() {
  return (
    <div className="trending-card skeleton-card cursor-default">
      <div className="trending-card-title">
        <Skeleton.Line width="70%" height={14} />
      </div>
      <div className="trending-card-meta">
        <Skeleton.Line width="92%" height={11} />
        <Skeleton.Line width="60%" height={11} />
      </div>
      <div className="trending-card-tags">
        <Skeleton.Block width={56} height={18} borderRadius={9} />
        <Skeleton.Block width={72} height={18} borderRadius={9} />
        <Skeleton.Block width={60} height={18} borderRadius={9} />
      </div>
    </div>
  );
}

function TrendingGridSkeleton({ count = 4 }) {
  return (
    <div className="trending-grid">
      {Array.from({ length: count }).map((_, i) => (
        <TrendingCardSkeleton key={i} />
      ))}
    </div>
  );
}

// Render a curated-model section (Starter or Recommend). Flat grid of
// cards — service (llm-api / sd-api) shows as a badge inside each card
// rather than as a separate group header. Sort: for_service primary,
// then sort_order, then vram_gb so same-service cards still cluster
// visually but without the header noise.
function renderCuratedSection({ title, items, loading, visible, toggleVisible, navigate }) {
  // Loading: show header + skeleton grid so the page lays out at final
  // height immediately instead of popping content in.
  // Loaded + empty: hide entire section (clean — no orphan headers).
  // Loaded + has data: render real cards.
  if (!loading && (!Array.isArray(items) || items.length === 0)) return null;

  // Best query to drop into the search bar on click — Civitai prefers
  // hash (round-trips via by-hash), HF uses external_id (owner/repo
  // text-search hits the catalog card directly).
  const queryFor = (m) => {
    if (m.source === "civitai" && m.hash) return m.hash;
    return m.external_id || "";
  };

  // sort_order is the primary key — operators set it explicitly in the
  // gate admin UI to choose what shows up first. Tiebreakers: service
  // (so same-service cards still cluster on equal sort) then vram_gb
  // (lighter first within a tie) then id.
  const sorted = Array.isArray(items)
    ? [...items].sort((a, b) =>
        (a.sort_order || 0) - (b.sort_order || 0) ||
        (a.for_service || "").localeCompare(b.for_service || "") ||
        (a.vram_gb || 0) - (b.vram_gb || 0) ||
        (a.id || 0) - (b.id || 0))
    : [];

  return (
    <div className="workspace-section workspace-section-starter">
      <div className="workspace-section-header">
        <h2 className="workspace-section-title">{title}</h2>
        {!loading && (
          <span
            className="workspace-section-link"
            onClick={() => toggleVisible((v) => !v)}
            title={visible ? "Hide" : "Show"}
          >
            {visible ? "Hide" : "Show"}
          </span>
        )}
      </div>
      {loading ? (
        <TrendingGridSkeleton count={4} />
      ) : visible && (
        <div className="trending-grid">
          {sorted.map((m) => {
            const q = queryFor(m);
            return (
              <div
                key={m.id}
                className="trending-card"
                onClick={() => q && navigate(`/search?q=${encodeURIComponent(q)}`)}
                title={m.note || ""}
              >
                <div className="trending-card-title">
                  <span className="trending-card-icon">📦</span>
                  <span className="trending-card-name">{m.external_id}</span>
                </div>
                {m.note && (
                  <div className="trending-card-meta">{m.note}</div>
                )}
                <div className="trending-card-tags">
                  {m.for_service && (
                    <span className="sw-service-tag">{m.for_service}</span>
                  )}
                  <span className={`brand-tag brand-tag-${m.source}`}>
                    {m.source === "huggingface" ? "HuggingFace" : "Civitai"}
                  </span>
                  {m.vram_gb > 0 && (
                    <span className="trending-tag">{m.vram_gb}GB+ VRAM</span>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

export default function Workspace() {
  const navigate = useNavigate();
  const { t } = useTranslation();
  const { role } = useAuth();
  const isAdmin = role === "owner" || role === "admin";

  const [cardsConfig, setCardsConfig] = useState({});
  useEffect(() => { fetchCards().then(setCardsConfig); }, []);
  const cards = buildCards(t)
    .filter(c => !c.adminOnly || isAdmin)
    .filter(c => isCardEnabled(c.id, cardsConfig));

  const [nodeCount, setNodeCount] = useState({ online: 0, total: 0 });
  const [trending, setTrending] = useState([]);
  // Per-section loading flags drive skeleton rendering. Initialized true
  // so the first paint shows shimmering placeholders instead of an empty
  // page; flipped to false in the fetch resolve/reject. After loading,
  // an empty array hides the section entirely.
  const [trendingLoading, setTrendingLoading] = useState(true);
  const [starterModels, setStarterModels] = useState([]);
  const [starterLoading, setStarterLoading] = useState(true);
  const [recommendModels, setRecommendModels] = useState([]);
  const [recommendLoading, setRecommendLoading] = useState(true);

  useEffect(() => {
    fetchCuratedModels("starter")
      .then((d) => setStarterModels(Array.isArray(d) ? d : []))
      .catch(() => {})
      .finally(() => setStarterLoading(false));
    fetchCuratedModels("recommend")
      .then((d) => setRecommendModels(Array.isArray(d) ? d : []))
      .catch(() => {})
      .finally(() => setRecommendLoading(false));
  }, []);

  // Curated section visibility — persisted independently per section
  // so the operator can hide one without affecting the other. Default
  // ON for both so new operators see the recommendations on first load.
  const [starterVisible, setStarterVisible] = useState(() => {
    try {
      const v = localStorage.getItem("iann.starterVisible");
      return v === null ? true : v === "true";
    } catch { return true; }
  });
  const [recommendVisible, setRecommendVisible] = useState(() => {
    try {
      const v = localStorage.getItem("iann.recommendVisible");
      return v === null ? true : v === "true";
    } catch { return true; }
  });
  useEffect(() => {
    try { localStorage.setItem("iann.starterVisible", String(starterVisible)); } catch {}
  }, [starterVisible]);
  useEffect(() => {
    try { localStorage.setItem("iann.recommendVisible", String(recommendVisible)); } catch {}
  }, [recommendVisible]);

  // One /v1/nodes fetch covers two callers below: nodeCount stat + trending
  // aggregation. Keep them synced by deriving from the same response.
  useEffect(() => {
    fetchNodes().then(nodes => {
      const list = Array.isArray(nodes) ? nodes : [];
      setNodeCount({
        total: list.length,
        online: list.filter(n => n.online).length,
      });

      // Aggregate by hash (or model name as fallback) → top-N most loaded.
      // Carry the first non-empty model_origin_url through the aggregation
      // so the trending card can render the same owner/repo prefix the
      // node cards do.
      const byKey = new Map();
      for (const n of list) {
        for (const s of parseSvcs(n.services)) {
          const hash = (s.model_hash || "").toLowerCase();
          const name = s.model || "";
          if (!hash && !name) continue;
          const key = hash || name;
          const cur = byKey.get(key) || { name, hash, count: 0, kind: s.name || "", model_origin_url: "" };
          cur.count += 1;
          if (!cur.name && name) cur.name = name;
          if (!cur.model_origin_url && s.model_origin_url) cur.model_origin_url = s.model_origin_url;
          byKey.set(key, cur);
        }
      }
      const top = Array.from(byKey.values())
        .filter(m => m.name)
        .sort((a, b) => b.count - a.count)
        .slice(0, 4);
      setTrending(top);
    }).catch(() => {}).finally(() => setTrendingLoading(false));
  }, []);

  // Hero search bar state. Submission navigates to /search with the query
  // and the chosen ordering — `?q=...&order=catalog` for example.
  const [searchInput, setSearchInput] = useState("");
  const [order, setOrder] = useState("");
  const [sources, setSources] = useState({
    running: true, huggingface: true, civitai: true,
  });
  const [hideNsfw, setHideNsfw] = useState(true);
  const detected = searchInput.trim() ? detectQueryType(searchInput) : null;

  const onSearchSubmit = (e) => {
    e.preventDefault();
    const q = searchInput.trim();
    if (!q) return;
    const params = new URLSearchParams();
    params.set("q", q);
    if (order) params.set("order", order);
    navigate(`/search?${params.toString()}`);
  };

  const handleCardClick = (card) => {
    if (card.external) window.location.assign(card.path);
    else navigate(card.path);
  };

  return (
    <div className="workspace-page">
      {/* ① Hero */}
      <div className="workspace-hero">
        <h1 className="workspace-title">{t("workspace.hero_title")}</h1>
        <p className="workspace-fullname">{t("workspace.hero_fullname")}</p>
        <p className="workspace-subtitle">{t("workspace.hero_subtitle")}</p>

        <div className="workspace-search-wrap">
          <form className="workspace-search-form" onSubmit={onSearchSubmit}>
            <input
              className="workspace-search-input"
              placeholder="Search by hash · address · model · node · GPU · engine"
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              spellCheck={false}
            />
            {detected && detected.type !== "text" && (
              <span className={`workspace-search-chip workspace-search-chip-${detected.type}`}>
                {detected.label}
              </span>
            )}
            <button
              type="submit"
              className="workspace-search-submit"
              disabled={!searchInput.trim()}
              title="Search"
              aria-label="Search"
            >
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="11" cy="11" r="7" />
                <line x1="21" y1="21" x2="16.65" y2="16.65" />
              </svg>
            </button>
          </form>
          <div className="workspace-filters">
            <FilterRow label="Order">
              <Chip mark active={order === ""}        onClick={() => setOrder("")}>⚡ Nodes first</Chip>
              <Chip mark active={order === "catalog"} onClick={() => setOrder("catalog")}>📦 Catalog first</Chip>
              <Chip mark active={order === "mixed"}   onClick={() => setOrder("mixed")}>⇆ Mixed</Chip>
            </FilterRow>
            <FilterRow label="Sources">
              <Chip
                mark
                active={sources.running}
                onClick={() => setSources(s => ({ ...s, running: !s.running }))}
              >
                Running
              </Chip>
              <Chip
                mark
                active={sources.huggingface}
                onClick={() => setSources(s => ({ ...s, huggingface: !s.huggingface }))}
              >
                HuggingFace
              </Chip>
              <Chip
                mark
                active={sources.civitai}
                onClick={() => setSources(s => ({ ...s, civitai: !s.civitai }))}
              >
                Civitai
              </Chip>
              <Chip disabled>ModelScope</Chip>
            </FilterRow>
            <FilterRow label="Filters">
              <Chip disabled>GPU: Any ▾</Chip>
              <Chip disabled>VRAM: Any ▾</Chip>
              <Chip disabled>Engine: Any ▾</Chip>
              <Chip mark active={hideNsfw} onClick={() => setHideNsfw(v => !v)}>
                Hide NSFW
              </Chip>
            </FilterRow>
          </div>
        </div>
      </div>

      {/* ② Trending Models — render skeleton during fetch, then real data
          (or hide when the loaded result is empty). */}
      {(trendingLoading || trending.length > 0) && (
        <div className="workspace-section workspace-section-trending">
          <div className="workspace-section-header">
            <h2 className="workspace-section-title">🔥 Trending Models</h2>
            {!trendingLoading && (
              <span
                className="workspace-section-link"
                onClick={() => navigate("/search?q=")}
              >
                View all →
              </span>
            )}
          </div>
          {trendingLoading ? (
            <TrendingGridSkeleton count={4} />
          ) : (
            <div className="trending-grid">
              {trending.map((m) => {
                // Civitai → hash (numeric prefix isn't searchable), HF →
                // owner/name path, anything else → hash if present, else name.
                const q = modelSearchQuery({
                  name: m.name, hash: m.hash, originUrl: m.model_origin_url,
                });
                return (
                <div
                  key={m.hash || m.name}
                  className="trending-card"
                  onClick={() => navigate(`/search?q=${encodeURIComponent(q)}`)}
                >
                  <div className="trending-card-title">
                    <span className="mr-4">📦</span>
                    <ModelLabel modelName={m.name} originUrl={m.model_origin_url} hash={m.hash} />
                  </div>
                  <div className="trending-card-meta">
                    <strong className="trending-card-count">
                      {m.count} node{m.count > 1 ? "s" : ""}
                    </strong>
                  </div>
                  <div className="trending-card-tags">
                    {m.kind && <span className="trending-tag">{m.kind}</span>}
                    {m.hash && <span className="trending-tag">sha256</span>}
                  </div>
                </div>
                );
              })}
            </div>
          )}
        </div>
      )}

      {/* ③ Curated Models — gate-managed list. Two sections:
          🌟 Starter (entry-level minimum set) and 💡 Recommend (curated
          quality / trend boosts). Same render shape, different category.
          Each section hides when its category is empty so partial outages
          don't strand an empty header. Click on a card sends the operator
          to the search page with a round-trippable query — for Civitai
          entries that's the SHA256 hash; for HF that's the owner/repo. */}
      {renderCuratedSection({
        title: "🌟 Starter Models",
        items: starterModels,
        loading: starterLoading,
        visible: starterVisible,
        toggleVisible: setStarterVisible,
        navigate,
      })}
      {renderCuratedSection({
        title: "💡 Recommend Models",
        items: recommendModels,
        loading: recommendLoading,
        visible: recommendVisible,
        toggleVisible: setRecommendVisible,
        navigate,
      })}

      {/* ④ Workspace tool cards */}
      <hr className="workspace-divider" />
      <div className="workspace-section">
        <div className="workspace-section-header">
          <h2 className="workspace-section-title">🛠 Workspace</h2>
          <span className="workspace-section-link-muted">Management tools — your assets / system</span>
        </div>
        <div className="workspace-cards">
          {cards.map(card => {
            const Wrapper = card.external ? "a" : "div";
            const classes = ["workspace-card"].filter(Boolean).join(" ");
            const wrapperProps = card.external
              ? { href: card.path, style: { "--card-accent": card.color, textDecoration: "none", color: "inherit" } }
              : { onClick: () => handleCardClick(card), style: { "--card-accent": card.color } };
            return (
              <Wrapper key={card.id} className={classes} {...wrapperProps}>
                <div className="wc-icon">{card.icon}</div>
                <div className="wc-body">
                  <h2 className="wc-title">{card.title}</h2>
                  <p className="wc-desc">{card.description}</p>
                  {card.id === "nodes" && nodeCount.total > 0 && (
                    <div className="wc-stats">
                      <span className="wc-stat-dot online" /> {nodeCount.online} {t("workspace.stats_online")}
                      <span className="wc-stat-sep" />
                      {nodeCount.total} {t("workspace.stats_total")}
                    </div>
                  )}
                  {card.sub && (
                    <div className="wc-sub">
                      {card.sub.map(s => <span key={s} className="wc-sub-tag">{s}</span>)}
                    </div>
                  )}
                </div>
                <div className="wc-arrow">&rarr;</div>
              </Wrapper>
            );
          })}
        </div>
      </div>
    </div>
  );
}
