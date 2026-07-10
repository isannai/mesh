import React from "react";
import usePipelineStore from "./store";
import Dropdown from "@components/Dropdown/Dropdown";
import { useTranslation } from "@i18n";
import "./PipelineInspector.scss";

// Service-specific option definitions
const SERVICE_OPTIONS = {
  "sd-api": [
    { key: "negative_prompt", label: "Negative Prompt", type: "text", default: "" },
    { key: "steps", label: "Steps", type: "number", default: 20 },
    { key: "width", label: "Width", type: "number", default: 512 },
    { key: "height", label: "Height", type: "number", default: 512 },
    { key: "cfg_scale", label: "CFG Scale", type: "number", default: 7.0 },
    { key: "strength", label: "Strength", type: "number", default: 0.75 },
    { key: "seed", label: "Seed", type: "number", default: -1 },
    { key: "sampler", label: "Sampler", type: "select", default: "euler_a", options: ["euler_a", "euler", "dpm++_2m", "dpm++_sde", "ddim", "lms"] },
  ],
  "llm-api": [
    { key: "temperature", label: "Temperature", type: "number", default: 0.7 },
    { key: "max_tokens", label: "Max Tokens", type: "number", default: 2048 },
    { key: "top_p", label: "Top P", type: "number", default: 0.9 },
    { key: "top_k", label: "Top K", type: "number", default: 40 },
    { key: "repeat_penalty", label: "Repeat Penalty", type: "number", default: 1.1 },
    { key: "frequency_penalty", label: "Frequency Penalty", type: "number", default: 0 },
    { key: "presence_penalty", label: "Presence Penalty", type: "number", default: 0 },
    { key: "seed", label: "Seed", type: "number", default: -1 },
    { key: "stop", label: "Stop Tokens", type: "text", default: "" },
  ],
  "whisper": [
    { key: "language", label: "Language", type: "text", default: "auto" },
  ],
  "tts-api": [
    { key: "voice", label: "Voice", type: "text", default: "default" },
    { key: "speed", label: "Speed", type: "number", default: 1.0 },
  ],
};

export default function PipelineInspector() {
  const { t } = useTranslation();
  const selectedNodeId = usePipelineStore(s => s.selectedNodeId);
  const selectedAnchor = usePipelineStore(s => s.selectedAnchor);
  const nodes = usePipelineStore(s => s.nodes);
  const edges = usePipelineStore(s => s.edges);
  const updateNodeData = usePipelineStore(s => s.updateNodeData);
  const deleteNode = usePipelineStore(s => s.deleteNode);
  const setSelectedAnchor = usePipelineStore(s => s.setSelectedAnchor);

  const node = nodes.find(n => n.id === selectedNodeId);

  if (!node) return (
    <div className="inspector-empty">
      Select a node to view properties
    </div>
  );

  const d = node.data;

  // === Chat Input node ===
  if (node.type === "chatInputNode") {
    const messages = d.messages || [];
    const roles = ["system", "user", "assistant", "tool"];

    const updateMessages = (newMessages) => updateNodeData(node.id, { messages: newMessages });

    const addMessage = () => updateMessages([...messages, { role: "user", content: "" }]);

    const removeMessage = (idx) => updateMessages(messages.filter((_, i) => i !== idx));

    const updateMessage = (idx, field, value) => {
      const updated = messages.map((m, i) => i === idx ? { ...m, [field]: value } : m);
      updateMessages(updated);
    };

    return (
      <>
        <div className="inspector-header">
          <div className="header-row">
            <span className="header-dot dot-chat" />
            <span className="header-title">{d.label}</span>
            <span className="header-id">{node.id}</span>
          </div>
        </div>

        <Section title={t("pipeline.section_general")}>
          <Field label="Type" value={node.type.replace("Node", "")} />
        </Section>

        <Section title={t("pipeline.section_messages")}>
          {messages.map((msg, idx) => (
            <div key={idx} className="message-item">
              <div className="message-header">
                <Dropdown
                  value={msg.role}
                  options={roles.map(r => ({ value: r, label: r }))}
                  onChange={(val) => updateMessage(idx, "role", val)}
                />
                <button
                  className="btn-remove-msg"
                  onClick={() => removeMessage(idx)}
                  title={t("pipeline.remove")}
                >&#10005;</button>
              </div>
              <textarea
                className="inspector-input textarea message-textarea"
                value={msg.content || ""}
                onChange={(e) => updateMessage(idx, "content", e.target.value)}
                placeholder={msg.role === "system" ? "System prompt..." : msg.role === "user" ? "User message..." : "..."}
                rows={2}
              />
            </div>
          ))}
          <button className="btn-add-message" onClick={addMessage}>+ Add Message</button>
        </Section>

        <Section title="">
          <button className="btn-delete-node" onClick={() => deleteNode(node.id)}>{t("pipeline.delete_node")}</button>
        </Section>
      </>
    );
  }

  // === Node Selector node ===
  if (node.type === "nodeSelectorNode") {
    const isFixed = d.strategy === "fixed";
    const networkNodes = usePipelineStore.getState().networkNodes || [];

    const uniqueServices = [...new Set(networkNodes.flatMap(n => (n.services || []).map(s => s.name || s.service).filter(Boolean)))];
    const uniqueModels = [...new Set(networkNodes.flatMap(n => (n.services || []).map(s => s.model).filter(Boolean)))];
    const uniqueGpus = [...new Set(networkNodes.flatMap(n => (n.hardware?.gpus || []).map(g => g.name).filter(Boolean)))];

    const filteredModels = d.service
      ? [...new Set(networkNodes.flatMap(n => (n.services || []).filter(s => (s.name || s.service) === d.service).map(s => s.model).filter(Boolean)))]
      : uniqueModels;

    return (
      <>
        <div className="inspector-header">
          <div className="header-row">
            <span className="header-dot dot-selector" />
            <span className="header-title">{d.label}</span>
            <span className="header-id">{node.id}</span>
          </div>
        </div>

        <Section title={t("pipeline.section_general")}>
          <Field label="Type" value={node.type.replace("Node", "")} />
          <Field label="Strategy" value={d.strategy || "—"} />
        </Section>

        {isFixed ? (
          <Section title={t("pipeline.section_node_id")}>
            <Dropdown
              value={d.nodeId || ""}
              options={[
                { value: "", label: "-- Select Node --" },
                ...networkNodes.map(n => ({
                  value: n.id,
                  label: `${n.id.length > 20 ? n.id.slice(0, 10) + "..." + n.id.slice(-4) : n.id} ${n.online ? "●" : "○"}`,
                })),
              ]}
              onChange={(val) => updateNodeData(node.id, { nodeId: val })}
              searchable
            />
          </Section>
        ) : (
          <>
            <Section title="Service">
              <Dropdown
                value={d.service || ""}
                options={[
                  { value: "", label: "Any" },
                  ...uniqueServices.map(s => ({ value: s, label: s })),
                ]}
                onChange={(val) => updateNodeData(node.id, { service: val, model: "" })}
                searchable
              />
            </Section>
            <Section title="Model">
              <Dropdown
                value={d.model || ""}
                options={[
                  { value: "", label: "Any" },
                  ...filteredModels.map(m => ({ value: m, label: m })),
                ]}
                onChange={(val) => updateNodeData(node.id, { model: val })}
                searchable
              />
            </Section>
            <Section title="GPU">
              <Dropdown
                value={d.gpu || ""}
                options={[
                  { value: "", label: "Any" },
                  ...uniqueGpus.map(g => ({ value: g, label: g })),
                ]}
                onChange={(val) => updateNodeData(node.id, { gpu: val })}
                searchable
              />
            </Section>
          </>
        )}

        <Section title="Auth">
          <div className="auth-hint">Empty = use MetaMask session</div>
          <div className="inspector-field">
            <div className="field-label">Signature</div>
            <input
              type="text"
              className="inspector-input"
              value={d.authSignature || ""}
              onChange={(e) => updateNodeData(node.id, { authSignature: e.target.value })}
              placeholder="ISANN signature"
            />
          </div>
          <div className="inspector-field">
            <div className="field-label">Message</div>
            <input
              type="text"
              className="inspector-input"
              value={d.authMessage || ""}
              onChange={(e) => updateNodeData(node.id, { authMessage: e.target.value })}
              placeholder="role:target:service:nonce:exp:nodes"
            />
          </div>
        </Section>

        <Section title="">
          <button className="btn-delete-node" onClick={() => deleteNode(node.id)}>{t("pipeline.delete_node")}</button>
        </Section>
      </>
    );
  }

  // === Poller node ===
  if (node.type === "pollerNode") {
    const cleanup = d.cleanup !== false; // default true
    return (
      <>
        <div className="inspector-header">
          <div className="header-row">
            <span className="header-dot dot-options" />
            <span className="header-title">{d.label || "Poller"}</span>
          </div>
        </div>

        <Section title="Options">
          <label className="field-row" title="Delete the job from the provider queue after the result is fetched">
            <span className="field-label-text">Auto-cleanup</span>
            <input
              type="checkbox"
              checked={cleanup}
              onChange={(e) => updateNodeData(node.id, { cleanup: e.target.checked })}
            />
          </label>
        </Section>

        <Section title="">
          <button className="btn-delete-node" onClick={() => deleteNode(node.id)}>{t("pipeline.delete_node")}</button>
        </Section>
      </>
    );
  }

  // === Options node ===
  if (node.type === "optionsNode") {
    const optionDefs = SERVICE_OPTIONS[d.service] || [];
    const opts = d.options || {};
    return (
      <>
        <div className="inspector-header">
          <div className="header-row">
            <span className="header-dot dot-options" />
            <span className="header-title">{d.label}</span>
            <span className="header-id">{node.id}</span>
          </div>
        </div>
        <Section title={t("pipeline.section_general")}>
          <Field label="Type" value={node.type.replace("Node", "")} />
          {d.service && <Field label="Service" value={d.service} accent />}
        </Section>
        <Section title="Parameters">
          {optionDefs.map(opt => (
            <div key={opt.key} className="inspector-field mb-8">
              <div className="field-label">{opt.label}</div>
              {opt.type === "select" ? (
                <Dropdown
                  value={opts[opt.key] ?? opt.default}
                  options={opt.options.map(o => ({ value: o, label: o }))}
                  onChange={(val) => updateNodeData(node.id, { options: { ...opts, [opt.key]: val } })}
                />
              ) : opt.type === "text" ? (
                <textarea
                  className="inspector-input textarea"
                  value={opts[opt.key] ?? opt.default}
                  rows={2}
                  onChange={(e) => updateNodeData(node.id, { options: { ...opts, [opt.key]: e.target.value } })}
                />
              ) : (
                <input
                  type="number"
                  className="inspector-input"
                  value={opts[opt.key] ?? opt.default}
                  onChange={(e) => updateNodeData(node.id, { options: { ...opts, [opt.key]: Number(e.target.value) } })}
                />
              )}
            </div>
          ))}
        </Section>
        <Section title="">
          <button className="btn-delete-node" onClick={() => deleteNode(node.id)}>{t("pipeline.delete_node")}</button>
        </Section>
      </>
    );
  }

  // === Options mode (top anchor clicked on AI node) ===
  if (selectedAnchor === "options" && d.service) {
    const optionDefs = SERVICE_OPTIONS[d.service] || [];
    const opts = d.options || {};

    return (
      <>
        <div className="inspector-header">
          <div className="header-row">
            <span className="header-dot dot-anchor" />
            <span className="header-title">{d.label} — Options</span>
            <span className="header-id">{node.id}</span>
          </div>
        </div>

        <Section title={`${d.service} Options`}>
          {optionDefs.length === 0 ? (
            <div className="no-options-msg">No options available</div>
          ) : (
            optionDefs.map(opt => (
              <div key={opt.key} className="inspector-field mb-8">
                <div className="field-label">{opt.label}</div>
                {opt.type === "select" ? (
                  <select
                    className="inspector-select"
                    value={opts[opt.key] ?? opt.default}
                    onChange={(e) => updateNodeData(node.id, { options: { ...opts, [opt.key]: e.target.value } })}
                  >
                    {opt.options.map(o => <option key={o} value={o}>{o}</option>)}
                  </select>
                ) : opt.type === "text" ? (
                  <textarea
                    className="inspector-input textarea"
                    value={opts[opt.key] ?? opt.default}
                    rows={2}
                    onChange={(e) => updateNodeData(node.id, { options: { ...opts, [opt.key]: e.target.value } })}
                  />
                ) : (
                  <input
                    type="number"
                    className="inspector-input"
                    value={opts[opt.key] ?? opt.default}
                    onChange={(e) => updateNodeData(node.id, { options: { ...opts, [opt.key]: Number(e.target.value) } })}
                  />
                )}
              </div>
            ))
          )}
        </Section>
      </>
    );
  }

  // === General mode (node body clicked) ===
  return (
    <>
      <div className="inspector-header">
        <div className="header-row">
          <span className="header-title">{d.label}</span>
          <span className="header-id">{node.id}</span>
        </div>
      </div>

      <Section title="General">
        <Field label="Type" value={node.type.replace("Node", "")} />
        {d.service !== undefined && (node.type === "llmNode" || node.type === "sdNode" || node.type === "optionsNode") ? (
          <div className="inspector-field">
            <div className="field-label">Service</div>
            <input
              type="text"
              className="inspector-input"
              value={d.service || ""}
              onChange={(e) => updateNodeData(node.id, { service: e.target.value })}
              placeholder="e.g. llm-api, vllm-api, sd-api"
            />
          </div>
        ) : d.service ? (
          <Field label="Service" value={d.service} accent />
        ) : null}
        {d.transform && <Field label="Transform" value={d.transform} />}
        {d.service !== "sd-api" && d.endpoint && <Field label="Endpoint" value={d.endpoint} mono />}
        {d.service === "sd-api" && (() => {
          const hasImage = edges.some(e => e.target === node.id && e.targetHandle === "image");
          const hasMask = edges.some(e => e.target === node.id && e.targetHandle === "mask");
          const mode = hasMask ? "inpaint" : hasImage ? "img2img" : "txt2img";
          const modeColor = { txt2img: "#3fb950", img2img: "#58a6ff", inpaint: "#bc8cff" };
          return <Field label="SD Mode" value={mode} style={{ color: modeColor[mode] }} />;
        })()}
        {d.viewer && <Field label="Viewer" value={d.viewer} />}
        {(node.type === "llmNode" || node.type === "sdNode") && (
          <div className="inspector-field">
            <div className="field-label">Execution Mode</div>
            <Dropdown
              value={d.waitMode ?? "sync"}
              options={[
                { value: "sync", label: "Sync (wait=true)" },
                { value: "async", label: "Async (job polling)" },
              ]}
              onChange={(val) => updateNodeData(node.id, { waitMode: val })}
            />
          </div>
        )}
      </Section>

      {d.params && Object.keys(d.params).length > 0 && d.service !== "sd-api" && (
        <Section title="Parameters">
          {Object.entries(d.params).map(([key, val]) => {
            const display = typeof val === "object" ? JSON.stringify(val, null, 1) : String(val);
            if (typeof val === "string") {
              return (
                <div key={key} className="inspector-field">
                  <div className="field-label">{key}</div>
                  <textarea
                    className="inspector-input textarea"
                    value={val}
                    rows={2}
                    onChange={(e) => updateNodeData(node.id, { params: { ...d.params, [key]: e.target.value } })}
                  />
                </div>
              );
            }
            if (typeof val === "number") {
              return (
                <div key={key} className="inspector-field">
                  <div className="field-label">{key}</div>
                  <input
                    type="number"
                    className="inspector-input"
                    value={val}
                    onChange={(e) => updateNodeData(node.id, { params: { ...d.params, [key]: Number(e.target.value) } })}
                  />
                </div>
              );
            }
            return (
              <div key={key} className="inspector-field">
                <div className="field-label">{key}</div>
                <div className="params-readonly">{display}</div>
              </div>
            );
          })}
        </Section>
      )}


      {node.type === "outputNode" && (
        <Section title="Viewer Size">
          <div className="viewer-size-row">
            <div className="viewer-size-cell">
              <div className="field-label">Width</div>
              <input type="number" className="inspector-input" value={d.viewerWidth || 180} onChange={(e) => updateNodeData(node.id, { viewerWidth: Number(e.target.value) })} />
            </div>
            <div className="viewer-size-cell">
              <div className="field-label">Height</div>
              <input type="number" className="inspector-input" value={d.viewerHeight || 100} onChange={(e) => updateNodeData(node.id, { viewerHeight: Number(e.target.value) })} />
            </div>
          </div>
        </Section>
      )}

      <Section title="Ports">
        {d.outputType && <Port direction="out" type={d.outputType} />}
        {d.inputType && <Port direction="in" type={d.inputType} />}
      </Section>

      <Section title="">
        <button className="btn-delete-node" onClick={() => deleteNode(node.id)}>Delete Node</button>
      </Section>
    </>
  );
}

function Section({ title, children }) {
  return (
    <div className="section">
      {title && <div className="section-title">{title}</div>}
      {children}
    </div>
  );
}

function Field({ label, value, mono, accent, style }) {
  const classes = ["field-value"];
  if (mono) classes.push("mono");
  if (accent) classes.push("accent");
  return (
    <div className="field-row">
      <span className="field-label-text">{label}</span>
      <span className={classes.join(" ")} style={style}>{value || "—"}</span>
    </div>
  );
}

const TYPE_COLORS = { text: "#3fb950", image: "#58a6ff", audio: "#bc8cff", json: "#d29922", file: "#8b949e", options: "#bc8cff" };

function Port({ direction, type, label }) {
  const color = TYPE_COLORS[type] || "#6e7681";
  return (
    <div className="port-row">
      <span className="port-dot" style={{ background: color }} />
      <span>{direction}</span>
      <span className="port-separator">&middot;</span>
      <span style={{ color }}>{type}</span>
      {label && <span className="port-label">{label}</span>}
    </div>
  );
}
