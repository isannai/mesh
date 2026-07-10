import React from "react";

const studios = [
  { id: "image", label: "Image Studio", icon: ImageIcon },
  { id: "pipeline", label: "Pipeline Studio", icon: PipelineIcon },
  { id: "llm", label: "LLM Studio", icon: LLMIcon, disabled: true },
  { id: "voice", label: "Voice Studio", icon: VoiceIcon, disabled: true },
];

const tools = [
  { id: "files", label: "Files", icon: FilesIcon },
];

export default function ActivityBar({ active, onSelect }) {
  return (
    <nav className="activity-bar">
      <div className="ab-group">
        {studios.map(s => (
          <div
            key={s.id}
            className={`ab-icon${active === s.id ? " active" : ""}${s.disabled ? " disabled" : ""}`}
            onClick={() => !s.disabled && onSelect(s.id)}
          >
            <s.icon />
            <span className="tooltip">{s.label}</span>
          </div>
        ))}
      </div>
      <div className="ab-separator" />
      <div className="ab-group">
        {tools.map(t => (
          <div
            key={t.id}
            className={`ab-icon${active === t.id ? " active" : ""}`}
            onClick={() => onSelect(t.id)}
          >
            <t.icon />
            <span className="tooltip">{t.label}</span>
          </div>
        ))}
      </div>
      <div className="ab-spacer" />
      <div className="ab-group">
        <div className="ab-icon" onClick={() => onSelect("settings")}>
          <SettingsIcon />
          <span className="tooltip">Settings</span>
        </div>
      </div>
    </nav>
  );
}

// Inline SVG icons
function ImageIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
      <rect x="3" y="3" width="18" height="18" rx="3" />
      <circle cx="8" cy="8" r="2" />
      <path d="M22 16l-5-5-3 3-3-3-8 8" />
    </svg>
  );
}

function PipelineIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
      <circle cx="5" cy="12" r="2" /><circle cx="12" cy="5" r="2" />
      <circle cx="12" cy="19" r="2" /><circle cx="19" cy="12" r="2" />
      <path d="M7 12h4M14 12h3M12 7v4M12 15v2" />
    </svg>
  );
}

function LLMIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
      <path d="M21 15a2 2 0 01-2 2H7l-4 4V5a2 2 0 012-2h14a2 2 0 012 2z" />
    </svg>
  );
}

function VoiceIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
      <path d="M12 1a3 3 0 00-3 3v8a3 3 0 006 0V4a3 3 0 00-3-3z" />
      <path d="M19 10v2a7 7 0 01-14 0v-2" />
      <line x1="12" y1="19" x2="12" y2="23" /><line x1="8" y1="23" x2="16" y2="23" />
    </svg>
  );
}

function FilesIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
      <path d="M22 19a2 2 0 01-2 2H4a2 2 0 01-2-2V5a2 2 0 012-2h5l2 3h9a2 2 0 012 2z" />
    </svg>
  );
}

function NodesIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
      <rect x="2" y="2" width="20" height="8" rx="2" /><rect x="2" y="14" width="20" height="8" rx="2" />
      <circle cx="6" cy="6" r="1" fill="currentColor" /><circle cx="6" cy="18" r="1" fill="currentColor" />
    </svg>
  );
}

function SettingsIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8">
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 01-2.83 2.83l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z" />
    </svg>
  );
}
