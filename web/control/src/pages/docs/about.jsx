import React from "react";
import { Link } from "react-router-dom";
import "./docs-guide.scss";

// About — marketing-style landing for the iSANN protocol. Ported from
// docs/TODO/mockup.html with broker tokens substituted for the standalone
// CSS variables. Architecture section is a placeholder — diagram is being
// prepared separately and will replace the empty card later.
export default function AboutPage() {
  return (
    <div className="page docs-marketing">
      <div className="page-body">
        <div className="docs-breadcrumb">
          <Link to="/docs">← Docs</Link>
        </div>

        {/* Hero — eyebrow + gradient title + lede + CTAs. The eyebrow
            uses a pulsing dot to match the docs index "Resources" line. */}
        <div className="docs-about-hero">
          <span className="docs-about-hero-eyebrow">
            <span className="docs-about-hero-dot" /> iSANN · Decentralized AI inference
          </span>
          <h1>Run AI on the<br/>network you own.</h1>
          <p>iSANN turns idle GPUs into a wallet-native inference marketplace. Submit jobs, get results, pay in stable on-chain credits — without giving up control of your data, your model, or your provider.</p>
          <div className="docs-about-hero-actions">
            <Link className="docs-about-btn docs-about-btn-primary" to="/welcome">Get Started →</Link>
            <Link className="docs-about-btn" to="/docs/roadmap">View Roadmap</Link>
            <a className="docs-about-btn" href="https://github.com/" target="_blank" rel="noopener noreferrer">GitHub</a>
          </div>
        </div>

        {/* Why iSANN — value-prop cards. Four pillars, each with an icon,
            short title, and one-sentence elaboration. Hover shows a soft
            gradient border to add motion without overwhelming the layout. */}
        <h2 className="docs-about-section">Why iSANN</h2>
        <div className="docs-about-values">
          <div className="docs-about-value-card">
            <div className="docs-about-value-icon">🔐</div>
            <div className="docs-about-value-title">Wallet-native auth</div>
            <div className="docs-about-value-desc">EOA signatures (EIP-191) gate every job. No accounts, no API keys, no leaks.</div>
          </div>
          <div className="docs-about-value-card">
            <div className="docs-about-value-icon">⚡</div>
            <div className="docs-about-value-title">Multi-engine</div>
            <div className="docs-about-value-desc">SD · LLM (llama.cpp) · vLLM. Same API surface, swap engines per node.</div>
          </div>
          <div className="docs-about-value-card">
            <div className="docs-about-value-icon">🌐</div>
            <div className="docs-about-value-title">Direct peer routing</div>
            <div className="docs-about-value-desc">QUIC tunnels broker ↔ provider. No central servers in the data path.</div>
          </div>
          <div className="docs-about-value-card">
            <div className="docs-about-value-icon">🧩</div>
            <div className="docs-about-value-title">OpenAI-compatible</div>
            <div className="docs-about-value-desc">Drop-in /v1/chat/completions and /v1/images/generations. Your existing client code just works.</div>
          </div>
        </div>

        {/* Architecture — intentionally empty placeholder. The real
            diagram is being authored separately (see mockup.html for the
            in-progress sketch); this card is a "coming soon" hold so the
            page outline stays anchored. */}
        <h2 className="docs-about-section">Architecture</h2>
        <div className="docs-about-arch-placeholder">
          <div className="docs-about-arch-eyebrow">Coming soon</div>
          <div className="docs-about-arch-text">Architecture diagram is being prepared separately — stay tuned.</div>
        </div>

        {/* Quick Start — four shortcut cards into the rest of the site.
            Provider install + API ref + Roadmap + Community. Mirrors the
            classic "next steps" footer pattern. */}
        <h2 className="docs-about-section">Quick Start</h2>
        <div className="docs-about-quick-grid">
          <Link className="docs-about-quick-card" to="/docs/provider-install">
            <div className="docs-about-quick-ico">📥</div>
            <div>
              <div className="docs-about-quick-title">Install Provider</div>
              <div className="docs-about-quick-desc">Bring your GPU online in 5 minutes</div>
            </div>
            <div className="docs-about-quick-arrow">→</div>
          </Link>
          <Link className="docs-about-quick-card" to="/docs/api">
            <div className="docs-about-quick-ico">📡</div>
            <div>
              <div className="docs-about-quick-title">API Reference</div>
              <div className="docs-about-quick-desc">Endpoints, ownership, examples</div>
            </div>
            <div className="docs-about-quick-arrow">→</div>
          </Link>
          <Link className="docs-about-quick-card" to="/docs/roadmap">
            <div className="docs-about-quick-ico">🗺️</div>
            <div>
              <div className="docs-about-quick-title">Roadmap</div>
              <div className="docs-about-quick-desc">What we are building this year</div>
            </div>
            <div className="docs-about-quick-arrow">→</div>
          </Link>
          <a className="docs-about-quick-card" href="https://t.me/" target="_blank" rel="noopener noreferrer">
            <div className="docs-about-quick-ico">💬</div>
            <div>
              <div className="docs-about-quick-title">Community</div>
              <div className="docs-about-quick-desc">Telegram · Discord · GitHub</div>
            </div>
            <div className="docs-about-quick-arrow">→</div>
          </a>
        </div>
      </div>
    </div>
  );
}
