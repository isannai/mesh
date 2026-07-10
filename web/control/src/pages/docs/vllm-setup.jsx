import React from "react";
import { Link } from "react-router-dom";
import "./docs-guide.scss";

export default function VllmSetupGuide() {
  return (
    <div className="page docs-guide">
      <div className="page-header">
        <div className="docs-breadcrumb">
          <Link to="/docs">Docs</Link> <span>/</span> <span>vLLM Setup Guide</span>
        </div>
        <h2>vLLM Setup Guide</h2>
        <p className="docs-lede">
          Run vLLM in Docker on Linux (native) or Windows (via WSL2) and connect it to IANN as an
          external engine. Covers GTX 1650 (4 GB) through RTX 4090 (24 GB) and multi-GPU rigs.
        </p>
      </div>

      <div className="page-body docs-body">
        <Section title="1. When to use this guide">
          <ul>
            <li>Internal: developing / testing IANN's external engine adapter.</li>
            <li>Trusted Operator: plugging a personal GPU rig into IANN.</li>
            <li>Learning: understanding the LLM serving stack end-to-end.</li>
          </ul>
          <h4>vLLM vs llama.cpp</h4>
          <table className="docs-table">
            <thead>
              <tr><th>Aspect</th><th>llama.cpp</th><th>vLLM</th></tr>
            </thead>
            <tbody>
              <tr><td>Target VRAM</td><td>4–24 GB</td><td>24 GB+ (multi-GPU preferred)</td></tr>
              <tr><td>Quantization</td><td>GGUF (many formats)</td><td>AWQ, GPTQ</td></tr>
              <tr><td>Multi-GPU</td><td>No</td><td>Yes (tensor parallelism)</td></tr>
              <tr><td>Batch throughput</td><td>Low</td><td>Very high</td></tr>
              <tr><td>Setup complexity</td><td>Low</td><td>Medium</td></tr>
            </tbody>
          </table>
          <p className="note">
            Rule of thumb: 4 GB consumer GPU → llama.cpp. Multi-GPU rig → vLLM.
          </p>
        </Section>

        <Section title="2. Prerequisites">
          <ul>
            <li>NVIDIA GPU (Pascal or newer, CUDA compute 6.0+)</li>
            <li>At least 15 GB free disk (Docker image ~10 GB + model 1–5 GB)</li>
            <li>Up-to-date NVIDIA driver</li>
          </ul>
          <h4>OS-specific</h4>
          <ul>
            <li><strong>Linux</strong>: Ubuntu 20.04/22.04, Debian 11+, or any modern distro with systemd. Install Docker + NVIDIA Container Toolkit directly on the host.</li>
            <li><strong>Windows</strong>: Windows 10 21H2+ or Windows 11. Run everything inside WSL2 (Ubuntu 22.04). The Windows NVIDIA driver exposes the GPU to WSL transparently — don't install a separate Linux driver inside WSL.</li>
          </ul>
        </Section>

        <Section title="3. Step 1 — Host setup">
          <h4>Linux (native)</h4>
          <p>Skip to Step 2 (Docker). You already have a Linux shell.</p>

          <h4>Windows → WSL2</h4>
          <p>From an elevated PowerShell:</p>
          <pre>{`wsl --version
wsl --list --verbose`}</pre>
          <p>If WSL version is not 2, update:</p>
          <pre>{`wsl --update`}</pre>
          <p>Install Ubuntu:</p>
          <pre>{`wsl --install -d Ubuntu-22.04
wsl --set-default Ubuntu-22.04
wsl`}</pre>
          <p className="note">
            From here on, every command runs inside the Linux shell (WSL or native Linux — same commands).
          </p>
        </Section>

        <Section title="4. Step 2 — Verify GPU">
          <pre>{`nvidia-smi`}</pre>
          <p>You should see GPU name + VRAM.</p>
          <ul>
            <li><strong>Linux native</strong>: if no output, install the NVIDIA driver via your distro's package manager (e.g. <code>sudo apt install nvidia-driver-535</code>) and reboot.</li>
            <li><strong>WSL2</strong>: if no output, update the <em>Windows</em> NVIDIA driver (not the Linux one) and run <code>wsl --shutdown</code> in PowerShell, then re-enter WSL.</li>
          </ul>
          <p className="note">
            <strong>WSL users: do NOT install <code>nvidia-utils-*</code> / <code>nvidia-driver-*</code> via apt inside WSL.</strong>
            The Windows driver already exposes the GPU — adding Linux drivers on top causes conflicts.
          </p>
        </Section>

        <Section title="5. Step 3 — Docker">
          <p>Same command on Linux native and inside WSL2:</p>
          <pre>{`curl -fsSL https://get.docker.com | sudo sh
sudo usermod -aG docker $USER`}</pre>

          <h4>Linux native</h4>
          <p>Log out and back in (for the group change), then:</p>
          <pre>{`sudo systemctl enable --now docker
docker run hello-world`}</pre>

          <h4>WSL2</h4>
          <p>Exit WSL and restart it so the group change takes effect:</p>
          <pre>{`# From PowerShell
wsl --shutdown
wsl

# Inside WSL
docker run hello-world`}</pre>
        </Section>

        <Section title="6. Step 4 — NVIDIA Container Toolkit">
          <p>Same steps on Linux native and inside WSL2:</p>
          <pre>{`curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey | \\
  sudo gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg

curl -s -L https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list | \\
  sed 's#deb https://#deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https://#g' | \\
  sudo tee /etc/apt/sources.list.d/nvidia-container-toolkit.list

sudo apt update
sudo apt install -y nvidia-container-toolkit
sudo nvidia-ctk runtime configure --runtime=docker`}</pre>
          <p>Restart Docker:</p>
          <pre>{`# Linux native
sudo systemctl restart docker

# WSL2
sudo service docker restart`}</pre>
          <p>Verify GPU is visible inside a container:</p>
          <pre>{`docker run --rm --gpus all nvidia/cuda:12.4.0-base-ubuntu22.04 nvidia-smi`}</pre>
        </Section>

        <Section title="7. Step 5 — Launch vLLM">
          <p>Simplest form (vLLM will pull the model from HuggingFace on first run):</p>
          <pre>{`docker run -d --name vllm \\
  --gpus all --shm-size 4g --ipc host \\
  -p 8000:8000 \\
  -v $HOME/.cache/huggingface:/root/.cache/huggingface \\
  vllm/vllm-openai:latest \\
  --model Qwen/Qwen2.5-1.5B-Instruct-AWQ \\
  --quantization awq \\
  --max-model-len 2048 \\
  --gpu-memory-utilization 0.75 \\
  --served-model-name qwen2.5-1.5b`}</pre>
          <p>Wait for startup (3–5 minutes for a small model; longer for CUDA graph capture):</p>
          <pre>{`docker logs -f vllm
# → "Uvicorn running on http://0.0.0.0:8000"`}</pre>
          <p>Test:</p>
          <pre>{`curl http://localhost:8000/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -d '{
    "model": "qwen2.5-1.5b",
    "messages": [{"role":"user","content":"Introduce yourself."}],
    "max_tokens": 80
  }'`}</pre>
        </Section>

        <Section title="8. VRAM-aware tuning">
          <h4>4 GB (GTX 1650, 2050)</h4>
          <pre>{`--model Qwen/Qwen2.5-1.5B-Instruct-AWQ
--quantization awq
--max-model-len 1024
--gpu-memory-utilization 0.75
--max-num-seqs 1
--enforce-eager           # disable CUDA graphs (~1 GB saved)`}</pre>

          <h4>8 GB (RTX 3050, 2070 Super)</h4>
          <pre>{`--model Qwen/Qwen2.5-3B-Instruct-AWQ
--quantization awq
--max-model-len 2048
--gpu-memory-utilization 0.85
--max-num-seqs 4`}</pre>

          <h4>12 GB (RTX 3060, 4070)</h4>
          <pre>{`--model Qwen/Qwen2.5-7B-Instruct-AWQ
--quantization awq
--max-model-len 4096
--gpu-memory-utilization 0.9`}</pre>

          <h4>24 GB (RTX 3090, 4090)</h4>
          <pre>{`--model Qwen/Qwen2.5-14B-Instruct   # FP16 fits
--max-model-len 8192
--gpu-memory-utilization 0.9`}</pre>

          <h4>Multi-GPU rig (tensor parallel)</h4>
          <pre>{`# 4 × 24 GB → 70 B FP16
--model meta-llama/Llama-3.3-70B-Instruct
--tensor-parallel-size 4
--max-model-len 8192
--gpu-memory-utilization 0.9`}</pre>
          <p className="note">
            <code>--tensor-parallel-size</code> is only efficient at powers of 2 (1, 2, 4, 8). For a 6-GPU rig,
            split as 4 + 2 across two model instances.
          </p>
        </Section>

        <Section title="9. Troubleshooting">
          <h4>Startup refuses: "Free memory less than desired"</h4>
          <p>
            Cause: <code>--gpu-memory-utilization × total VRAM</code> exceeds what's actually free.
            Fix: lower the utilization. Example — free 3.22 GiB out of 4 GiB → keep utilization ≤ 0.80.
          </p>

          <h4>Connection reset on first request</h4>
          <p>
            Cause: CUDA graph capture ran out of VRAM at runtime. Fix:
          </p>
          <pre>{`--enforce-eager       # disable CUDA graphs
--max-model-len 1024  # shrink KV cache
--max-num-seqs 1      # one request at a time`}</pre>

          <h4>CUDA out of memory during model load</h4>
          <p>
            Cause: loading an unquantized FP16 model that doesn't fit. Fix: use an AWQ/GPTQ variant.
          </p>
          <ul>
            <li>❌ <code>Qwen/Qwen2.5-7B-Instruct</code> (14 GB FP16)</li>
            <li>✓ <code>Qwen/Qwen2.5-7B-Instruct-AWQ</code> (~5 GB)</li>
          </ul>

          <h4>max_tokens cannot be greater than max_model_len</h4>
          <p>
            The request asked for more tokens than the server's <code>--max-model-len</code>. Either lower the
            request's <code>max_tokens</code> or restart vLLM with a larger <code>--max-model-len</code>
            (costs more VRAM).
          </p>
        </Section>

        <Section title="10. CUDA graph vs eager mode">
          <ul>
            <li><strong>CUDA graph (default)</strong>: ~10–30% faster, costs 1–1.5 GiB extra VRAM.</li>
            <li><strong>Eager (<code>--enforce-eager</code>)</strong>: slightly slower, saves ~1 GiB.</li>
            <li>Under 8 GB VRAM: eager is effectively required. 12 GB+: keep CUDA graphs on.</li>
          </ul>
        </Section>

        <Section title="11. Quantization formats">
          <ul>
            <li><strong>AWQ</strong> — 4-bit, well-optimized in vLLM, best speed/quality balance. Recommended.</li>
            <li><strong>GPTQ</strong> — 4-bit, similar quality, more common on older models.</li>
            <li><strong>FP8</strong> — only on Ada Lovelace (RTX 40xx) / Hopper and newer.</li>
            <li><strong>GGUF</strong> — llama.cpp only. Not supported by vLLM.</li>
          </ul>
        </Section>

        <Section title="12. IANN external engine integration">
          <p>Once vLLM is up, add a service entry to <code>conf/provider.json</code>:</p>
          <pre>{`{
  "services": [
    {
      "name": "llm-xl-api",
      "addr": "127.0.0.1:8000",
      "type": "vllm",
      "manifest": "manifests/engines/vllm.json"
    }
  ]
}`}</pre>
          <p>
            Restart the provider. Health is probed via <code>/v1/models</code> and live metrics are scraped
            from <code>/metrics</code> (Prometheus). IANN does not manage the vLLM process lifecycle — you own it.
          </p>
        </Section>

        <Section title="13. Container management">
          <pre>{`# Inspect
docker ps
docker logs -f vllm
docker stats vllm
nvidia-smi

# Lifecycle
docker stop vllm
docker start vllm
docker restart vllm
docker rm -f vllm

# Cleanup
docker images
docker image prune -a
du -sh ~/.cache/huggingface/hub/*`}</pre>
          <p className="note">
            Pin the image tag (e.g. <code>vllm/vllm-openai:v0.6.3</code>) instead of <code>:latest</code>
            to avoid surprise upgrades and save disk from multiple cached layers.
          </p>
        </Section>

        <Section title="14. Useful endpoints">
          <h4>OpenAI-compatible</h4>
          <ul>
            <li><code>GET /v1/models</code> — list served models</li>
            <li><code>POST /v1/chat/completions</code> — chat</li>
            <li><code>POST /v1/completions</code> — text completion</li>
            <li><code>POST /v1/embeddings</code> — embeddings (if the model supports it)</li>
          </ul>
          <h4>vLLM-specific</h4>
          <ul>
            <li><code>GET /health</code> — liveness</li>
            <li><code>GET /metrics</code> — Prometheus metrics (scraped by IANN heartbeat)</li>
          </ul>
          <h4>Metrics IANN consumes</h4>
          <ul>
            <li><code>vllm:num_requests_running</code> — in-flight requests</li>
            <li><code>vllm:num_requests_waiting</code> — queued</li>
            <li><code>vllm:request_success_total</code> — cumulative successful requests (summed across <code>finished_reason</code> labels)</li>
            <li><code>vllm:e2e_request_latency_seconds_sum / _count</code> — derive average latency</li>
          </ul>
        </Section>

        <Section title="15. Real-world 4 GB VRAM recipe">
          <p>Tested on a GTX 1650 (learning rig):</p>
          <pre>{`docker run -d --name vllm \\
  --gpus all --shm-size 2g --ipc host \\
  -p 8000:8000 \\
  vllm/vllm-openai:latest \\
  --model Qwen/Qwen2.5-1.5B-Instruct-AWQ \\
  --quantization awq \\
  --max-model-len 1024 \\
  --gpu-memory-utilization 0.75 \\
  --max-num-seqs 1 \\
  --enforce-eager \\
  --served-model-name qwen2.5-1.5b`}</pre>
          <p>Observed footprint:</p>
          <ul>
            <li>Xwayland: ~780 MiB</li>
            <li>Model weights: ~1.1 GiB</li>
            <li>CUDA context: ~400 MiB</li>
            <li>KV cache (1024 × 1 seq): ~500 MiB</li>
            <li>Activations: ~300 MiB</li>
            <li><strong>Total ≈ 3 GiB</strong> — comfortable within 4 GiB</li>
          </ul>
          <p>Throughput: ~15 tokens/sec, time-to-first-token 1–2 s. Good enough for learning and feature tests, not production.</p>
        </Section>

        <Section title="16. References">
          <ul>
            <li>vLLM docs: <a href="https://docs.vllm.ai" target="_blank" rel="noopener noreferrer">docs.vllm.ai</a></li>
            <li>Docker image: <a href="https://hub.docker.com/r/vllm/vllm-openai" target="_blank" rel="noopener noreferrer">hub.docker.com/r/vllm/vllm-openai</a></li>
            <li>NVIDIA Container Toolkit: <a href="https://docs.nvidia.com/datacenter/cloud-native/container-toolkit/" target="_blank" rel="noopener noreferrer">docs.nvidia.com</a></li>
            <li>WSL GPU guide: <a href="https://learn.microsoft.com/en-us/windows/ai/directml/gpu-cuda-in-wsl" target="_blank" rel="noopener noreferrer">learn.microsoft.com</a></li>
          </ul>
        </Section>
      </div>
    </div>
  );
}

function Section({ title, children }) {
  return (
    <section className="docs-section">
      <h3>{title}</h3>
      {children}
    </section>
  );
}
