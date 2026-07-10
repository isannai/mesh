package station

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/daesob/http3proxy/pkg/auth"
	"github.com/daesob/http3proxy/pkg/engine/manifest"
	"github.com/daesob/http3proxy/pkg/glog"
	"github.com/daesob/http3proxy/pkg/installclient"
	"github.com/isannai/mesh/pkg/station/queue"
	"github.com/daesob/http3proxy/pkg/setup"
	"github.com/daesob/http3proxy/pkg/tunnel"
	"github.com/quic-go/quic-go"
)

// Provider serves inference services over a plain HTTP server; isannd fronts it
// (terminates TLS + reverse-proxies). The legacy direct-serve QUIC transport was
// removed with the app-mesh migration.
type Provider struct {
	*tunnel.Base

	// Cached static hardware spec
	hwStatic     *setup.HardwareSpec
	hwStaticOnce sync.Once

	// Installer client for spawning installer processes
	InstallClient *installclient.InstallClient

	// Sync manager for snapshot/token management
	syncMgr *syncManager

	// Register delta tracking — keeps last-sent snapshot of static fields so
	// subsequent registers can omit unchanged ones.
	regMu       sync.Mutex
	regSeq      uint64
	regSent     bool // true after first FullSync sent
	lastEmblem  string
	lastVersion string
	lastBinHash string
	lastLAddr   string
	lastHWHash  string // simple hash of marshaled hardware

	// TPM challenge response (pending, sent in next heartbeat)
	tpmMu       sync.Mutex
	tpmResponse []byte

	// Session issued by rendezvous on successful FullSync register. Used to
	// HMAC UDP heartbeat bodies. Never written to disk.
	sessionMu sync.RWMutex
	session   *tunnel.Session

	// Service lifecycle state machine (keyed by service name).
	svcStateMu sync.Mutex
	svcStates  map[string]serviceState

	// Wakes runFullSyncLoop early when RV hints a resync (e.g. RV restart
	// invalidated our session). Non-blocking send; a full channel means a
	// resync is already pending and that's fine.
	resyncCh chan struct{}

	// Phase 7 of queue migration: Provider-owned job queue + HTTP routes.
	// queueMgr / storage may be nil in legacy mode (engine-runner still owns
	// the queue); jobsHandler is built only when queueMgr is non-nil and is
	// dispatched to by stream.go for /provider/v1/jobs*, /provider/outputs/*,
	// and /provider/v1/queue/stats paths.
	queueMgr    *queue.Manager
	storage     *queue.Storage
	jobsHandler *JobsHandler

	// Phase 3 event-driven heartbeat: queue lifecycle hooks (job started /
	// completed / failed) post a wake signal here. runHeartbeatLoop selects
	// on this alongside the periodic ticker, so RV sees state changes within
	// ~ms instead of waiting for the next 5s tick. Buffered=1 with non-blocking
	// send semantics — multiple changes within one tick collapse into one wake.
	heartbeatNowCh chan struct{}

	// metricsBatcher coalesces queue lifecycle callbacks into one
	// service_event frame per debounce window. A single tick that fires
	// for sd-api, llm-api, and vllm-api in quick succession used to send
	// three separate frames — now they collapse into one frame whose
	// MetricsBatch carries all three rows.
	metricsBatchMu    sync.Mutex
	metricsBatchTimer *time.Timer

	// isanndCli — single shared TCP NLB client to isannd's rv-control
	// listener. Lazy-initialised on first use via isanndClient(). Sharing
	// across register / heartbeat / metrics goroutines means one TCP conn
	// per backend (not three) — RV's last-writer-wins controlConns map
	// would otherwise close earlier conns whenever a new hello arrives.
	isanndCliMu sync.Mutex
	isanndCli   *tunnel.IsanndClient

	// pingIntervalSec / registerIntervalSec — RV-dictated cadence (set on
	// each register_ack via OnPush callback). 0 = not yet received → loop
	// falls back to the fallback* constants in rendezvous.go (used while
	// RV is unreachable or has not yet acked). Atomic so the running
	// loops can pick up changes between ticks without a lock.
	pingIntervalSec     atomic.Int32
	registerIntervalSec atomic.Int32

	// rejectBackoffUntilMs — unix-ms until which need_register-driven
	// re-registers back off, set when RV returns an admission-denied error.
	// Stops a credential-less node from FullSync-spamming the RV every
	// heartbeat under protected mode. 0 = no backoff.
	rejectBackoffUntilMs atomic.Int64
}

// isanndClient returns the shared IsanndClient, creating it on first call.
// Subsequent callers reuse the same TCP socket to isannd's rv-control.
func (p *Provider) isanndClient() *tunnel.IsanndClient {
	p.isanndCliMu.Lock()
	defer p.isanndCliMu.Unlock()
	if p.isanndCli == nil {
		p.isanndCli = tunnel.NewIsanndClient(p.Cfg.OutboundGateway.URL(), p.Cfg.OutboundGateway.ControlHostPort())
	}
	return p.isanndCli
}

// serviceState tracks the most recent event pushed for a given service, so
// the provider does not re-announce the same state.
type serviceState struct {
	phase     string // "starting" | "ready" | "stopped"
	childPID  int
	model     string
	updatedAt time.Time
}

// New creates a new Provider.
func New(base *tunnel.Base) *Provider {
	return &Provider{
		Base:           base,
		InstallClient:  installclient.New(),
		syncMgr:        newSyncManager(),
		svcStates:      make(map[string]serviceState),
		resyncCh:       make(chan struct{}, 1),
		heartbeatNowCh: make(chan struct{}, 1),
	}
}

// NotifyJobChange wakes the heartbeat loop so the next packet flushes
// within milliseconds of a job state transition (queued/started/done/
// failed). Non-blocking: a full channel means a wake is already pending.
// Safe to call from any goroutine, including from inside queue locks
// (no IO performed here).
func (p *Provider) NotifyJobChange() {
	if p == nil || p.heartbeatNowCh == nil {
		return
	}
	select {
	case p.heartbeatNowCh <- struct{}{}:
	default:
	}
}

// setSession stores the session issued by RV. Safe for concurrent callers.
func (p *Provider) setSession(s *tunnel.Session) {
	p.sessionMu.Lock()
	p.session = s
	p.sessionMu.Unlock()
}

// wakeFullSyncNow forces the next runFullSyncLoop iteration to send a
// FullSync register immediately instead of waiting for the regular cadence
// (24h or session expiry). Callers:
//
//  1. RV-driven resync ack — historically inlined where the ack arrives.
//  2. handleTPMChallenge — the TPMResponse must reach RV via FullSync
//     register (heartbeat protobuf has no TPM field). Without this the
//     response sits in p.tpmResponse for up to 24h and the operator's
//     TPM badge stays gray indefinitely.
//
// Safe to call concurrently — regMu serialises the flag flip and the
// resyncCh send is non-blocking (buffer=1, drop on full).
func (p *Provider) wakeFullSyncNow() {
	p.regMu.Lock()
	p.regSent = false
	p.regMu.Unlock()
	select {
	case p.resyncCh <- struct{}{}:
	default: // already pending — drop
	}
}

// hasPendingTPMResponse reports whether a TPM challenge signature is
// waiting to be flushed to RV. runFullSyncLoop checks this in addition
// to currentSession() == nil so a wakeFullSyncNow() poke from
// handleTPMChallenge actually triggers sendFullSync — without this
// branch the loop sees a valid session and skips, the TPMResponse never
// rides out, and TPM verification stalls for the rest of the session.
func (p *Provider) hasPendingTPMResponse() bool {
	p.tpmMu.Lock()
	defer p.tpmMu.Unlock()
	return len(p.tpmResponse) > 0
}

// currentSession returns the active session, or nil if no FullSync has
// completed or the token has expired.
func (p *Provider) currentSession() *tunnel.Session {
	p.sessionMu.RLock()
	defer p.sessionMu.RUnlock()
	if p.session == nil || p.session.IsExpired(time.Now()) {
		return nil
	}
	return p.session
}

// Run starts the provider (QUIC listener).
func (p *Provider) Run(ctx context.Context) error {
	// Propagate the conf-declared isannd base URL to the package-level
	// pollServiceViaIsannd helper. Defaults to http://127.0.0.1:8443 when
	// the conf leaves it blank — works for the standard same-host setup.
	SetIsanndProbeBaseURL(p.Cfg.OutboundGateway.URL())

	// Cache static hardware spec once (replaces what the old register
	// loop did in its first iteration).
	p.initStaticHardware()

	// Phase 2B: backend → isannd internal API. Register / ping / metrics 모두
	// /internal/rv/* 로 forward. isannd 가 RV TCP control 연결 보유.
	p.startIsanndForwarder(ctx)
	p.startPingLoop(ctx)
	p.startServiceStatusLoop(ctx)

	// Phase 1-C: lifecycle watcher pushes service_event frames on state
	// transitions via the isannd client.
	go p.runServiceWatcher(ctx)

	// Progress callback wiring used to live here — engine-runner POSTed
	// parser progress events to a localhost listener. Engine-runner was
	// retired; progress now arrives through isannd's Docker log capture
	// (host file → fsnotify → parser → metrics). The plumbing for that
	// lands with the DockerDriver batch.

	// Phase 7 of queue migration: build per-service Queue infrastructure
	// (Manager + Storage + Factory). Idempotent — initQueueSubsystem returns
	// nil pieces when the conf has not opted in. Workers spin up lazily on
	// first GetOrCreate.
	p.queueMgr, p.storage = initQueueSubsystem(ctx, p.Cfg, p.PackagesDir, func(name, event, jobID string, pending, running int) {
		p.Log.Log(glog.Lifecycle, "[station] queue: svc=%s event=%s job=%s pending=%d running=%d",
			name, event, jobID, pending, running)
		// received는 큐 진입만 알리고 heartbeat 푸시는 생략 — 곧 started로
		// 갈아엎히는 짧은 윈도우라 RV 입장에선 noise. started/completed/failed
		// 만 푸시해도 RunningJobID + TotalJobsDone이 정확히 갱신됨.
		if event == "received" {
			return
		}
		p.NotifyJobChange()
		// Event-driven metrics push — debounce 100ms so a burst of
		// callbacks (e.g. all services transition at the same tick)
		// collapses into one batched service_event frame instead of
		// N separate frames.
		p.notifyMetricsChange(ctx)
		_ = name // kept for log clarity at callsites; batch carries all rows
	})
	if p.storage != nil {
		p.Log.Log(glog.Lifecycle, "[station] queue storage at %s (ttl=%ds)",
			p.Cfg.Queue.OutputDir, p.Cfg.Queue.OutputTTLSec)
	}
	if p.queueMgr != nil {
		// Snapshot Cfg.Services so JobsHandler resolves service names without
		// holding CfgMu on every request. Snapshot is refreshed only when the
		// provider restarts — Cfg.Services is treated as static at runtime.
		p.CfgMu.RLock()
		svcs := append([]setup.ServiceEntry(nil), p.Cfg.Services...)
		p.CfgMu.RUnlock()
		p.jobsHandler = NewJobsHandler(p.queueMgr, p.storage, svcs, p.apiSpecFor, p.engineDown)
		p.Log.Log(glog.Lifecycle, "[station] jobs handler ready (%d services)", len(svcs))
	}

	// Legacy 60s UDP JSON register path removed — FullSync (signed, 24h)
	// plus the 1 Hz heartbeat cover registration and liveness without the
	// unsigned legacy channel.

	// Plain HTTP server — receives reverse-proxied requests from isannd
	// (which has terminated TLS). Same role broker's http.go serves: expose
	// jobs handler + admin status to external callers (broker dispatching
	// jobs to this provider, UI polling status, etc.).
	httpMux := http.NewServeMux()
	if p.jobsHandler != nil {
		p.jobsHandler.Register(httpMux)
	}
	// /provider/*, /installer/*, /service/* — share routing with the QUIC
	// stream path via a bufStream adapter that captures the raw HTTP/1.1
	// response and replays it onto the HTTP ResponseWriter.
	httpMux.HandleFunc("/provider/", p.HandleProviderHTTP)
	httpMux.HandleFunc("/installer/", p.HandleProviderHTTP)
	httpMux.HandleFunc("/service/", p.HandleProviderHTTP)
	httpMux.HandleFunc("/svc/", p.HandleServiceProxy)
	srv := &http.Server{
		Addr:              p.Cfg.ListenAddr,
		Handler:           httpMux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { <-ctx.Done(); _ = srv.Close() }()
	p.Log.Log(glog.Lifecycle, "[station] HTTP on %s (TLS terminates at isannd)", p.Cfg.ListenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("provider HTTP listen: %w", err)
	}
	return nil
}

// bufStream is a no-network quic.Stream adapter backed by a byte buffer.
// stream.go's handlers write a raw HTTP/1.1 response (status line + headers
// + body) into it; HandleProviderHTTP then parses that buffer with
// http.ReadResponse and replays it onto the http.ResponseWriter.
type bufStream struct {
	*bytes.Buffer
}

func (b *bufStream) StreamID() quic.StreamID            { return 0 }
func (b *bufStream) CancelRead(_ quic.StreamErrorCode)  {}
func (b *bufStream) SetReadDeadline(_ time.Time) error  { return nil }
func (b *bufStream) Close() error                       { return nil }
func (b *bufStream) CancelWrite(_ quic.StreamErrorCode) {}
func (b *bufStream) Context() context.Context           { return context.Background() }
func (b *bufStream) SetWriteDeadline(_ time.Time) error { return nil }
func (b *bufStream) SetDeadline(_ time.Time) error      { return nil }

// HandleProviderHTTP is the HTTP-path mount for /provider/* and
// /installer/* — paths that historically only served on the QUIC
// orchestrator stream. We feed the request through the shared dispatcher
// using a bufStream adapter and replay the captured raw HTTP/1.1 response
// onto the real ResponseWriter. Keeps a single source of truth for the
// routing switch in stream.go.
func (p *Provider) HandleProviderHTTP(w http.ResponseWriter, r *http.Request) {
	bs := &bufStream{Buffer: new(bytes.Buffer)}
	p.dispatchOrchestratorRequest(bs, r)
	// Parse the raw HTTP response captured in the buffer.
	resp, err := http.ReadResponse(bufio.NewReader(bs.Buffer), r)
	if err != nil {
		// Empty response (handler returned without writing) — treat as 502.
		http.Error(w, "provider dispatch: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		w.Header()[k] = vv
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// routeServiceJobsPath intercepts provider-level paths under /svc/{name}/
// and dispatches them to the JobsHandler with service= query injected.
// Returns true when the request was handled (caller must stop) and false
// when the path is not provider-level and the caller should continue to
// the engine reverse-proxy. Mirrors the QUIC stream dispatcher's logic
// in stream.go:1817 so the same URL shape works on both transports.
func (p *Provider) routeServiceJobsPath(w http.ResponseWriter, r *http.Request, svcName, rest string) bool {
	switch r.Method {
	case http.MethodGet, http.MethodDelete, http.MethodPost:
	default:
		return false
	}

	injectService := func() {
		q := r.URL.Query()
		q.Set("service", svcName)
		r.URL.RawQuery = q.Encode()
	}

	switch {
	case r.Method == http.MethodGet && rest == "/run-schema":
		// M1: serve the service's api.run schema so the CLI can build its
		// --flags dynamically. The target node serves its own (engine/version
		// may differ — remote is the source of truth for cross-node).
		q := r.URL.Query()
		p.handleRunSchema(w, svcName, q.Get("path"), q.Get("variants") == "1")
		return true
	case r.Method == http.MethodPost && rest == "/v1/jobs":
		// Submit. Inject service so callers that put the name in the URL
		// (rather than in the JSON body) still resolve the right queue.
		r.URL.Path = "/v1/jobs"
		p.jobsHandler.handleSubmitForService(w, r, svcName)
		return true
	case r.Method == http.MethodGet && (rest == "/v1/queue/stats" || rest == "/v1/jobs" || rest == "/v1/jobs/"):
		injectService()
		r.URL.Path = "/v1/queue/stats"
		p.jobsHandler.handleStats(w, r)
		return true
	case strings.HasPrefix(rest, "/v1/jobs/"):
		r.URL.Path = rest
		p.jobsHandler.handleByID(w, r)
		return true
	case r.Method == http.MethodGet && strings.HasPrefix(rest, "/outputs/"):
		r.URL.Path = rest
		p.jobsHandler.handleOutputs(w, r)
		return true
	}
	return false
}

// apiSpecFor resolves a service's manifest api block, or nil when the service
// has no manifest. Injected into JobsHandler so submit-time param mapping can
// find the run template(s), the path allowlist, and any wire encoding.
func (p *Provider) apiSpecFor(svcName string) *manifest.APISpec {
	svc, ok := p.findService(svcName)
	if !ok {
		return nil
	}
	m := loadServiceManifest(svc, p.PackagesDir)
	if m == nil {
		return nil
	}
	return &m.API
}

// engineDown reports whether the named service's engine can't currently serve
// inference, so a submission fails fast (503) instead of queuing a job that
// can only fail at dispatch. Two-tier so a stopped engine ALWAYS rejects:
//
//   - if the watcher already saw it "ready"/"starting" → trust that (no I/O)
//   - otherwise (stopped/stopping, OR never observed because the provider
//     started while it was down) → probe the manifest ready_check once
//
// The probe is a plain HTTP GET — it never runs a wsl/docker command, so a
// stopped WSL/engine is not woken (a dead port just refuses instantly).
// docs/TODO/isann-cli-phase3.md.
func (p *Provider) engineDown(svcName string) bool {
	p.svcStateMu.Lock()
	st, seen := p.svcStates[svcName]
	p.svcStateMu.Unlock()
	if seen && (st.phase == "ready" || st.phase == "starting") {
		return false
	}
	// stopped / stopping / never-seen → confirm by probing the engine.
	return !p.engineReachable(svcName)
}

// engineReachable probes a service's manifest ready_check endpoint once
// (HTTP GET, short timeout) and reports whether it answered 200. HTTP only —
// no wsl/docker call — so it never wakes a stopped engine/WSL. Returns true
// when there is nothing to probe (unknown service / no ready_check) so we
// never block submissions on uncertainty.
func (p *Provider) engineReachable(svcName string) bool {
	svc, ok := p.findService(svcName)
	if !ok {
		return true
	}
	m := loadServiceManifest(svc, p.PackagesDir)
	if m == nil || m.ReadyCheck.URL == "" {
		return true
	}
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(resolveAddrTemplate(m.ReadyCheck.URL, svc.Addr))
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// runSchemaSet is the ?variants=1 response: the default run plus every run
// variant, so the CLI can build a union of --flags and pick a variant (e.g.
// sd's /v1/images/edits img2img) by which params the user supplied.
type runSchemaSet struct {
	Default  *manifest.RunSpec  `json:"default"`
	Variants []manifest.RunSpec `json:"variants"`
}

// handleRunSchema serves GET /svc/<name>/run-schema — the service's run spec
// (path/method/params/result) the CLI reads to build its --flags dynamically
// (M1). Query forms:
//   - (none)         → the default run
//   - ?path=<p>      → the run variant matching engine path p (e.g. img2img)
//   - ?variants=1    → { default, variants[] } for union-of-flags + selection
//
// 404 when the service is unknown or has no matching run spec.
func (p *Provider) handleRunSchema(w http.ResponseWriter, svcName, path string, variants bool) {
	api := p.apiSpecFor(svcName)
	if api == nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no api.run schema for service: " + svcName})
		return
	}
	if variants {
		writeJSON(w, http.StatusOK, runSchemaSet{Default: api.Run, Variants: api.Runs})
		return
	}
	rs := api.RunFor(path)
	if rs == nil {
		writeJSON(w, http.StatusNotFound, errorResponse{Error: "no api.run schema for service: " + svcName})
		return
	}
	writeJSON(w, http.StatusOK, rs)
}

// HandleServiceProxy reverse-proxies /svc/{serviceName}/{rest} to the
// service's backend address (provider.json's services[name].addr — the
// engine's listening port, e.g. localhost:17860 for sd, localhost:7862
// for llama). Replaces the legacy QUIC HTTPService stream path.
//
// Provider-level endpoints (queue, jobs, outputs) are intercepted before
// the reverse-proxy: /v1/jobs[/...], /v1/queue/stats, /outputs/... resolve
// against the per-service JobsHandler rather than the engine binary, since
// sd-server / llama-server / vllm don't implement those paths — IANN's
// queue is the source of truth. Mirrors the dispatch in stream.go:1817.
func (p *Provider) HandleServiceProxy(w http.ResponseWriter, r *http.Request) {
	trimmed := strings.TrimPrefix(r.URL.Path, "/svc/")
	parts := strings.SplitN(trimmed, "/", 2)
	if len(parts) < 1 || parts[0] == "" {
		http.Error(w, "invalid /svc/{name}/... path", http.StatusBadRequest)
		return
	}
	svcName := parts[0]
	rest := "/"
	if len(parts) == 2 && parts[1] != "" {
		rest = "/" + parts[1]
	}

	// Provider-level paths handled by JobsHandler — engine never sees them.
	// service= query is injected so handleStats / handleSubmit pick the
	// right per-service queue without the caller spelling it out twice.
	if p.jobsHandler != nil && svcName != "terminal" {
		if p.routeServiceJobsPath(w, r, svcName, rest) {
			return
		}
	}

	p.CfgMu.RLock()
	var addr string
	for _, s := range p.Cfg.Services {
		if s.Name == svcName {
			addr = s.Addr
			break
		}
	}
	p.CfgMu.RUnlock()
	if addr == "" {
		http.Error(w, "service not found: "+svcName, http.StatusNotFound)
		return
	}
	targetURL := "http://" + addr + rest
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		http.Error(w, "svc proxy request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for k, vv := range r.Header {
		if k == "Connection" || k == "Keep-Alive" || k == "Transfer-Encoding" || k == "Upgrade" {
			continue
		}
		for _, v := range vv {
			proxyReq.Header.Add(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(proxyReq)
	if err != nil {
		http.Error(w, `{"error":"svc proxy: `+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32*1024)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			break
		}
	}
}

// HandleAuthVerifyHTTP is the HTTP-path counterpart of handleAuthVerify
// (QUIC stream). Called via broker → isannd /node/p:<EOA>/provider/
// auth-verify → peer isannd → this. Returns the wallet's role on this
// provider (owner / admin) for the broker UI's per-node auth handshake.
func (p *Provider) HandleAuthVerifyHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "POST required"})
		return
	}
	sig := strings.TrimPrefix(r.Header.Get("Authorization"), "ISANN ")
	message := r.Header.Get("X-ISANN-Message")
	if sig == "" || message == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing auth headers"})
		return
	}
	address, err := auth.RecoverAddress(message, sig)
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid signature"})
		return
	}
	role := ""
	if p.Auth.Owner != "" && strings.EqualFold(address, p.Auth.Owner) {
		role = "owner"
	} else {
		for _, a := range p.Auth.Admins {
			if strings.EqualFold(address, a) {
				role = "admin"
				break
			}
		}
	}
	if role == "" {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "not authorized"})
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"ok": "true", "role": role, "address": address})
}
