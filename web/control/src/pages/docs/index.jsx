import React from "react";
import { Link } from "react-router-dom";
import { useTranslation } from "@i18n";
import "./docs-guide.scss";

// Docs hub — two-tier layout. The top tier is a flashy "Resources" surface
// (About + Roadmap) borrowed from the docs/TODO/mockup.html marketing draft
// so first-time visitors see the vision before the technical guides. The
// bottom tier keeps the original guide grid (Install / Run / TPM / vLLM)
// in the minimal style consistent with the rest of the broker UI.
export default function Docs() {
  const { t } = useTranslation();

  // Featured "Resources" cards — flashy gradient + conic-ring on hover.
  // Kept inline (not pulled into the guide array) because the visual
  // treatment is distinct and the destinations are conceptually a
  // different category than the install/run guides.
  const featured = [
    {
      to: "/docs/about",
      kind: "about",
      eyebrow: "Read the story",
      icon: "📖",
      title: "About iSANN",
      desc: "Wallet-native, multi-engine, peer-routed AI inference. Why we built it, how it works, and what makes it different.",
      cta: "Explore",
    },
    {
      to: "/docs/roadmap",
      kind: "roadmap",
      eyebrow: "What's next",
      icon: "🗺️",
      title: "Roadmap",
      desc: "Phase-by-phase milestones — close beta, public launch, Web3 integration, and platform reach through 2027.",
      cta: "View timeline",
    },
  ];

  // Standard guide cards — minimal style, unchanged from the previous
  // index. Translations preserved.
  const guides = [
    {
      to: "/docs/provider-install",
      icon: "📥",
      title: t("docs.install.title"),
      desc: t("docs.install.lede"),
    },
    {
      to: "/docs/provider-run",
      icon: "⚙️",
      title: t("docs.run.title"),
      desc: t("docs.run.lede"),
    },
    {
      to: "/docs/tpm-setup",
      icon: "🔐",
      title: t("docs.tpm.title"),
      desc: t("docs.tpm.lede"),
    },
    {
      to: "/docs/vllm-setup",
      icon: "🧠",
      title: "vLLM Setup Guide",
      desc: "Run vLLM on WSL2 + Docker and connect it to IANN as an external engine.",
    },
  ];

  return (
    <div className="page docs-index">
      <div className="page-body">
        {/* Resources hero — gradient title with eyebrow line. Mirrors the
            marketing mockup so the docs landing reads as a product story
            first, manual second. */}
        <div className="docs-hero">
          <div className="docs-hero-eyebrow">Resources</div>
          <h1>Everything you need<br/>to know about iSANN.</h1>
          <p>Browse our vision, learn how the protocol works, and follow the quarter-by-quarter plan toward a fully decentralized inference marketplace.</p>
        </div>

        {/* Featured cards — flashy treatment with conic-gradient ring + glow.
            Restricted to two items (About / Roadmap) so the layout always
            renders as a balanced two-column row. */}
        <div className="docs-featured-grid">
          {featured.map(f => (
            <Link key={f.to} to={f.to} className={`docs-featured-card docs-featured-card-${f.kind}`}>
              <div className="docs-featured-bgico">{f.icon}</div>
              <div className="docs-featured-top">
                <div className="docs-featured-eyebrow"><span className="docs-featured-dot" /> {f.eyebrow}</div>
                <h2>{f.title}</h2>
                <div className="docs-featured-desc">{f.desc}</div>
              </div>
              <div className="docs-featured-cta">
                {f.cta} <span className="docs-featured-cta-arrow">→</span>
              </div>
            </Link>
          ))}
        </div>

        {/* Guides — original card layout. Section divider + heading keeps
            the technical guides visually separated from the marketing
            surface above. */}
        <div className="docs-guides-section">
          <div className="docs-guides-head">
            <h3>Guides</h3>
            <span className="docs-guides-hint">Step-by-step setup for operators</span>
          </div>
          <div className="docs-cards">
            {guides.map(c => (
              <Link key={c.to} to={c.to} className="docs-card">
                <div className="docs-card-ico">{c.icon}</div>
                <div className="docs-card-body">
                  <div className="docs-card-title">{c.title}</div>
                  <div className="docs-card-desc">{c.desc}</div>
                </div>
                <div className="docs-card-arrow">→</div>
              </Link>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
