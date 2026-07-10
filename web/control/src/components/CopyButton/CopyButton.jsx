import { useState } from "react";

export default function CopyButton({ value, title = "Copy", className = "" }) {
  const [copied, setCopied] = useState(false);
  if (!value) return null;

  const onClick = (e) => {
    e.stopPropagation();
    if (!navigator.clipboard) return;
    navigator.clipboard.writeText(String(value)).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    });
  };

  return (
    <button
      type="button"
      className={`copy-icon-btn ${className} ${copied ? "copied" : ""}`}
      title={copied ? "Copied!" : title}
      aria-label={title}
      onClick={onClick}
    >
      {copied ? "✓" : "⎘"}
    </button>
  );
}
