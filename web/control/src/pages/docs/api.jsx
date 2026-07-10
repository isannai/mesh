import React, { useState, useEffect, useCallback, useRef } from "react";
import Dropdown from "@components/Dropdown/Dropdown";
import { getAuthHeaders } from "@utils/wallet";
import { wrapSdCppExtraArgs } from "@utils/sdcpp";
import "./api.scss";

// noAuth — hide the auth method selector entirely. Used on tracking
// endpoints (poll / subscribe) where the broker doesn't enforce ownership
// at all — anyone with a jobId can read status. Showing the selector
// there would imply (incorrectly) that auth choice matters.
function TryIt({ children, auth, noAuth }) {
  const [open, setOpen] = useState(false);
  // Auth method selector — applies to every Try It panel now (not just
  // `auth={true}` endpoints) so operators can test all three ownership
  // paths without leaving the docs page:
  //   • session   — current logged-in wallet (most common; getAuthHeaders())
  //   • anonymous — no auth header at all; submissions land in free tier,
  //                 results readable by anyone with the jobId
  //   • manual    — paste sig + message directly (for cross-wallet tests
  //                 or debugging another address's job ownership)
  // Default = "session" so the existing behavior (signed when logged in)
  // is preserved.
  const [authMethod, setAuthMethod] = useState("session");
  const sessionHeaders = getAuthHeaders() || {};
  const hasSession = !!sessionHeaders.Authorization;

  const connectMetaMask = async () => {
    try {
      const { connectMetaMask: connect, buildMessage, signWithMetaMask, saveSession } = await import("@utils/wallet");
      const address = await connect();
      const { message, expiresAt } = buildMessage("owner", "broker", 3600);
      const sig = await signWithMetaMask(address, message);
      saveSession(address, message, sig, expiresAt);
      window.location.reload();
    } catch (e) {
      alert("MetaMask: " + e.message);
    }
  };

  return (
    <>
      <div className="api-try-toggle" onClick={() => setOpen(!open)}>
        <span className={`api-try-arrow${open ? " open" : ""}`}>▶</span>
        <span className="api-try-label">Try it</span>
      </div>
      {open && (
        <div className="api-try-panel">
          {!noAuth && (
            <>
              <div className="api-try-row">
                <label>auth</label>
                <div style={{ flex: 1 }}>
                  <Dropdown
                    value={authMethod}
                    onChange={setAuthMethod}
                    options={[
                      { value: "session", label: "Session (current wallet)" },
                      { value: "anonymous", label: "Anonymous (no auth)" },
                      { value: "manual", label: "Manual (paste signature)" },
                    ]}
                  />
                </div>
                {/* getTryItAuth reads the auth method via a DOM query on
                    the panel. The shared Dropdown component renders into a
                    portal so its <li>s aren't reachable from the panel;
                    mirror the selected value into a hidden input so the
                    DOM probe still works without plumbing React state
                    down to every TryIt button handler. */}
                <input type="hidden" className="api-auth-method" value={authMethod} readOnly />
              </div>
              {authMethod === "session" && !hasSession && (
                <div className="api-try-row">
                  <label></label>
                  <button className="api-try-send" onClick={connectMetaMask}>🦊 Connect MetaMask</button>
                </div>
              )}
              {authMethod === "session" && hasSession && (
                <div className="api-try-row">
                  <label></label>
                  <span style={{ fontSize: 10, color: "var(--color-success)" }}>✓ Connected — broker will inject X-Caller-Address</span>
                </div>
              )}
              {authMethod === "anonymous" && (
                <div className="api-try-row">
                  <label></label>
                  <span style={{ fontSize: 10, color: "var(--text-muted)" }}>
                    No auth header sent. Submissions saved with empty SubmitterAddress — anyone with the jobId can fetch the result.
                  </span>
                </div>
              )}
              {authMethod === "manual" && (
                <>
                  <div className="api-try-row"><label>signature</label><input type="text" className="api-auth-sig" placeholder="ISANN signature hex" /></div>
                  <div className="api-try-row"><label>message</label><input type="text" className="api-auth-msg" placeholder="role:target:service:nonce:expiresAt:nodes" /></div>
                </>
              )}
            </>
          )}
          {children}
        </div>
      )}
    </>
  );
}

// OpenAI chat-completion role set. `tool` is for messages carrying the
// result of a prior assistant tool_call back into the conversation.
const ROLE_OPTIONS = [
  { value: "system", label: "system" },
  { value: "user", label: "user" },
  { value: "assistant", label: "assistant" },
  { value: "tool", label: "tool" },
];

// Editor for chat-completion `messages` arrays — lets the operator add /
// remove / re-role multi-turn messages instead of being forced into a
// single user message. Mirrors its serialized JSON into a hidden input
// (#`id`) so the existing DOM-query Send Request handlers can read it
// without React state plumbing.
function MessagesEditor({ id, defaults }) {
  const [msgs, setMsgs] = useState(defaults || [
    { role: "system", content: "You are a helpful assistant." },
    { role: "user", content: "Hello" },
  ]);
  const update = (idx, field, value) =>
    setMsgs(arr => arr.map((m, i) => (i === idx ? { ...m, [field]: value } : m)));
  const remove = idx => setMsgs(arr => arr.filter((_, i) => i !== idx));
  const add = () => {
    // Auto-pick the natural next role — alternate user/assistant for
    // typical chat turns. `tool` is never auto-inserted; operators pick
    // it explicitly from the dropdown after a real assistant tool_call.
    const last = msgs[msgs.length - 1];
    const next = last?.role === "user" ? "assistant" : "user";
    setMsgs(arr => [...arr, { role: next, content: "" }]);
  };
  return (
    <div className="api-try-messages">
      <div className="api-try-messages-header">
        <label>messages <span className="req">*</span></label>
        <button type="button" className="api-try-msg-add" onClick={add}>+ Add message</button>
      </div>
      {msgs.map((m, idx) => (
        <div key={idx} className="api-try-message-row">
          <div className="api-try-msg-role">
            <Dropdown
              value={m.role}
              onChange={v => update(idx, "role", v)}
              placeholder=""
              options={ROLE_OPTIONS}
            />
          </div>
          <textarea
            rows={2}
            value={m.content}
            onChange={e => update(idx, "content", e.target.value)}
            placeholder={
              m.role === "system" ? "system instruction…"
              : m.role === "user" ? "user turn…"
              : m.role === "tool" ? "tool result (JSON or text)…"
              : "assistant turn…"
            }
          />
          <button
            type="button"
            className="api-try-msg-remove"
            onClick={() => remove(idx)}
            disabled={msgs.length <= 1}
            title="Remove message"
          >×</button>
        </div>
      ))}
      <input type="hidden" id={id} value={JSON.stringify(msgs)} readOnly />
    </div>
  );
}

// Helper: get auth headers based on the TryIt panel's auth-method selector.
//
// Three modes:
//   • session    → current wallet via getAuthHeaders() (default)
//   • anonymous  → empty headers (free-tier submission / no ownership)
//   • manual     → paste sig + msg directly (cross-wallet ownership tests)
//
// Returning {} on anonymous is intentional — the call site spreads this
// into request init.headers so empty means "no auth fields appended".
// Broker's ownership-gating sees no X-Caller-Address and either matches
// the empty SubmitterAddress (free-tier path) or returns 403 if the job
// was originally signed.
function getTryItAuth(el) {
  const panel = el.closest(".api-try-panel");
  const method = panel?.querySelector(".api-auth-method")?.value;
  if (method === "anonymous") {
    return {};
  }
  if (method === "manual") {
    const sig = panel?.querySelector(".api-auth-sig")?.value;
    const msg = panel?.querySelector(".api-auth-msg")?.value;
    if (sig && msg) {
      return { "Authorization": "ISANN " + sig, "X-ISANN-Message": msg };
    }
    return {};
  }
  // "session" or no selector (older code path) → fall back to session.
  return getAuthHeaders() || {};
}

// Standard auth header set shown when an endpoint is ownership-gated or
// requires a wallet signature. Kept in one place so the docs stay
// consistent across every job submit / result endpoint.
const OWNERSHIP_HEADERS = [
  { name: "Authorization", required: false, type: "string", desc: "Wallet signature in the form 'ISANN <65-byte hex>'. Required only when you want the broker to inject X-Caller-Address and record ownership on the resulting job. Omitting → submission lands as anonymous (free-tier)." },
  { name: "X-ISANN-Message", required: false, type: "string", desc: "The signed message body, paired with Authorization (format: role:target:service:nonce:expiresAt:nodes)." },
];
const AUTH_REQUIRED_HEADERS = [
  { name: "Authorization", required: true, type: "string", desc: "Wallet signature 'ISANN <hex>'. Required — endpoint mutates state or returns owner-restricted data." },
  { name: "X-ISANN-Message", required: true, type: "string", desc: "Signed message body (role:target:service:nonce:expiresAt:nodes)." },
];

function ApiCard({ method, path, title, badge, auth, ownership, danger, desc, params, pathParams, queryParams, headers, response, example, children }) {
  const cardId = title ? "api-" + title.toLowerCase().replace(/[^a-z0-9]+/g, "-") : undefined;
  // Auto-inject the standard auth header block when the card flags itself
  // as ownership-gated (submit/result endpoints) or fully-required (`auth`)
  // and the caller didn't supply an explicit custom headers list. Lets
  // most cards stay terse while still surfacing the auth contract.
  const effectiveHeaders = headers
    ? headers
    : auth ? AUTH_REQUIRED_HEADERS
    : ownership ? OWNERSHIP_HEADERS
    : null;
  return (
    <div className="api-card" id={cardId}>
      <div className="api-card-header">
        <div className="api-card-header-left">
          <span className={`api-method ${method}`}>{method.toUpperCase()}</span>
          <span className="api-card-path">{path}</span>
        </div>
        <div className="api-card-header-right">
          {auth && <span className="api-card-badge api-card-badge-auth" title="Requires wallet signature (owner/admin)">Auth</span>}
          {ownership && !auth && <span className="api-card-badge api-card-badge-ownership" title="Optional wallet signature — anonymous submissions allowed, signed submissions get ownership protection">Ownership-gated</span>}
          {danger && <span className="api-card-badge api-card-badge-critical" title="Destructive — mutates node state">{typeof danger === "string" ? danger : "Critical"}</span>}
          {badge && <span className={`api-card-badge api-card-badge-${String(badge).toLowerCase().replace(/[^a-z0-9]+/g, "-")}`}>{badge}</span>}
        </div>
      </div>
      <div className="api-card-body">
        <div className="api-card-desc">{desc}</div>
        {pathParams && (<>
          <h4>Path Parameters</h4>
          <table className="api-param-table">
            <thead><tr><th>Parameter</th><th>Type</th><th>Description</th></tr></thead>
            <tbody>{pathParams.map(p => (
              <tr key={p.name}><td>{p.name} <span className="req">required</span></td><td><span className="type">{p.type}</span></td><td>{p.desc}</td></tr>
            ))}</tbody>
          </table>
        </>)}
        {queryParams && (<>
          <h4>Query Parameters</h4>
          <table className="api-param-table">
            <thead><tr><th>Parameter</th><th>Type</th><th>Default</th><th>Description</th></tr></thead>
            <tbody>{queryParams.map(p => (
              <tr key={p.name}>
                <td>{p.name} <span className={p.required ? "req" : "opt"}>{p.required ? "required" : "optional"}</span></td>
                <td><span className="type">{p.type}</span></td>
                <td className="api-table-default">{p.default || "—"}</td>
                <td>{p.desc}</td>
              </tr>
            ))}</tbody>
          </table>
        </>)}
        {effectiveHeaders && (<>
          <h4>Headers</h4>
          <table className="api-param-table">
            <thead><tr><th>Header</th><th>Type</th><th>Description</th></tr></thead>
            <tbody>{effectiveHeaders.map(h => (
              <tr key={h.name}>
                <td>{h.name} <span className={h.required ? "req" : "opt"}>{h.required ? "required" : "optional"}</span></td>
                <td><span className="type">{h.type}</span></td>
                <td>{h.desc}</td>
              </tr>
            ))}</tbody>
          </table>
        </>)}
        {params && (<>
          <h4>Request Body <span className="api-content-type">application/json</span></h4>
          <table className="api-param-table">
            <thead><tr><th>Parameter</th><th>Type</th><th>Default</th><th>Description</th></tr></thead>
            <tbody>{params.map(p => (
              <tr key={p.name}>
                <td>{p.name} <span className={p.required ? "req" : "opt"}>{p.required ? "required" : "optional"}</span></td>
                <td><span className="type">{p.type}</span></td>
                <td className="api-table-default">{p.default || "—"}</td>
                <td>{p.desc}</td>
              </tr>
            ))}</tbody>
          </table>
        </>)}
        {response && (<>
          <h4>Response <span className="api-content-type">200 OK</span></h4>
          <pre className="api-code">{response}</pre>
        </>)}
        {example && (<>
          <h4>Example (curl)</h4>
          <pre className="api-code">{example}</pre>
        </>)}
        {children}
      </div>
    </div>
  );
}

// OwnershipNote — short inline reminder for endpoints that enforce
// per-job ownership. Placed inside an ApiCard via children.
function OwnershipNote({ kind = "read" }) {
  const verb = kind === "delete" ? "delete" : kind === "consume" ? "consume" : "fetch";
  return (
    <div className="api-note api-note-ownership">
      <b>Ownership:</b> If the job was submitted with a wallet signature, only the same wallet can {verb} it.
      Anonymous submissions are free-tier (no gating). Mismatch → <code>403 Forbidden</code>. See
      the <a href="#api-job-ownership">Job Ownership</a> banner for the full flow.
    </div>
  );
}

// ═══════ Common 카테고리 ═══════
function CommonPage({ tryFetch, selectedNode }) {
  return (<>

    {/* ── Job Ownership (policy banner, 1 곳) ─────────────────────── */}
    <div className="api-card" id="api-job-ownership">
      <div className="api-card-header">
        <div className="api-card-header-left">
          <span className="api-method get">POLICY</span>
          <span className="api-card-path">Job Ownership</span>
        </div>
      </div>
      <div className="api-card-body">
        <div className="api-card-desc">
          When a client submits a job with a wallet signature, the broker injects the verified
          EOA address into <code>X-Caller-Address</code> before forwarding to the provider, and the
          provider records it on the job as <code>submitter_address</code>. From then on, status / result /
          outputs / delete operations require the caller to present the same wallet signature
          (same headers as the submit call). Submissions made without a signature are stored
          with empty <code>submitter_address</code> and remain free-tier (no gating).
        </div>
        <h4>Required Headers (when wallet-authenticated)</h4>
        <table className="api-param-table">
          <thead><tr><th>Header</th><th>Description</th></tr></thead>
          <tbody>
            <tr><td><code>Authorization</code></td><td><code>ISANN {`{`}65-byte sig hex{`}`}</code> — EIP-191 personal_sign over the message</td></tr>
            <tr><td><code>X-ISANN-Message</code></td><td><code>{`{role}:{target}:{service}:{nonce}:{expiresAt}:{nodes}`}</code></td></tr>
          </tbody>
        </table>
        <h4>Flow</h4>
        <pre className="api-code">{`Client
  Authorization: ISANN {sig}
  X-ISANN-Message: user:broker:*:0:1715600000:*
        │
        ▼
Broker authMiddleware
  ecrecover(message, sig) → 0xABC...
  → X-Caller-Address: 0xABC...   (Set, never Add — overrides client)
        │
        ▼
Provider
  Submit  → job.submitter_address = "0xABC..."
  Fetch   → if job.submitter_address != "" && header != it → 403 Forbidden`}</pre>
        <h4>Affected Endpoints</h4>
        <ul className="api-list">
          <li><code>GET /v1/jobs/{`{jobId}`}</code> — status JSON</li>
          <li><code>GET /v1/jobs/{`{jobId}`}/result</code> — result body (also <code>?consume=true</code>)</li>
          <li><code>GET /outputs/{`{filename}`}</code> — disk artifact (also <code>?consume=true</code>)</li>
          <li><code>DELETE /v1/jobs/{`{jobId}`}</code> — explicit eviction</li>
        </ul>
        <h4>Not Gated (intentional)</h4>
        <ul className="api-list">
          <li><code>GET /v1/jobs/poll/{`{jobId}`}</code> and <code>/v1/jobs/subscribe/{`{jobId}`}</code> — metadata-only progress channel</li>
          <li><code>POST</code> submit endpoints — anyone with a valid (or no) signature can submit</li>
        </ul>
      </div>
    </div>

    {/* ── Health & Info (public) ──────────────────────────────────── */}
    <ApiCard method="GET" path="/health" title="Health Check"
      desc="Check Broker server health status."
      response={`{ "status": "ok", "version": "0.1.0", "hash": "abc123..." }`}
      example={`curl https://your-broker:8080/health`}
    >
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/health", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/info" title="Info"
      desc="Return broker self-description: target proxy ID, configured RV, and broker's node ID. Used by the Setup page and clients to identify which broker they are talking to."
      response={`{
  "target": "0xBADa8bE8...",          // target_proxy_id (if proxy mode)
  "router": "0x...",                  // router address (if set)
  "rendezvous": "https://rv-host:9000",
  "id": "0xBADa8bE8..."               // this broker's node identity
}`}
      example={`curl https://your-broker:8080/info`}
    >
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/info", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/node-id" title="Node ID"
      desc="Return the broker's own NodeIdentity (full record). Lower-level variant of /info — useful for debugging signature/EOA mapping."
      response={`{
  "address": "0xBADa8bE8...",         // EOA derived from the broker's signing key
  "public_key": "...",
  "version": "0.1.0",
  "bin_hash": "..."
}`}
      example={`curl https://your-broker:8080/node-id`}
    >
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/node-id", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    {/* ── Catalog (public) ────────────────────────────────────────── */}
    <ApiCard method="GET" path="/v1/nodes" title="Node Discovery"
      desc="Fetch registered nodes from the Rendezvous server. Includes status, hardware specs, and service info for each node. Proxies to RV /v1/nodes with default role=provider when no query is set."
      params={[
        { name: "role", type: "string", default: "provider", desc: "Filter by role: provider | broker" },
        { name: "service", type: "string", desc: "Only nodes that run this service (e.g. llm-api)" },
        { name: "model", type: "string", desc: "Exact model match" },
        { name: "gpu", type: "string", desc: "GPU name substring (e.g. 4070, 3060)" },
        { name: "min_vram", type: "float", desc: "Minimum VRAM GB on any GPU" },
        { name: "status", type: "string", desc: "idle | busy | loading | stopped" },
        { name: "online", type: "bool", desc: "true = only nodes active in last 90s" },
        { name: "q", type: "string", desc: "Substring search on node ID or owner address" },
        { name: "page", type: "int", desc: "Page number (1-based, requires limit)" },
        { name: "limit", type: "int", default: "50", desc: "Page size" },
      ]}
      response={`[{
  "id": "0x7622De2460Db4eE712b0C84C0b8E98E635fF88C9",
  "addr": "127.0.0.1:4433",
  "status": "idle",           // idle | busy | offline
  "version": "0.1.0",
  "hardware": {
    "gpus": [{ "name": "NVIDIA GeForce RTX 4090", "vram_free_gb": 23.5 }],
    "ram": { "total_gb": 31.9, "free_gb": 20.0 }
  },
  "services": [{ "name": "sd-api", "model": "v1-5-pruned.safetensors", "queue_depth": 0 }],
  "tpm_verified": true,
  "ek_cert_issuer": "CSME ADL PTT  01SVN"
}]`}
    >
      <div className="api-note"><b>Tip:</b> Select a node with <code>status: "idle"</code> + <code>queue_depth: 0</code> for immediate processing. Actual speed depends on the node's GPU.</div>
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/v1/nodes", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/v1/metrics" title="Metrics"
      desc={<>Live per-(node, service) service metrics proxied from the Rendezvous server's <code>/v1/metrics</code>. Returns one row per (node, service) with current status, queue depth, total jobs done, and average job duration.</>}
      params={[
        { name: "service", type: "string", desc: "Filter by service name (e.g. llm-api, sd-api)" },
        { name: "status", type: "string", desc: "idle | busy | loading | stopped" },
        { name: "model", type: "string", desc: "Exact model match (cross-ref via static /v1/nodes)" },
        { name: "gpu", type: "string", desc: "GPU name substring (cross-ref via static)" },
        { name: "min_vram", type: "float", desc: "Minimum VRAM GB (cross-ref via static)" },
        { name: "node_id", type: "string", desc: "Exact node ID(s), comma-separated. Overrides other filters." },
      ]}
      response={`[
  {
    "node_id": "P:0x8fF81256...",
    "service": "sd-api",
    "status": "idle",
    "queue_depth": 0,
    "total_jobs_done": 150,
    "avg_job_sec": 12,
    "running_job_id": ""
  }
]`}
      example={`curl "https://your-broker:8080/v1/metrics?service=llm-api&status=idle"`}
    >
      <TryIt>
        <div className="api-try-row"><label>service</label><input type="text" id="try-metrics-service" placeholder="llm-api / sd-api (optional)" /></div>
        <div className="api-try-row"><label>status</label><input type="text" id="try-metrics-status" placeholder="idle / busy (optional)" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const qs = new URLSearchParams();
            const svc = document.getElementById("try-metrics-service")?.value; if (svc) qs.set("service", svc);
            const st = document.getElementById("try-metrics-status")?.value; if (st) qs.set("status", st);
            const q = qs.toString();
            tryFetch("GET", "/v1/metrics" + (q ? "?" + q : ""), null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/v1/search/nodes" title="Search Nodes"
      desc={<>Universal search across nodes. Auto-routes the query — hash (sha256), Ethereum address, GPU model name, engine name, node ID, or free-text — and returns a flat result list. 5-second response cache. Etherscan-style "single search box" UX.</>}
      params={[
        { name: "q", type: "string", required: true, desc: "Search query — auto-detected (hash / 0x address / GPU / engine / node ID / text)" },
      ]}
      response={`{
  "query": "4090",
  "kind": "gpu",                    // hash | address | gpu | engine | node | text
  "results": [
    {
      "node_id": "P:0x8fF81256...",
      "addr": "...",
      "hardware": {
        "gpus": [{ "name": "NVIDIA GeForce RTX 4090", "vram_total_gb": 24 }]
      },
      "services": [{ "name": "llm-api", "model": "..." }]
    }
  ],
  "cached": false
}`}
      example={`# GPU
curl "https://your-broker:8080/v1/search/nodes?q=4090"

# Owner address
curl "https://your-broker:8080/v1/search/nodes?q=0xB171fe0B..."

# Free text (matches model / service name)
curl "https://your-broker:8080/v1/search/nodes?q=qwen"`}
    >
      <TryIt>
        <div className="api-try-row"><label>q <span className="req">*</span></label><input type="text" id="try-search-q" placeholder="4090, 0xabc, qwen, sd-api ..." /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const q = document.getElementById("try-search-q")?.value;
            if (!q) { el.style.display = "block"; el.textContent = "Enter q"; return; }
            tryFetch("GET", "/v1/search/nodes?q=" + encodeURIComponent(q), null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    {/* ── UI Metadata (public) ────────────────────────────────────── */}
    <ApiCard method="GET" path="/v1/cards" title="Cards"
      desc={<>Per-card UI visibility map. Public read — every client uses this to hide cards / menu items the broker owner disabled in Settings. Missing keys default to enabled.</>}
      response={`{
  "cards": {
    "logs":    { "enabled": false },
    "install": { "enabled": false }
    // missing keys default to enabled
  }
}`}
      example={`curl https://your-broker:8080/v1/cards`}
    >
      <div className="api-note">
        <b>Known card IDs:</b> <code>nodes</code>, <code>my-nodes</code>, <code>pipeline</code>, <code>resources</code>, <code>api</code>, <code>install</code>, <code>settings</code>, <code>logs</code>.<br/>
        <b>Write:</b> owners use <code>PUT /v1/admin/cards</code> (owner-sig required).
      </div>
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/v1/cards", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/v1/api/policy" title="API Policy"
      desc={<>Current broker feature gate policy. Public read — clients use this to hide UI features whose backend routes are disabled by the owner. Disabled features return <code>403 feature_disabled</code> on the server side.</>}
      response={`{
  "enabled_features": [
    "info", "node_discovery", "gate_proxy", "auth_verify",
    "my_nodes", "pipeline",
    "node_proxy_svc", "node_proxy_provider", "node_proxy_installer"
  ],
  "all_features": [ /* full feature catalog */ ],
  "presets": ["central", "personal"]
}`}
      example={`curl https://your-broker:8080/v1/api/policy`}
    >
      <div className="api-note">
        <b>Features:</b> Semantic route groups — e.g. <code>pipeline</code> gates all <code>/v1/pipeline/*</code>, <code>node_proxy_svc</code> gates <code>/node/{`{id}`}/svc/...</code>.<br/>
        <b>Write:</b> owners use <code>PUT /v1/admin/api-features</code> or <code>POST /v1/admin/api-features/preset</code>.
      </div>
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/v1/api/policy", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    {/* ── Auth (wallet-sig) ───────────────────────────────────────── */}
    <ApiCard method="POST" path="/v1/auth/verify" title="Auth Verify" auth
      desc="Verify MetaMask (EIP-191) signature and return the resolved role."
      params={[
        { name: "Authorization", type: "header", required: true, desc: "ISANN {signature}" },
        { name: "X-ISANN-Message", type: "header", required: true, desc: "role:target:service:nonce:expiresAt:nodes" },
      ]}
      response={`{ "ok": "true", "role": "owner", "address": "0xB171fe0B..." }`}
      example={`curl -X POST https://your-broker:8080/v1/auth/verify \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:broker:*:0:1743521600:*"`}
    >
      <TryIt auth>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("POST", "/v1/auth/verify", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/v1/my-nodes/{nodeId}/auth" title="Node Auth" auth
      desc="Verify node ownership via QUIC tunnel to Provider. Only owner/admin in Provider's auth.json can pass."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID to verify ownership" }]}
      response={`{ "status": "ok", "auth": true, "role": "owner" }`}
      example={`curl -X POST https://your-broker:8080/v1/my-nodes/{nodeId}/auth \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:broker:*:0:1743521600:*"`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>nodeId <span className="req">*</span></label><input type="text" id="try-nodeauth-id" defaultValue={selectedNode} placeholder="P:0x7622De..." /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const nid = document.getElementById("try-nodeauth-id")?.value;
            if (!nid) { el.style.display = "block"; el.textContent = "Enter node ID"; return; }
            tryFetch("POST", `/v1/my-nodes/${encodeURIComponent(nid)}/auth`, null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>
  </>);
}

// ═══════ Image Generation 카테고리 ═══════
function ImageGenPage({ tryFetch, selectedNode }) {
  return (<>
    <h2>Image Generation API</h2>
    <p className="api-page-subtitle-tight">Image generation API via sd-api service (Stable Diffusion).</p>

    <div className="api-flow">
      <div className="api-flow-step active">1. Generate</div>
      <span className="api-flow-arrow">→</span>
      <div className="api-flow-step">2. Track Job</div>
      <span className="api-flow-arrow">→</span>
      <div className="api-flow-step">3. Download</div>
    </div>

    {/* Health */}
    <h3 className="api-section-title">Health / Status</h3>

    <ApiCard method="get" path="/node/{nodeId}/svc/sd-api/health" title="Health / Status"
      desc="Check sd-api service health status."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      response={`{
  "status": "ok",
  "model": "v1-5-pruned-emaonly.safetensors",
  "server": true,              // sd-server running
  "server_loading": false      // model loading in progress
}`}
    >
      <TryIt>
        <div className="api-note api-note-gap"><b>nodeId</b>: Request will be sent to the node selected in the dropdown above.</div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/node/{nodeId}/svc/sd-api/health", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="get" path="/node/{nodeId}/svc/sd-api/v1/queue/stats" title="Queue Stats"
      desc="Check the job queue status of the node."
      response={`{
  "pending": 0,               // pending jobs
  "running_job_id": "",       // currently running job
  "total_jobs_done": 42,      // total completed jobs
  "avg_job_sec": 8.5          // average processing time (sec)
}`}
    >
      <TryIt>
        <div className="api-note api-note-gap"><b>nodeId</b>: Request will be sent to the node selected in the dropdown above.</div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/node/{nodeId}/svc/sd-api/v1/queue/stats", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="get" path="/node/{nodeId}/svc/sd-api/v1/models" title="Models"
      desc="List installed model files on the node."
      response={`["v1-5-pruned-emaonly.safetensors", "sd_xl_base_1.0.safetensors"]`}
    >
      <TryIt>
        <div className="api-note api-note-gap"><b>nodeId</b>: Request will be sent to the node selected in the dropdown above.</div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/node/{nodeId}/svc/sd-api/v1/models", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    {/* txt2img */}
    <h3 className="api-section-title section-wide">Generation</h3>

    <ApiCard method="post" path="/node/{nodeId}/svc/sd-api/v1/images/generations" title="txt2img (Async)" badge="Async" ownership
      desc={<>
        Generate an image from a text prompt. Processed asynchronously, returns <code>job_id</code>. Track progress via SSE/poll.
        <br /><br />
        <strong>sd.cpp wrap convention</strong> — stock sd.cpp's OpenAI-compat handler ignores top-level <code>steps</code> / <code>cfg_scale</code> / <code>seed</code> / <code>sample_method</code> / <code>negative_prompt</code> / <code>strength</code>. Embed them inside the <code>prompt</code> as a <code>&lt;sd_cpp_extra_args&gt;</code> JSON tag instead. The Try It panel below does this automatically before sending.
      </>}
      params={[
        { name: "prompt", type: "string", required: true, desc: "Image description. May embed `<sd_cpp_extra_args>{...}</sd_cpp_extra_args>` JSON tag to override defaults." },
        { name: "size", type: "string", default: '"512x512"', desc: 'Image dimensions, "WxH" (OpenAI standard).' },
        { name: "response_format", type: "string", default: '"b64_json"', desc: '"b64_json" or "url".' },
        { name: "n", type: "int", default: "1", desc: "Number of images to generate." },
        { name: "steps", type: "int", default: "20", desc: "Sampling steps. **Wrap into <sd_cpp_extra_args>**." },
        { name: "cfg_scale", type: "float", default: "7.0", desc: "CFG scale. **Wrap into <sd_cpp_extra_args>**." },
        { name: "seed", type: "int", default: "-1", desc: "Seed (-1: random). **Wrap into <sd_cpp_extra_args>**." },
        { name: "sample_method", type: "string", default: "euler_a", desc: "Sampler name. **Wrap into <sd_cpp_extra_args>**." },
        { name: "negative_prompt", type: "string", default: '""', desc: "Elements to exclude. **Wrap into <sd_cpp_extra_args>**." },
      ]}
      response={`{
  "job_id": "a1b2c3d4e5f6",  // use for Job Tracking
  "status": "queued",
  "position": 0              // position in queue
}`}
      example={`curl -X POST http://broker:7860/node/{nodeId}/svc/sd-api/v1/images/generations \\
  -H "Content-Type: application/json" \\
  -d '{
    "prompt":"sunset over mountains<sd_cpp_extra_args>{\\"steps\\":25,\\"cfg_scale\\":8,\\"seed\\":42}</sd_cpp_extra_args>",
    "size":"1024x1024",
    "response_format":"b64_json"
  }'`}
    >
      <TryIt>
        <div className="api-try-row"><label>prompt <span className="req">*</span></label><input id="try-prompt" type="text" placeholder="a beautiful sunset over mountains" /></div>
        <div className="api-try-row"><label>negative_prompt</label><input id="try-neg" type="text" placeholder="blurry, ugly" /></div>
        <div className="api-try-row">
          <label>width</label><input id="try-w" type="number" defaultValue={512} className="try-input-narrow" />
          <label className="try-label-inline">height</label><input id="try-h" type="number" defaultValue={512} className="try-input-narrow" />
          <label className="try-label-inline">steps</label><input id="try-steps" type="number" defaultValue={20} className="try-input-tiny" />
        </div>
        <div className="api-try-row">
          <label>cfg_scale</label><input id="try-cfg" type="number" defaultValue={7.0} step={0.5} className="try-input-narrow" />
          <label className="try-label-inline">seed</label><input id="try-seed" type="number" defaultValue={-1} className="try-input-mid" />
        </div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const panel = e.target.closest(".api-try-panel");
            const el = panel.querySelector(".api-try-response");
            const w = parseInt(document.getElementById("try-w")?.value) || 512;
            const h = parseInt(document.getElementById("try-h")?.value) || 512;
            const seedRaw = parseInt(document.getElementById("try-seed")?.value);
            // -1 (UI default) means "let sd.cpp pick a random seed" — drop it
            // so it doesn't get baked into the prompt tag and force a fixed -1.
            const draft = {
              prompt: document.getElementById("try-prompt")?.value || "",
              size: `${w}x${h}`,
              response_format: "b64_json",
              negative_prompt: document.getElementById("try-neg")?.value || "",
              steps: parseInt(document.getElementById("try-steps")?.value) || 20,
              cfg_scale: parseFloat(document.getElementById("try-cfg")?.value) || 7.0,
              ...(Number.isFinite(seedRaw) && seedRaw >= 0 ? { seed: seedRaw } : {}),
            };
            const body = wrapSdCppExtraArgs(draft);
            el.style.display = "block"; el.textContent = "Sending...";
            fetch(`/node/${encodeURIComponent(selectedNode)}/svc/sd-api/v1/images/generations`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) })
              .then(r => r.json()).then(d => { el.textContent = JSON.stringify(d, null, 2); })
              .catch(err => { el.textContent = "Error: " + err.message; });
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="post" path="/node/{nodeId}/svc/sd-api/v1/images/edits" title="img2img / inpaint (Async)" badge="Async"
      desc="Generate a modified image from an existing image. Also used for inpainting. Returns job_id, track progress via SSE/poll."
      params={[
        { name: "image", type: "file", required: true, desc: "Source image (multipart form-data)" },
        { name: "prompt", type: "string", required: true, desc: "Modification description" },
        { name: "strength", type: "float", default: "0.75", desc: "Modification strength (0.0~1.0)" },
        { name: "mask", type: "file", default: "—", desc: "Inpaint mask (white=edit area)" },
      ]}
      response={`{ "job_id": "b2c3d4e5f6a1", "status": "queued", "position": 0 }`}
    />

    {/* Job Tracking */}
    <h3 className="api-section-title section-wide" id="api-job-tracking">Job Tracking</h3>
    <p className="api-page-subtitle-tight">Image generation is asynchronous. Track progress using <code>job_id</code>.</p>
    <div className="api-flow api-flow-gap">
      <div className="api-flow-step"><span className="api-badge queued">queued</span></div>
      <span className="api-flow-arrow">→</span>
      <div className="api-flow-step"><span className="api-badge running">running</span></div>
      <span className="api-flow-arrow">→</span>
      <div className="api-flow-step"><span className="api-badge done">done</span> / <span className="api-badge failed">failed</span></div>
    </div>

    <ApiCard method="get" path="/node/{nodeId}/svc/sd-api/v1/jobs/subscribe/{jobId}" badge="SSE (recommended)"
      desc={<>Receive real-time progress via Server-Sent Events. Close connection after <code>done</code> or <code>failed</code>.</>}
      pathParams={[
        { name: "nodeId", type: "string", desc: "Target node ID" },
        { name: "jobId", type: "string", desc: "Job ID to track" },
      ]}
      response={`// Running
data: {"status":"running", "progress":45, "step":9, "total":20}

// Done
data: {"status":"done", "progress":100,
       "actual_seed":1645409091,
       "url":"/outputs/1710312000_a1b2c3d4_1645409091.png"}

// Failed
data: {"status":"failed", "error":"model not found"}`}
      example={`// JavaScript
const es = new EventSource(\`/node/\${nodeId}/svc/sd-api/v1/jobs/subscribe/\${jobId}\`);
es.onmessage = e => {
  const d = JSON.parse(e.data);
  if (d.status === "done")   { console.log("Image:", d.url); es.close(); }
  if (d.status === "failed") { console.error(d.error); es.close(); }
};`}
    >
      <TryIt noAuth>
        <div className="api-try-row"><label>job_id <span className="req">*</span></label><input id="try-sse-jobid" type="text" placeholder="a1b2c3d4e5f6" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const jobId = document.getElementById("try-sse-jobid")?.value;
            if (!jobId) { el.style.display = "block"; el.textContent = "Enter job_id"; return; }
            el.style.display = "block"; el.textContent = "Connecting...";
            const url = `/node/${encodeURIComponent(selectedNode)}/svc/sd-api/v1/jobs/subscribe/${jobId}`;
            const es = new EventSource(url);
            es.onmessage = (ev) => { el.textContent += "\n" + ev.data; };
            es.onerror = () => { el.textContent += "\n[Connection closed]"; es.close(); };
            e.target._es = es;
          }}>Subscribe</button>
          <button className="api-try-send danger" onClick={e => {
            const btn = e.target.closest(".api-try-actions").querySelector(".api-try-send");
            if (btn?._es) { btn._es.close(); }
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            if (el) el.textContent += "\n[Stopped]";
          }}>Stop</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="get" path="/node/{nodeId}/svc/sd-api/v1/jobs/poll/{jobId}"
      desc="Poll job status periodically when SSE is not available."
      response={`// Queued
{ "job_id": "a1b2c3d4", "status": "queued", "position": 2 }

// Running
{ "job_id": "a1b2c3d4", "status": "running", "progress": 50 }

// Done
{ "job_id": "a1b2c3d4", "status": "done", "progress": 100,
  "actual_seed": 1645409091,
  "url": "/outputs/1710312000_a1b2c3d4_1645409091.png" }`}
    >
      <div className="api-note"><b>Tip:</b> Recommended polling interval: 1~3 seconds.</div>
      <TryIt noAuth>
        <div className="api-try-row"><label>job_id <span className="req">*</span></label><input id="try-poll-jobid" type="text" placeholder="a1b2c3d4e5f6" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const jobId = document.getElementById("try-poll-jobid")?.value;
            if (!jobId) { el.style.display = "block"; el.textContent = "Enter job_id"; return; }
            tryFetch("GET", `/node/{nodeId}/svc/sd-api/v1/jobs/poll/${jobId}`, null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="delete" path="/node/{nodeId}/svc/sd-api/v1/jobs/{jobId}" title="Delete Job"
      desc={<>Evict a finished (<code>done</code> / <code>failed</code>) job from the provider queue. Also deletes the on-disk result file. Returns <code>204 No Content</code> on success. <code>queued</code> / <code>running</code> jobs return <code>409 Conflict</code> (no per-job cancel mechanism yet). See <code>?consume=true</code> on Result for one-shot fetch+delete.</>}
    >
      <OwnershipNote kind="delete" />
      <TryIt>
        <div className="api-try-row"><label>job_id <span className="req">*</span></label><input id="try-cancel-jobid" type="text" placeholder="a1b2c3d4e5f6" /></div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const jobId = document.getElementById("try-cancel-jobid")?.value;
            if (!jobId) { el.style.display = "block"; el.textContent = "Enter job_id"; return; }
            el.style.display = "block"; el.textContent = "Deleting...";
            fetch(`/node/${encodeURIComponent(selectedNode)}/svc/sd-api/v1/jobs/${jobId}`, { method: "DELETE" })
              .then(async r => {
                if (r.status === 204) { el.textContent = "204 No Content (deleted)"; return; }
                let body = ""; try { body = JSON.stringify(await r.json(), null, 2); } catch { body = await r.text(); }
                el.textContent = `${r.status}\n${body}`;
              })
              .catch(err => { el.textContent = "Error: " + err.message; });
          }}>Delete Job</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    {/* Download */}
    <h3 className="api-section-title section-wide">Result Download</h3>

    <ApiCard method="get" path="/node/{nodeId}/svc/sd-api/v1/jobs/{jobId}/result" title="Job Result" ownership
      desc={<>Fetch the final result body of a finished job. Response is the raw image bytes with <code>Content-Type: image/png</code> (or the underlying engine's content type). Returns <code>202</code> when not yet done, <code>403</code> when ownership check fails, <code>500</code> on failed, <code>404</code> when not found. Pass <code>?consume=true</code> to evict the job from the queue (and delete the on-disk file) immediately after a successful fetch.</>}
      pathParams={[
        { name: "nodeId", type: "string", desc: "Target node ID" },
        { name: "jobId", type: "string", desc: "Job ID from submit / poll response" },
      ]}
    >
      <OwnershipNote kind="read" />
      <TryIt>
        <div className="api-try-row"><label>job_id <span className="req">*</span></label><input id="try-result-jobid" type="text" placeholder="a1b2c3d4e5f6" /></div>
        <div className="api-try-row"><label>consume</label><input id="try-result-consume" type="checkbox" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const jobId = document.getElementById("try-result-jobid")?.value;
            const consume = document.getElementById("try-result-consume")?.checked;
            if (!jobId) { el.style.display = "block"; el.textContent = "Enter job_id"; return; }
            const qs = consume ? "?consume=true" : "";
            el.style.display = "block"; el.textContent = "Fetching...";
            fetch(`/node/${encodeURIComponent(selectedNode)}/svc/sd-api/v1/jobs/${jobId}/result${qs}`, {
              // Ownership-gated endpoint — wallet sig must round-trip so the
              // broker can inject X-Caller-Address. Bare fetch was anonymous,
              // matched job.SubmitterAddress=="" branch, and 403'd whenever
              // the submit came from a logged-in wallet.
              headers: getTryItAuth(el),
            })
              .then(async r => {
                if (!r.ok) { el.textContent = `${r.status} ${r.statusText}`; return; }
                const ct = r.headers.get("Content-Type") || "";
                el.textContent = `${r.status} OK — Content-Type: ${ct}, bytes: ${(await r.blob()).size}` + (consume ? " (job evicted)" : "");
              })
              .catch(err => { el.textContent = "Error: " + err.message; });
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="get" path="/node/{nodeId}/svc/sd-api/outputs/{filename}" title="Result Download"
      desc={<>Download the generated image. Use the <code>url</code> field from the done response. Returns <code>403</code> when the file belongs to another wallet.</>}
      pathParams={[
        { name: "nodeId", type: "string", desc: "Target node ID" },
        { name: "filename", type: "string", desc: "Filename from done response url" },
      ]}
      queryParams={[
        { name: "consume", type: "bool", default: "false", desc: "When true, evict the job from the queue and delete the on-disk file immediately after the download is served." },
      ]}
      response={`// Full URL
const imageUrl = \`/node/\${nodeId}/svc/sd-api\${job.url}\`;
// → /node/0x7622.../svc/sd-api/outputs/17103_a1b2c3d4_16454.png

// Response: PNG image binary (image/png)`}
      example={`# Save with curl
curl -o result.png http://broker:7860/node/{nodeId}/svc/sd-api/outputs/{filename}

# Python
import requests
resp = requests.get("http://broker:7860/node/{nodeId}/svc/sd-api/outputs/{filename}")
with open("result.png", "wb") as f:
    f.write(resp.content)`}
    >
      <OwnershipNote kind="read" />
      <TryIt>
        <div className="api-try-row"><label>filename <span className="req">*</span></label><input id="try-dl-filename" type="text" placeholder="1710312000_a1b2c3d4_1645409091.png" /></div>
        <div className="api-try-row"><label>consume</label><input id="try-dl-consume" type="checkbox" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const filename = document.getElementById("try-dl-filename")?.value;
            if (!filename) return;
            const consume = document.getElementById("try-dl-consume")?.checked;
            const qs = consume ? "?consume=true" : "";
            const url = `/node/${encodeURIComponent(selectedNode)}/svc/sd-api/outputs/${filename}${qs}`;
            window.open(url, "_blank");
          }}>Open Image</button>
          <button className="api-try-send api-try-send-success" onClick={e => {
            const filename = document.getElementById("try-dl-filename")?.value;
            if (!filename) return;
            const consume = document.getElementById("try-dl-consume")?.checked;
            const qs = consume ? "?consume=true" : "";
            const url = `/node/${encodeURIComponent(selectedNode)}/svc/sd-api/outputs/${filename}${qs}`;
            const a = document.createElement("a"); a.href = url; a.download = filename; a.click();
          }}>Download</button>
        </div>
        <div id="try-dl-preview" className="api-preview-hidden"></div>
      </TryIt>
    </ApiCard>
  </>);
}

// ═══════ LLM Page ═══════
function LLMPage({ tryFetch, selectedNode }) {
  return (<>
    <h2>LLM API</h2>
    <p className="api-page-subtitle">OpenAI-compatible LLM service API. Powered by engine-runner + llama.cpp.</p>

    <ApiCard method="GET" path="/node/{nodeId}/svc/llm-api/health" title="Health Check"
      desc="Check LLM service health status."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      response={`{
  "status": "ok",
  "server": true,
  "server_loading": false,
  "model": "Qwen2.5-14B-Instruct-Q4_K_M.gguf",
  "engine": "llama.cpp",
  "version": "0.1.0",
  "child_pid": 1234,
  "child_name": "llama-server.exe"
}`}>
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", `/node/{nodeId}/svc/llm-api/health`, null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/node/{nodeId}/svc/llm-api/v1/queue/stats" title="Queue Stats"
      desc="Check the job queue status."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      response={`{
  "pending": 0,
  "running": 0,
  "running_job_id": "",
  "estimated_wait_sec": 0,
  "total_jobs_done": 42,
  "avg_job_sec": 2.5
}`}>
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", `/node/{nodeId}/svc/llm-api/v1/queue/stats`, null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/node/{nodeId}/svc/llm-api/v1/models" title="Models"
      desc="List loaded models."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      response={`{
  "models": [{ "name": "Qwen2.5-14B-Instruct-Q4_K_M.gguf", "model": "Qwen2.5-14B-Instruct-Q4_K_M.gguf" }],
  "object": "list"
}`}>
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", `/node/{nodeId}/svc/llm-api/v1/models`, null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/svc/llm-api/v1/chat/completions?wait=true" title="Chat Completion (Sync)" badge="Sync" ownership
      desc={<>OpenAI-compatible chat API. Add <code>?wait=true</code> for sync mode — returns result directly. Append <code>&amp;consume=true</code> to auto-evict the job from the provider queue right after the response is streamed back. The response includes <code>X-Job-ID</code> header for correlation.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "messages", type: "array", required: true, desc: "Array of message objects [{role, content}]" },
        { name: "temperature", type: "float", default: "0.7", desc: "Sampling temperature (0.0~2.0)" },
        { name: "max_tokens", type: "int", default: "2048", desc: "Maximum response tokens" },
        { name: "top_p", type: "float", default: "0.9", desc: "Nucleus sampling threshold" },
        { name: "top_k", type: "int", default: "40", desc: "Top-K sampling" },
        { name: "repeat_penalty", type: "float", default: "1.1", desc: "Repetition penalty" },
        { name: "frequency_penalty", type: "float", default: "0", desc: "Frequency penalty" },
        { name: "presence_penalty", type: "float", default: "0", desc: "Presence penalty" },
        { name: "seed", type: "int", default: "-1", desc: "Seed (-1: random)" },
        { name: "stop", type: "string", default: '""', desc: "Stop token(s)" },
      ]}
      response={`{
  "choices": [{
    "finish_reason": "stop",
    "message": { "role": "assistant", "content": "Hello! How can I help?" }
  }],
  "model": "Qwen2.5-14B-Instruct-Q4_K_M.gguf",
  "usage": { "prompt_tokens": 20, "completion_tokens": 10, "total_tokens": 30 }
}`}>
      <TryIt>
        <MessagesEditor id="try-llm-messages" />
        <div className="api-try-row"><label>temperature</label><input type="number" id="try-llm-temp" defaultValue="0.7" step="0.1" /><label>max_tokens</label><input type="number" id="try-llm-max" defaultValue="2048" /></div>
        <div className="api-try-row"><label>consume</label><input type="checkbox" id="try-llm-chat-consume" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            let messages = [];
            try { messages = JSON.parse(document.getElementById("try-llm-messages")?.value || "[]"); } catch (_) {}
            messages = messages.filter(m => m && (m.content || "").trim());
            if (messages.length === 0) messages = [{ role: "user", content: "Hello" }];
            const temp = parseFloat(document.getElementById("try-llm-temp")?.value) || 0.7;
            const maxTok = parseInt(document.getElementById("try-llm-max")?.value) || 2048;
            const consume = document.getElementById("try-llm-chat-consume")?.checked;
            const body = { messages, temperature: temp, max_tokens: maxTok };
            const qs = consume ? "?wait=true&consume=true" : "?wait=true";
            tryFetch("POST", `/node/{nodeId}/svc/llm-api/v1/chat/completions${qs}`, body, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/svc/llm-api/v1/completions?wait=true" title="Text Completion (Sync)" badge="Sync"
      desc={<>Text completion API (non-chat). Generates text continuation from a prompt string. Append <code>&amp;consume=true</code> to auto-evict the job after response. Returns <code>X-Job-ID</code> header.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "prompt", type: "string", required: true, desc: "Text prompt to complete" },
        { name: "temperature", type: "float", default: "0.7", desc: "Sampling temperature" },
        { name: "max_tokens", type: "int", default: "256", desc: "Maximum tokens to generate" },
        { name: "top_p", type: "float", default: "0.9", desc: "Nucleus sampling threshold" },
        { name: "stop", type: "string", default: '""', desc: "Stop sequence" },
      ]}
      response={`{
  "choices": [{
    "text": "The quick brown fox jumped over the lazy dog.",
    "finish_reason": "stop"
  }],
  "model": "Qwen2.5-14B-Instruct-Q4_K_M.gguf",
  "usage": { "prompt_tokens": 5, "completion_tokens": 10, "total_tokens": 15 }
}`}
      example={`curl -X POST "https://broker:8080/node/{nodeId}/svc/llm-api/v1/completions?wait=true" \\
  -H "Content-Type: application/json" \\
  -d '{"prompt":"The quick brown fox","max_tokens":50}'`}
    >
      <TryIt>
        <div className="api-try-row"><label>prompt <span className="req">*</span></label><input type="text" id="try-llm-comp-prompt" defaultValue="The quick brown fox" /></div>
        <div className="api-try-row"><label>max_tokens</label><input type="number" id="try-llm-comp-max" defaultValue="256" /></div>
        <div className="api-try-row"><label>consume</label><input type="checkbox" id="try-llm-comp-consume" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const prompt = document.getElementById("try-llm-comp-prompt")?.value || "";
            const maxTokens = parseInt(document.getElementById("try-llm-comp-max")?.value) || 256;
            const consume = document.getElementById("try-llm-comp-consume")?.checked;
            const qs = consume ? "?wait=true&consume=true" : "?wait=true";
            tryFetch("POST", `/node/{nodeId}/svc/llm-api/v1/completions${qs}`, { prompt, max_tokens: maxTokens }, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/svc/llm-api/v1/embeddings" title="Embeddings (Async)" badge="Async"
      desc="Generate embedding vectors for input text. Useful for semantic search, RAG, and similarity matching."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "input", type: "string|array", required: true, desc: "Text string or array of strings to embed" },
      ]}
      response={`{
  "data": [
    { "embedding": [0.0123, -0.0456, ...], "index": 0 }
  ],
  "model": "Qwen2.5-14B-Instruct-Q4_K_M.gguf",
  "usage": { "prompt_tokens": 5, "total_tokens": 5 }
}`}
      example={`curl -X POST "https://broker:8080/node/{nodeId}/svc/llm-api/v1/embeddings" \\
  -H "Content-Type: application/json" \\
  -d '{"input":"Hello world"}'`}
    >
      <TryIt>
        <div className="api-try-row"><label>input <span className="req">*</span></label><input type="text" id="try-llm-embed-input" defaultValue="Hello world" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const input = document.getElementById("try-llm-embed-input")?.value || "";
            tryFetch("POST", `/node/{nodeId}/svc/llm-api/v1/embeddings`, { input }, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/svc/llm-api/v1/chat/completions" title="Chat Completion (Async)" badge="Async" ownership
      desc="Async mode — returns job_id immediately. Poll result via /v1/jobs/{jobId}."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "messages", type: "array", required: true, desc: "Array of message objects [{role, content}]" },
        { name: "temperature", type: "float", default: "0.7", desc: "Sampling temperature" },
        { name: "max_tokens", type: "int", default: "2048", desc: "Maximum response tokens" },
      ]}
      body={`{
  "messages": [
    { "role": "user", "content": "Hello" }
  ]
}`}
      response={`{
  "job_id": "abc123def456",
  "status": "queued",
  "position": 0
}`}>
      <TryIt>
        <MessagesEditor id="try-llm-async-messages" />
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            let messages = [];
            try { messages = JSON.parse(document.getElementById("try-llm-async-messages")?.value || "[]"); } catch (_) {}
            messages = messages.filter(m => m && (m.content || "").trim());
            if (messages.length === 0) messages = [{ role: "user", content: "Hello" }];
            tryFetch("POST", `/node/{nodeId}/svc/llm-api/v1/chat/completions`, { messages }, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    {/* Job Tracking */}
    <h3 className="api-section-title section-wide" id="api-job-tracking">Job Tracking</h3>
    <p className="api-page-subtitle-tight">Async LLM jobs follow the same shape as Image Generation. Track progress using <code>job_id</code>.</p>
    <div className="api-flow api-flow-gap">
      <div className="api-flow-step"><span className="api-badge queued">queued</span></div>
      <span className="api-flow-arrow">→</span>
      <div className="api-flow-step"><span className="api-badge running">running</span></div>
      <span className="api-flow-arrow">→</span>
      <div className="api-flow-step"><span className="api-badge done">done</span> / <span className="api-badge failed">failed</span></div>
    </div>

    <ApiCard method="get" path="/node/{nodeId}/svc/llm-api/v1/jobs/subscribe/{jobId}" badge="SSE (recommended)"
      desc={<>Receive real-time progress via Server-Sent Events. Close connection after <code>done</code> or <code>failed</code>.</>}
      pathParams={[
        { name: "nodeId", type: "string", desc: "Target node ID" },
        { name: "jobId", type: "string", desc: "Job ID to track" },
      ]}
      response={`// Running
data: {"status":"running", "progress":45}

// Done
data: {"status":"done", "progress":100,
       "url":"/outputs/llm-api_abc123.json"}

// Failed
data: {"status":"failed", "error":"model not loaded"}`}
      example={`// JavaScript
const es = new EventSource(\`/node/\${nodeId}/svc/llm-api/v1/jobs/subscribe/\${jobId}\`);
es.onmessage = e => {
  const d = JSON.parse(e.data);
  if (d.status === "done")   { console.log("Result:", d.url); es.close(); }
  if (d.status === "failed") { console.error(d.error); es.close(); }
};`}
    >
      <TryIt noAuth>
        <div className="api-try-row"><label>job_id <span className="req">*</span></label><input id="try-llm-sse-jobid" type="text" placeholder="abc123def456" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const jobId = document.getElementById("try-llm-sse-jobid")?.value;
            if (!jobId) { el.style.display = "block"; el.textContent = "Enter job_id"; return; }
            el.style.display = "block"; el.textContent = "Connecting...";
            const url = `/node/${encodeURIComponent(selectedNode)}/svc/llm-api/v1/jobs/subscribe/${jobId}`;
            const es = new EventSource(url);
            es.onmessage = (ev) => { el.textContent += "\n" + ev.data; };
            es.onerror = () => { el.textContent += "\n[Connection closed]"; es.close(); };
            e.target._es = es;
          }}>Subscribe</button>
          <button className="api-try-send danger" onClick={e => {
            const btn = e.target.closest(".api-try-actions").querySelector(".api-try-send");
            if (btn?._es) { btn._es.close(); }
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            if (el) el.textContent += "\n[Stopped]";
          }}>Stop</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="get" path="/node/{nodeId}/svc/llm-api/v1/jobs/poll/{jobId}"
      desc="Poll job status periodically when SSE is not available."
      response={`// Queued
{ "job_id": "abc123", "status": "queued", "position": 2 }

// Running
{ "job_id": "abc123", "status": "running", "progress": 50 }

// Done
{ "job_id": "abc123", "status": "done", "progress": 100,
  "url": "/outputs/llm-api_abc123.json" }`}
    >
      <div className="api-note"><b>Tip:</b> Recommended polling interval: 1~3 seconds.</div>
      <TryIt noAuth>
        <div className="api-try-row"><label>job_id <span className="req">*</span></label><input id="try-llm-poll-jobid" type="text" placeholder="abc123def456" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const jobId = document.getElementById("try-llm-poll-jobid")?.value;
            if (!jobId) { el.style.display = "block"; el.textContent = "Enter job_id"; return; }
            tryFetch("GET", `/node/{nodeId}/svc/llm-api/v1/jobs/poll/${jobId}`, null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="delete" path="/node/{nodeId}/svc/llm-api/v1/jobs/{jobId}" title="Delete Job"
      desc={<>Evict a finished (<code>done</code> / <code>failed</code>) job from the provider queue. Returns <code>204 No Content</code> on success. <code>queued</code> / <code>running</code> jobs return <code>409 Conflict</code>. See <code>?consume=true</code> on Result for one-shot fetch+delete.</>}
    >
      <OwnershipNote kind="delete" />
      <TryIt>
        <div className="api-try-row"><label>job_id <span className="req">*</span></label><input id="try-llm-cancel-jobid" type="text" placeholder="abc123def456" /></div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const jobId = document.getElementById("try-llm-cancel-jobid")?.value;
            if (!jobId) { el.style.display = "block"; el.textContent = "Enter job_id"; return; }
            el.style.display = "block"; el.textContent = "Deleting...";
            fetch(`/node/${encodeURIComponent(selectedNode)}/svc/llm-api/v1/jobs/${jobId}`, { method: "DELETE" })
              .then(async r => {
                if (r.status === 204) { el.textContent = "204 No Content (deleted)"; return; }
                let body = ""; try { body = JSON.stringify(await r.json(), null, 2); } catch { body = await r.text(); }
                el.textContent = `${r.status}\n${body}`;
              })
              .catch(err => { el.textContent = "Error: " + err.message; });
          }}>Delete Job</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="get" path="/node/{nodeId}/svc/llm-api/v1/jobs/{jobId}/result" title="Job Result" ownership
      desc={<>Fetch the final result body of a finished job. Response is the raw JSON (or underlying content type). Returns <code>202</code> when not yet done, <code>403</code> when ownership check fails, <code>500</code> on failed, <code>404</code> when not found. Pass <code>?consume=true</code> to evict the job from the queue immediately after a successful fetch.</>}
      pathParams={[
        { name: "nodeId", type: "string", desc: "Target node ID" },
        { name: "jobId", type: "string", desc: "Job ID from submit / poll response" },
      ]}
    >
      <OwnershipNote kind="read" />
      <TryIt>
        <div className="api-try-row"><label>job_id <span className="req">*</span></label><input id="try-llm-result-jobid" type="text" placeholder="abc123def456" /></div>
        <div className="api-try-row"><label>consume</label><input id="try-llm-result-consume" type="checkbox" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const jobId = document.getElementById("try-llm-result-jobid")?.value;
            const consume = document.getElementById("try-llm-result-consume")?.checked;
            if (!jobId) { el.style.display = "block"; el.textContent = "Enter job_id"; return; }
            const qs = consume ? "?consume=true" : "";
            el.style.display = "block"; el.textContent = "Fetching...";
            fetch(`/node/${encodeURIComponent(selectedNode)}/svc/llm-api/v1/jobs/${jobId}/result${qs}`, {
              headers: getTryItAuth(el),
            })
              .then(async r => {
                if (!r.ok) { el.textContent = `${r.status} ${r.statusText}`; return; }
                let body = ""; try { body = JSON.stringify(await r.json(), null, 2); } catch { body = await r.text(); }
                el.textContent = `${r.status} OK${consume ? " (job evicted)" : ""}\n${body}`;
              })
              .catch(err => { el.textContent = "Error: " + err.message; });
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/node/{nodeId}/svc/llm-api/outputs/{filename}" title="Result Download"
      desc={<>Download the result JSON of a completed job. Use the <code>url</code> field from the done response.</>}
      pathParams={[
        { name: "nodeId", type: "string", desc: "Target node ID" },
        { name: "filename", type: "string", desc: "Filename from done response url" },
      ]}
      queryParams={[
        { name: "consume", type: "bool", default: "false", desc: "When true, evict the job from the queue and delete the on-disk file immediately after the download is served." },
      ]}
      response={`// Full URL
const resultUrl = \`/node/\${nodeId}/svc/llm-api\${job.url}\`;
// → /node/0x7622.../svc/llm-api/outputs/llm-api_abc123.json

// Response: JSON file (application/json)`}
      example={`# Save with curl
curl -o result.json http://broker:7860/node/{nodeId}/svc/llm-api/outputs/{filename}

# Python
import requests, json
resp = requests.get("http://broker:7860/node/{nodeId}/svc/llm-api/outputs/{filename}")
data = resp.json()`}
    >
      <OwnershipNote kind="read" />
      <TryIt>
        <div className="api-try-row"><label>filename <span className="req">*</span></label><input id="try-llm-dl-filename" type="text" placeholder="llama.cpp_abc123def456.json" /></div>
        <div className="api-try-row"><label>consume</label><input id="try-llm-dl-consume" type="checkbox" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const filename = document.getElementById("try-llm-dl-filename")?.value;
            if (!filename) return;
            const consume = document.getElementById("try-llm-dl-consume")?.checked;
            const qs = consume ? "?consume=true" : "";
            const url = `/node/${encodeURIComponent(selectedNode)}/svc/llm-api/outputs/${filename}${qs}`;
            window.open(url, "_blank");
          }}>Open File</button>
          <button className="api-try-send api-try-send-success" onClick={e => {
            const filename = document.getElementById("try-llm-dl-filename")?.value;
            if (!filename) return;
            const consume = document.getElementById("try-llm-dl-consume")?.checked;
            const qs = consume ? "?consume=true" : "";
            const url = `/node/${encodeURIComponent(selectedNode)}/svc/llm-api/outputs/${filename}${qs}`;
            const a = document.createElement("a"); a.href = url; a.download = filename; a.click();
          }}>Download</button>
        </div>
      </TryIt>
    </ApiCard>
  </>);
}

// ═══════ vLLM Page ═══════
function VLLMPage({ tryFetch, selectedNode }) {
  return (<>
    <h2>vLLM API</h2>
    <p className="api-page-subtitle">OpenAI-compatible LLM service backed by an external vLLM server. Same async/sync pattern as llm-api — provider's queue subsystem gates all calls.</p>

    <ApiCard method="GET" path="/node/{nodeId}/svc/vllm-api/health" title="Health Check"
      desc="Check vLLM service health status."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      response={`{
  "status": "ok",
  "server": true,
  "model": "qwen2.5-1.5b",
  "engine": "vllm",
  "version": "0.1.0"
}`}>
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", `/node/{nodeId}/svc/vllm-api/health`, null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/node/{nodeId}/svc/vllm-api/v1/queue/stats" title="Queue Stats"
      desc="Check the job queue status."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      response={`{
  "pending": 0,
  "running": 0,
  "running_job_id": "",
  "estimated_wait_sec": 0,
  "total_jobs_done": 42,
  "avg_job_sec": 1.9
}`}>
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", `/node/{nodeId}/svc/vllm-api/v1/queue/stats`, null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/node/{nodeId}/svc/vllm-api/v1/models" title="Models"
      desc="List loaded vLLM models."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      response={`{
  "data": [{ "id": "qwen2.5-1.5b", "object": "model", "max_model_len": 16384 }],
  "object": "list"
}`}>
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", `/node/{nodeId}/svc/vllm-api/v1/models`, null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/svc/vllm-api/v1/chat/completions?wait=true" title="Chat Completion (Sync)" badge="Sync" ownership
      desc={<>OpenAI-compatible chat API. Add <code>?wait=true</code> for sync mode — returns result directly. Append <code>&amp;consume=true</code> to auto-evict the job from the provider queue right after the response. Returns <code>X-Job-ID</code> header.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "messages", type: "array", required: true, desc: "Array of message objects [{role, content}]" },
        { name: "temperature", type: "float", default: "0.7", desc: "Sampling temperature (0.0~2.0)" },
        { name: "max_tokens", type: "int", default: "2048", desc: "Maximum response tokens" },
        { name: "top_p", type: "float", default: "0.9", desc: "Nucleus sampling threshold" },
        { name: "seed", type: "int", default: "-1", desc: "Seed (-1: random)" },
        { name: "stop", type: "string", default: '""', desc: "Stop token(s)" },
      ]}
      response={`{
  "choices": [{
    "finish_reason": "stop",
    "message": { "role": "assistant", "content": "Hello! How can I help?" }
  }],
  "model": "qwen2.5-1.5b",
  "usage": { "prompt_tokens": 20, "completion_tokens": 10, "total_tokens": 30 }
}`}>
      <TryIt>
        <MessagesEditor id="try-vllm-messages" />
        <div className="api-try-row"><label>temperature</label><input type="number" id="try-vllm-temp" defaultValue="0.7" step="0.1" /><label>max_tokens</label><input type="number" id="try-vllm-max" defaultValue="2048" /></div>
        <div className="api-try-row"><label>consume</label><input type="checkbox" id="try-vllm-chat-consume" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            let messages = [];
            try { messages = JSON.parse(document.getElementById("try-vllm-messages")?.value || "[]"); } catch (_) {}
            messages = messages.filter(m => m && (m.content || "").trim());
            if (messages.length === 0) messages = [{ role: "user", content: "Hello" }];
            const temp = parseFloat(document.getElementById("try-vllm-temp")?.value) || 0.7;
            const maxTok = parseInt(document.getElementById("try-vllm-max")?.value) || 2048;
            const consume = document.getElementById("try-vllm-chat-consume")?.checked;
            const body = { messages, temperature: temp, max_tokens: maxTok };
            const qs = consume ? "?wait=true&consume=true" : "?wait=true";
            tryFetch("POST", `/node/{nodeId}/svc/vllm-api/v1/chat/completions${qs}`, body, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/svc/vllm-api/v1/completions?wait=true" title="Text Completion (Sync)" badge="Sync"
      desc={<>Text completion API (non-chat). Generates text continuation from a prompt string. Append <code>&amp;consume=true</code> to auto-evict the job after response. Returns <code>X-Job-ID</code> header.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "prompt", type: "string", required: true, desc: "Text prompt to complete" },
        { name: "temperature", type: "float", default: "0.7", desc: "Sampling temperature" },
        { name: "max_tokens", type: "int", default: "256", desc: "Maximum tokens to generate" },
        { name: "top_p", type: "float", default: "0.9", desc: "Nucleus sampling threshold" },
        { name: "stop", type: "string", default: '""', desc: "Stop sequence" },
      ]}
      response={`{
  "choices": [{
    "text": "The quick brown fox jumped over the lazy dog.",
    "finish_reason": "stop"
  }],
  "model": "qwen2.5-1.5b",
  "usage": { "prompt_tokens": 5, "completion_tokens": 10, "total_tokens": 15 }
}`}
      example={`curl -X POST "https://broker:8080/node/{nodeId}/svc/vllm-api/v1/completions?wait=true" \\
  -H "Content-Type: application/json" \\
  -d '{"prompt":"The quick brown fox","max_tokens":50}'`}
    >
      <TryIt>
        <div className="api-try-row"><label>prompt <span className="req">*</span></label><input type="text" id="try-vllm-comp-prompt" defaultValue="The quick brown fox" /></div>
        <div className="api-try-row"><label>max_tokens</label><input type="number" id="try-vllm-comp-max" defaultValue="256" /></div>
        <div className="api-try-row"><label>consume</label><input type="checkbox" id="try-vllm-comp-consume" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const prompt = document.getElementById("try-vllm-comp-prompt")?.value || "";
            const maxTokens = parseInt(document.getElementById("try-vllm-comp-max")?.value) || 256;
            const consume = document.getElementById("try-vllm-comp-consume")?.checked;
            const qs = consume ? "?wait=true&consume=true" : "?wait=true";
            tryFetch("POST", `/node/{nodeId}/svc/vllm-api/v1/completions${qs}`, { prompt, max_tokens: maxTokens }, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/svc/vllm-api/v1/embeddings" title="Embeddings (Async)" badge="Async"
      desc="Generate embedding vectors for input text. Useful for semantic search, RAG, and similarity matching."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "input", type: "string|array", required: true, desc: "Text string or array of strings to embed" },
      ]}
      response={`{
  "data": [
    { "embedding": [0.0123, -0.0456, ...], "index": 0 }
  ],
  "model": "qwen2.5-1.5b",
  "usage": { "prompt_tokens": 5, "total_tokens": 5 }
}`}
      example={`curl -X POST "https://broker:8080/node/{nodeId}/svc/vllm-api/v1/embeddings" \\
  -H "Content-Type: application/json" \\
  -d '{"input":"Hello world"}'`}
    >
      <TryIt>
        <div className="api-try-row"><label>input <span className="req">*</span></label><input type="text" id="try-vllm-embed-input" defaultValue="Hello world" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const input = document.getElementById("try-vllm-embed-input")?.value || "";
            tryFetch("POST", `/node/{nodeId}/svc/vllm-api/v1/embeddings`, { input }, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/svc/vllm-api/v1/chat/completions" title="Chat Completion (Async)" badge="Async" ownership
      desc="Async mode — returns job_id immediately. Poll result via /v1/jobs/{jobId}."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "messages", type: "array", required: true, desc: "Array of message objects [{role, content}]" },
        { name: "temperature", type: "float", default: "0.7", desc: "Sampling temperature" },
        { name: "max_tokens", type: "int", default: "2048", desc: "Maximum response tokens" },
      ]}
      body={`{
  "messages": [
    { "role": "user", "content": "Hello" }
  ]
}`}
      response={`{
  "job_id": "abc123def456",
  "status": "queued",
  "position": 0
}`}>
      <TryIt>
        <MessagesEditor id="try-vllm-async-messages" />
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            let messages = [];
            try { messages = JSON.parse(document.getElementById("try-vllm-async-messages")?.value || "[]"); } catch (_) {}
            messages = messages.filter(m => m && (m.content || "").trim());
            if (messages.length === 0) messages = [{ role: "user", content: "Hello" }];
            tryFetch("POST", `/node/{nodeId}/svc/vllm-api/v1/chat/completions`, { messages }, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    {/* Job Tracking */}
    <h3 className="api-section-title section-wide" id="api-job-tracking">Job Tracking</h3>
    <p className="api-page-subtitle-tight">Async vLLM jobs follow the same shape as llm-api. Track progress using <code>job_id</code>.</p>
    <div className="api-flow api-flow-gap">
      <div className="api-flow-step"><span className="api-badge queued">queued</span></div>
      <span className="api-flow-arrow">→</span>
      <div className="api-flow-step"><span className="api-badge running">running</span></div>
      <span className="api-flow-arrow">→</span>
      <div className="api-flow-step"><span className="api-badge done">done</span> / <span className="api-badge failed">failed</span></div>
    </div>

    <ApiCard method="get" path="/node/{nodeId}/svc/vllm-api/v1/jobs/subscribe/{jobId}" badge="SSE (recommended)"
      desc={<>Receive real-time progress via Server-Sent Events. Close connection after <code>done</code> or <code>failed</code>.</>}
      pathParams={[
        { name: "nodeId", type: "string", desc: "Target node ID" },
        { name: "jobId", type: "string", desc: "Job ID to track" },
      ]}
      response={`// Running
data: {"status":"running", "progress":45}

// Done
data: {"status":"done", "progress":100,
       "url":"/outputs/vllm-api_abc123.json"}

// Failed
data: {"status":"failed", "error":"upstream vllm error"}`}
      example={`// JavaScript
const es = new EventSource(\`/node/\${nodeId}/svc/vllm-api/v1/jobs/subscribe/\${jobId}\`);
es.onmessage = e => {
  const d = JSON.parse(e.data);
  if (d.status === "done")   { console.log("Result:", d.url); es.close(); }
  if (d.status === "failed") { console.error(d.error); es.close(); }
};`}
    >
      <TryIt noAuth>
        <div className="api-try-row"><label>job_id <span className="req">*</span></label><input id="try-vllm-sse-jobid" type="text" placeholder="abc123def456" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const jobId = document.getElementById("try-vllm-sse-jobid")?.value;
            if (!jobId) { el.style.display = "block"; el.textContent = "Enter job_id"; return; }
            el.style.display = "block"; el.textContent = "Connecting...";
            const url = `/node/${encodeURIComponent(selectedNode)}/svc/vllm-api/v1/jobs/subscribe/${jobId}`;
            const es = new EventSource(url);
            es.onmessage = (ev) => { el.textContent += "\n" + ev.data; };
            es.onerror = () => { el.textContent += "\n[Connection closed]"; es.close(); };
            e.target._es = es;
          }}>Subscribe</button>
          <button className="api-try-send danger" onClick={e => {
            const btn = e.target.closest(".api-try-actions").querySelector(".api-try-send");
            if (btn?._es) { btn._es.close(); }
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            if (el) el.textContent += "\n[Stopped]";
          }}>Stop</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="get" path="/node/{nodeId}/svc/vllm-api/v1/jobs/poll/{jobId}"
      desc="Poll job status periodically when SSE is not available."
      response={`// Queued
{ "job_id": "abc123", "status": "queued", "position": 2 }

// Running
{ "job_id": "abc123", "status": "running", "progress": 50 }

// Done
{ "job_id": "abc123", "status": "done", "progress": 100,
  "url": "/outputs/vllm-api_abc123.json" }`}
    >
      <div className="api-note"><b>Tip:</b> Recommended polling interval: 1~3 seconds.</div>
      <TryIt noAuth>
        <div className="api-try-row"><label>job_id <span className="req">*</span></label><input id="try-vllm-poll-jobid" type="text" placeholder="abc123def456" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const jobId = document.getElementById("try-vllm-poll-jobid")?.value;
            if (!jobId) { el.style.display = "block"; el.textContent = "Enter job_id"; return; }
            tryFetch("GET", `/node/{nodeId}/svc/vllm-api/v1/jobs/poll/${jobId}`, null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="delete" path="/node/{nodeId}/svc/vllm-api/v1/jobs/{jobId}" title="Delete Job"
      desc={<>Evict a finished (<code>done</code> / <code>failed</code>) job from the provider queue. Returns <code>204 No Content</code> on success. <code>queued</code> / <code>running</code> jobs return <code>409 Conflict</code>. See <code>?consume=true</code> on Result for one-shot fetch+delete.</>}
    >
      <OwnershipNote kind="delete" />
      <TryIt>
        <div className="api-try-row"><label>job_id <span className="req">*</span></label><input id="try-vllm-cancel-jobid" type="text" placeholder="abc123def456" /></div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const jobId = document.getElementById("try-vllm-cancel-jobid")?.value;
            if (!jobId) { el.style.display = "block"; el.textContent = "Enter job_id"; return; }
            el.style.display = "block"; el.textContent = "Deleting...";
            fetch(`/node/${encodeURIComponent(selectedNode)}/svc/vllm-api/v1/jobs/${jobId}`, { method: "DELETE" })
              .then(async r => {
                if (r.status === 204) { el.textContent = "204 No Content (deleted)"; return; }
                let body = ""; try { body = JSON.stringify(await r.json(), null, 2); } catch { body = await r.text(); }
                el.textContent = `${r.status}\n${body}`;
              })
              .catch(err => { el.textContent = "Error: " + err.message; });
          }}>Delete Job</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="get" path="/node/{nodeId}/svc/vllm-api/v1/jobs/{jobId}/result" title="Job Result" ownership
      desc={<>Fetch the final result body of a finished job. Response is the raw JSON (or underlying content type). Returns <code>202</code> when not yet done, <code>403</code> when ownership check fails, <code>500</code> on failed, <code>404</code> when not found. Pass <code>?consume=true</code> to evict the job from the queue immediately after a successful fetch.</>}
      pathParams={[
        { name: "nodeId", type: "string", desc: "Target node ID" },
        { name: "jobId", type: "string", desc: "Job ID from submit / poll response" },
      ]}
    >
      <OwnershipNote kind="read" />
      <TryIt>
        <div className="api-try-row"><label>job_id <span className="req">*</span></label><input id="try-vllm-result-jobid" type="text" placeholder="abc123def456" /></div>
        <div className="api-try-row"><label>consume</label><input id="try-vllm-result-consume" type="checkbox" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const jobId = document.getElementById("try-vllm-result-jobid")?.value;
            const consume = document.getElementById("try-vllm-result-consume")?.checked;
            if (!jobId) { el.style.display = "block"; el.textContent = "Enter job_id"; return; }
            const qs = consume ? "?consume=true" : "";
            el.style.display = "block"; el.textContent = "Fetching...";
            fetch(`/node/${encodeURIComponent(selectedNode)}/svc/vllm-api/v1/jobs/${jobId}/result${qs}`, {
              headers: getTryItAuth(el),
            })
              .then(async r => {
                if (!r.ok) { el.textContent = `${r.status} ${r.statusText}`; return; }
                let body = ""; try { body = JSON.stringify(await r.json(), null, 2); } catch { body = await r.text(); }
                el.textContent = `${r.status} OK${consume ? " (job evicted)" : ""}\n${body}`;
              })
              .catch(err => { el.textContent = "Error: " + err.message; });
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/node/{nodeId}/svc/vllm-api/outputs/{filename}" title="Result Download"
      desc={<>Download the result JSON of a completed job. Use the <code>url</code> field from the done response.</>}
      pathParams={[
        { name: "nodeId", type: "string", desc: "Target node ID" },
        { name: "filename", type: "string", desc: "Filename from done response url" },
      ]}
      queryParams={[
        { name: "consume", type: "bool", default: "false", desc: "When true, evict the job from the queue and delete the on-disk file immediately after the download is served." },
      ]}
      response={`// Full URL
const resultUrl = \`/node/\${nodeId}/svc/vllm-api\${job.url}\`;
// → /node/0xB171.../svc/vllm-api/outputs/vllm-api_abc123.json

// Response: JSON file (application/json)`}
      example={`# Save with curl
curl -o result.json http://broker:7860/node/{nodeId}/svc/vllm-api/outputs/{filename}

# Python
import requests, json
resp = requests.get("http://broker:7860/node/{nodeId}/svc/vllm-api/outputs/{filename}")
data = resp.json()`}
    >
      <OwnershipNote kind="read" />
      <TryIt>
        <div className="api-try-row"><label>filename <span className="req">*</span></label><input id="try-vllm-dl-filename" type="text" placeholder="vllm-api_abc123def456.json" /></div>
        <div className="api-try-row"><label>consume</label><input id="try-vllm-dl-consume" type="checkbox" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const filename = document.getElementById("try-vllm-dl-filename")?.value;
            if (!filename) return;
            const consume = document.getElementById("try-vllm-dl-consume")?.checked;
            const qs = consume ? "?consume=true" : "";
            const url = `/node/${encodeURIComponent(selectedNode)}/svc/vllm-api/outputs/${filename}${qs}`;
            window.open(url, "_blank");
          }}>Open File</button>
          <button className="api-try-send api-try-send-success" onClick={e => {
            const filename = document.getElementById("try-vllm-dl-filename")?.value;
            if (!filename) return;
            const consume = document.getElementById("try-vllm-dl-consume")?.checked;
            const qs = consume ? "?consume=true" : "";
            const url = `/node/${encodeURIComponent(selectedNode)}/svc/vllm-api/outputs/${filename}${qs}`;
            const a = document.createElement("a"); a.href = url; a.download = filename; a.click();
          }}>Download</button>
        </div>
      </TryIt>
    </ApiCard>
  </>);
}

// ═══════ Pipeline Page ═══════
function PipelinePage({ tryFetch, selectedNode }) {
  const defaultGraph = `{
  "nodes": [
    { "id": "input1", "type": "inputNode", "data": { "label": "Text Input", "outputType": "text", "params": { "value": "Hello World" } } },
    { "id": "tmpl1", "type": "transformNode", "data": { "label": "Template", "transform": "template", "inputType": "text", "outputType": "text", "params": { "template": "You said: {{prev}}" } } },
    { "id": "out1", "type": "outputNode", "data": { "label": "Text Viewer", "viewer": "text" } }
  ],
  "edges": [
    { "source": "input1", "target": "tmpl1" },
    { "source": "tmpl1", "target": "out1" }
  ]
}`;

  return (<>
    <h2>Pipeline API</h2>
    <p className="api-page-subtitle">Server-side pipeline execution. Paste a pipeline graph JSON (exported from Pipeline Studio) and execute it.</p>

    <ApiCard method="POST" path="/v1/pipeline/execute" title="Pipeline Execute"
      desc={<>Execute a pipeline graph. Default is async (returns job_id). Add <code>?wait=true</code> for synchronous execution.</>}
      pathParams={[
        { name: "wait", type: "query", desc: "true = sync (block until done), default = async" },
      ]}
      params={[
        { name: "nodes", type: "array", required: true, desc: "Pipeline node definitions [{id, type, data}]" },
        { name: "edges", type: "array", required: true, desc: "Node connections [{source, target, sourceHandle?, targetHandle?}]" },
        { name: "networkNodes", type: "array", desc: "Available provider nodes (for Node Finder)" },
        { name: "authHeaders", type: "object", desc: "Global auth headers {Authorization, X-ISANN-Message}" },
      ]}
      response={`// Async (default) — 202 Accepted
{ "job_id": "abc123", "status": "queued" }

// Sync (?wait=true) — 200 OK
{
  "stepResults": {
    "input1": "Hello, how are you?",
    "llm1": { "choices": [{ "message": { "content": "I'm fine!" } }] },
    "ext1": "I'm fine!",
    "out1": "I'm fine!"
  },
  "steps": [
    { "id": "input1", "type": "inputNode", "status": "done", "durationMs": 0 },
    { "id": "llm1", "type": "aiNode", "status": "done", "durationMs": 2500 },
    { "id": "ext1", "type": "transformNode", "status": "done", "durationMs": 1 },
    { "id": "out1", "type": "outputNode", "status": "done", "durationMs": 0 }
  ]
}`}
      example={`curl -X POST "https://broker:8080/v1/pipeline/execute?wait=true" \\
  -H "Content-Type: application/json" \\
  -d '${defaultGraph.replace(/\n/g, "").replace(/\s+/g, " ")}'`}
    >
      <TryIt>
        <div className="api-try-row" style={{ alignItems: "flex-start" }}>
          <label>Graph JSON<br/><span style={{ fontSize: 8, color: "var(--text-muted)" }}>Paste pipeline export</span></label>
          <textarea id="try-pipe-graph" rows={12} style={{ flex: 1, fontFamily: "Consolas, monospace", fontSize: 10 }} defaultValue={defaultGraph} />
        </div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={async e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            el.style.display = "block";
            const raw = document.getElementById("try-pipe-graph")?.value;
            let body;
            try { body = JSON.parse(raw); } catch { el.textContent = "Invalid JSON"; return; }
            el.textContent = "Executing pipeline (sync)...\n";
            try {
              const authHeaders = getTryItAuth(el);
              const resp = await fetch("/v1/pipeline/execute?wait=true", { method: "POST", headers: { "Content-Type": "application/json", ...authHeaders }, body: JSON.stringify(body) });
              const data = await resp.json();
              el.textContent += JSON.stringify(data, null, 2);
            } catch (err) { el.textContent += "Error: " + err.message; }
          }}>Execute (sync)</button>
          <button className="api-try-send" style={{ background: "rgba(255,255,255,0.08)" }} onClick={async e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            el.style.display = "block";
            const raw = document.getElementById("try-pipe-graph")?.value;
            let body;
            try { body = JSON.parse(raw); } catch { el.textContent = "Invalid JSON"; return; }
            el.textContent = "Submitting pipeline (async)...\n";
            try {
              const authHeaders = getTryItAuth(el);
              const resp = await fetch("/v1/pipeline/execute", { method: "POST", headers: { "Content-Type": "application/json", ...authHeaders }, body: JSON.stringify(body) });
              const data = await resp.json();
              el.textContent += JSON.stringify(data, null, 2);
            } catch (err) { el.textContent += "Error: " + err.message; }
          }}>Submit (async)</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/v1/pipeline/jobs" title="List Jobs"
      desc="List all pipeline jobs (active + completed within TTL)."
      response={`[
  { "id": "abc123", "status": "done", "steps": [...] },
  { "id": "def456", "status": "running", "progress": 50 }
]`}>
      <TryIt>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/v1/pipeline/jobs", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/v1/pipeline/jobs/{id}" title="Job Status"
      desc="Get current status and partial results of a pipeline job."
      pathParams={[{ name: "id", type: "string", desc: "Pipeline job ID" }]}
      response={`{
  "id": "abc123",
  "status": "running",
  "stepResults": { "input1": "Hello" },
  "steps": [{ "id": "input1", "status": "done", "durationMs": 0 }]
}`}>
      <TryIt>
        <div className="api-try-row"><label>job_id <span className="req">*</span></label><input type="text" id="try-pipe-jobid" placeholder="abc123" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const id = document.getElementById("try-pipe-jobid")?.value;
            if (!id) { el.style.display = "block"; el.textContent = "Enter job ID"; return; }
            tryFetch("GET", `/v1/pipeline/jobs/${id}`, null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/v1/pipeline/jobs/{id}/result" title="Job Result"
      desc="Get the final execution result. Returns 409 if the job is not finished."
      pathParams={[{ name: "id", type: "string", desc: "Pipeline job ID" }]}
      response={`{
  "stepResults": { "input1": "Hello", "llm1": {...}, "ext1": "I'm fine!", "out1": "I'm fine!" },
  "steps": [...]
}`} />

    <ApiCard method="DELETE" path="/v1/pipeline/jobs/{id}" title="Cancel Job"
      desc="Cancel a running or queued pipeline job."
      pathParams={[{ name: "id", type: "string", desc: "Pipeline job ID" }]}
      response={`{ "status": "cancelled" }`} />

    <ApiCard method="GET" path="/v1/pipeline/entities" title="Entity Types"
      desc="List all registered pipeline entity types and their I/O schema. Used by Pipeline Studio to populate the node palette."
      response={`[
  { "type": "inputNode", "label": "Text Input", "inputs": [], "outputs": ["text"] },
  { "type": "aiNode", "label": "AI Service", "inputs": ["input", "node", "options"], "outputs": ["json"] },
  ...
]`}>
      <TryIt>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/v1/pipeline/entities", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>
  </>);
}

// ═══════ Broker Admin Page (BROKER ADMIN category — owner-only writes & monitoring) ═══════
function BrokerAdminPage({ tryFetch }) {
  return (<>
    <h2>Broker Admin API</h2>
    <p className="api-page-subtitle">
      Owner-only broker administration — server status, configuration, log inspection,
      UI cards visibility, and API feature toggles. All endpoints under <code>/v1/admin/*</code>
      require the broker owner's wallet signature ([middleware.go:78](../../../pkg/broker/middleware.go) — exact match with <code>Auth.Owner</code>; admin role does not pass).
    </p>

    <h3 className="api-section-title">Status</h3>

    <ApiCard method="GET" path="/v1/admin/status" title="Admin Status" auth
      desc="Live broker server status — uptime, current mode, listen addresses, configured rendezvous and auth_mode. Lightweight read for monitoring dashboards."
      response={`{
  "uptime_seconds": 86423,
  "mode":            "broker",
  "proxy_id":        "0xBADa8bE8...",
  "listen_addr":     ":8080",
  "rendezvous_addr": "https://rv:9000",
  "target_addr":     "127.0.0.1:4433",
  "router_addr":     "",
  "auth_mode":       "open"
}`}
      example={`curl -k https://your-broker:8080/v1/admin/status \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:broker:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/v1/admin/status", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <h3 className="api-section-title">Config</h3>

    <ApiCard method="GET" path="/v1/admin/config" title="Get Config" auth
      desc="Read the broker's effective config (mode, addrs, auth, TLS paths). Exposes admins/users lists too — operator-only data."
      response={`{
  "mode":               "broker",
  "id":                 "0xBADa8bE8...",
  "target_proxy_id":    "",
  "listen_addr":        ":8080",
  "target_addr":        "127.0.0.1:4433",
  "router_addr":        "",
  "rendezvous_addr":    "https://rv:9000",
  "signaling_addr":     "rv:9001",
  "signaling_transport":"quic",
  "gate_addr":          "https://gate:8800",
  "region":             "kr-1",
  "auth_mode":          "open",
  "auth_owner":         "0xB171fe0B...",
  "auth_admins":        ["0x..."],
  "auth_users":         ["0x..."],
  "tls_cert":           "broker.crt",
  "tls_key":            "broker.key"
}`}
      example={`curl -k https://your-broker:8080/v1/admin/config \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:broker:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/v1/admin/config", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="PUT" path="/v1/admin/config" title="Update Config" auth danger="Restart"
      desc={<>Patch broker config. Most fields are <strong>hot-applied</strong>; <code>listen_addr</code>, <code>tls_cert</code>, <code>tls_key</code> require a <strong>process restart</strong> (response indicates which). Auth fields (owner/admins/users/mode) saved to <code>auth.json</code> separately.</>}
      params={[
        { name: "target_proxy_id", type: "string", desc: "Target proxy ID (proxy mode only)" },
        { name: "target_addr", type: "string", desc: "Direct target address" },
        { name: "router_addr", type: "string", desc: "Router address" },
        { name: "rendezvous_addr", type: "string", desc: "Rendezvous server URL" },
        { name: "signaling_addr", type: "string", desc: "Signaling server addr" },
        { name: "signaling_transport", type: "string", desc: "udp | quic" },
        { name: "gate_addr", type: "string", desc: "Gate server URL" },
        { name: "region", type: "string", desc: "Broker region tag" },
        { name: "auth_mode", type: "string", desc: "open | protected (auth.json)" },
        { name: "auth_owner", type: "string", desc: "Owner EOA address (auth.json)" },
        { name: "auth_admins", type: "string[]", desc: "Admin EOA addresses (auth.json)" },
        { name: "auth_users", type: "string[]", desc: "User EOA addresses (auth.json)" },
        { name: "listen_addr", type: "string", desc: "⚠️ Restart required — broker bind addr" },
        { name: "tls_cert", type: "string", desc: "⚠️ Restart required — TLS cert path" },
        { name: "tls_key", type: "string", desc: "⚠️ Restart required — TLS key path" },
      ]}
      response={`{ "status": "ok", "restart_required": false }`}
      example={`# Change RV (hot apply)
curl -k -X PUT https://your-broker:8080/v1/admin/config \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:broker:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{"rendezvous_addr":"https://new-rv:9000"}'

# Change listen port (restart_required: true)
curl -k -X PUT https://your-broker:8080/v1/admin/config \\
  -H "Content-Type: application/json" \\
  -d '{"listen_addr":":8081"}'`}
    >
      <TryIt auth>
        <div className="api-try-row" style={{ alignItems: "flex-start" }}>
          <label>config <span className="req">*</span></label>
          <textarea id="try-admin-cfg-body" rows={6} style={{ flex: 1, fontFamily: "Consolas, monospace", fontSize: 10 }} defaultValue={`{"rendezvous_addr":"https://new-rv:9000"}`}></textarea>
        </div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const raw = document.getElementById("try-admin-cfg-body")?.value;
            let body;
            try { body = JSON.parse(raw || "{}"); }
            catch { el.style.display = "block"; el.textContent = "Invalid JSON"; return; }
            const keys = Object.keys(body).join(", ");
            const restartKeys = ["listen_addr", "tls_cert", "tls_key"].filter(k => k in body);
            const msg = restartKeys.length
              ? `Update fields [${keys}]? ⚠️ Will require broker restart for: ${restartKeys.join(", ")}.`
              : `Update fields [${keys}]? Changes apply immediately.`;
            if (!window.confirm(msg)) return;
            tryFetch("PUT", "/v1/admin/config", body, el);
          }}>Update Config</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <h3 className="api-section-title">Logs</h3>

    <ApiCard method="GET" path="/v1/admin/logs?n=100" title="Get Logs" auth
      desc="Tail recent lines from the broker's in-memory log buffer. Useful for quick debugging when disk logs are not yet flushed."
      params={[
        { name: "n", type: "int", default: "100", desc: "Number of trailing lines to return" },
      ]}
      response={`{
  "lines": [
    "2026-05-12 08:00:01 [broker] starting on :8080",
    "2026-05-12 08:00:02 [broker] rendezvous online"
  ]
}`}
      example={`curl -k "https://your-broker:8080/v1/admin/logs?n=50" \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:broker:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>n</label><input type="number" id="try-admin-logs-n" defaultValue={100} /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const n = parseInt(document.getElementById("try-admin-logs-n")?.value) || 100;
            tryFetch("GET", "/v1/admin/logs?n=" + n, null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/v1/admin/logs/stream" title="Logs Stream" auth badge="SSE"
      desc={<>Live log stream via Server-Sent Events. Useful for live tailing in the admin UI. Connection stays open until client closes.</>}
      response={`// SSE event stream
data: 2026-05-12 08:00:01 [broker] request /v1/nodes 200 12ms

data: 2026-05-12 08:00:02 [broker] heartbeat ok`}
      example={`# JavaScript
const es = new EventSource("/v1/admin/logs/stream");
es.onmessage = e => console.log(e.data);
// (auth headers passed via cookie or URL-embedded sig in real flow)`}
    >
      <TryIt auth>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            el.style.display = "block"; el.textContent = "Connecting...";
            const url = "/v1/admin/logs/stream";
            const es = new EventSource(url);
            es.onmessage = (ev) => { el.textContent += "\n" + ev.data; };
            es.onerror = () => { el.textContent += "\n[Connection closed]"; es.close(); };
            e.target._es = es;
          }}>Subscribe</button>
          <button className="api-try-send danger" onClick={e => {
            const btn = e.target.closest(".api-try-actions").querySelector(".api-try-send");
            if (btn?._es) { btn._es.close(); }
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            if (el) el.textContent += "\n[Stopped]";
          }}>Stop</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/v1/admin/logs/files" title="Log Files (Broker)" auth
      desc={<>List broker log files on disk (rotated). Returned files match the broker's <code>log.file</code> dir; defaults to <code>logs/</code> relative to cwd. Each entry: name + bytes.</>}
      response={`[
  { "name": "broker.log",          "size": 245312 },
  { "name": "broker-2026-05-11.log","size": 1820224 }
]`}
      example={`curl -k https://your-broker:8080/v1/admin/logs/files \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:broker:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/v1/admin/logs/files", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/v1/admin/logs/file" title="Log Tail (Broker)" auth
      desc={<>Tail a specific broker log file from disk. Supports trailing N lines (max 2000) and case-insensitive substring search. Path traversal is blocked — only files inside the broker's log dir.</>}
      params={[
        { name: "name", type: "string", required: true, desc: "Log file name (e.g. broker.log)" },
        { name: "tail", type: "int", default: "200", desc: "Number of trailing lines (max 2000)" },
        { name: "q", type: "string", desc: "Case-insensitive substring filter applied after tail" },
      ]}
      response={`{
  "name":  "broker.log",
  "lines": [
    "2026-05-12 07:55:01 [broker] ...\\n",
    "2026-05-12 07:55:03 [broker] error: ...\\n"
  ],
  "total": 2
}`}
      example={`curl -k "https://your-broker:8080/v1/admin/logs/file?name=broker.log&tail=200&q=error" \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:broker:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>name <span className="req">*</span></label><input type="text" id="try-admin-logf-name" defaultValue="broker.log" /></div>
        <div className="api-try-row"><label>tail</label><input type="number" id="try-admin-logf-tail" defaultValue={200} /></div>
        <div className="api-try-row"><label>q</label><input type="text" id="try-admin-logf-q" placeholder="optional substring filter" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const name = document.getElementById("try-admin-logf-name")?.value;
            const tail = document.getElementById("try-admin-logf-tail")?.value;
            const q = document.getElementById("try-admin-logf-q")?.value;
            if (!name) { el.style.display = "block"; el.textContent = "Enter name"; return; }
            const qs = new URLSearchParams({ name });
            if (tail) qs.set("tail", tail);
            if (q) qs.set("q", q);
            tryFetch("GET", "/v1/admin/logs/file?" + qs.toString(), null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <h3 className="api-section-title">UI & Feature Toggles</h3>

    <ApiCard method="PUT" path="/v1/admin/cards" title="Update Cards" auth danger="UI"
      desc={<>Set the per-card UI visibility map (write counterpart of <code>GET /v1/cards</code>). Disabling a card hides its sidebar entry / Workspace tile across all clients on next page load. Persisted to broker.json runtime sidecar.</>}
      params={[
        { name: "cards", type: "object", required: true, desc: "Map of card ID → { enabled: bool }. Missing keys default to enabled." },
      ]}
      response={`{ "status": "ok", "cards": { "logs": { "enabled": false }, ... } }`}
      example={`# Known card IDs: nodes, my-nodes, pipeline, resources, api, install, settings, logs
curl -k -X PUT https://your-broker:8080/v1/admin/cards \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:broker:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{"cards":{"logs":{"enabled":false},"install":{"enabled":false}}}'`}
    >
      <TryIt auth>
        <div className="api-try-row" style={{ alignItems: "flex-start" }}>
          <label>cards <span className="req">*</span></label>
          <textarea id="try-admin-cards-body" rows={5} style={{ flex: 1, fontFamily: "Consolas, monospace", fontSize: 10 }} defaultValue={`{"logs":{"enabled":false}}`}></textarea>
        </div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const raw = document.getElementById("try-admin-cards-body")?.value;
            let cards;
            try { cards = JSON.parse(raw || "{}"); }
            catch { el.style.display = "block"; el.textContent = "Invalid JSON"; return; }
            const keys = Object.keys(cards).join(", ");
            if (!window.confirm(`Update visibility for cards [${keys}]? All clients will see the change on next page load.`)) return;
            tryFetch("PUT", "/v1/admin/cards", { cards }, el);
          }}>Update Cards</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="PUT" path="/v1/admin/api-features" title="Update API Features" auth danger="API"
      desc={<>Set per-feature backend gating. Disabled features return <code>403 feature_disabled</code> for matching routes. Write counterpart of <code>GET /v1/api/policy</code>. Persisted to broker.json runtime sidecar.</>}
      params={[
        { name: "features", type: "object", required: true, desc: "Map of feature name → { enabled: bool }. Missing keys fall back to DefaultPreset." },
      ]}
      response={`{ "status": "ok", "features": { "pipeline": { "enabled": false }, ... } }`}
      example={`# Known features: info, node_discovery, gate_proxy, auth_verify, my_nodes,
#                  pipeline, node_proxy_svc, node_proxy_provider, node_proxy_installer
curl -k -X PUT https://your-broker:8080/v1/admin/api-features \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:broker:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{"features":{"pipeline":{"enabled":false}}}'`}
    >
      <TryIt auth>
        <div className="api-try-row" style={{ alignItems: "flex-start" }}>
          <label>features <span className="req">*</span></label>
          <textarea id="try-admin-feat-body" rows={5} style={{ flex: 1, fontFamily: "Consolas, monospace", fontSize: 10 }} defaultValue={`{"pipeline":{"enabled":false}}`}></textarea>
        </div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const raw = document.getElementById("try-admin-feat-body")?.value;
            let features;
            try { features = JSON.parse(raw || "{}"); }
            catch { el.style.display = "block"; el.textContent = "Invalid JSON"; return; }
            const keys = Object.keys(features).join(", ");
            if (!window.confirm(`Toggle features [${keys}]? Disabled routes will return 403 immediately.`)) return;
            tryFetch("PUT", "/v1/admin/api-features", { features }, el);
          }}>Update Features</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/v1/admin/api-features/preset" title="API Features Preset" auth danger="Bulk"
      desc={<>Bulk-apply a named preset (e.g. <code>central</code>, <code>personal</code>) to the feature toggle map. Convenience for switching the broker's deployment style — Settings UI uses this for the preset buttons.</>}
      params={[
        { name: "name", type: "string", required: true, desc: "Preset name: central | personal" },
      ]}
      response={`{ "status": "ok", "preset": "central", "features": { ... } }`}
      example={`curl -k -X POST https://your-broker:8080/v1/admin/api-features/preset \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:broker:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{"name":"central"}'`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>name <span className="req">*</span></label><input type="text" id="try-admin-preset-name" defaultValue="central" placeholder="central | personal" /></div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const name = document.getElementById("try-admin-preset-name")?.value;
            if (!name) { el.style.display = "block"; el.textContent = "Enter preset name"; return; }
            if (!window.confirm(`Apply preset '${name}'? Overwrites all current feature toggles.`)) return;
            tryFetch("POST", "/v1/admin/api-features/preset", { name }, el);
          }}>Apply Preset</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>
  </>);
}

// ═══════ Admin Page (BROKER category — public-ish endpoints) ═══════
function AdminPage({ tryFetch, selectedNode }) {
  return (<>
    <h2>Broker API</h2>
    <p className="api-page-subtitle">Broker endpoints — health, catalog discovery, UI metadata, and authentication. Owner-only admin writes live in the <strong>Broker Admin</strong> category.</p>

    <CommonPage tryFetch={tryFetch} selectedNode={selectedNode} />
  </>);
}

// ═══════ Node Page (Phase 4: Read-only Provider Status) ═══════
function NodePage({ tryFetch, selectedNode }) {
  return (<>
    <h2>Node API (Read-only Provider Status)</h2>
    <p className="api-page-subtitle">
      Read-only operator-facing endpoints exposed by Provider. All calls go through the broker's
      <code> /node/{`{nodeId}`}/provider/* </code> tunnel — the broker forwards over a QUIC
      orchestrator stream (<code>0x80</code>) to the selected node.
    </p>
    <p className="api-page-subtitle-tight">
      Pick the target node from the dropdown on the right.
      Provider-side validates an owner/admin signature from your wallet session.
    </p>

    <h3 className="api-section-title">Status</h3>

    <ApiCard method="GET" path="/node/{nodeId}/provider/versions" title="Provider Versions" auth
      desc="Return installed software versions known to Provider: cores (isann, broker, provider, engine-runner), engines, and services. Used by Deploy page to show what is installed and up to date."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      response={`{
  "cores": [
    { "name": "isann",         "version": "0.5.1", "installed": true,  "latest": "0.5.1" },
    { "name": "provider",      "version": "0.5.1", "installed": true,  "latest": "0.5.1" },
    { "name": "engine-runner", "version": "0.5.1", "installed": true,  "latest": "0.5.1" }
  ],
  "engines": [
    { "name": "llama.cpp",     "version": "b4500",  "installed": true,  "latest": "b4500" }
  ],
  "services": [
    { "name": "llm-api",       "version": "0.5.1", "installed": true,  "model": "Qwen2.5-1.5B-Instruct-Q4_K_M.gguf" }
  ]
}`}
      example={`curl -k https://your-broker:8080/node/{nodeId}/provider/versions \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-note api-note-gap"><b>nodeId</b>: Request goes to the node selected in the right panel.</div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/node/{nodeId}/provider/versions", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <h3 className="api-section-title">Logs</h3>

    <ApiCard method="GET" path="/node/{nodeId}/provider/logs" title="Log Files" auth
      desc={<>List available log files on the node. Plain logs (<code>logs/*.log</code>) and structured event logs (<code>logs/events/*.jsonl</code>) are returned together with a <code>category</code> discriminator.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      response={`[
  { "name": "provider.log",         "size": 245312,  "category": "log" },
  { "name": "engine-runner.log",    "size": 98221,   "category": "log" },
  { "name": "events/llm-api.jsonl", "size": 12450,   "category": "event" }
]`}
      example={`curl -k https://your-broker:8080/node/{nodeId}/provider/logs \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-note api-note-gap"><b>nodeId</b>: Request goes to the node selected in the right panel.</div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/node/{nodeId}/provider/logs", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/node/{nodeId}/provider/logs?file=&tail=&q=" title="Log Tail" auth
      desc={<>Tail a single log file. Supports last N lines (max 1000) and case-insensitive substring search. Path traversal blocked — files must live under the node's <code>logs/</code> directory.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "file", type: "string", required: true, desc: "Log file name (e.g. provider.log, events/llm-api.jsonl)" },
        { name: "tail", type: "int", default: "100", desc: "Number of trailing lines (max 1000)" },
        { name: "q", type: "string", desc: "Case-insensitive substring filter applied after tail" },
      ]}
      response={`{
  "lines": [
    "2026-05-12 07:32:01 [provider] starting llm-api ...\\n",
    "2026-05-12 07:32:03 [provider] llm-api ready\\n"
  ],
  "total": 2
}`}
      example={`curl -k "https://your-broker:8080/node/{nodeId}/provider/logs?file=provider.log&tail=50" \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>file <span className="req">*</span></label><input type="text" id="try-node-log-file" defaultValue="provider.log" placeholder="provider.log / events/llm-api.jsonl" /></div>
        <div className="api-try-row"><label>tail</label><input type="number" id="try-node-log-tail" defaultValue={100} /></div>
        <div className="api-try-row"><label>q</label><input type="text" id="try-node-log-q" placeholder="optional substring search" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const file = document.getElementById("try-node-log-file")?.value;
            const tail = document.getElementById("try-node-log-tail")?.value;
            const q = document.getElementById("try-node-log-q")?.value;
            if (!file) { el.style.display = "block"; el.textContent = "Enter file"; return; }
            const qs = new URLSearchParams({ file });
            if (tail) qs.set("tail", tail);
            if (q) qs.set("q", q);
            tryFetch("GET", "/node/{nodeId}/provider/logs?" + qs.toString(), null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <h3 className="api-section-title">Profile & Sync</h3>

    <ApiCard method="GET" path="/node/{nodeId}/provider/profiles" title="Profiles" auth
      desc="List all configuration profiles on the node and which one is currently active. Profiles bundle per-service settings (concurrency, max queue, LoRA defaults, etc.)."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      response={`{
  "active": "default",
  "profiles": [
    { "name": "default", "description": "factory default", "services": ["llm-api", "sd-api"] },
    { "name": "highmem", "description": "32GB VRAM tuning",  "services": ["llm-api", "sd-api"] }
  ]
}`}
      example={`curl -k https://your-broker:8080/node/{nodeId}/provider/profiles \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-note api-note-gap"><b>nodeId</b>: Request goes to the node selected in the right panel.</div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/node/{nodeId}/provider/profiles", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/node/{nodeId}/provider/sync/status" title="Sync Status" auth
      desc={<>Current state of node's sync with the Rendezvous server: are we registered, when was the last FullSync, any pending TPM challenges. Use this to diagnose "why is my node offline in catalog".</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      response={`{
  "registered": true,
  "session_id": "0xabcd1234...",
  "last_full_sync_at": "2026-05-12T07:30:00Z",
  "last_heartbeat_at": "2026-05-12T08:45:23Z",
  "rendezvous_addr": "https://110.44.52.98:9000",
  "tpm_pending": false,
  "tpm_verified": true
}`}
      example={`curl -k https://your-broker:8080/node/{nodeId}/provider/sync/status \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-note api-note-gap"><b>nodeId</b>: Request goes to the node selected in the right panel.</div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/node/{nodeId}/provider/sync/status", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <h3 className="api-section-title">Provider Queue / Jobs / Outputs</h3>
    <p className="api-page-subtitle-tight">
      Provider owns the queue (Phase 8 migration). These endpoints are at the <strong>node level</strong> — not per service.
      Per-service queue stats are exposed by each service under <code>/svc/{`{name}`}/v1/queue/stats</code>.
    </p>

    <ApiCard method="GET" path="/node/{nodeId}/provider/v1/queue/stats" title="Provider Queue Stats" auth
      desc="Aggregated queue depth, jobs currently running, total jobs done since boot, and average job duration across all services on the node."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      response={`{
  "pending": 2,
  "running": 1,
  "total_jobs_done": 5821,
  "avg_job_sec": 3.4,
  "by_service": {
    "llm-api": { "pending": 0, "running": 1, "total_jobs_done": 4910 },
    "sd-api":  { "pending": 2, "running": 0, "total_jobs_done":  911 }
  }
}`}
      example={`curl -k https://your-broker:8080/node/{nodeId}/provider/v1/queue/stats \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-note api-note-gap"><b>nodeId</b>: Request goes to the node selected in the right panel.</div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/node/{nodeId}/provider/v1/queue/stats", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/provider/v1/jobs" title="Submit Job" badge="Async"
      desc={<>Submit an inference job directly to Provider's queue. Most clients hit a service path (e.g. <code>/svc/llm-api/v1/chat/completions</code>) instead — that path internally calls this same queue. Use this when you want explicit control over service routing, params shape, or wait semantics.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "service", type: "string", required: true, desc: "Service name to dispatch to (e.g. llm-api, sd-api, vllm-api)" },
        { name: "path", type: "string", default: "/v1/inference", desc: "Override the path forwarded to the underlying service" },
        { name: "params", type: "json", required: true, desc: "Body forwarded to the service (e.g. {messages:[...], temperature:0.7} for llm-api)" },
        { name: "wait", type: "bool", default: "false", desc: "true → block until the worker finishes and return the raw service response; false → return 202 + job_id immediately" },
      ]}
      response={`// Async (default) — 202 Accepted
{
  "job_id":      "abc123def456",
  "service":     "llm-api",
  "position":    0,
  "queue_depth": 1,
  "queue_max":   50
}

// Sync (wait=true) — 200 + raw service response body

// Queue full — 429 Too Many Requests
{ "error": "queue_full", "queue_depth": 50, "queue_max": 50 }`}
      example={`# Async submit
curl -k -X POST https://your-broker:8080/node/{nodeId}/provider/v1/jobs \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{"service":"llm-api","params":{"messages":[{"role":"user","content":"hello"}]}}'

# Sync — body is the service's raw response
curl -k -X POST https://your-broker:8080/node/{nodeId}/provider/v1/jobs \\
  -H "Content-Type: application/json" \\
  -d '{"service":"llm-api","wait":true,"params":{"messages":[{"role":"user","content":"hello"}]}}'`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>service <span className="req">*</span></label><input type="text" id="try-node-submit-svc" defaultValue="llm-api" placeholder="llm-api | sd-api | vllm-api" /></div>
        <div className="api-try-row"><label>params <span className="req">*</span></label><textarea id="try-node-submit-params" rows={6} style={{ flex: 1, fontFamily: "Consolas, monospace", fontSize: 10 }} defaultValue={`{"messages":[{"role":"user","content":"hello"}]}`}></textarea></div>
        <div className="api-try-row"><label>wait</label><input type="text" id="try-node-submit-wait" placeholder="true | false (optional)" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const svc = document.getElementById("try-node-submit-svc")?.value;
            const raw = document.getElementById("try-node-submit-params")?.value;
            const wait = document.getElementById("try-node-submit-wait")?.value === "true";
            if (!svc) { el.style.display = "block"; el.textContent = "Enter service"; return; }
            let params;
            try { params = JSON.parse(raw || "{}"); }
            catch { el.style.display = "block"; el.textContent = "Invalid JSON in params"; return; }
            tryFetch("POST", "/node/{nodeId}/provider/v1/jobs", { service: svc, params, wait }, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/node/{nodeId}/provider/v1/jobs/{jobId}" title="Provider Job by ID" auth
      desc="Get full snapshot of a single job — status, progress, timing, and any output URL if completed."
      pathParams={[
        { name: "nodeId", type: "string", desc: "Target node ID" },
        { name: "jobId", type: "string", desc: "Job ID returned from queue submission" },
      ]}
      response={`{
  "job_id":     "abc123def456",
  "service":    "llm-api",
  "status":     "done",
  "progress":   100,
  "queued_at":   "2026-05-12T08:45:00Z",
  "started_at":  "2026-05-12T08:45:00Z",
  "finished_at": "2026-05-12T08:45:04Z",
  "duration_ms": 4031,
  "url": "/outputs/llm-api_abc123def456.json",
  "submitter_address": "0xABC...0123"  // empty when anonymously submitted
}`}
      example={`curl -k https://your-broker:8080/node/{nodeId}/provider/v1/jobs/abc123def456 \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>jobId <span className="req">*</span></label><input type="text" id="try-node-job-id" placeholder="abc123def456" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const id = document.getElementById("try-node-job-id")?.value;
            if (!id) { el.style.display = "block"; el.textContent = "Enter jobId"; return; }
            tryFetch("GET", `/node/{nodeId}/provider/v1/jobs/${encodeURIComponent(id)}`, null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/node/{nodeId}/provider/v1/jobs/{jobId}/result" title="Provider Job Result" auth
      desc={<>Fetch the raw response body of a completed job. Returns <code>409 Conflict</code> when the job is not yet finished.</>}
      pathParams={[
        { name: "nodeId", type: "string", desc: "Target node ID" },
        { name: "jobId", type: "string", desc: "Job ID" },
      ]}
      response={`// LLM example — body is whatever the underlying service returned
{
  "choices": [{
    "message": { "role": "assistant", "content": "..." },
    "finish_reason": "stop"
  }],
  "model": "Qwen2.5-1.5B-Instruct-Q4_K_M.gguf",
  "usage": { "prompt_tokens": 20, "completion_tokens": 30, "total_tokens": 50 }
}`}
      example={`curl -k https://your-broker:8080/node/{nodeId}/provider/v1/jobs/abc123def456/result \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>jobId <span className="req">*</span></label><input type="text" id="try-node-jobres-id" placeholder="abc123def456" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const id = document.getElementById("try-node-jobres-id")?.value;
            if (!id) { el.style.display = "block"; el.textContent = "Enter jobId"; return; }
            tryFetch("GET", `/node/{nodeId}/provider/v1/jobs/${encodeURIComponent(id)}/result`, null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/node/{nodeId}/provider/outputs/{filename}" title="Provider Output File" auth
      desc={<>Download the saved output of a completed job (image, JSON, etc.). Use the <code>url</code> returned by Job by ID / Job Result.</>}
      pathParams={[
        { name: "nodeId", type: "string", desc: "Target node ID" },
        { name: "filename", type: "string", desc: "Filename portion of the url field" },
      ]}
      response={`// Binary or JSON body — same shape as the service that produced it.
// e.g. sd-api → image/png, llm-api → application/json.`}
      example={`# Save with curl
curl -k -o result.bin \\
  https://your-broker:8080/node/{nodeId}/provider/outputs/llm-api_abc123def456.json \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>filename <span className="req">*</span></label><input type="text" id="try-node-out-filename" placeholder="llm-api_abc123def456.json" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const filename = document.getElementById("try-node-out-filename")?.value;
            if (!filename) return;
            const url = `/node/${encodeURIComponent(selectedNode)}/provider/outputs/${encodeURIComponent(filename)}`;
            window.open(url, "_blank");
          }}>Open File</button>
          <button className="api-try-send api-try-send-success" onClick={e => {
            const filename = document.getElementById("try-node-out-filename")?.value;
            if (!filename) return;
            const url = `/node/${encodeURIComponent(selectedNode)}/provider/outputs/${encodeURIComponent(filename)}`;
            const a = document.createElement("a"); a.href = url; a.download = filename; a.click();
          }}>Download</button>
        </div>
      </TryIt>
    </ApiCard>

    <h3 className="api-section-title">Management — Config</h3>
    <p className="api-page-subtitle-tight">
      ⚠️ <strong>Management endpoints</strong> — always require owner/admin wallet signature
      regardless of the node's <code>auth_mode</code>. Destructive operations are flagged
      with a red <code>Critical</code> badge and a confirm dialog in TryIt.
    </p>

    <ApiCard method="GET" path="/node/{nodeId}/provider/config" title="Get Provider Config" auth
      desc={<>Read one config file from the node's <code>conf/</code> directory by name (e.g. <code>provider</code>, <code>llm-api</code>, <code>sd-api</code>). Each service has its own conf file. Owner/admin only.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "name", type: "string", required: true, desc: "Config file name without .json (e.g. provider, llm-api, sd-api)" },
      ]}
      response={`{
  "mode": "broker",
  "listen_addr": ":4433",
  "rendezvous_addr": "https://rv:9000",
  "auth_mode": "open",
  "auth_owner": "0xB171fe0B...",
  "services": [
    { "name": "llm-api", "addr": "127.0.0.1:7862", "type": "llm-api" }
  ]
}`}
      example={`curl -k "https://your-broker:8080/node/{nodeId}/provider/config?name=provider" \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>name <span className="req">*</span></label><input type="text" id="try-node-get-config-name" defaultValue="provider" placeholder="provider | llm-api | sd-api" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const name = document.getElementById("try-node-get-config-name")?.value;
            if (!name) { el.style.display = "block"; el.textContent = "Enter name"; return; }
            tryFetch("GET", "/node/{nodeId}/provider/config?name=" + encodeURIComponent(name), null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/provider/config" title="Update Provider Config" auth danger="Restart"
      desc={<>Patch one config file. Body is deep-merged into the existing JSON — partial updates are safe (other fields preserved). If the patched file is the provider's main config, the change is <strong>hot-reloaded</strong> within ~1s; otherwise the related service restarts.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "name", type: "string", required: true, desc: "Config file name (e.g. provider, llm-api)" },
        { name: "config", type: "object", required: true, desc: "Partial config object — deep-merged into existing file" },
      ]}
      response={`{ "status": "ok" }`}
      example={`curl -k -X POST https://your-broker:8080/node/{nodeId}/provider/config \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{
    "name":"provider",
    "config":{ "auth_mode": "protected" }
  }'`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>name <span className="req">*</span></label><input type="text" id="try-node-set-config-name" defaultValue="provider" placeholder="provider | llm-api" /></div>
        <div className="api-try-row" style={{ alignItems: "flex-start" }}>
          <label>config <span className="req">*</span></label>
          <textarea id="try-node-set-config-body" rows={6} style={{ flex: 1, fontFamily: "Consolas, monospace", fontSize: 10 }} defaultValue={`{"auth_mode":"open"}`}></textarea>
        </div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const name = document.getElementById("try-node-set-config-name")?.value;
            const raw = document.getElementById("try-node-set-config-body")?.value;
            if (!name) { el.style.display = "block"; el.textContent = "Enter name"; return; }
            let cfg;
            try { cfg = JSON.parse(raw || "{}"); }
            catch { el.style.display = "block"; el.textContent = "Invalid JSON in config"; return; }
            if (!window.confirm(`Update '${name}' config on this node? May trigger reload/restart.`)) return;
            tryFetch("POST", "/node/{nodeId}/provider/config", { name, config: cfg }, el);
          }}>Update Config</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <h3 className="api-section-title">Management — Profiles</h3>

    <ApiCard method="POST" path="/node/{nodeId}/provider/active-profile" title="Set Active Profile" auth danger="Restart"
      desc={<>Switch which profile is currently active for a service. Restarts the service (only for IANN-managed services — external like <code>vllm</code> skipped).</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "service", type: "string", required: true, desc: "Service name (e.g. llm-api, sd-api)" },
        { name: "name", type: "string", required: true, desc: "Profile name to activate" },
      ]}
      response={`{ "status": "active_profile_updated", "restarted": true }`}
      example={`curl -k -X POST https://your-broker:8080/node/{nodeId}/provider/active-profile \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{"service":"llm-api","name":"qwen14b-12gb"}'`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>service <span className="req">*</span></label><input type="text" id="try-node-active-svc" defaultValue="llm-api" /></div>
        <div className="api-try-row"><label>name <span className="req">*</span></label><input type="text" id="try-node-active-name" placeholder="default | highmem | ..." /></div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const svc = document.getElementById("try-node-active-svc")?.value;
            const name = document.getElementById("try-node-active-name")?.value;
            if (!svc || !name) { el.style.display = "block"; el.textContent = "Enter service + name"; return; }
            if (!window.confirm(`Switch active profile of '${svc}' to '${name}'? Service will restart.`)) return;
            tryFetch("POST", "/node/{nodeId}/provider/active-profile", { service: svc, name }, el);
          }}>Set Active</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/provider/profile" title="Create Profile" auth
      desc={<>Upsert (create or update) a named profile inside a service's profile set. With <code>set_active:true</code> the new profile becomes active and the service restarts.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "service", type: "string", required: true, desc: "Service name" },
        { name: "name", type: "string", required: true, desc: "Profile name (lowercase slug — a-z, 0-9, -)" },
        { name: "label", type: "string", desc: "Human-readable label" },
        { name: "architecture", type: "string", desc: "Model architecture (sd15 | sdxl | ... for sd-api)" },
        { name: "values", type: "object", required: true, desc: "Profile values keyed by manifest.inspect field (e.g. {ctx_size:32768, gpu_layers:35})" },
        { name: "loras", type: "object", desc: "LoRA settings (sd-api only)" },
        { name: "set_active", type: "bool", default: "false", desc: "Immediately make this profile active + restart service" },
      ]}
      response={`{ "status": "saved", "active": "qwen14b-32k", "restarted": true }`}
      example={`curl -k -X POST https://your-broker:8080/node/{nodeId}/provider/profile \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{
    "service":"llm-api","name":"qwen14b-32k","label":"Qwen 14B 32K",
    "values":{"ctx_size":32768,"gpu_layers":35},
    "set_active":true
  }'`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>service <span className="req">*</span></label><input type="text" id="try-node-upsert-svc" defaultValue="llm-api" /></div>
        <div className="api-try-row"><label>name <span className="req">*</span></label><input type="text" id="try-node-upsert-name" placeholder="qwen14b-32k" /></div>
        <div className="api-try-row"><label>label</label><input type="text" id="try-node-upsert-label" placeholder="Qwen 14B 32K (optional)" /></div>
        <div className="api-try-row" style={{ alignItems: "flex-start" }}>
          <label>values <span className="req">*</span></label>
          <textarea id="try-node-upsert-values" rows={4} style={{ flex: 1, fontFamily: "Consolas, monospace", fontSize: 10 }} defaultValue={`{"ctx_size":16384,"gpu_layers":32}`}></textarea>
        </div>
        <div className="api-try-row"><label>set_active</label><input type="text" id="try-node-upsert-active" placeholder="true | false (optional)" /></div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const svc = document.getElementById("try-node-upsert-svc")?.value;
            const name = document.getElementById("try-node-upsert-name")?.value;
            const label = document.getElementById("try-node-upsert-label")?.value;
            const setActive = document.getElementById("try-node-upsert-active")?.value === "true";
            const raw = document.getElementById("try-node-upsert-values")?.value;
            if (!svc || !name) { el.style.display = "block"; el.textContent = "Enter service + name"; return; }
            let values;
            try { values = JSON.parse(raw || "{}"); }
            catch { el.style.display = "block"; el.textContent = "Invalid JSON in values"; return; }
            const body = { service: svc, name, values, set_active: setActive };
            if (label) body.label = label;
            tryFetch("POST", "/node/{nodeId}/provider/profile", body, el);
          }}>Save Profile</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="DELETE" path="/node/{nodeId}/provider/profile" title="Delete Profile" auth danger="Critical"
      desc={<>Remove a profile from a service's set. If the deleted profile was active, another profile takes over and the service restarts. Read-only services (e.g. <code>vllm</code>) return 403.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "service", type: "query", required: true, desc: "Service name (e.g. llm-api)" },
        { name: "name", type: "query", required: true, desc: "Profile name to delete" },
      ]}
      response={`{ "status": "deleted", "active": "default", "restarted": false }`}
      example={`curl -k -X DELETE "https://your-broker:8080/node/{nodeId}/provider/profile?service=llm-api&name=oldprofile" \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>service <span className="req">*</span></label><input type="text" id="try-node-delprof-svc" defaultValue="llm-api" /></div>
        <div className="api-try-row"><label>name <span className="req">*</span></label><input type="text" id="try-node-delprof-name" placeholder="profile name" /></div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const svc = document.getElementById("try-node-delprof-svc")?.value;
            const name = document.getElementById("try-node-delprof-name")?.value;
            if (!svc || !name) { el.style.display = "block"; el.textContent = "Enter service + name"; return; }
            if (!window.confirm(`Permanently delete profile '${name}' from '${svc}'? This cannot be undone.`)) return;
            const qs = new URLSearchParams({ service: svc, name }).toString();
            tryFetch("DELETE", "/node/{nodeId}/provider/profile?" + qs, null, el);
          }}>Delete Profile</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <h3 className="api-section-title">Management — Emblem</h3>

    <ApiCard method="POST" path="/node/{nodeId}/provider/emblem" title="Set Emblem" auth
      desc={<>Upload a base64 data-URL image as the node's public emblem (icon). PNG/JPG/WEBP supported. Written to <code>home_dir/profile/emblem.{`{ext}`}</code> and recorded in config.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "image", type: "string", required: true, desc: "Data URL: data:image/png;base64,... (or jpeg/webp)" },
      ]}
      response={`{ "emblem": "profile/emblem.png" }`}
      example={`curl -k -X POST https://your-broker:8080/node/{nodeId}/provider/emblem \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{"image":"data:image/png;base64,iVBORw0KGgoAAA..."}'`}
    >
      <TryIt auth>
        <div className="api-try-row" style={{ alignItems: "flex-start" }}>
          <label>image <span className="req">*</span></label>
          <textarea id="try-node-emblem-img" rows={3} style={{ flex: 1, fontFamily: "Consolas, monospace", fontSize: 10 }} placeholder="data:image/png;base64,..."></textarea>
        </div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const image = document.getElementById("try-node-emblem-img")?.value;
            if (!image) { el.style.display = "block"; el.textContent = "Paste data URL"; return; }
            tryFetch("POST", "/node/{nodeId}/provider/emblem", { image }, el);
          }}>Upload Emblem</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="DELETE" path="/node/{nodeId}/provider/emblem" title="Delete Emblem" auth
      desc="Remove the node's emblem file (if any) and clear the config reference."
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      response={`{ "status": "ok" }`}
      example={`curl -k -X DELETE https://your-broker:8080/node/{nodeId}/provider/emblem \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            if (!window.confirm("Remove this node's emblem?")) return;
            tryFetch("DELETE", "/node/{nodeId}/provider/emblem", null, el);
          }}>Delete Emblem</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <h3 className="api-section-title">Management — Filesystem & Sync</h3>

    <ApiCard method="POST" path="/node/{nodeId}/provider/scan-local" title="Scan Local" auth
      desc={<>Scan one or more local paths under <code>ai/models/</code> on the node, recursively walk directories, filter by model extensions (.gguf, .safetensors, .pt, ...) and return file metadata (size + relative install_path). Used internally by the catalog Search page when registering pre-downloaded models.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "paths", type: "string[]", required: true, desc: "Paths relative to ai/models or absolute paths under ai/models" },
      ]}
      response={`[
  { "file_name": "qwen2.5-1.5b-Q4_K_M.gguf", "install_path": "ai/models/llm-api", "size_bytes": 1011700000 }
]`}
      example={`curl -k -X POST https://your-broker:8080/node/{nodeId}/provider/scan-local \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{"paths":["llm-api"]}'`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>paths <span className="req">*</span></label><input type="text" id="try-node-scan-paths" defaultValue="llm-api" placeholder="comma-separated paths under ai/models" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const raw = document.getElementById("try-node-scan-paths")?.value;
            if (!raw) { el.style.display = "block"; el.textContent = "Enter at least one path"; return; }
            const paths = raw.split(",").map(s => s.trim()).filter(Boolean);
            tryFetch("POST", "/node/{nodeId}/provider/scan-local", { paths }, el);
          }}>Scan</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/provider/register" title="Force Re-register" auth
      desc={<>Force an immediate FullSync re-register to the Rendezvous server. Use when the node's catalog entry looks stale (services / inspect fields out of date). Idempotent — repeated calls coalesce.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      response={`{ "status": "register_queued" }`}
      example={`curl -k -X POST https://your-broker:8080/node/{nodeId}/provider/register \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*"`}
    >
      <TryIt auth>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("POST", "/node/{nodeId}/provider/register", null, el);
          }}>Re-register</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/provider/sync/create" title="Sync Create" auth
      desc={<>Kick off async snapshot creation. The provider walks WorkDir, hashes files, and exposes a token-protected snapshot for the requesting party. Result is consumed by paired peers via <code>sync/snapshot</code> + <code>sync/file</code> (bearer-token endpoints).</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "ttl_hours", type: "int", default: "1", desc: "Snapshot lifetime in hours (1-24, clamped)" },
      ]}
      response={`{ "status": "started", "token": "...", "expires_at": "..." }`}
      example={`curl -k -X POST https://your-broker:8080/node/{nodeId}/provider/sync/create \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{"ttl_hours":2}'`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>ttl_hours</label><input type="number" id="try-node-sync-ttl" defaultValue={1} /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const ttl = parseInt(document.getElementById("try-node-sync-ttl")?.value) || 1;
            tryFetch("POST", "/node/{nodeId}/provider/sync/create", { ttl_hours: ttl }, el);
          }}>Create Snapshot</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <h3 className="api-section-title">Management — Software Lifecycle</h3>

    <ApiCard method="POST" path="/node/{nodeId}/installer/install" title="Install Software" auth danger="Critical" badge="SSE"
      desc={<>Install software (core / engine / service / model) on the node. Returns a Server-Sent Events stream with download / install progress. Equivalent to the <code>isann install</code> CLI but driven over the broker tunnel.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "type", type: "string", required: true, desc: "core | engine | service | model | lora | dep" },
        { name: "name", type: "string", desc: "Component name (e.g. provider, sd.cpp). For -model with --src, derived from URL filename" },
        { name: "src", type: "string", desc: "URL for engine/model/lora/dep" },
        { name: "repo", type: "string", desc: "Repo URL (alternative to src for -model: HF / Civitai shortlinks)" },
        { name: "for_service", type: "string", desc: "Target service (sd-api/llm-api) for -model installs" },
        { name: "architecture", type: "string", desc: "Model architecture for -model (sd15/sdxl/...)" },
      ]}
      response={`// SSE stream
data: {"type":"start","name":"sd.cpp"}
data: {"type":"download","file":"sd.cpp.zip","percent":42}
data: {"type":"verify","ok":true}
data: {"type":"done","name":"sd.cpp","version":"0.5.1"}

// or
data: {"type":"error","message":"network timeout"}`}
      example={`curl -k -N -X POST https://your-broker:8080/node/{nodeId}/installer/install \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{"type":"engine","name":"sd.cpp"}'`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>type <span className="req">*</span></label><input type="text" id="try-node-install-type" defaultValue="engine" placeholder="core | engine | service | model" /></div>
        <div className="api-try-row"><label>name</label><input type="text" id="try-node-install-name" placeholder="sd.cpp (optional for -model)" /></div>
        <div className="api-try-row"><label>src</label><input type="text" id="try-node-install-src" placeholder="URL (for -model / -engine)" /></div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const type = document.getElementById("try-node-install-type")?.value;
            const name = document.getElementById("try-node-install-name")?.value;
            const src = document.getElementById("try-node-install-src")?.value;
            if (!type) { el.style.display = "block"; el.textContent = "Enter type"; return; }
            if (!window.confirm(`Install ${type}${name ? ` '${name}'` : ""} on this node? Download may be large.`)) return;
            const body = { type };
            if (name) body.name = name;
            if (src) body.src = src;
            tryFetch("POST", "/node/{nodeId}/installer/install", body, el);
          }}>Install</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/installer/uninstall" title="Uninstall Software" auth danger="Critical"
      desc={<>Remove software from the node. <strong>For models</strong>: deletes the model files from disk. <strong>For engines / services</strong>: stops the service first, then removes binaries.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "type", type: "string", required: true, desc: "core | engine | service | model | lora | dep" },
        { name: "name", type: "string", required: true, desc: "Component name to remove" },
        { name: "for_service", type: "string", desc: "Required for -model: which service the model belongs to (sd-api, llm-api)" },
        { name: "architecture", type: "string", desc: "Optional model architecture filter (sd15/sdxl)" },
      ]}
      response={`{ "status": "ok", "removed": ["ai/models/llm-api/qwen2.5-1.5b.gguf"] }`}
      example={`curl -k -X POST https://your-broker:8080/node/{nodeId}/installer/uninstall \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{"type":"model","name":"qwen2.5-1.5b","for_service":"llm-api"}'`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>type <span className="req">*</span></label><input type="text" id="try-node-uninstall-type" defaultValue="model" placeholder="model | engine | service" /></div>
        <div className="api-try-row"><label>name <span className="req">*</span></label><input type="text" id="try-node-uninstall-name" placeholder="component name" /></div>
        <div className="api-try-row"><label>for_service</label><input type="text" id="try-node-uninstall-for" placeholder="llm-api | sd-api (model only)" /></div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const type = document.getElementById("try-node-uninstall-type")?.value;
            const name = document.getElementById("try-node-uninstall-name")?.value;
            const forSvc = document.getElementById("try-node-uninstall-for")?.value;
            if (!type || !name) { el.style.display = "block"; el.textContent = "Enter type + name"; return; }
            if (!window.confirm(`Uninstall ${type} '${name}'? This deletes files from disk.`)) return;
            const body = { type, name };
            if (forSvc) body.for_service = forSvc;
            tryFetch("POST", "/node/{nodeId}/installer/uninstall", body, el);
          }}>Uninstall</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <h3 className="api-section-title">Management — Service Lifecycle</h3>

    <ApiCard method="POST" path="/node/{nodeId}/provider/start" title="Start Service" auth danger="Critical"
      desc={<>Spawn (or re-spawn) a managed service process — engine-runner with the manifest for that service. Idempotent if the service is already running, but will attempt fresh spawn after any crash.</>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "name", type: "string", required: true, desc: "Service name (e.g. llm-api, sd-api)" },
      ]}
      response={`{ "status": "ok" }`}
      example={`curl -k -X POST https://your-broker:8080/node/{nodeId}/provider/start \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{"name":"llm-api"}'`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>name <span className="req">*</span></label><input type="text" id="try-node-start-name" defaultValue="llm-api" placeholder="service name" /></div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const name = document.getElementById("try-node-start-name")?.value;
            if (!name) { el.style.display = "block"; el.textContent = "Enter service name"; return; }
            if (!window.confirm(`Start service '${name}' on this node?`)) return;
            tryFetch("POST", "/node/{nodeId}/provider/start", { name }, el);
          }}>Start</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/node/{nodeId}/provider/stop" title="Stop Service" auth danger="Critical"
      desc={<>Gracefully stop a running service. SIGTERM with timeout; any running inference jobs in the queue will fail. <strong>Use Kill Process for hard termination.</strong></>}
      pathParams={[{ name: "nodeId", type: "string", desc: "Target node ID" }]}
      params={[
        { name: "name", type: "string", required: true, desc: "Service name" },
      ]}
      response={`{ "status": "ok" }`}
      example={`curl -k -X POST https://your-broker:8080/node/{nodeId}/provider/stop \\
  -H "Authorization: ISANN {sig}" \\
  -H "X-ISANN-Message: owner:provider:*:0:{expiresAt}:*" \\
  -H "Content-Type: application/json" \\
  -d '{"name":"llm-api"}'`}
    >
      <TryIt auth>
        <div className="api-try-row"><label>name <span className="req">*</span></label><input type="text" id="try-node-stop-name" defaultValue="llm-api" placeholder="service name" /></div>
        <div className="api-try-actions">
          <button className="api-try-send danger" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const name = document.getElementById("try-node-stop-name")?.value;
            if (!name) { el.style.display = "block"; el.textContent = "Enter service name"; return; }
            if (!window.confirm(`Stop service '${name}'? Running jobs will fail.`)) return;
            tryFetch("POST", "/node/{nodeId}/provider/stop", { name }, el);
          }}>Stop</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

  </>);
}

// ═══════ Gate Page ═══════
function GatePage({ tryFetch }) {
  return (<>
    <h2>Gate API (via Broker proxy)</h2>
    <p className="api-page-subtitle">
      Gate hosts the software catalog (cores / engines / services / models) and aggregates RV nodes across regions.
      All endpoints below are reached through the broker's <code>/gate/v1/*</code> proxy — broker forwards to its configured Gate.
    </p>

    <ApiCard method="GET" path="/gate/v1/rendezvous" title="List Rendezvous"
      desc="List all Rendezvous servers registered in Gate. Used by the region selector to switch between RVs."
      response={`[
  {
    "id": "rv-kr-1",
    "addr": "https://110.44.52.98:9000",
    "region": "KR-Seoul",
    "status": "online",
    "node_count": 17,
    "version": "0.1.0",
    "last_report": "2026-05-11T03:14:00Z"
  }
]`}
      example={`curl https://your-broker:8080/gate/v1/rendezvous`}
    >
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/gate/v1/rendezvous", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/gate/v1/nodes" title="All Nodes Catalog"
      desc={<>Aggregated node catalog across all Rendezvous servers Gate is tracking. Useful when the broker's primary RV only knows a subset of nodes (e.g. cross-region discovery). Forwards to Gate's <code>/v1/nodes?role=provider</code>.</>}
      response={`[{
  "node_id": "P:0xabc...",
  "role": "provider",
  "rendezvous_id": "rv-kr-1",
  "addr": "tcp://1.2.3.4:4433",
  "status": "idle",
  "hardware": "{...JSON string (volatile fields stripped)...}",
  "services": "[...JSON string...]",
  "last_seen": "2026-05-11T03:13:21Z"
}]`}
      example={`curl https://your-broker:8080/gate/v1/nodes`}
    >
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/gate/v1/nodes", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/gate/v1/curated-models" title="Curated Models"
      desc={<>Operator-curated model recommendations rendered as cards on the Workspace home (Starter / Recommend sections). Read-only — owner curates via the Gate admin UI.</>}
      params={[
        { name: "category", type: "string", desc: "Filter: starter | recommend. Omit for both." },
      ]}
      response={`[
  {
    "id": 7,
    "category": "starter",
    "source": "huggingface",          // huggingface | civitai
    "external_id": "Qwen/Qwen2.5-1.5B-Instruct-GGUF",
    "hash": "",                       // sha256 (civitai only)
    "for_service": "llm-api",         // llm-api | sd-api | vllm-api
    "vram_gb": 4,
    "note": "lightweight starter chat",
    "sort_order": 10,
    "enabled": true
  }
]`}
      example={`curl https://your-broker:8080/gate/v1/curated-models
curl https://your-broker:8080/gate/v1/curated-models?category=starter`}
    >
      <TryIt>
        <div className="api-try-row"><label>category</label><input type="text" id="try-gate-cm-cat" placeholder="starter | recommend (optional)" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const cat = document.getElementById("try-gate-cm-cat")?.value;
            tryFetch("GET", "/gate/v1/curated-models" + (cat ? "?category=" + encodeURIComponent(cat) : ""), null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/gate/v1/software" title="Software Catalog"
      desc={<>Unified software catalog — cores (isann/broker/provider/engine-runner), engines (sd.cpp, llama.cpp), services (sd-api, llm-api), and curated models. Used by Deploy and Models pages.</>}
      params={[
        { name: "type", type: "string", desc: "Filter: core | service | engine | model" },
      ]}
      response={`[
  {
    "name": "isann",
    "type": "core",
    "display_name": "iSANN",
    "install_path": "",
    "base_url": "/files/cores/{platform}/isann/{version}",
    "is_system": true,
    "has_model": false
  },
  {
    "name": "llama.cpp",
    "type": "engine",
    "service_name": "llm-api"
  }
]`}
      example={`curl https://your-broker:8080/gate/v1/software
curl https://your-broker:8080/gate/v1/software?type=engine`}
    >
      <TryIt>
        <div className="api-try-row"><label>type</label><input type="text" id="try-gate-sw-type" placeholder="core | service | engine | model (optional)" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const t = document.getElementById("try-gate-sw-type")?.value;
            tryFetch("GET", "/gate/v1/software" + (t ? "?type=" + encodeURIComponent(t) : ""), null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/gate/v1/software/package" title="Software Package"
      desc={<>Resolved package descriptor for a single software item — list of downloadable files + verify manifest. This is the isann CLI's install entry point. Returns 404 if the software is unknown or the requested version doesn't exist.</>}
      params={[
        { name: "type", type: "string", required: true, desc: "Software type: core | service | engine | model" },
        { name: "name", type: "string", required: true, desc: "Software name (e.g. isann, llama.cpp)" },
        { name: "version", type: "string", desc: "Specific version. Omit (or 'latest') for the latest." },
      ]}
      response={`{
  "name": "isann",
  "type": "core",
  "version": "0.5.1",
  "platform": "windows-x64",
  "latest": true,
  "install_root": "ai/isann/",
  "downloads": [
    {
      "file_name": "isann.exe",
      "download_url": "https://gate.example.com/files/cores/windows-x64/isann/0.5.1/isann.exe",
      "install_path": "ai/isann/",
      "size_bytes": 19664896,
      "required": true,
      "file_type": "download"
    }
  ],
  "verify": [
    {
      "file_name": "isann.exe",
      "hash": "abcd...sha256",
      "size_bytes": 19664896,
      "file_type": "verify-file"
    }
  ]
}`}
      example={`curl "https://your-broker:8080/gate/v1/software/package?type=core&name=isann"
curl "https://your-broker:8080/gate/v1/software/package?type=core&name=isann&version=0.5.1"`}
    >
      <TryIt>
        <div className="api-try-row"><label>type <span className="req">*</span></label><input type="text" id="try-gate-pkg-type" defaultValue="core" placeholder="core | engine | model" /></div>
        <div className="api-try-row"><label>name <span className="req">*</span></label><input type="text" id="try-gate-pkg-name" defaultValue="isann" placeholder="isann / llama.cpp / ..." /></div>
        <div className="api-try-row"><label>version</label><input type="text" id="try-gate-pkg-ver" placeholder="latest (or specific version)" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const type = document.getElementById("try-gate-pkg-type")?.value;
            const name = document.getElementById("try-gate-pkg-name")?.value;
            const ver = document.getElementById("try-gate-pkg-ver")?.value;
            if (!type || !name) { el.style.display = "block"; el.textContent = "Enter type and name"; return; }
            const qs = new URLSearchParams({ type, name });
            if (ver) qs.set("version", ver);
            tryFetch("GET", "/gate/v1/software/package?" + qs.toString(), null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/gate/v1/software/scan-url" title="Scan URL"
      desc={<>Scan a URL — if it's a directory listing (Apache/nginx autoindex), recursively walk and return file list. If it's a single file, return one-line response. Used by "Add Custom Model" flow to fill the file table automatically.</>}
      params={[
        { name: "url", type: "string", required: true, desc: "Directory or file URL" },
      ]}
      response={`// Directory
{
  "base_url": "https://example.com/foo/",
  "type": "directory",
  "files": [
    {
      "file_name": "isann.exe",
      "download_url": "https://example.com/foo/isann.exe",
      "install_path": "",
      "hash": "",
      "size_bytes": 0
    }
  ]
}

// Single file
{
  "base_url": "",
  "type": "file",
  "files": [
    { "file_name": "model.safetensors", "download_url": "...", "size_bytes": 4123456789 }
  ]
}`}
      example={`curl -X POST https://your-broker:8080/gate/v1/software/scan-url \\
  -H "Content-Type: application/json" \\
  -d '{"url":"https://example.com/files/cores/windows-x64/isann/0.5.1/"}'`}
    >
      <TryIt>
        <div className="api-try-row"><label>url <span className="req">*</span></label><input type="text" id="try-gate-scan-url" placeholder="https://example.com/foo/" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const url = document.getElementById("try-gate-scan-url")?.value;
            if (!url) { el.style.display = "block"; el.textContent = "Enter url"; return; }
            tryFetch("POST", "/gate/v1/software/scan-url", { url }, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="POST" path="/gate/v1/software/scan-file" title="Scan File"
      desc={<>Compute SHA-256 hash and file size of a single URL. Use <code>?size_only=true</code> to skip hashing (for large model files where size alone is enough).</>}
      params={[
        { name: "url", type: "string", required: true, desc: "Direct file URL" },
        { name: "size_only", type: "string", desc: "'true' = skip hash computation (large files)" },
      ]}
      response={`{
  "url": "https://example.com/model.gguf",
  "hash": "abcd...sha256",       // empty when size_only=true
  "size_bytes": 4123456789
}`}
      example={`curl -X POST https://your-broker:8080/gate/v1/software/scan-file \\
  -H "Content-Type: application/json" \\
  -d '{"url":"https://example.com/files/cores/windows-x64/isann/0.5.1/isann.exe"}'

# Size only (large model file)
curl -X POST "https://your-broker:8080/gate/v1/software/scan-file?size_only=true" \\
  -H "Content-Type: application/json" \\
  -d '{"url":"https://huggingface.co/.../model.safetensors"}'`}
    >
      <TryIt>
        <div className="api-try-row"><label>url <span className="req">*</span></label><input type="text" id="try-gate-scan-file-url" placeholder="https://example.com/file.bin" /></div>
        <div className="api-try-row"><label>size_only</label><input type="text" id="try-gate-scan-file-sizeonly" placeholder="true (optional)" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const url = document.getElementById("try-gate-scan-file-url")?.value;
            const sizeOnly = document.getElementById("try-gate-scan-file-sizeonly")?.value;
            if (!url) { el.style.display = "block"; el.textContent = "Enter url"; return; }
            const path = "/gate/v1/software/scan-file" + (sizeOnly === "true" ? "?size_only=true" : "");
            tryFetch("POST", path, { url }, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>
  </>);
}

// ═══════ Rendezvous Page ═══════
function RendezvousPage({ tryFetch }) {
  return (<>
    <h2>Rendezvous API</h2>
    <p className="api-page-subtitle">Rendezvous server — node registry, service metrics, and health. Queried by the broker to discover providers and pick routing targets.</p>

    <div className="api-note">
      <b>Base URL:</b> Rendezvous exposes REST over <code>rest_addr</code> (TCP). Protocol is HTTPS when <code>tls.enabled</code>, plain HTTP otherwise. Example: <code>https://rv-host:9002</code>.<br/>
      <b>Via Broker proxy:</b> <code>/rendezvous/v1/*?addr=&lt;rv-host&gt;</code> (see Broker → Node Discovery).
    </div>

    <h3 className="api-section-title" id="api-health-check">Health</h3>

    <ApiCard method="GET" path="/health" title="Rendezvous Health"
      desc="Check Rendezvous server health and version."
      response={`{
  "status": "ok",
  "version": "0.1.0",
  "hash": "abc123..."
}`}
      example={`curl -k https://rv-host:9002/health`}
    >
      <TryIt>
        <p className="api-empty-hint">No parameters</p>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            tryFetch("GET", "/rendezvous/health", null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <h3 className="api-section-title" id="api-static-nodes">Static: Nodes</h3>

    <ApiCard method="GET" path="/v1/nodes" title="List Nodes"
      desc={<>
        Return registered nodes with their <strong>static</strong> info: hardware specs (CPU name, GPU name + VRAM total, RAM total), services registered (name, version, model, ready), owner address, and TPM verification. Volatile metrics (queue depth, status, jobs) are served by <code>/v1/metrics</code>. Supports ETag (304).
      </>}
      params={[
        { name: "node_id", type: "string", desc: "Exact node ID(s), comma-separated. If set, all other filters are ignored." },
        { name: "role", type: "string", desc: "Filter by role: provider | broker" },
        { name: "service", type: "string", desc: "Only nodes that run this service (e.g. llm-api)" },
        { name: "model", type: "string", desc: "Exact model match (e.g. Qwen2.5-7B-Instruct-Q4_K_M.gguf)" },
        { name: "gpu", type: "string", desc: "GPU name substring (e.g. 4070, 3060, RTX)" },
        { name: "min_vram", type: "float", desc: "Minimum VRAM GB on any GPU" },
        { name: "status", type: "string", desc: "idle | busy | loading | stopped (cross-referenced from /v1/metrics)" },
        { name: "online", type: "bool", desc: "true = only nodes active in last 90s" },
        { name: "q", type: "string", desc: "Substring search on node ID or owner address" },
        { name: "page", type: "int", desc: "Page number (1-based) — requires limit for pagination" },
        { name: "limit", type: "int", desc: "Page size (default 50)" },
      ]}
      response={`[{
  "id": "P:0x8fF81256...",
  "role": "provider",
  "addr": "221.140.63.180:4433",
  "online": true,
  "last_seen": 1776955587,
  "started_at": "2026-04-23T14:37:55Z",
  "version": "0.1.0",
  "bin_hash": "54010d...",
  "owner_address": "0xB171fe0B...",
  "status": "idle",
  "auth_mode": "open",
  "tpm_verified": true,
  "hardware": {
    "cpus": [{ "name": "Intel i5-10500", "cores": 6, "clock_mhz": 3101 }],
    "gpus": [{ "name": "NVIDIA GTX 1650", "driver": "576.52", "vram_total_gb": 4 }],
    "ram": { "total_gb": 31.9 }
  },
  "services": [
    { "name": "sd-api", "version": "0.1.0", "model": "v1-5-pruned.safetensors", "server_ready": true, "child_pid": 10080, "child_name": "sd-server.exe" }
  ]
}]`}
      example={`# Direct
curl -k https://rv-host:9002/v1/nodes?role=provider&gpu=4070

# Via broker proxy
curl -k https://your-broker:8080/rendezvous/v1/nodes?addr=rv-host:9000&role=provider`}
    >
      <TryIt>
        <div className="api-try-row"><label>role</label><input type="text" id="try-rv-nodes-role" defaultValue="provider" /></div>
        <div className="api-try-row"><label>service</label><input type="text" id="try-rv-nodes-service" placeholder="llm-api / sd-api (optional)" /></div>
        <div className="api-try-row"><label>gpu</label><input type="text" id="try-rv-nodes-gpu" placeholder="4070 / 3060 (optional)" /></div>
        <div className="api-try-row"><label>status</label><input type="text" id="try-rv-nodes-status" placeholder="idle / busy (optional)" /></div>
        <div className="api-try-row"><label>online</label><input type="text" id="try-rv-nodes-online" placeholder="true (optional)" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const qs = new URLSearchParams();
            const role = document.getElementById("try-rv-nodes-role")?.value; if (role) qs.set("role", role);
            const svc = document.getElementById("try-rv-nodes-service")?.value; if (svc) qs.set("service", svc);
            const gpu = document.getElementById("try-rv-nodes-gpu")?.value; if (gpu) qs.set("gpu", gpu);
            const status = document.getElementById("try-rv-nodes-status")?.value; if (status) qs.set("status", status);
            const online = document.getElementById("try-rv-nodes-online")?.value; if (online) qs.set("online", online);
            const q = qs.toString();
            tryFetch("GET", "/v1/nodes" + (q ? "?" + q : ""), null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/v1/nodes?node_id=P:0x..." title="Node by ID"
      desc={<>Look up one or more specific nodes by ID. When <code>node_id</code> is present, all other filters are ignored. Comma-separated to fetch multiple nodes.</>}
      params={[
        { name: "node_id", type: "string", required: true, desc: "Node ID(s), comma-separated (e.g. P:0x123,P:0x456)" },
      ]}
      response={`[{
  "id": "P:0x8fF81256...",
  "role": "provider",
  "addr": "...",
  "hardware": {...},
  "services": [...]
}]`}
      example={`curl -k "https://rv-host:9002/v1/nodes?node_id=P:0x8fF81256F3866fe...,P:0xBADa8bE8..."`}
    >
      <TryIt>
        <div className="api-try-row"><label>node_id <span className="req">*</span></label><input type="text" id="try-rv-nodes-byid" placeholder="P:0x8fF..." /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const nid = document.getElementById("try-rv-nodes-byid")?.value;
            if (!nid) { el.style.display = "block"; el.textContent = "Enter node_id"; return; }
            tryFetch("GET", "/v1/nodes?node_id=" + encodeURIComponent(nid), null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <h3 className="api-section-title" id="api-metrics">Live Service Metrics</h3>

    <ApiCard method="GET" path="/v1/metrics" title="List Metrics"
      desc={<>
        Return heartbeat-aggregated service metrics (1 Hz). Each row = one (node, service) combination with current <code>status</code>, <code>queue_depth</code>, <code>total_jobs_done</code>, <code>avg_job_sec</code>. Routing layer uses this to pick the least-loaded node for a service. Static filters (<code>model</code>, <code>gpu</code>, <code>min_vram</code>) are cross-referenced against <code>/v1/nodes</code> by node_id.
      </>}
      params={[
        { name: "node_id", type: "string", desc: "Exact node ID(s), comma-separated. Ignores other filters when set." },
        { name: "service", type: "string", desc: "Service name (e.g. llm-api, sd-api)" },
        { name: "src", type: "string", desc: "Alias for service — shorthand filter" },
        { name: "model", type: "string", desc: "Exact model match (cross-ref via static)" },
        { name: "gpu", type: "string", desc: "GPU name substring (cross-ref via static)" },
        { name: "min_vram", type: "float", desc: "Minimum VRAM GB (cross-ref via static)" },
        { name: "status", type: "string", desc: "idle | busy | loading | stopped" },
      ]}
      response={`[
  {
    "node_id": "P:0x8fF81256...",
    "service": "sd-api",
    "status": "idle",
    "queue_depth": 0,
    "total_jobs_done": 150,
    "avg_job_sec": 12,
    "last_job_ms": 3120,
    "running_job_id": ""
  },
  {
    "node_id": "P:0xBADa8bE8...",
    "service": "llm-api",
    "status": "busy",
    "queue_depth": 2,
    "total_jobs_done": 47,
    "avg_job_sec": 8,
    "running_job_id": "job-abc123"
  }
]`}
      example={`# Find idle llm-api nodes
curl -k "https://rv-host:9002/v1/metrics?service=llm-api&status=idle"

# Specific nodes
curl -k "https://rv-host:9002/v1/metrics?node_id=P:0x8fF...,P:0xBAD..."

# Legacy alias (kept for back-compat): /v1/nodes/metrics`}
    >
      <TryIt>
        <div className="api-try-row"><label>service</label><input type="text" id="try-rv-metrics-service" placeholder="llm-api / sd-api (optional)" /></div>
        <div className="api-try-row"><label>status</label><input type="text" id="try-rv-metrics-status" placeholder="idle / busy (optional)" /></div>
        <div className="api-try-row"><label>gpu</label><input type="text" id="try-rv-metrics-gpu" placeholder="4070 (optional, cross-ref)" /></div>
        <div className="api-try-row"><label>model</label><input type="text" id="try-rv-metrics-model" placeholder="Qwen2.5-7B (optional, cross-ref)" /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const qs = new URLSearchParams();
            const svc = document.getElementById("try-rv-metrics-service")?.value; if (svc) qs.set("service", svc);
            const status = document.getElementById("try-rv-metrics-status")?.value; if (status) qs.set("status", status);
            const gpu = document.getElementById("try-rv-metrics-gpu")?.value; if (gpu) qs.set("gpu", gpu);
            const model = document.getElementById("try-rv-metrics-model")?.value; if (model) qs.set("model", model);
            const q = qs.toString();
            tryFetch("GET", "/v1/metrics" + (q ? "?" + q : ""), null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>

    <ApiCard method="GET" path="/v1/metrics?node_id=P:0x..." title="Metrics by Node ID"
      desc={<>Fetch live metrics for one or more specific nodes. When <code>node_id</code> is present, all other filters are ignored.</>}
      params={[
        { name: "node_id", type: "string", required: true, desc: "Node ID(s), comma-separated" },
      ]}
      response={`[
  { "node_id": "P:0x8fF...", "service": "sd-api", "status": "idle", "queue_depth": 0, "total_jobs_done": 150 }
]`}
      example={`curl -k "https://rv-host:9002/v1/metrics?node_id=P:0x8fF81256F3866fe..."`}
    >
      <TryIt>
        <div className="api-try-row"><label>node_id <span className="req">*</span></label><input type="text" id="try-rv-metrics-byid" placeholder="P:0x8fF..." /></div>
        <div className="api-try-actions">
          <button className="api-try-send" onClick={e => {
            const el = e.target.closest(".api-try-panel").querySelector(".api-try-response");
            const nid = document.getElementById("try-rv-metrics-byid")?.value;
            if (!nid) { el.style.display = "block"; el.textContent = "Enter node_id"; return; }
            tryFetch("GET", "/v1/metrics?node_id=" + encodeURIComponent(nid), null, el);
          }}>Send Request</button>
        </div>
        <pre className="api-try-response api-try-response-hidden"></pre>
      </TryIt>
    </ApiCard>
  </>);
}

// ═══════ 카테고리 정의 ═══════
const CATEGORIES = [
  { key: "image", label: "Image Generation", items: ["Health / Status", "Queue Stats", "Models", "txt2img (Async)", "img2img / inpaint (Async)", "Job Tracking", "Result Download"] },
  { key: "llm", label: "LLM", items: ["Health Check", "Queue Stats", "Models", "Chat Completion (Sync)", "Text Completion (Sync)", "Embeddings (Async)", "Chat Completion (Async)", "Job Status (Poll)", "Result Download"] },
  { key: "vllm", label: "vLLM", items: ["Health Check", "Queue Stats", "Models", "Chat Completion (Sync)", "Text Completion (Sync)", "Embeddings (Async)", "Chat Completion (Async)", "Job Status (Poll)", "Result Download"] },
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

// ═══════ Main Component ═══════
export default function ApiReference() {
  const [nodes, setNodes] = useState([]);
  const [selectedNode, setSelectedNode] = useState("");
  const [activeCategory, setActiveCategory] = useState("image");
  const [collapsed, setCollapsed] = useState({});
  const [panelOpen, setPanelOpen] = useState(true);
  const contentRef = useRef(null);

  // Static info from /v1/nodes (id, addr, hardware, services). One-shot —
  // these don't change at runtime so polling is wasteful.
  // Live status (alive/stale/offline) lives in /v1/metrics and is polled
  // separately below. Splitting the two reflects the RV's deliberate
  // separation of static and dynamic data.
  useEffect(() => {
    fetch("/v1/nodes").then(r => r.json()).then(d => {
      const list = Array.isArray(d) ? d : [];
      setNodes(list);
      if (list.length > 0) {
        // Pick first node — actual online status will get patched in
        // by the metrics polling effect below.
        setSelectedNode(prev => prev || list[0].id);
      }
    }).catch(() => {});
  }, []);

  // Poll /v1/metrics every 5s for live conn_status. Build a per-node map
  // keyed on node_id (using the node-level summary row where service=="").
  // Side panel reads this map for the "Node is offline" warning so it
  // reflects current liveness instead of static file data.
  const [metricsByNode, setMetricsByNode] = useState({});
  useEffect(() => {
    let cancelled = false;
    const refresh = async () => {
      try {
        const r = await fetch("/v1/metrics");
        const d = await r.json();
        if (cancelled) return;
        const rows = Array.isArray(d) ? d : [];
        const byID = {};
        for (const row of rows) {
          if (!row.node_id) continue;
          // Prefer the node-level summary (service=="") for liveness.
          if (!byID[row.node_id] || row.service === "") {
            byID[row.node_id] = row;
          }
        }
        setMetricsByNode(byID);
      } catch {}
    };
    refresh();
    const t = setInterval(refresh, 5000);
    return () => { cancelled = true; clearInterval(t); };
  }, []);

  const selectedNodeObj = nodes.find(n => n.id === selectedNode);
  // Live status comes from /v1/metrics (conn_status: alive/stale/offline).
  // Static /v1/nodes does not carry this field — it would be misleading
  // since liveness changes at heartbeat cadence (~5s).
  const metricsRow = metricsByNode[selectedNode];
  const nodeStatus = metricsRow?.conn_status || (metricsRow?.online ? "alive" : "offline");
  const nodeServices = (selectedNodeObj?.services || []).map(s => s.name || s.service).filter(Boolean);
  const availableApis = nodeServices.length > 0
    ? nodeServices.map(s => s === "sd-api" ? "Image Generation" : s === "llm-api" ? "LLM" : s).join(", ")
    : null;

  const tryFetch = useCallback(async (method, path, body, responseEl) => {
    if (!responseEl) return;
    responseEl.style.display = "block";
    responseEl.textContent = "Loading...";
    try {
      const url = path.replace("{nodeId}", encodeURIComponent(selectedNode));
      const authHeaders = getTryItAuth(responseEl);
      const opts = { method, headers: { ...authHeaders } };
      if (body) { opts.headers["Content-Type"] = "application/json"; opts.body = JSON.stringify(body); }
      const resp = await fetch(url, opts);
      const data = await resp.json();
      responseEl.textContent = JSON.stringify(data, null, 2);
    } catch (e) { responseEl.textContent = "Error: " + e.message; }
  }, [selectedNode]);

  const nodeLabel = (n) => n.id;

  const toggleCollapse = (key) => setCollapsed(prev => ({ ...prev, [key]: !prev[key] }));

  // Category switch from sidebar
  useEffect(() => {
    window.__apiSetCategory = (cat) => {
      setActiveCategory(cat);
      if (contentRef.current) contentRef.current.scrollTop = 0;
    };
    return () => { delete window.__apiSetCategory; };
  }, []);

  return (
    <div className="page">
      <div className="page-header">
        <h1>API Reference</h1>
      </div>
      <div className="api-docs">
        <div className="api-main">
        <div className="api-content" ref={contentRef}>
          {activeCategory === "image" && <ImageGenPage tryFetch={tryFetch} selectedNode={selectedNode} />}
          {activeCategory === "llm" && <LLMPage tryFetch={tryFetch} selectedNode={selectedNode} />}
          {activeCategory === "vllm" && <VLLMPage tryFetch={tryFetch} selectedNode={selectedNode} />}
          {activeCategory === "pipeline" && <PipelinePage tryFetch={tryFetch} selectedNode={selectedNode} />}
          {activeCategory === "node" && <NodePage tryFetch={tryFetch} selectedNode={selectedNode} />}
          {activeCategory === "admin" && <AdminPage tryFetch={tryFetch} selectedNode={selectedNode} />}
          {activeCategory === "broker-admin" && <BrokerAdminPage tryFetch={tryFetch} />}
          {activeCategory === "gate" && <GatePage tryFetch={tryFetch} />}
          {activeCategory === "rendezvous" && <RendezvousPage tryFetch={tryFetch} />}
          {activeCategory === "voice" && (
            <div className="api-placeholder">
              <h2>Voice API</h2>
              <p>Coming soon</p>
            </div>
          )}
        </div>
        {/* Panel toggle button */}
        <button
          className={`api-panel-toggle${panelOpen ? " open" : ""}`}
          onClick={() => setPanelOpen(v => !v)}
          title={panelOpen ? "Hide panel" : "Show panel"}
        >
          {panelOpen ? "▶" : "◀"}
        </button>

        {/* Side panel */}
        {panelOpen && (
          <div className="api-float-panel">
            <div className="api-float-base">
              Base URL: <code>{window.location.origin}</code>
            </div>
            <div className="api-float-node">
              <div className="api-float-node-row1">
                <label>Node:</label>
                {nodeStatus && <span className="api-node-status">● {nodeStatus}</span>}
                {selectedNodeObj?.addr && <span className="api-node-addr">{selectedNodeObj.addr}</span>}
              </div>
              <div className="api-float-node-row2">
                <Dropdown
                  value={selectedNode}
                  options={nodes.map(n => ({ value: n.id, label: nodeLabel(n) }))}
                  onChange={v => {
                    setSelectedNode(v);
                  }}
                  placeholder="Select node"
                  searchable
                />
              </div>
            </div>
            {nodeStatus === "offline" ? (
              <div className="api-float-warning"><span>⚠</span> Node is offline</div>
            ) : availableApis ? (
              <div className="api-float-services">
                <span style={{ color: "var(--text-muted)", fontSize: "0.75rem" }}>Available:</span>{" "}
                <span style={{ fontSize: "0.75rem" }}>{availableApis}</span>
              </div>
            ) : (
              <div className="api-float-warning"><span>⚠</span> No services available on this node</div>
            )}
          </div>
        )}
      </div>
      </div>
    </div>
  );
}
