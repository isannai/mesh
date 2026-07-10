import React from "react";
import { Link } from "react-router-dom";
import "./docs-guide.scss";

// Roadmap — quarterly milestones ported from docs/TODO/mockup.html.
// Manually maintained — items move from "Planned" to "Shipped" once they
// land. Beyond-section holds vision items without dates; they graduate
// into a phase when scoped.
//
// Phase data lives here as a flat config so adding/reordering quarters
// is a single edit rather than chasing JSX. Each phase belongs to a
// year (used for the year header band) and carries its own list of
// shipped/planned bullets.
const PHASES = [
  {
    year: 2026,
    yearTag: "H2 — launch year",
    phases: [
      {
        cls: "p1",
        label: "Jul – Nov 2026",
        num: "Phase 1",
        status: "Planned",
        theme: "Close beta · stabilize · developer surface.",
        items: [
          "Close Beta",
          "Stability hardening",
          "Multi-modal AI model support",
          "Voice AI engine support",
          "Broker Light",
          "Discord Bot · Telegram Bot AI integration",
          "Public announcement",
        ],
      },
      {
        cls: "p2",
        label: "Dec 2026 – Feb 2027",
        num: "Phase 2",
        status: "Planned",
        theme: "Web3 infra · payment rails.",
        items: [
          "Chain selection",
          "Credit Faucet",
          "ERC-6551 wallet integration (under consideration)",
          "ERC-8183 + ERC-8004 + x402 crypto payment integration (design + contracts in development)",
          "Credit ↔ ERC-20 conversion",
        ],
      },
    ],
  },
  {
    year: 2027,
    yearTag: "marketplace · pipeline · reach",
    phases: [
      {
        cls: "p3",
        label: "Mar – May 2027",
        num: "Phase 3",
        status: "Planned",
        theme: "Marketplace · pipeline · Jarvis.",
        items: [
          "Marketplace",
          "Pipeline multi-modal processing",
          "Jarvis builder (SDK)",
        ],
      },
      {
        cls: "p4",
        label: "Jun – Sep 2027",
        num: "Phase 4",
        status: "Planned",
        theme: "Platform reach + RV federation.",
        items: [
          "macOS official support",
          "RV Bridge",
        ],
      },
    ],
  },
];

const BEYOND = [
  { title: "Mobile SDK", desc: "iOS / Android with wallet integration" },
  { title: "Federated fine-tuning", desc: "Light-weight tuning jobs across providers" },
  { title: "Confidential compute", desc: "TEE / SGX integration for sensitive jobs" },
  { title: "Pipeline marketplace", desc: "Share / discover Studio pipelines" },
  { title: "Multi-chain settlement", desc: "Expand beyond the Phase 2 single-chain" },
];

export default function RoadmapPage() {
  return (
    <div className="page docs-marketing docs-roadmap">
      <div className="page-body">
        <div className="docs-breadcrumb">
          <Link to="/docs">← Docs</Link>
        </div>

        {/* Roadmap hero — same eyebrow + gradient title pattern as the
            About page, but with the timeline-specific lede. Manual update
            cadence is intentional (we don't want a stale "auto-generated"
            tag — every change is a deliberate edit here). */}
        <div className="docs-roadmap-hero">
          <h1>Roadmap</h1>
          <p>Quarterly milestones across launch, Web3 integration, and platform reach. Updated manually after each phase ships.</p>
        </div>

        <div className="docs-tl">
          {PHASES.map((yearBlock) => (
            <React.Fragment key={yearBlock.year}>
              <div className="docs-tl-year">
                <div className="docs-tl-year-row">
                  <h2>{yearBlock.year}</h2>
                  <span className="docs-tl-year-pill">{yearBlock.yearTag}</span>
                </div>
              </div>
              {yearBlock.phases.map((p) => (
                <div key={p.num} className={`docs-tl-phase docs-tl-phase-${p.cls}`}>
                  <div className="docs-tl-q-card">
                    <div className="docs-tl-q-head">
                      <div>
                        <div className="docs-tl-q-label">{p.label}</div>
                        <div className="docs-tl-q-num">{p.num}</div>
                      </div>
                      <span className="docs-tl-q-badge">{p.status}</span>
                    </div>
                    <p className="docs-tl-q-theme">{p.theme}</p>
                    <ul className="docs-tl-q-list">
                      {p.items.map((it) => <li key={it}>{it}</li>)}
                    </ul>
                  </div>
                </div>
              ))}
            </React.Fragment>
          ))}
        </div>

        {/* Beyond — vision section without dates. Items here are explicitly
            "no commitment", so they're rendered as a denser grid with no
            phase styling and no status badge. */}
        <div className="docs-tl-beyond">
          <h2>Beyond Q3 2027</h2>
          <p className="docs-tl-beyond-sub">Vision items — no dates, no priority order. Items graduate into a phase when scoped.</p>
          <div className="docs-tl-beyond-grid">
            {BEYOND.map((b) => (
              <div key={b.title} className="docs-tl-beyond-item">
                <b>{b.title}</b>
                {b.desc}
              </div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
