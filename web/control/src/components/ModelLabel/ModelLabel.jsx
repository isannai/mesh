import React from "react";
import { formatModelDisplay, externalSearchURL } from "@utils/modelPath";

// ModelLabel renders a service's model name with an HF/Civitai owner
// prefix derived from the package's download_url. Falls back to the
// dimmed "imported" placeholder when no origin URL is available
// (file:// imports, missing package metadata).
//
// Click behavior: opens the upstream catalog (Civitai by hash for sd
// models, HF by owner/name for HF models, HF by bare name for imports)
// in a new tab. stopPropagation keeps the parent card click handler
// from also firing — the parent can still wrap the rest of the card
// for navigation while the model name routes outward.
//
// Used by every card surface (Trending / my-nodes / nodes / node-detail
// / search) so the same `owner/model` shape renders consistently and
// can be copy-pasted back into the search bar for a HF lookup.
export default function ModelLabel({ modelName, originUrl, hash, className = "" }) {
  if (!modelName) return null;
  const { prefix, muted } = formatModelDisplay(modelName, originUrl);
  const extURL = externalSearchURL({ name: modelName, hash, originUrl });
  const onClick = extURL
    ? (e) => {
        e.stopPropagation();
        window.open(extURL, "_blank", "noopener,noreferrer");
      }
    : undefined;
  // title attr surfaces the full "owner/name" string on hover, plus
  // the destination URL when clicking opens an external catalog.
  const title = extURL
    ? `${prefix}/${modelName} — open ${extURL}`
    : `${prefix}/${modelName}`;
  const cls = `model-label-with-prefix ${className} ${extURL ? "model-label-link" : ""}`.trim();
  return (
    <span
      className={cls}
      title={title}
      onClick={onClick}
      role={extURL ? "link" : undefined}
    >
      <span className={muted ? "model-prefix muted" : "model-prefix"}>{prefix}</span>
      <span className="model-prefix-sep">/</span>
      <span className="model-name">{modelName}</span>
    </span>
  );
}
