import React, { useState, useEffect, useCallback, useRef } from "react";
import ReactMarkdown from "react-markdown";
import { useParams, useNavigate } from "react-router-dom";
import { useTranslation } from "@i18n";
import { fetchNodes, fetchRendezvousList, fetchNodesByRendezvousAddr, fetchMetrics, fetchMetricsByAddr, mergeMetricsIntoNodes, fetchMyNodes, addMyNode, deleteMyNode } from "../../api/nodes";
import { useAuth } from "../../context/AuthContext";
import { generateImage, subscribeJob, pollJob, getOutputUrl, getJobResult } from "../../api/sd";
import { fetchProfiles } from "../../api/profiles";
import { getAuthHeaders } from "@utils/wallet";
import StatusTag from "@components/StatusTag/StatusTag";
import Dropdown from "@components/Dropdown/Dropdown";
import NumberInput from "@components/NumberInput/NumberInput";
import CopyButton from "@components/CopyButton/CopyButton";
import { getEmblemPalette } from "@utils/emblem";
import { summarizeGpus } from "@utils/gpu";
import { formatUptime } from "@utils/format";
import ModelLabel from "@components/ModelLabel/ModelLabel";
import Skeleton from "@components/Skeleton/Skeleton";
import "./index.scss";

const SAMPLERS = ["euler_a", "euler", "dpm++2m", "dpm++2m_karras", "ddim"];
const SIZES = ["512 x 512", "512 x 768", "768 x 512", "768 x 768", "1024 x 1024"];

function EmblemPlaceholder({ nodeId }) {
  const { initials, gradient } = getEmblemPalette(nodeId);
  return (
    <div className="nd-emblem-placeholder" style={{ background: gradient }}>{initials}</div>
  );
}

function EmblemImg({ src, nodeId }) {
  const [failed, setFailed] = useState(false);
  if (!src || failed) return <EmblemPlaceholder nodeId={nodeId} />;
  return <img src={src} alt="" onError={() => setFailed(true)} />;
}

function parseNode(n) {
  let hw = n.hardware || {};
  let svcs = n.services || [];
  if (typeof hw === "string") { try { hw = JSON.parse(hw); } catch { hw = {}; } }
  if (typeof svcs === "string") { try { svcs = JSON.parse(svcs); } catch { svcs = []; } }
  return { ...n, id: n.node_id || n.id, hardware: hw, services: svcs };
}

function getEmblemUrl(node) {
  const emblem = node.emblem;
  if (!emblem) return null;
  if (emblem.startsWith("http")) return emblem;
  return `/node/${encodeURIComponent(node.id || node.node_id)}/provider/file?path=${encodeURIComponent(emblem)}`;
}

function truncateId(id) {
  if (!id || id.length <= 16) return id || "";
  return id.slice(0, 10) + "..." + id.slice(-4);
}

function getSvcStatus(svc) {
  if (svc.server_loading) return "loading";
  if (svc.model) return "running";
  return "stopped";
}

// Picks the most user-relevant inspect field — ctx_size for llama.cpp,
// max_model_len for vllm — and renders the value as a compact "8K" badge.
// Returns null when no relevant field exists, so older providers without
// inspect data simply omit the badge.
function formatCtxBadge(svc) {
  const insp = svc?.inspect;
  if (!insp) return null;
  const raw = insp.ctx_size ?? insp.max_model_len;
  if (raw === undefined || raw === null || raw === "") return null;
  const n = parseInt(raw, 10);
  if (!Number.isFinite(n) || n <= 0) return null;
  if (n >= 1024 && n % 1024 === 0) return `${n / 1024}K`;
  return String(n);
}

// Generates a Markdown body shown in the About section when the operator has
// not written one. Keeps the card from looking empty by surfacing what the
// page already knows: identity, hardware, and live services.
function buildDefaultAbout(node, svcs, gpus, ram) {
  if (!node) return "";
  const lines = [];
  lines.push(`# ${truncateId(node.id || node.node_id || "Node")}`);
  if (node.owner_address) lines.push(`> Owner: \`${node.owner_address}\``);
  lines.push("");

  const hwParts = [];
  const gpu0 = (gpus && gpus[0]) || null;
  if (gpu0?.model) hwParts.push(`**GPU** ${gpu0.model}`);
  if (gpu0?.vram_total_gb) hwParts.push(`**VRAM** ${gpu0.vram_total_gb} GB`);
  if (ram?.total_gb) hwParts.push(`**RAM** ${ram.total_gb} GB`);
  if (hwParts.length) {
    lines.push("## Hardware");
    lines.push(hwParts.join(" · "));
    lines.push("");
  }

  const running = Array.isArray(svcs) ? svcs : [];
  if (running.length > 0) {
    lines.push("## Services");
    running.forEach(s => {
      const ctx = formatCtxBadge(s);
      const tail = [];
      if (s.model) tail.push(`\`${s.model}\``);
      if (ctx) tail.push(`ctx ${ctx}`);
      lines.push(`- **${s.name}**${tail.length ? " — " + tail.join(" · ") : ""}`);
    });
  } else {
    lines.push("_No services running._");
  }
  return lines.join("\n");
}

// ─── SD Studio ───
function SdStudio({ nodeId, model, service }) {
  const svcName = service || "sd-api";
  const [prompt, setPrompt] = useState("");
  const [negPrompt, setNegPrompt] = useState("");
  const [size, setSize] = useState("512 x 512");
  const [steps, setSteps] = useState(20);
  const [cfg, setCfg] = useState(7);
  const [seed, setSeed] = useState("-1");
  const [sampler, setSampler] = useState("euler_a");
  const [advOpen, setAdvOpen] = useState(false);
  const [generating, setGenerating] = useState(false);
  const [progress, setProgress] = useState(0);
  const [imageUrl, setImageUrl] = useState(null);
  const [error, setError] = useState("");
  const [added, setAdded] = useState(false);
  // Auto-delete the job from the provider queue once the result is fetched.
  const [autoCleanup, setAutoCleanup] = useState(true);
  const sseRef = useRef(null);

  // LoRA selection state — pre-loaded from active profile's defaults so the
  // panel mirrors what the server would auto-apply if `loras` is omitted.
  // `touched` distinguishes "use server-side defaults" (false → omit field
  // from request) from "user explicitly chose this set" (true → send array).
  // Profile-level disable is shown as a read-only banner (no inputs).
  const [loraArch, setLoraArch] = useState("");
  const [loraProfileDisabled, setLoraProfileDisabled] = useState(false);
  const [loraRows, setLoraRows] = useState([]);     // [{name, package_ref, weight, defaultWeight, checked, isDefault, source, sizeBytes}]
  const [loraTouched, setLoraTouched] = useState(false);

  // Build the initial row list from /provider/packages + active profile.
  // Re-runs when nodeId / svcName changes (rare) or when the user clicks
  // Restore defaults (handled by setting `loraTouched=false` and bumping
  // a refresh trigger via re-fetch).
  useEffect(() => {
    if (!nodeId || !svcName) return;
    let cancelled = false;
    (async () => {
      try {
        const [versionsRes, profSet] = await Promise.all([
          fetch(`/node/${encodeURIComponent(nodeId)}/provider/packages`, { headers: getAuthHeaders() })
            .then((r) => r.ok ? r.json() : [])
            .catch(() => []),
          fetchProfiles(nodeId, svcName).catch(() => null),
        ]);
        if (cancelled) return;
        const active = profSet && Array.isArray(profSet.profiles)
          ? (profSet.profiles.find((p) => p.name === profSet.active) || profSet.profiles[0])
          : null;
        const arch = active?.architecture || "";
        const ploras = active?.loras || {};
        setLoraArch(arch);
        setLoraProfileDisabled(!!ploras.disabled);
        // Map default → weight so we can pre-check matching rows. Map keyed
        // by package_ref because that's the canonical identifier (same
        // arch+name combo always resolves to one ref).
        const defaultsByRef = new Map();
        for (const d of (ploras.defaults || [])) {
          defaultsByRef.set(d.package_ref, d.weight ?? 0.7);
        }
        // Architecture filter mirrors engine-runner: only matching-arch
        // LoRAs end up in --lora-model-dir, so only those would actually
        // resolve at sd.cpp side. Hiding incompatible rows prevents
        // operators from picking LoRAs that the engine would silently
        // skip.
        const compatible = (Array.isArray(versionsRes) ? versionsRes : [])
          .filter((v) => v.type === "lora" && v.architecture === arch);
        const rows = compatible.map((l) => {
          const ref = `loras/${l.architecture}/${l.name}`;
          const dw = defaultsByRef.get(ref);
          const dl = (l.downloads || [])[0] || {};
          const url = (dl.download_url || "").toLowerCase();
          let source = "";
          if (url.includes("civitai.com") || l.hash_source === "civitai") source = "civitai";
          else if (url.includes("huggingface.co") || l.hash_source === "huggingface") source = "huggingface";
          else if (l.mode === "reference" || dl.ref_path) source = "imported";
          return {
            name: l.name,
            package_ref: ref,
            weight: dw != null ? dw : 0.7,
            defaultWeight: dw != null ? dw : 0.7,
            checked: dw != null,    // pre-checked when in profile defaults
            isDefault: dw != null,
            source,
            sizeBytes: dl.size_bytes || 0,
          };
        });
        setLoraRows(rows);
        setLoraTouched(false);
      } catch {}
    })();
    return () => { cancelled = true; };
  }, [nodeId, svcName]);

  // matchesDefaults: does the current selection equal the profile's default
  // state? When true the request can omit `loras` entirely and let the
  // server auto-prepend — that's the "untouched" path. We re-evaluate after
  // every toggle / weight change so re-checking a row back to its default
  // weight quietly clears the "overriding" indicator (instead of stickying
  // true once any interaction occurs).
  const matchesDefaults = (rows) => rows.every((r) =>
    r.checked === r.isDefault && (!r.checked || r.weight === r.defaultWeight)
  );

  // Toggle a row's checked state. Marks the panel touched so handleGenerate
  // sends an explicit `loras` array (overriding profile defaults). If the
  // post-toggle state matches defaults (e.g. user re-checked back to the
  // initial set), drop the override flag so the request goes back to
  // server-default.
  const toggleLoraRow = (name) => {
    setLoraRows((rows) => {
      const next = rows.map((r) => r.name === name ? { ...r, checked: !r.checked } : r);
      setLoraTouched(!matchesDefaults(next));
      return next;
    });
  };
  const setLoraWeight = (name, weight) => {
    setLoraRows((rows) => {
      const next = rows.map((r) => r.name === name ? { ...r, weight } : r);
      setLoraTouched(!matchesDefaults(next));
      return next;
    });
  };
  const disableAllLoras = () => {
    setLoraRows((rows) => {
      const next = rows.map((r) => ({ ...r, checked: false }));
      setLoraTouched(!matchesDefaults(next));
      return next;
    });
  };
  const restoreLoraDefaults = () => {
    setLoraRows((rows) => rows.map((r) => ({ ...r, checked: r.isDefault, weight: r.defaultWeight })));
    setLoraTouched(false);
  };

  const handleGenerate = async () => {
    if (!prompt.trim()) return;
    setGenerating(true);
    setAdded(false);
    setProgress(0);
    setError("");
    setImageUrl(null);

    const [w, h] = size.split(" x ").map(Number);
    const params = {
      prompt: prompt.trim(),
      negative_prompt: negPrompt.trim(),
      size: `${w}x${h}`,
      steps, cfg_scale: cfg,
      seed: seed === "-1" ? -1 : parseInt(seed) || -1,
      sampler_name: sampler,
    };
    // LoRA wiring — only attach `loras` when the user has interacted with
    // the panel. Untouched + has profile defaults → server's
    // applyLoraDefaultsToBody auto-prepends. This keeps the most common
    // path (use the profile defaults as-is) zero-effort and keeps explicit
    // user intent (override / disable all) reflected as `loras: [...]` /
    // `loras: []` in the body. Profile-level disable is handled server-side
    // (engine spawn drops --lora-model-dir) so the UI doesn't need to
    // gate generation here.
    if (loraTouched) {
      params.loras = loraRows
        .filter((r) => r.checked)
        .map((r) => ({ name: r.name, weight: r.weight }));
    }

    try {
      const job = await generateImage(nodeId, params, svcName);
      const jobId = job.job_id || job.id;
      if (!jobId) { setError("No job ID returned"); setGenerating(false); return; }

      // Fetch the final result body separately — poll/subscribe is
      // metadata-only now. consume=true auto-deletes the job after fetch.
      const loadResult = async () => {
        try {
          const r = await getJobResult(nodeId, jobId, svcName, { consume: autoCleanup });
          setImageUrl(r.type === "image" ? r.url : null);
        } catch (e) {
          setError(e.message || "Failed to fetch result");
        } finally {
          setGenerating(false);
        }
      };

      // Try SSE first
      if (sseRef.current) sseRef.current.close();
      sseRef.current = subscribeJob(nodeId, jobId, (data) => {
        setProgress(data.progress || 0);
        if (data.status === "done") {
          loadResult();
        } else if (data.status === "failed") {
          setError(data.error || "Generation failed");
          setGenerating(false);
        }
      }, () => {
        // SSE failed, fallback to polling
        const poll = setInterval(async () => {
          try {
            const data = await pollJob(nodeId, jobId, svcName);
            setProgress(data.progress || 0);
            if (data.status === "done") {
              clearInterval(poll);
              loadResult();
            } else if (data.status === "failed") {
              clearInterval(poll);
              setError(data.error || "Generation failed");
              setGenerating(false);
            }
          } catch {}
        }, 2000);
      }, svcName);
    } catch (e) {
      setError(e.message || "Failed to start generation");
      setGenerating(false);
    }
  };

  useEffect(() => { return () => { if (sseRef.current) sseRef.current.close(); }; }, []);

  return (
    <div className="nd-studio-panel">
      <div className="nd-sd-layout">
        <div className="nd-sd-controls">
          <div className="nd-sd-field">
            <label className="nd-sd-label">Prompt</label>
            <textarea className="nd-sd-textarea" rows={3} placeholder="Describe what you want to generate..."
              value={prompt} onChange={e => setPrompt(e.target.value)} />
          </div>
          <div className="nd-sd-field">
            <label className="nd-sd-label">Negative Prompt</label>
            <textarea className="nd-sd-textarea nd-neg" rows={2} placeholder="What to avoid..."
              value={negPrompt} onChange={e => setNegPrompt(e.target.value)} />
          </div>
          <div className="nd-sd-field">
            <label className="nd-sd-label">Model</label>
            <span className="nd-sd-model-badge">{model || "—"}</span>
          </div>
          <button className="nd-sd-adv-toggle" onClick={() => setAdvOpen(!advOpen)}>
            <span className={`nd-arrow ${advOpen ? "open" : ""}`}>&#9654;</span> Advanced
          </button>
          {advOpen && (
            <div className="nd-sd-adv">
              <div className="nd-sd-row">
                <div className="nd-sd-field nd-flex1">
                  <label className="nd-sd-label">Size</label>
                  <Dropdown value={size} options={SIZES.map(s => ({ value: s, label: s }))} onChange={setSize} />
                </div>
                <div className="nd-sd-field nd-flex1">
                  <label className="nd-sd-label">Sampler</label>
                  <Dropdown value={sampler} options={SAMPLERS.map(s => ({ value: s, label: s }))} onChange={setSampler} />
                </div>
              </div>
              <div className="nd-sd-row">
                <div className="nd-sd-field nd-flex1">
                  <label className="nd-sd-label">Steps</label>
                  <input className="nd-sd-input" type="number" value={steps} onChange={e => setSteps(Number(e.target.value))} />
                </div>
                <div className="nd-sd-field nd-flex1">
                  <label className="nd-sd-label">CFG</label>
                  <input className="nd-sd-input" type="number" step="0.5" value={cfg} onChange={e => setCfg(Number(e.target.value))} />
                </div>
                <div className="nd-sd-field nd-flex1">
                  <label className="nd-sd-label">Seed</label>
                  <input className="nd-sd-input" type="text" value={seed} onChange={e => setSeed(e.target.value)} />
                </div>
              </div>

              {/* LoRA panel — visible only when at least one compatible LoRA
                  is installed OR the active profile has LoRA fully disabled
                  (so the operator sees why their <lora:> tokens won't fire). */}
              {(loraRows.length > 0 || loraProfileDisabled) && (
                <div className="nd-sd-lora-block">
                  <div className="nd-sd-lora-head">
                    <span className="nd-sd-label">LoRAs <b className="nd-sd-arch">{loraArch && `(${loraArch})`}</b></span>
                    {!loraProfileDisabled && (
                      <>
                        <button
                          type="button"
                          className="nd-sd-lora-btn"
                          onClick={disableAllLoras}
                          disabled={!loraRows.some((r) => r.checked)}
                        >Disable all</button>
                        {loraTouched && (
                          <button
                            type="button"
                            className="nd-sd-lora-btn nd-sd-lora-btn-restore"
                            onClick={restoreLoraDefaults}
                          >Restore defaults</button>
                        )}
                      </>
                    )}
                  </div>
                  {loraProfileDisabled ? (
                    <div className="nd-sd-lora-banner">
                      🚫 LoRA disabled on this profile (Profiles tab → Disable LoRA toggle).
                      Engine spawned without <code>--lora-model-dir</code> — no LoRA can apply.
                    </div>
                  ) : (
                    <>
                      <div className="nd-sd-lora-list">
                        {loraRows.map((r) => (
                          <div key={r.name} className={`nd-sd-lora-row ${r.checked ? "is-checked" : ""}`}>
                            <label className="nd-sd-lora-check">
                              <input
                                type="checkbox"
                                checked={r.checked}
                                onChange={() => toggleLoraRow(r.name)}
                              />
                            </label>
                            <span className="nd-sd-lora-name" title={r.name}>{r.name}</span>
                            {r.source && (
                              <span className={`nd-sd-lora-badge nd-sd-lora-badge-${r.source}`}>
                                {r.source === "huggingface" ? "HF" : r.source === "civitai" ? "Civitai" : "Imported"}
                              </span>
                            )}
                            <div className={`nd-sd-lora-weight ${r.checked ? "" : "muted"}`}>
                              <input
                                type="range"
                                min="0"
                                max="1.5"
                                step="0.05"
                                value={r.weight}
                                disabled={!r.checked}
                                onChange={(e) => setLoraWeight(r.name, parseFloat(e.target.value))}
                                className="nd-sd-lora-slider"
                              />
                              <span className="nd-sd-lora-wval">{r.checked ? r.weight.toFixed(2) : "—"}</span>
                            </div>
                          </div>
                        ))}
                      </div>
                      <div className="nd-sd-lora-foot">
                        <span>
                          <b>{loraRows.filter((r) => r.checked).length}</b> selected · <b>{loraRows.length}</b> available
                          {loraTouched && <span className="nd-sd-lora-override"> · overriding defaults</span>}
                        </span>
                      </div>
                    </>
                  )}
                </div>
              )}
            </div>
          )}
          <div className="nd-sd-actions">
            <button className="nd-btn-gen" onClick={handleGenerate} disabled={generating || !prompt.trim()}>
              {generating ? `Generating... ${progress}%` : "Generate"}
            </button>
            <label className="nd-sd-cleanup" title="Delete the job from the provider queue after the result is fetched">
              <input type="checkbox" checked={autoCleanup} onChange={(e) => setAutoCleanup(e.target.checked)} />
              <span>Auto-cleanup</span>
            </label>
          </div>
          {error && <div className="nd-sd-error">{error}</div>}
        </div>
        <div className="nd-sd-result-area">
          <div className="nd-sd-result">
            {imageUrl ? <img src={imageUrl} alt="result" /> : (
              <span className="nd-sd-result-placeholder">
                {generating ? `Generating... ${progress}%` : "Generated image will appear here"}
              </span>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

// ─── LLM Studio ───
function LlmStudio({ nodeId, model, service }) {
  const svcPath = `/node/${encodeURIComponent(nodeId)}/svc/${encodeURIComponent(service || "llm-api")}`;
  const [messages, setMessages] = useState([
    { role: "system", content: "You are a helpful assistant." },
    { role: "user", content: "" },
  ]);
  const [temperature, setTemperature] = useState(0.7);
  const [maxTokens, setMaxTokens] = useState(512);
  const [topP, setTopP] = useState(0.9);
  const [waitMode, setWaitMode] = useState("sync"); // "sync" | "async"
  const [autoCleanup, setAutoCleanup] = useState(true); // async 모드에서 결과 받은 후 job 자동 삭제
  const [response, setResponse] = useState("");
  const [sending, setSending] = useState(false);
  const [progress, setProgress] = useState(""); // async 진행 상태 메시지
  const [error, setError] = useState("");

  const updateMsg = (i, field, val) => {
    setMessages(prev => prev.map((m, j) => j === i ? { ...m, [field]: val } : m));
  };

  const removeMsg = (i) => {
    setMessages(prev => prev.filter((_, j) => j !== i));
  };

  const addMsg = () => {
    setMessages(prev => [...prev, { role: "user", content: "" }]);
  };

  const clearAll = () => {
    setMessages([{ role: "system", content: "You are a helpful assistant." }, { role: "user", content: "" }]);
    setResponse("");
    setError("");
  };

  // Wait for job completion via poll (metadata-only). The result body
  // is fetched separately by the caller via getJobResult.
  const waitForJob = async (jobId) => {
    await new Promise(r => setTimeout(r, 1000));
    for (let i = 0; i < 120; i++) {
      try {
        const res = await fetch(`${svcPath}/v1/jobs/poll/${jobId}`);
        if (res.ok) {
          const job = await res.json();
          if (job.progress != null) setProgress(`running ${job.progress}%`);
          if (job.status === "done") return;
          if (job.status === "failed") throw new Error(job.error || "Job failed");
        }
      } catch (e) {
        if (i > 10) throw e;
      }
      await new Promise(r => setTimeout(r, i < 5 ? 1000 : i < 10 ? 2000 : 3000));
    }
    throw new Error("Job timed out");
  };

  const handleSend = async () => {
    const filtered = messages.filter(m => m.content.trim());
    if (filtered.length === 0) return;
    setSending(true);
    setError("");
    setResponse("");
    setProgress("");

    try {
      const body = {
        model,
        messages: filtered,
        temperature, max_tokens: maxTokens, top_p: topP,
      };
      // sync: wait + (optionally) auto-evict via ?consume=true.
      // async: submit only returns job_id — cleanup is handled by getJobResult({consume}).
      let suffix = "";
      if (waitMode === "sync") {
        suffix = autoCleanup ? "?wait=true&consume=true" : "?wait=true";
      }
      const res = await fetch(`${svcPath}/v1/chat/completions${suffix}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      });
      const data = await res.json();
      if (!res.ok) {
        const msg =
          (typeof data.error === "object" && data.error?.message) ||
          (typeof data.error === "string" && data.error) ||
          data.message ||
          `HTTP ${res.status}`;
        throw new Error(msg);
      }

      let finalData = data;
      // Async mode: 서버가 job_id 만 돌려주면 진행 상태 폴링 후 결과 별도 fetch.
      if (data.job_id && !data.choices) {
        setProgress("queued...");
        await waitForJob(data.job_id);
        const r = await getJobResult(nodeId, data.job_id, service || "llm-api", { consume: autoCleanup });
        finalData = r.type === "json" ? r.data : r.text;
      }

      const content =
        finalData.choices?.[0]?.message?.content ||
        finalData.result?.choices?.[0]?.message?.content ||
        finalData.content ||
        finalData.result ||
        JSON.stringify(finalData);
      setResponse(typeof content === "string" ? content : JSON.stringify(content, null, 2));
    } catch (e) {
      setError(e.message || "Request failed");
    }
    setSending(false);
    setProgress("");
  };

  return (
    <div className="nd-studio-panel">
      <div className="nd-llm-layout">
        <div className="nd-llm-settings">
          <div className="nd-llm-opt">
            <label className="nd-sd-label">Model</label>
            <span className="nd-sd-model-badge">{model || "—"}</span>
          </div>
          <div className="nd-llm-opt">
            <label className="nd-sd-label">Temp</label>
            <div className="nd-input-xs">
              <NumberInput value={temperature} step={0.1} min={0} max={2} onChange={setTemperature} />
            </div>
          </div>
          <div className="nd-llm-opt">
            <label className="nd-sd-label">Max Tokens</label>
            <div className="nd-input-sm">
              <NumberInput value={maxTokens} step={256} onChange={setMaxTokens} />
            </div>
          </div>
          <div className="nd-llm-opt">
            <label className="nd-sd-label">Top-p</label>
            <div className="nd-input-xs">
              <NumberInput value={topP} step={0.05} min={0} max={1} onChange={setTopP} />
            </div>
          </div>
          <div className="nd-llm-opt">
            <label className="nd-sd-label">Mode</label>
            <div className="nd-input-md">
              <Dropdown
                value={waitMode}
                options={[
                  { value: "sync", label: "Sync" },
                  { value: "async", label: "Async" },
                ]}
                onChange={setWaitMode}
              />
            </div>
          </div>
          <div className="nd-llm-opt">
            <label className="nd-sd-label">Cleanup</label>
            <label className="nd-sd-cleanup" title="Delete the job from the provider queue after the result is received">
              <input type="checkbox" checked={autoCleanup} onChange={(e) => setAutoCleanup(e.target.checked)} />
              <span>Auto-delete</span>
            </label>
          </div>
        </div>

        <div className="nd-msg-list">
          {messages.map((m, i) => (
            <div className="nd-msg-row" key={i}>
              <div className="nd-msg-role-wrap">
                <Dropdown
                  value={m.role}
                  options={[
                    { value: "system", label: "system" },
                    { value: "user", label: "user" },
                    { value: "assistant", label: "assistant" },
                    { value: "tool", label: "tool" },
                  ]}
                  onChange={(val) => updateMsg(i, "role", val)}
                />
              </div>
              <textarea className={`nd-msg-content ${m.role === "system" ? "system-msg" : ""}`}
                rows={m.role === "system" ? 2 : 1}
                value={m.content} onChange={e => updateMsg(i, "content", e.target.value)}
                placeholder={m.role === "system" ? "System prompt..." : "Message..."} />
              <button className="nd-msg-del" onClick={() => removeMsg(i)}>&times;</button>
            </div>
          ))}
        </div>

        <div className="nd-llm-actions">
          <button className="nd-btn-add-msg" onClick={addMsg}>+ Add Message</button>
          <button className="nd-btn-send" onClick={handleSend} disabled={sending}>
            {sending ? (progress || "Sending...") : "Send"}
          </button>
          <button className="nd-btn-clear" onClick={clearAll}>Clear All</button>
        </div>

        {(response || error) && (
          <div className="nd-llm-response">
            <div className="nd-llm-response-label">
              {error ? "Error" : "Assistant Response"}
            </div>
            <div className={`nd-llm-response-text${error ? " is-error" : ""}`}>
              {error || response}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

// ─── Main Page ───
export default function NodeDetailPage() {
  const { nodeId } = useParams();
  const navigate = useNavigate();
  const { t } = useTranslation();
  const { isLoggedIn } = useAuth();
  const [node, setNode] = useState(null);
  const [loading, setLoading] = useState(true);
  const [activeTab, setActiveTab] = useState(null);
  const [about, setAbout] = useState("");
  const [isFav, setIsFav] = useState(false);

  // Track whether this node is already in the user's My Nodes list so the
  // bottom-right action button can render as "added" vs "+ Add to My Nodes".
  // Only meaningful when logged in — otherwise the button is disabled and
  // the underlying IndexedDB list is per-browser anyway.
  useEffect(() => {
    if (!isLoggedIn) { setIsFav(false); return; }
    let cancelled = false;
    fetchMyNodes()
      .then((list) => {
        if (cancelled) return;
        const ids = new Set((list || []).map((n) => n.id));
        setIsFav(ids.has(nodeId));
      })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [nodeId, isLoggedIn]);

  const toggleFav = useCallback(async () => {
    if (!isLoggedIn) return;
    try {
      if (isFav) {
        await deleteMyNode(nodeId);
        setIsFav(false);
      } else {
        await addMyNode(nodeId, "");
        setIsFav(true);
      }
    } catch (e) { console.warn("toggleFav:", e); }
  }, [isFav, nodeId, isLoggedIn]);

  const loadNode = useCallback(async () => {
    const start = Date.now();
    let result = null;
    try {
      const rvData = await fetchRendezvousList().catch(() => []);
      const rvList = Array.isArray(rvData) ? rvData : [];
      for (const rv of rvList) {
        if (!rv.addr) continue;
        // Volatile metrics (queue_depth, total_jobs_done, …) live in
        // /v1/metrics and overwrite the static /v1/nodes snapshot. Without
        // this merge, this page shows the register-time values which lag
        // behind the heartbeat-tracked counters (sd-api can have done many
        // jobs since last re-register).
        const [nodes, metrics] = await Promise.all([
          fetchNodesByRendezvousAddr(rv.addr).catch(() => []),
          fetchMetricsByAddr(rv.addr).catch(() => []),
        ]);
        mergeMetricsIntoNodes(nodes, metrics);
        const found = (Array.isArray(nodes) ? nodes : []).find(n => (n.id || n.node_id) === nodeId);
        if (found) { result = parseNode(found); break; }
      }
      if (!result) {
        const [direct, metrics] = await Promise.all([
          fetchNodes().catch(() => []),
          fetchMetrics().catch(() => []),
        ]);
        mergeMetricsIntoNodes(direct, metrics);
        const found = (Array.isArray(direct) ? direct : []).find(n => (n.id || n.node_id) === nodeId);
        if (found) { result = parseNode(found); }
      }
    } catch {}
    // Minimum skeleton display time for smooth transition
    const elapsed = Date.now() - start;
    if (elapsed < 400) await new Promise(r => setTimeout(r, 400 - elapsed));
    if (result) setNode(result);
    setLoading(false);
  }, [nodeId]);

  useEffect(() => {
    loadNode();
    // 5s refresh — match the heartbeat cadence so card aggregate and
    // per-service stats stay in sync. 30s was too coarse — the overview
    // page picks up new total_jobs_done within seconds, leaving this page
    // showing stale 0 long after the user knows a job completed.
    const timer = setInterval(loadNode, 5000);
    return () => clearInterval(timer);
  }, [loadNode]);

  // Set default tab when node loads — pick the first running service.
  useEffect(() => {
    if (node && !activeTab) {
      const running = (node.services || []).filter(s => getSvcStatus(s) === "running");
      if (running.length > 0) setActiveTab(running[0].name);
    }
  }, [node, activeTab]);

  useEffect(() => {
    if (nodeId) {
      fetch(`/node/${encodeURIComponent(nodeId)}/provider/about`)
        .then(r => r.ok ? r.json() : {})
        .then(d => setAbout(d.about || ""))
        .catch(() => {});
    }
  }, [nodeId]);

  if (loading) {
    return (
      <div className="nd-page">
        <div className="nd-back-bar">
          <button className="nd-btn-back" onClick={() => navigate(-1)}>&#8592; Back</button>
        </div>
        <div className="p-24">
          <div className="flex-row gap-16 mb-20">
            <Skeleton.Circle size={64} />
            <div className="flex-1">
              <Skeleton.Line width="35%" className="mb-8" />
              <Skeleton.Line width="20%" height={12} className="mb-6" />
              <Skeleton.Line width="50%" height={12} />
            </div>
          </div>
          <div className="flex-row gap-8 mb-20">
            <Skeleton.Block width="80px" height={28} borderRadius={6} />
            <Skeleton.Block width="80px" height={28} borderRadius={6} />
            <Skeleton.Block width="80px" height={28} borderRadius={6} />
          </div>
          <Skeleton.Block height={100} className="mb-12" />
          <div className="flex-row gap-10">
            <Skeleton.Block width="48%" height={140} />
            <Skeleton.Block width="48%" height={140} />
          </div>
        </div>
      </div>
    );
  }

  if (!node) {
    return (
      <div className="nd-page">
        <div className="nd-back-bar">
          <button className="nd-btn-back" onClick={() => navigate(-1)}>&#8592; Back</button>
          <span className="nd-node-id">{nodeId}</span>
        </div>
        <div className="nd-loading">Node not found or offline</div>
      </div>
    );
  }

  const hw = node.hardware || {};
  const gpus = hw.gpus || [];
  const gpuSummary = summarizeGpus(gpus);
  const ram = hw.ram || {};
  const allSvcs = node.services || [];
  const svcs = allSvcs.filter(s => getSvcStatus(s) === "running");
  const activeSvc = svcs.find(s => s.name === activeTab);

  const renderStudio = () => {
    if (!activeSvc) return <div className="nd-empty">Select a service tab above.</div>;
    const svcName = activeSvc.name || "";
    if (svcName.includes("sd")) {
      return <SdStudio nodeId={node.id} model={activeSvc.model || "—"} service={svcName} />;
    }
    if (svcName.includes("llm")) {
      return <LlmStudio nodeId={node.id} model={activeSvc.model || "—"} service={svcName} />;
    }
    return <div className="nd-empty">Studio for "{svcName}" is not yet supported.</div>;
  };

  return (
    <div className="nd-page">
      <div className="nd-back-bar">
        <button className="nd-btn-back" onClick={() => navigate(-1)}>&#8592; Back</button>
        <span className="nd-node-id">{node.id}</span>
        <CopyButton value={node.id} title="Copy Node ID" />
        <StatusTag value={node.status || "offline"} />
        {node.auth_mode === "protected" && <svg className="shield-lock-icon" viewBox="0 0 24 24" width="20" height="20" title="Protected"><rect x="6" y="11" width="12" height="9" rx="2" fill="#4a9eff" stroke="#3080d0" strokeWidth="0.8"/><path d="M9 11V8a3 3 0 0 1 6 0v3" fill="none" stroke="#3080d0" strokeWidth="1.5" strokeLinecap="round"/><circle cx="12" cy="15.5" r="1.5" fill="#fff"/></svg>}
      </div>

      <div className="nd-content">
        {/* Hero */}
        <div className="nd-hero">
          <div className="nd-emblem">
            <EmblemImg src={getEmblemUrl(node)} nodeId={node.id || node.node_id} />
          </div>
          <div className="nd-hero-info">
            <div className="nd-hw-tags">
              {gpuSummary && (
                <div className="nd-hw-tag">
                  <span className="nd-hw-label">GPU</span>
                  <span className="nd-hw-value">{gpuSummary}</span>
                </div>
              )}
              {gpus.length > 0 && gpus[0].vram_total_gb && (
                <div className="nd-hw-tag"><span className="nd-hw-label">VRAM</span><span className="nd-hw-value">{gpus[0].vram_total_gb} GB</span></div>
              )}
              {ram.total_gb && (
                <div className="nd-hw-tag"><span className="nd-hw-label">RAM</span><span className="nd-hw-value">{ram.total_gb} GB</span></div>
              )}
              {node.version && (
                <div className="nd-hw-tag"><span className="nd-hw-label">Version</span><span className="nd-hw-value">{node.version}</span></div>
              )}
              {formatUptime(node.started_at) && (
                <div className="nd-hw-tag" title="Uptime since first register"><span className="nd-hw-label">Uptime</span><span className="nd-hw-value">{formatUptime(node.started_at)}</span></div>
              )}
              {node.ek_cert_issuer && (
                <div className="nd-hw-tag"><span className="nd-hw-label">TPM</span><span className="nd-hw-value">{node.ek_cert_issuer}</span></div>
              )}
            </div>
            <div className="nd-svc-cards">
              {svcs.map((s, i) => {
                const st = getSvcStatus(s);
                const ctx = formatCtxBadge(s);
                return (
                  <div className="nd-svc-card" key={i}>
                    <div className="nd-svc-name">
                      <span className={`svc-dot ${st}`} />
                      {s.name}
                      {ctx && <span className="svc-ctx-badge" title="Context length">{ctx}</span>}
                    </div>
                    <div className="nd-svc-model">{s.model
                      ? <ModelLabel modelName={s.model} originUrl={s.model_origin_url} hash={s.model_hash} />
                      : "\u2014"}</div>
                    <div className="nd-svc-stats">
                      <span>Pending <strong>{s.queue_depth || 0}</strong></span>
                      <span>Done <strong>{(s.total_jobs_done || 0).toLocaleString()}</strong></span>
                      {/* Avg job latency — only shown when the service
                          has actually processed jobs. Sub-second values
                          render as "ms" so quick generations don't read
                          as "0.0s"; 1s+ renders with one decimal. */}
                      {s.avg_job_sec > 0 && (
                        <span>Avg <strong>{
                          s.avg_job_sec < 1
                            ? `${Math.round(s.avg_job_sec * 1000)}ms`
                            : `${s.avg_job_sec.toFixed(1)}s`
                        }</strong></span>
                      )}
                    </div>
                  </div>
                );
              })}
              {svcs.length === 0 && <div className="nd-svc-empty">No services running</div>}
            </div>
            <div className="nd-owner-row">
              {node.owner_address && <div className="nd-owner">Owner: <strong>{node.owner_address}</strong><CopyButton value={node.owner_address} title="Copy Owner" /></div>}
              <button
                type="button"
                className={`nd-fav-btn${isFav ? " is-fav" : ""}`}
                onClick={toggleFav}
                disabled={!isLoggedIn}
                title={!isLoggedIn ? "Login required" : (isFav ? "Remove from My Nodes" : "Add to My Nodes")}
              >
                {isFav ? "★ My Nodes" : "☆ + My Nodes"}
              </button>
            </div>
          </div>
        </div>

        {/* About — falls back to a node-summary default when the operator
            has not written one. Avoids the awkward empty-card state. */}
        <div className="nd-section">
          <div className="nd-section-title">About</div>
          <div className="nd-about">
            <ReactMarkdown>{(about && about.trim()) || buildDefaultAbout(node, svcs, gpus, ram)}</ReactMarkdown>
            {!(about && about.trim()) && (
              <div className="nd-about-default-hint">— auto-generated summary —</div>
            )}
          </div>
        </div>

        {/* Studio */}
        <div className="nd-section">
          <div className="nd-section-title">Studio</div>
          {svcs.length > 0 ? (
            <>
              <div className="nd-studio-tabs">
                {svcs.map((s, i) => (
                  <span key={i} className={`nd-studio-tab ${activeTab === s.name ? "active" : ""}`}
                    onClick={() => setActiveTab(s.name)}>{s.name}</span>
                ))}
              </div>
              {renderStudio()}
            </>
          ) : (
            <div className="nd-empty">No services available.</div>
          )}
        </div>

      </div>
    </div>
  );
}
