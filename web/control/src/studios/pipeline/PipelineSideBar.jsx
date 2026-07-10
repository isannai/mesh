import React, { useState } from "react";

const paletteItems = [
  {
    category: "AI Services",
    items: [
      { label: "SD", type: "sdNode", data: { label: "SD", service: "sd-api", waitMode: "async", endpoint: "/v1/images/generations", method: "POST", outputType: "image", params: { prompt: "" }, options: { steps: 20, width: 512, height: 512, cfg_scale: 7.0 } } },
      { label: "LLM", type: "llmNode", data: { label: "LLM", service: "llm-api", waitMode: "sync", endpoint: "/v1/chat/completions", method: "POST", outputType: "json", params: {}, options: {} } },
      { label: "TTS", type: "ttsNode", disabled: true, data: { label: "TTS", service: "tts-api", endpoint: "/v1/audio/speech", method: "POST", outputType: "audio", params: {}, options: {} } },
      { label: "STT", type: "sttNode", disabled: true, data: { label: "STT", service: "whisper", endpoint: "/v1/audio/transcriptions", method: "POST", outputType: "text", params: {}, options: {} } },
    ],
  },
  {
    category: "Transform",
    items: [
      { label: "Extract", type: "transformNode", data: { label: "Extract", transform: "extract", inputType: "json", outputType: "text", params: { path: "" } } },
      { label: "Template", type: "transformNode", data: { label: "Template", transform: "template", inputType: "text", outputType: "text", params: { template: "" } } },
      { label: "Regex", type: "transformNode", data: { label: "Regex", transform: "regex", inputType: "text", outputType: "text", params: { pattern: "", replace: "" } } },
      { label: "JSON Merge", type: "transformNode", data: { label: "Merge", transform: "json_merge", inputType: "json", outputType: "json", params: {} } },
      { label: "Poller", type: "pollerNode", data: { label: "Poller" } },
    ],
  },
  {
    category: "Options",
    items: [
      { label: "SD Options", type: "optionsNode", data: { label: "SD Options", service: "sd-api", options: { negative_prompt: "", steps: 20, width: 512, height: 512, cfg_scale: 7.0, sampler: "euler_a", seed: -1 } } },
      { label: "LLM Options", type: "optionsNode", data: { label: "LLM Options", service: "llm-api", options: { temperature: 0.7, max_tokens: 2048, top_p: 0.9, top_k: 40, repeat_penalty: 1.1, frequency_penalty: 0, presence_penalty: 0, seed: -1, stop: "" } } },
      { label: "TTS Options", type: "optionsNode", disabled: true, data: { label: "TTS Options", service: "tts-api", options: { voice: "default", speed: 1.0 } } },
      { label: "STT Options", type: "optionsNode", disabled: true, data: { label: "STT Options", service: "whisper", options: { language: "auto" } } },
    ],
  },
  {
    category: "Nodes",
    items: [
      { label: "Fixed Node", type: "nodeSelectorNode", data: { label: "Fixed Node", strategy: "fixed", nodeId: "", tags: "" } },
      { label: "Node Finder", type: "nodeSelectorNode", data: { label: "Node Finder", strategy: "first_online", service: "", model: "", gpu: "" } },
    ],
  },
  {
    category: "Inputs",
    items: [
      { label: "Text Input", type: "inputNode", data: { label: "Text Input", outputType: "text", params: { value: "" } } },
      { label: "Image Input", type: "inputNode", data: { label: "Image Input", outputType: "image", params: { value: "" } } },
      { label: "Mask Input", type: "inputNode", data: { label: "Mask Input", outputType: "image", params: { value: "" } } },
      { label: "Chat Input", type: "chatInputNode", data: { label: "Chat Input", outputType: "json", messages: [{ role: "system", content: "" }, { role: "user", content: "" }] } },
    ],
  },
  {
    category: "Outputs",
    items: [
      { label: "Image Viewer", type: "outputNode", data: { label: "Image Viewer", viewer: "image", inputType: "image" } },
      { label: "Text Viewer", type: "outputNode", data: { label: "Text Viewer", viewer: "text", inputType: "text" } },
      { label: "Audio Viewer", type: "outputNode", disabled: true, data: { label: "Audio Viewer", viewer: "audio", inputType: "audio" } },
    ],
  },
];

const typeColors = {
  llmNode: "#58a6ff",
  sdNode: "#58a6ff",
  ttsNode: "#58a6ff",
  sttNode: "#58a6ff",
  transformNode: "#f0883e",
  inputNode: "#3fb950",
  outputNode: "#f85149",
  nodeSelectorNode: "#bc8cff",
  optionsNode: "#d29922",
  progressNode: "#58a6ff",
  pollerNode: "#f0883e",
  chatInputNode: "#3fb950",
};

export default function PipelineSideBar() {
  const [collapsed, setCollapsed] = useState({});

  const toggle = (cat) => setCollapsed(c => ({ ...c, [cat]: !c[cat] }));

  const onDragStart = (event, item) => {
    event.dataTransfer.setData(
      "application/pipeline-node",
      JSON.stringify({ type: item.type, data: item.data })
    );
    event.dataTransfer.effectAllowed = "move";
  };

  return (
    <>
      {paletteItems.map(group => (
        <React.Fragment key={group.category}>
          <div className="sb-section-title" onClick={() => toggle(group.category)}>
            <span className={`arrow ${collapsed[group.category] ? "collapsed" : ""}`}>&#9660;</span>
            {group.category}
          </div>
          {!collapsed[group.category] && group.items.map(item => (
            <div
              key={item.label}
              className={`sb-item ${item.disabled ? "disabled" : ""}`}
              draggable={!item.disabled}
              onDragStart={(e) => item.disabled ? e.preventDefault() : onDragStart(e, item)}
              title={item.disabled ? "Coming soon" : item.label}
            >
              <span className={`sb-item-dot ${item.disabled ? "disabled" : ""}`} style={!item.disabled ? { background: typeColors[item.type] || "var(--text-muted)" } : undefined} />
              {item.label}
            </div>
          ))}
        </React.Fragment>
      ))}
    </>
  );
}
