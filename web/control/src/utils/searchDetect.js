// Etherscan-style input type detection for the unified search bar.
// Mirrors broker/search.go detectQueryType so the chip matches what the
// server will route on. Detection is local (regex-only) — no API hit while
// the user is still typing.
//
// Returns { type, normalized, label } where:
//   type       — "hash" | "address" | "node" | "engine" | "gpu" | "text"
//   normalized — server-ready form of the query (lowercase hash, etc.)
//   label      — short human label for the DetectionChip ("SHA-256 hash")

import { normalizeSHA256 } from "./hash";

const RE_EVM_ADDRESS = /^0x[a-fA-F0-9]{40}$/;
const RE_NODE_ID = /^node-[\w-]+$/i;
const RE_HEX_64 = /^[a-fA-F0-9]{64}$/;
const RE_SHA_PREFIX = /^sha256:[a-fA-F0-9]{64}$/i;

// Heuristic GPU model markers — keep cheap so we can run on every keystroke.
// A known prefix wins, otherwise free text.
const GPU_PREFIXES = [
  "rtx ", "gtx ", "geforce ", "quadro ",   // NVIDIA consumer
  "h100", "h200", "a100", "a40", "a10",    // NVIDIA datacenter
  "l40", "l4 ", "t4 ",
  "mi300", "mi250", "mi210", "mi100",      // AMD CDNA
  "rx ", "radeon",                          // AMD consumer
  "arc ",                                   // Intel
];

const ENGINE_KEYWORDS = ["vllm", "sd.cpp", "llama.cpp", "ollama", "tgi", "exllama"];

export function detectQueryType(raw) {
  const q = String(raw || "").trim();
  if (!q) return { type: "text", normalized: "", label: "" };

  if (RE_EVM_ADDRESS.test(q)) {
    return {
      type: "address",
      normalized: q.toLowerCase(),
      label: "EVM address",
    };
  }

  if (RE_SHA_PREFIX.test(q) || RE_HEX_64.test(q)) {
    const norm = normalizeSHA256(q);
    return {
      type: "hash",
      normalized: norm,
      label: "SHA-256 hash",
    };
  }

  if (RE_NODE_ID.test(q)) {
    return {
      type: "node",
      normalized: q,
      label: "Node ID",
    };
  }

  const lower = q.toLowerCase();
  for (const kw of ENGINE_KEYWORDS) {
    if (lower === kw) {
      return { type: "engine", normalized: lower, label: "Engine" };
    }
  }
  for (const prefix of GPU_PREFIXES) {
    if (lower.startsWith(prefix)) {
      return { type: "gpu", normalized: q, label: "GPU model" };
    }
  }

  return { type: "text", normalized: q, label: "Text search" };
}

// describeDetected returns a one-line hint for the chip:
//   "0x1234…abcd · EVM address" | "abc1…def · SHA-256 hash" | "RTX 4090 · GPU"
// Useful for showing the user "the server will route this as X" before they
// hit Enter.
export function describeDetected(raw) {
  const det = detectQueryType(raw);
  if (!det.type || det.type === "text") return "";
  const display = abbreviate(det.normalized);
  return `${display} · ${det.label}`;
}

function abbreviate(s) {
  if (!s) return "";
  if (s.length <= 16) return s;
  return s.slice(0, 6) + "…" + s.slice(-4);
}
