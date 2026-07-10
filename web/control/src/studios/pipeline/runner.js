// Pipeline runner — step-by-step execution in browser (Debug Run)
import { runTransform, template } from "./transforms";
import { getAuthHeaders } from "@utils/wallet";
import { getJobResult } from "@api/sd";
import { wrapSdCppExtraArgs } from "@utils/sdcpp";

/**
 * Topological sort: order nodes by edge dependencies.
 * Returns array of node IDs in execution order.
 */
function topoSort(nodes, edges) {
  const graph = {};
  const inDegree = {};
  for (const n of nodes) {
    graph[n.id] = [];
    inDegree[n.id] = 0;
  }
  for (const e of edges) {
    // Skip config edges (node selector, options) — they don't affect execution order
    if (e.targetHandle === "node" || e.targetHandle === "options") continue;
    if (graph[e.source]) {
      graph[e.source].push(e.target);
      inDegree[e.target] = (inDegree[e.target] || 0) + 1;
    }
  }

  const queue = [];
  for (const id in inDegree) {
    if (inDegree[id] === 0) queue.push(id);
  }

  const sorted = [];
  while (queue.length > 0) {
    const id = queue.shift();
    sorted.push(id);
    for (const next of (graph[id] || [])) {
      inDegree[next]--;
      if (inDegree[next] === 0) queue.push(next);
    }
  }
  return sorted;
}

/**
 * Resolve which node ID to use for an AI node.
 * Checks if a NodeSelector is connected via the "node" handle.
 */
function resolveNodeSelector(aiNode, nodes, edges, networkNodes) {
  // Check if a NodeSelector is connected to the "node" handle
  const nodeEdge = edges.find(e => e.target === aiNode.id && e.targetHandle === "node");
  let selectorData = null;
  let nodeId = null;

  if (nodeEdge) {
    const selectorNode = nodes.find(n => n.id === nodeEdge.source);
    if (selectorNode?.data) {
      selectorData = selectorNode.data;
      const sd = selectorData;
      if (sd.strategy === "fixed" && sd.nodeId) nodeId = sd.nodeId;
      else if (sd.strategy === "first_online") {
        const match = (networkNodes || []).find(n => {
          if (!n.online) return false;
          if (sd.service && !(n.services || []).some(s => (s.name || s.service) === sd.service)) return false;
          if (sd.model && !(n.services || []).some(s => s.model === sd.model)) return false;
          if (sd.gpu && !(n.hardware?.gpus || []).some(g => g.name?.toLowerCase().includes(sd.gpu.toLowerCase()))) return false;
          return true;
        });
        if (match) nodeId = match.id;
      }
    }
  }

  // Fallback: use node's own nodeId
  if (!nodeId && aiNode.data.nodeId) nodeId = aiNode.data.nodeId;

  // Auto-select: first online node with matching service
  if (!nodeId) {
    const service = aiNode.data.service;
    if (service && networkNodes) {
      const match = networkNodes.find(n =>
        n.online && (n.services || []).some(s => (s.name || s.service) === service)
      );
      if (match) nodeId = match.id;
    }
  }

  // Auth from NodeSelector (if connected)
  const auth = (selectorData?.authSignature || selectorData?.authMessage)
    ? { signature: selectorData.authSignature, message: selectorData.authMessage }
    : null;

  return { nodeId, auth };
}

/**
 * Resolve options for an AI node.
 * Checks if an OptionsNode is connected via the "options" handle.
 */
function resolveOptions(aiNode, nodes, edges) {
  const optEdge = edges.find(e => e.target === aiNode.id && e.targetHandle === "options");
  let opts;
  if (optEdge) {
    const optNode = nodes.find(n => n.id === optEdge.source);
    opts = optNode?.data?.options || {};
  } else {
    opts = aiNode.data.options || {};
  }
  // Filter out empty strings and default-sentinel values that confuse the engine
  const filtered = {};
  for (const [k, v] of Object.entries(opts)) {
    if (v === "" || v === null || v === undefined) continue;
    if (k === "seed" && v === -1) continue; // -1 = random, omit
    filtered[k] = v;
  }
  return filtered;
}

/**
 * Substitute {{...}} templates in params.
 */
function substituteParams(params, stepResults) {
  if (!params) return params;
  const result = {};
  for (const [key, val] of Object.entries(params)) {
    if (typeof val === "string") {
      result[key] = template(val, val, stepResults);
    } else if (Array.isArray(val)) {
      result[key] = val.map(item => {
        if (typeof item === "object" && item !== null) {
          return substituteParams(item, stepResults);
        }
        if (typeof item === "string") return template(item, item, stepResults);
        return item;
      });
    } else if (typeof val === "object" && val !== null) {
      result[key] = substituteParams(val, stepResults);
    } else {
      result[key] = val;
    }
  }
  return result;
}

/**
 * Call a remote AI service via broker proxy.
 */
async function callService(nodeId, service, endpoint, method, body, customAuth, waitMode = "sync") {
  let url = `/node/${nodeId}/svc/${service}${endpoint}`;
  if (waitMode === "sync") {
    const sep = endpoint.includes("?") ? "&" : "?";
    url += sep + "wait=true";
  }
  const authHeaders = (customAuth?.signature && customAuth?.message)
    ? { Authorization: "ISANN " + customAuth.signature, "X-ISANN-Message": customAuth.message }
    : getAuthHeaders();
  const res = await fetch(url, {
    method: method || "POST",
    headers: {
      "Content-Type": "application/json",
      ...authHeaders,
    },
    body: method === "GET" ? undefined : JSON.stringify(body),
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`${service} returned ${res.status}: ${text}`);
  }
  return res.json();
}

/**
 * Run pipeline step-by-step (Debug Run).
 *
 * @param {Object} store - Zustand store (usePipelineStore)
 * @param {Function} onStepUpdate - callback(stepId, { status, progress, result, error, duration })
 * @returns {Object} final result
 */
export async function debugRunPipeline(store, onStepUpdate) {
  const { nodes, edges, networkNodes } = store;
  const order = topoSort(nodes, edges);
  const stepResults = {};

  onStepUpdate("__pipeline", { status: "running" });

  for (const nodeId of order) {
    const node = nodes.find(n => n.id === nodeId);
    if (!node) continue;

    // Skip progress node (deprecated)
    if (node.type === "progressNode") {
      continue;
    }

    const startTime = Date.now();
    console.log(`[pipeline] executing: ${nodeId} (${node.type})`, node.data);
    onStepUpdate(nodeId, { status: "running", progress: 0 });

    try {
      // Get input from connected source
      const inputEdge = edges.find(e => e.target === nodeId && e.targetHandle !== "node" && e.targetHandle !== "options");
      const inputData = inputEdge ? stepResults[inputEdge.source] : undefined;

      let result;

      switch (node.type) {
        case "inputNode": {
          result = node.data.params?.value || node.data.params?.prompt || "";
          break;
        }

        case "nodeSelectorNode": {
          result = {
            strategy: node.data.strategy,
            nodeId: node.data.nodeId || null,
            service: node.data.service || null,
            model: node.data.model || null,
            gpu: node.data.gpu || null,
          };
          break;
        }

        case "optionsNode": {
          result = node.data.options || {};
          break;
        }

        case "chatInputNode": {
          result = { messages: (node.data.messages || []).filter(m => m.content) };
          break;
        }

        case "llmNode":
        case "sdNode": {
          const { nodeId: resolvedNodeId, auth: selectorAuth } = resolveNodeSelector(node, nodes, edges, networkNodes);
          if (!resolvedNodeId) throw new Error("No node available for " + node.data.service);

          const options = resolveOptions(node, nodes, edges);
          const params = substituteParams(node.data.params || {}, stepResults);
          const body = { ...params, ...options };

          // input anchor — messages 배열 또는 텍스트 프롬프트
          const promptEdge = edges.find(e => e.target === nodeId && (e.targetHandle === "input" || !e.targetHandle));
          if (promptEdge && stepResults[promptEdge.source] != null) {
            const inputData = stepResults[promptEdge.source];
            if (inputData?.messages) {
              body.messages = inputData.messages;
            } else {
              body.prompt = typeof inputData === "string" ? inputData : JSON.stringify(inputData);
            }
          }

          // SD: 연결 상태에 따라 endpoint + image/mask 자동 결정
          let endpoint = node.data.endpoint;
          let finalBody = body;
          if (node.data.service === "sd-api") {
            const imageEdge = edges.find(e => e.target === nodeId && e.targetHandle === "image");
            const maskEdge = edges.find(e => e.target === nodeId && e.targetHandle === "mask");
            const imageData = imageEdge ? stepResults[imageEdge.source] : null;
            const maskData = maskEdge ? stepResults[maskEdge.source] : null;

            if (imageData) {
              endpoint = "/v1/images/edits"; // img2img or inpaint
              body.image = imageData;
              if (maskData) body.mask = maskData;
            } else {
              endpoint = "/v1/images/generations"; // txt2img
            }
            // sd.cpp wrap convention — top-level steps/cfg_scale/seed/
            // sample_method/negative_prompt/strength 는 무시되므로 prompt
            // 안 `<sd_cpp_extra_args>` 태그로 옮김. managed mode 의
            // engine-runner injectExtraArgs 와 동일한 처리지만, external
            // (sd-external) 으로 컨테이너에 직접 proxy 되는 경로에서도
            // 동작하도록 클라이언트가 직접 wrap.
            finalBody = wrapSdCppExtraArgs(body);
          }

          const waitMode = node.data.waitMode ?? "sync";
          onStepUpdate(nodeId, { status: "running", progress: 10, node: resolvedNodeId, request: { endpoint, nodeId: resolvedNodeId, body: finalBody } });
          result = await callService(resolvedNodeId, node.data.service, endpoint, node.data.method, finalBody, selectorAuth, waitMode);

          // AI 노드는 즉시 반환 — 비동기 작업은 Poller 노드가 처리
          if (result?.job_id) {
            result._nodeId = resolvedNodeId;
            result._service = node.data.service;
          }
          break;
        }

        case "transformNode": {
          result = runTransform(node.data.transform, inputData, node.data.params, stepResults);
          break;
        }

        case "pollerNode": {
          // inputData 에 job_id, _nodeId, _service 가 들어있어야 함
          if (!inputData?.job_id) throw new Error("Poller: no job_id received");
          const pollNodeId = inputData._nodeId;
          const pollService = inputData._service;
          if (!pollNodeId) throw new Error("Poller: no node ID");

          onStepUpdate(nodeId, { status: "running", progress: 0, message: "polling...", nodeType: "pollerNode" });
          await pollJob(pollNodeId, pollService, inputData.job_id, (p) => {
            const pct = Math.round(p * 100);
            onStepUpdate(nodeId, { status: "running", progress: pct, message: `running ${pct}%`, nodeType: "pollerNode" });
          });

          // poll/subscribe is metadata-only — fetch the final result separately.
          // node.data.cleanup (default true) → ?consume=true → provider evicts the job after fetch.
          const cleanup = node.data?.cleanup !== false;
          const fetched = await getJobResult(pollNodeId, inputData.job_id, pollService, { consume: cleanup });
          if (fetched.type === "image") result = fetched.url;
          else if (fetched.type === "json") result = fetched.data;
          else result = fetched.text;
          break;
        }

        case "outputNode": {
          result = inputData;
          break;
        }

        default:
          result = inputData;
      }

      const duration = Date.now() - startTime;
      stepResults[nodeId] = result;
      console.log(`[pipeline] done: ${nodeId}`, { duration, result: typeof result === "string" ? result.slice(0, 100) : result });
      onStepUpdate(nodeId, { status: "done", progress: 100, result, duration, inputData, nodeType: node.type });

    } catch (err) {
      const duration = Date.now() - startTime;
      onStepUpdate(nodeId, { status: "error", error: err.message, duration });
      onStepUpdate("__pipeline", { status: "error", error: `Step ${nodeId} failed: ${err.message}` });
      return { error: err.message, stepResults };
    }
  }

  onStepUpdate("__pipeline", { status: "done" });
  return { stepResults };
}

/**
 * Poll a job until done (fallback).
 */
async function pollJob(nodeId, service, jobId, onProgress) {
  // 첫 요청 전 1초 대기 (job 등록 시간 확보)
  await new Promise(r => setTimeout(r, 1000));

  for (let i = 0; i < 120; i++) {
    try {
      const res = await fetch(`/node/${nodeId}/svc/${service}/v1/jobs/poll/${jobId}`);
      if (res.ok) {
        const job = await res.json();
        console.log(`[pipeline] poll ${jobId}:`, job.status, job.progress);
        if (job.progress) onProgress(job.progress / 100);
        if (job.status === "done") return job;
        if (job.status === "failed") throw new Error(job.error || "Job failed");
      } else {
        console.log(`[pipeline] poll ${jobId}: ${res.status}`);
      }
    } catch (e) {
      if (i > 10) throw e;
    }
    // 점진적 대기: 처음 1초 → 나중에 3초
    await new Promise(r => setTimeout(r, i < 5 ? 1000 : i < 10 ? 2000 : 3000));
  }
  throw new Error("Job timed out");
}
