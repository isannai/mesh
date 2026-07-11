package station

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/daesob/http3proxy/pkg/engine/manifest"
	"github.com/daesob/http3proxy/pkg/setup"
	"github.com/daesob/http3proxy/pkg/tunnel"
)

// manifestErrLogged dedupes per-service manifest load/validate error logs.
// Without it the 1 Hz heartbeat loop would spam the same line every second.
var manifestErrLogged sync.Map

func logManifestErrOnce(svcName, format string, args ...interface{}) {
	if _, loaded := manifestErrLogged.LoadOrStore(svcName, true); !loaded {
		log.Printf(format, args...)
	}
}

// loadServiceManifest reads and validates the engine manifest for a service
// entry. Resolution order (S1 marker pivot: unified apps/ tree first):
//
//  1. apps/<svc.Name>/manifest.json · apps/<svc.Engine>/manifest.json ─ engine
//     in the unified artifacts/addon/apps/ tree (canonical target)
//  2. <root>/manifests/<svc.Name>.json · <root>/manifests/<svc.Engine>.json ─
//     legacy flat (same path isannd uses for its docker probe)
//  3. <packagesDir>/engines/<svc.Engine>/manifest.json ─ legacy wrapped
//     layout; kept so older deployments keep working until they migrate
//
// Returns nil when nothing resolves. Errors are logged exactly once per
// service so the 1 Hz heartbeat loop doesn't flood the log.
func loadServiceManifest(svc setup.ServiceEntry, packagesDir string) *manifest.Manifest {
	candidates := []string{}
	// Unified apps/ tree first (engine = apps/<name>/manifest.json).
	if svc.Name != "" {
		candidates = append(candidates, manifest.AppManifestPath(svc.Name))
	}
	if svc.Engine != "" {
		candidates = append(candidates, manifest.AppManifestPath(svc.Engine))
	}
	if svc.Name != "" {
		candidates = append(candidates, manifest.ManifestPath(svc.Name))
	}
	if svc.Engine != "" {
		candidates = append(candidates, manifest.ManifestPath(svc.Engine))
		candidates = append(candidates, filepath.Join(packagesDir, "engines", svc.Engine, "manifest.json"))
	}
	for _, path := range candidates {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		m, err := manifest.Load(path)
		if err != nil {
			logManifestErrOnce(svc.Name, "[station] manifest load failed for %q (%s): %v", svc.Name, path, err)
			continue
		}
		if err := m.Validate(); err != nil {
			logManifestErrOnce(svc.Name, "[station] manifest validate failed for %q (%s): %v", svc.Name, path, err)
			continue
		}
		return m
	}
	logManifestErrOnce(svc.Name, "[station] no manifest found for %q (tried %v)", svc.Name, candidates)
	return nil
}

// resolveAddrTemplate substitutes the `{addr}` token in a manifest URL with
// the runtime service address declared in conf/provider.json.
func resolveAddrTemplate(tmpl, addr string) string {
	return strings.ReplaceAll(tmpl, "{addr}", addr)
}

// buildHardwareForMsg returns the provider's hardware spec for inclusion in
// register payloads. As of Phase 1-D, only STATIC fields are sent: CPU
// name/cores/clock, GPU name/driver/vram_total, RAM total. Volatile metrics
// (CPU temp, GPU util/temp/vram_free, RAM free) are served on demand via
// the P2P QUIC /provider/monitor endpoint instead of the RV register path.
//
// The `expose` flag still gates whether hardware is shared publicly: when
// false, returns nil so the RV never stores any spec for this node.
func (p *Provider) buildHardwareForMsg(expose bool) *setup.HardwareSpec {
	if !expose {
		return nil
	}
	if p.hwStatic == nil {
		return nil
	}
	// Clone so the caller can mutate without affecting the cached snapshot.
	hw := *p.hwStatic
	return &hw
}

// pollService polls a single registered service to collect static engine
// info (model, ready). Volatile runtime metrics (queue depth, total jobs,
// avg job sec, running job id) flow through pollServiceQueue / the
// heartbeat path instead.
//
// All services are probed via the manifest-declared ready_check URL —
// the engine-runner /health contract was retired along with the wrapped
// pattern. Service kinds that need a different probe (e.g. terminal's
// TCP dial) are handled inline.
func pollService(svc setup.ServiceEntry, packagesDir string) (info setup.ServiceInfo, alive bool, busy bool) {
	info, alive, busy, _ = pollServiceWithAPI(svc, packagesDir)
	return
}

// pollServiceWithAPI is the same as pollService but additionally returns
// the raw engine probe response body. Used by the register path to surface
// inspect fields with `from: "api"`. Returns nil apiBody on probe failure.
func pollServiceWithAPI(svc setup.ServiceEntry, packagesDir string) (info setup.ServiceInfo, alive bool, busy bool, apiBody []byte) {
	if svc.Name == "terminal" {
		info = setup.ServiceInfo{Name: svc.Name, Engine: svc.Engine}
		conn, err := net.DialTimeout("tcp", svc.Addr, 1*time.Second)
		if err == nil {
			conn.Close()
			alive = true
		}
		return
	}
	return pollServiceExternal(svc, packagesDir)
}

// pollServiceExternal probes any externally-managed engine (vLLM, Ollama,
// TGI, LMDeploy, sd-server-in-Docker, …) where IANN does not own the
// process lifecycle.
//
// The probe URL + per-request timeout are taken from the manifest's
// ready_check block:
//
//	manifest.ready_check.url         (required, `{addr}` substituted)
//	manifest.ready_check.timeout_ms  (per-GET timeout, default 3000)
//
// If no manifest is wired up or ready_check.url is empty, we refuse to
// silently guess a probe URL — the service is reported as unreachable and
// a one-shot warning is logged. This prevents an OpenAI-shaped `/v1/models`
// fallback from happening to pass on a service that actually speaks a
// different API (A1111, ollama, custom).
//
// The response body is parsed as the OpenAI /v1/models shape (.data[].id)
// to extract the active model name as a best-effort bonus — failures don't
// flip ServerReady off. The raw body is returned as apiBody so callers
// (register path) can pass it to inspect's `from: "api"` resolver without
// re-fetching.
// isanndProbeBaseURL is the base URL provider uses to reach the same-host
// isannd's internal listener for docker probe / lifecycle calls. Default
// matches the standard node-bridge port; provider.Run() overwrites it with
// the conf's outbound_gateway.node_bridge_addr so non-default deployments
// (different host, alternate port) still work.
var isanndProbeBaseURL = "http://127.0.0.1:8443"

// SetIsanndProbeBaseURL is called from provider.Run() at startup so the
// package-level pollServiceViaIsannd uses the operator-configured isannd
// address instead of the default loopback.
func SetIsanndProbeBaseURL(url string) {
	if url != "" {
		isanndProbeBaseURL = url
	}
}

// pollServiceViaIsannd delegates a docker-launched engine's probe to the
// host-side isannd's /internal/api/docker/probe/{name} endpoint where name is
// the actual docker container name (not the conf service name).
func pollServiceViaIsannd(containerName string, info setup.ServiceInfo) (setup.ServiceInfo, bool, bool, []byte) {
	url := isanndProbeBaseURL + "/internal/api/docker/probe/" + containerName
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		logManifestErrOnce(info.Name+":probe", "[station] dockerProbe %q failed (url=%s): %v", containerName, url, err)
		return info, false, false, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		logManifestErrOnce(info.Name+":probe", "[station] dockerProbe %q (url=%s) returned %d: %s", containerName, url, resp.StatusCode, string(body))
		return info, false, false, nil
	}
	var reply struct {
		HTTPOK bool   `json:"http_ok"`
		Model  string `json:"model,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		logManifestErrOnce(info.Name+":probe", "[station] dockerProbe %q decode: %v", containerName, err)
		return info, false, false, nil
	}
	info.Model = reply.Model
	info.ServerReady = reply.HTTPOK
	return info, reply.HTTPOK, false, nil
}

func pollServiceExternal(svc setup.ServiceEntry, packagesDir string) (info setup.ServiceInfo, alive bool, busy bool, apiBody []byte) {
	info = setup.ServiceInfo{Name: svc.Name, Type: svc.Type, Engine: svc.Engine}

	m := loadServiceManifest(svc, packagesDir)
	if m == nil || m.ReadyCheck.URL == "" {
		logManifestErrOnce(svc.Name,
			"[station] external service %q has no manifest.ready_check.url — probe disabled (set service.engine and ship engines/<engine>/manifest.json)",
			svc.Name)
		return // alive=false, ServerReady=false
	}
	// Surface manifest launcher (docker / external) to broker UI so it can
	// decide whether to expose Start/Stop controls. Defaults to "external"
	// (manifest.go's IsExternal contract: empty launcher = external).
	if m.Launcher != "" {
		info.Launcher = m.Launcher
	} else {
		info.Launcher = "external"
	}

	// docker launcher → delegate to isannd's combined inspect+probe endpoint.
	// isannd is the host-trusted owner of the docker socket; it knows the
	// container's running state AND has the manifest available to perform
	// the HTTP probe with model_path extraction. provider just forwards the
	// result so we avoid duplicating manifest loading / port discovery /
	// model_path JSON walking.
	//
	// container 이름: 우선순위 = manifest.Container.Name → svc.Engine (예:
	// "sd") → svc.Name (예: "sd-api"). svc.Name 그대로 쓰면 conf 의 서비스
	// 이름 ("sd-api") 으로 컨테이너 찾는데 실제 컨테이너 이름은 "sd" 라서
	// not found 됨. 매니페스트 파일 stem (= svc.Engine) 이 컨테이너 이름과
	// 같다는 규약으로 안전하게 매칭.
	if m.Launcher == "docker" {
		containerName := svc.Name
		if m.Container != nil && m.Container.Name != "" {
			containerName = m.Container.Name
		} else if svc.Engine != "" {
			containerName = svc.Engine
		}
		return pollServiceViaIsannd(containerName, info)
	}
	url := resolveAddrTemplate(m.ReadyCheck.URL, svc.Addr)

	// Per-request HTTP timeout. Intentionally NOT m.ReadyCheck.TimeoutMS —
	// that field is engine-runner's WaitReady total budget (vllm declares
	// 600000ms / 10min for model boot). Use the separate poll_timeout_ms
	// knob which the manifest can tune per service; default 1s. A short
	// value is critical because the watcher loop blocks on this every tick;
	// healthy /v1/models or /sdapi/v1/sd-models on localhost responds in
	// milliseconds, so 1s already leaves a wide margin.
	timeout := time.Duration(m.ReadyCheck.PollTimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = time.Second
	}
	c := &http.Client{Timeout: timeout}
	resp, err := c.Get(url)
	if err != nil {
		return // unreachable
	}
	alive = true
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Reachable but not ready (engine still loading, etc.)
		info.ServerLoading = true
		return
	}

	body, _ := io.ReadAll(resp.Body)
	apiBody = body

	// HTTP 200 from the manifest-declared ready_check is the authoritative
	// liveness signal — model-name extraction below is a best-effort bonus.
	// Engines that return non-OpenAI shapes (A1111 sd-server's
	// /sdapi/v1/sd-models returns a top-level array, ollama returns
	// {"models":[...]}, …) still show as ready; broker UI surfaces whatever
	// inspect.fields path matches their response, or falls back to the
	// configured engine name.
	info.ServerReady = true

	// Model name extraction is engine-specific. The manifest declares where
	// the active model lives via ready_check.model_path (JSON path walked by
	// manifest.JSONPath). Engines that don't expose a model name in their
	// readiness response (or whose manifest omits the field) leave
	// info.Model blank — broker UI falls back to displaying the engine name.
	//
	// Stage 5: provider only fills Model. ModelHash / ModelOriginURL are
	// injected by isannd's nlb_listener at register forward time from the
	// host-side .isann/engine-state/<engine>.json — provider runs inside a
	// container and can't see the package.json on host disk.
	if m.ReadyCheck.ModelPath != "" {
		var apiObj any
		if json.Unmarshal(body, &apiObj) == nil {
			if v := manifest.JSONPath(apiObj, m.ReadyCheck.ModelPath); v != nil {
				if s, ok := v.(string); ok && s != "" {
					info.Model = s
				}
			}
		}
	}
	return
}

// pollServiceQueue fetches live queue metrics for a service.
//
// Phase 8 of the queue migration: managed services (engine-runner spawned)
// no longer expose /v1/queue/stats — engine-runner became a thin
// reverse-proxy and Provider's own queue is the single source of truth.
// For those, read the in-memory Manager.Stats(name) directly.
//
// External services (vLLM, etc.) still need to be scraped via their
// manifest-declared Prometheus endpoint, since Provider has no
// authoritative view of in-flight jobs there.
func (p *Provider) pollServiceQueue(svc setup.ServiceEntry, packagesDir string) (
	queueDepth int,
	runningCount int,
	totalJobsDone int64,
	avgJobSec float64,
	lastSubmittedAt int64,
	runningJobID string,
) {
	if svc.Name == "terminal" {
		return
	}

	// Managed: read Provider's own queue. The Manager lazy-creates queues
	// on first dispatch — Stats returns the zero value before then which
	// is the desired "no traffic yet" signal.
	if svc.IsManagedLocally() && p.queueMgr != nil {
		s := p.queueMgr.Stats(svc.Name)
		queueDepth = s.Pending
		runningCount = s.Running
		totalJobsDone = s.TotalJobsDone
		avgJobSec = s.AvgJobSec
		runningJobID = s.RunningJobID
		return
	}

	// External: prometheus / other manifest-declared metrics endpoint.
	if m := loadServiceManifest(svc, packagesDir); m != nil && m.Metrics != nil {
		switch m.Metrics.Type {
		case "prometheus":
			return pollServiceQueuePrometheus(svc, m)
		}
	}

	// External without metrics declaration → silent zeros.
	return
}

// pollServiceQueuePrometheus scrapes a Prometheus /metrics endpoint declared
// in the engine manifest and translates engine-specific key names to IANN's
// standard metric fields via the manifest's metrics.mapping table.
//
// Standard mapping keys consumed:
//   - queue_depth     → integer gauge
//   - running_count   → integer gauge
//   - total_jobs_done → counter
//   - latency_sum     → histogram sum  (used with latency_count for avg)
//   - latency_count   → histogram count
//
// External engines have no queryable job IDs, so running_job_id is a
// synthetic "<service>-active" flag whenever anything is in flight.
// lastSubmittedAt is left zero — Prometheus exporters typically do not
// expose per-request timestamps.
func pollServiceQueuePrometheus(svc setup.ServiceEntry, m *manifest.Manifest) (
	queueDepth int,
	runningCount int,
	totalJobsDone int64,
	avgJobSec float64,
	lastSubmittedAt int64,
	runningJobID string,
) {
	url := resolveAddrTemplate(m.Metrics.URL, svc.Addr)
	c := &http.Client{Timeout: 1 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}

	metrics, err := tunnel.ParsePrometheus(resp.Body)
	if err != nil {
		return
	}

	mp := m.Metrics.Mapping
	queueDepth = int(metrics[mp["queue_depth"]])
	runningCount = int(metrics[mp["running_count"]])
	totalJobsDone = int64(metrics[mp["total_jobs_done"]])

	if count := metrics[mp["latency_count"]]; count > 0 {
		avgJobSec = metrics[mp["latency_sum"]] / count
	}

	if runningCount > 0 {
		runningJobID = svc.Name + "-active"
	}
	return
}
