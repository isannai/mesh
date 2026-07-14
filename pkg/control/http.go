package control

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/isannai/mesh/pkg/glog"
	"github.com/isannai/mesh/pkg/setup"
	"github.com/isannai/mesh/pkg/tunnel"
)

// verifyWithProvider asks the Provider's /provider/auth-verify endpoint
// whether the wallet that signed authHeader/message has owner/admin
// rights on the node, routed through this host's isannd outbound peer
// path: broker → localhost isannd /node/p:<EOA>/provider/auth-verify →
// peer isannd → provider HTTP. Returns the role ("owner"/"admin") on
// success.
func (b *Broker) verifyWithProvider(ctx context.Context, nodeID, authHeader, message string) (string, error) {
	parts := strings.SplitN(nodeID, ":", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid nodeID: %q", nodeID)
	}
	isanndBase := b.Cfg.OutboundGateway.URL()
	if isanndBase == "" {
		return "", fmt.Errorf("outbound_gateway.addr not configured")
	}
	url := isanndBase + "/node/" + strings.ToLower(parts[0]) + ":" + parts[1] + "/provider/auth-verify"

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", authHeader)
	req.Header.Set("X-ISANN-Message", message)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("isannd dial %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, rerr := io.ReadAll(resp.Body)
	// Diagnostic — surface the actual bytes (and any read error) so silent
	// transport truncation can't masquerade as "decode response: unexpected
	// end of JSON input" downstream. Uses standard log.Printf so it shows
	// regardless of glog level configuration.
	log.Printf("[control] verifyWithProvider: status=%d bodyLen=%d readErr=%v contentType=%q body=%q",
		resp.StatusCode, len(body), rerr, resp.Header.Get("Content-Type"), string(body))
	if rerr != nil {
		return "", fmt.Errorf("read response body (got %d bytes): %w", len(body), rerr)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("provider returned %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		Role string `json:"role"`
	}
	if jerr := json.Unmarshal(body, &out); jerr != nil {
		return "", fmt.Errorf("decode response: %w", jerr)
	}
	return out.Role, nil
}


// Run starts the broker HTTP server.
func (b *Broker) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	webDir := b.Cfg.WebDir
	if webDir == "" {
		webDir = "web/control/build"
	}
	mux.Handle("/", b.brokerSPA(webDir))

	// Pipeline runner (Go-native, replaces Node.js iann-vm reverse proxy).
	mux.HandleFunc("/v1/pipeline/execute", b.handlePipelineExecute)
	mux.HandleFunc("/v1/pipeline/jobs", b.handlePipelineJobs)
	mux.HandleFunc("/v1/pipeline/jobs/", b.handlePipelineJobByID)
	mux.HandleFunc("/v1/pipeline/entities", b.handlePipelineEntities)

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "ok",
			"version": setup.ControlVersion,
			"hash":    setup.SelfHash(),
		})
	})
	mux.HandleFunc("/info", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"node_bridge_addr": b.Cfg.OutboundGateway.NodeBridgeAddr,
			"rv_control_addr":  b.Cfg.OutboundGateway.RVControlAddr,
			"rendezvous":       b.Cfg.OutboundGateway.RendezvousAddr,
			"id":               b.NodeIdentity.Address,
		})
	})

	mux.HandleFunc("/node-id", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(b.NodeIdentity)
	})

	// proxyToRendezvous forwards a request to RV via the local isannd
	// node-bridge listener (/internal/rv/*). broker never dials RV REST
	// directly anymore — the bridge handles TLS termination + reverse
	// proxy + source-IP guard. NodeBridgeAddr is required for any RV-
	// backed endpoint to work; empty config returns an empty list so the
	// UI shows "no data" rather than 5xx.
	proxyToRendezvous := func(w http.ResponseWriter, r *http.Request, rvPath string, defaults map[string]string) {
		w.Header().Set("Content-Type", "application/json")
		b.CfgMu.RLock()
		bridgeBase := b.Cfg.OutboundGateway.URL()
		b.CfgMu.RUnlock()
		if bridgeBase == "" {
			json.NewEncoder(w).Encode([]any{})
			return
		}

		// Merge caller query with defaults (caller wins).
		q := r.URL.Query()
		for k, v := range defaults {
			if q.Get(k) == "" {
				q.Set(k, v)
			}
		}
		qs := q.Encode()

		url := bridgeBase + "/internal/rv" + rvPath
		if qs != "" {
			url += "?" + qs
		}
		client := &http.Client{Timeout: 10 * time.Second}

		req, _ := http.NewRequest("GET", url, nil)
		if inm := r.Header.Get("If-None-Match"); inm != "" {
			req.Header.Set("If-None-Match", inm)
		}
		resp, err := client.Do(req)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer resp.Body.Close()
		if etag := resp.Header.Get("ETag"); etag != "" {
			w.Header().Set("ETag", etag)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "" {
			w.Header().Set("Cache-Control", cc)
		}
		w.Header().Set("Access-Control-Expose-Headers", "ETag")
		w.WriteHeader(resp.StatusCode)
		if resp.StatusCode != http.StatusNotModified {
			io.Copy(w, resp.Body)
		}
	}

	mux.HandleFunc("/v1/nodes", func(w http.ResponseWriter, r *http.Request) {
		// Default to role=provider only when no filter params are supplied,
		// so "Try it" in API docs can drive arbitrary queries.
		var defaults map[string]string
		if r.URL.RawQuery == "" {
			defaults = map[string]string{"role": "provider"}
		}
		proxyToRendezvous(w, r, "/v1/nodes", defaults)
	})

	// /v1/search/nodes — marketplace unified search. Etherscan-style auto
	// detection of input type (hash / address / GPU / engine / node / text)
	// over a 5s-cached snapshot of RV /v1/nodes. See pkg/control/search.go.
	searchIdx := &SearchIndex{}
	mux.HandleFunc("/v1/search/nodes", b.handleSearchNodes(searchIdx))

	mux.HandleFunc("/v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		proxyToRendezvous(w, r, "/v1/metrics", nil)
	})

	mux.HandleFunc("/rendezvous/health", func(w http.ResponseWriter, r *http.Request) {
		proxyToRendezvous(w, r, "/health", nil)
	})

	// Gate proxy — all /gate/v1/* endpoints go through isannd's
	// /internal/gate/* reverse proxy. Broker never contacts the external
	// Gate directly; isannd holds the gate URL in its discovery config and
	// terminates outbound HTTPS. Empty fallback per endpoint is preserved so
	// UIs see "no items" rather than 5xx when gate isn't configured.
	mux.HandleFunc("/gate/v1/rendezvous", func(w http.ResponseWriter, r *http.Request) {
		b.fetchFromIsanndGate(w, r, "/v1/rendezvous", []byte("[]"))
	})
	mux.HandleFunc("/gate/v1/nodes", func(w http.ResponseWriter, r *http.Request) {
		b.fetchFromIsanndGate(w, r, "/v1/nodes?role=provider", []byte(`{"items":[],"total":0}`))
	})

	// rvDirectClient is shared by the /rv/v1/* handlers below. RV REST is
	// plain HTTP(S) over TCP — http3.Transport would hang on RV :9000.
	// Self-signed dev certs are accepted via InsecureSkipVerify.
	rvDirectClient := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		Timeout:   10 * time.Second,
	}

	// rvTargetURL resolves the upstream RV URL for an /rv/v1/* request.
	// Routing decision:
	//
	//   - X-Forwarded-Host set  → direct-dial that host (multi-RV path,
	//     used by the broker UI's region selector).
	//     X-Forwarded-Proto controls scheme (default "https").
	//   - X-Forwarded-Host empty → route via the local isannd node-bridge
	//     /internal/rv/* (default RV path, same as /v1/nodes).
	//
	// Returns ("", false) when the request must go via the bridge — caller
	// builds the bridge URL itself (so the right NodeBridgeAddr default
	// kicks in even when this returns "" for an unconfigured deployment).
	//
	// SSRF gate: X-Forwarded-Host is client-controllable. A production
	// hardening batch should restrict the accepted host set to RV servers
	// the operator has registered (broker.json allowlist). Until then any
	// reachable host can be dialed via this endpoint — adequate for dev
	// where the UI is trusted, NOT safe for hostile-network deployments.
	rvTargetURL := func(r *http.Request, path string) (url string, direct bool) {
		host := r.Header.Get("X-Forwarded-Host")
		if host == "" {
			return "", false
		}
		proto := r.Header.Get("X-Forwarded-Proto")
		if proto == "" {
			proto = "https"
		}
		return proto + "://" + host + path, true
	}

	// /rv/v1/nodes — fetch nodes from the configured RV (default) or from
	// the host indicated by X-Forwarded-Host (multi-RV).
	mux.HandleFunc("/rv/v1/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		url, direct := rvTargetURL(r, "/v1/nodes?role=provider")
		if !direct {
			b.CfgMu.RLock()
			bridge := b.Cfg.OutboundGateway.URL()
			b.CfgMu.RUnlock()
			if bridge == "" {
				json.NewEncoder(w).Encode([]any{})
				return
			}
			url = bridge + "/internal/rv/v1/nodes?role=provider"
		}
		req, _ := http.NewRequest("GET", url, nil)
		if inm := r.Header.Get("If-None-Match"); inm != "" {
			req.Header.Set("If-None-Match", inm)
		}
		resp, err := rvDirectClient.Do(req)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer resp.Body.Close()
		if etag := resp.Header.Get("ETag"); etag != "" {
			w.Header().Set("ETag", etag)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "" {
			w.Header().Set("Cache-Control", cc)
		}
		w.Header().Set("Access-Control-Expose-Headers", "ETag")
		w.WriteHeader(resp.StatusCode)
		if resp.StatusCode != http.StatusNotModified {
			io.Copy(w, resp.Body)
		}
	})

	// /rv/v1/metrics — same routing rules as /rv/v1/nodes.
	mux.HandleFunc("/rv/v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		url, direct := rvTargetURL(r, "/v1/metrics")
		if !direct {
			b.CfgMu.RLock()
			bridge := b.Cfg.OutboundGateway.URL()
			b.CfgMu.RUnlock()
			if bridge == "" {
				json.NewEncoder(w).Encode([]any{})
				return
			}
			url = bridge + "/internal/rv/v1/metrics"
		}
		resp, err := rvDirectClient.Get(url)
		if err != nil {
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	})

	mux.HandleFunc("/gate/v1/curated-models", func(w http.ResponseWriter, r *http.Request) {
		b.fetchFromIsanndGate(w, r, "/v1/curated-models", []byte("[]"))
	})

	// POST /v1/my-nodes/:nodeId/auth — verify node ownership via Provider QUIC tunnel.
	// My Nodes CRUD has moved to browser IndexedDB; only the auth proxy remains server-side.
	mux.HandleFunc("/v1/my-nodes/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		rest := strings.TrimPrefix(r.URL.Path, "/v1/my-nodes/")
		if rest == "" {
			w.WriteHeader(400)
			return
		}
		if strings.HasSuffix(rest, "/auth") && r.Method == http.MethodPost {
			nodeID := strings.TrimSuffix(rest, "/auth")
			role, perr := b.verifyWithProvider(r.Context(), nodeID, r.Header.Get("Authorization"), r.Header.Get("X-ISANN-Message"))
			if perr != nil || (role != "owner" && role != "admin") {
				msg := "not authorized for this node"
				if perr != nil {
					msg = perr.Error()
				}
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]any{"status": "error", "auth": false, "message": msg})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "auth": true, "role": role})
			return
		}
		w.WriteHeader(404)
	})

	// (Note: /v1/local/* endpoints removed. Provider bootstrap is done by
	// running `installer install --name=provider` from the CLI on the host
	// machine; the broker UI's Setup page redirects to /welcome which has
	// the full guide. Post-bootstrap software installs go through
	// /node/{provider-id}/installer/* over the QUIC tunnel.)

	// Auth (EOA signature verify)
	mux.HandleFunc("/v1/auth/verify", b.handleAuthVerify)

	// Node proxy
	mux.HandleFunc("/node/", b.handleNodeProxy)

	// Admin API
	mux.HandleFunc("/v1/admin/status", b.HandleAdminStatus)
	mux.HandleFunc("/v1/admin/config", b.HandleAdminConfig)
	mux.HandleFunc("/v1/admin/logs", b.HandleAdminLogs)
	mux.HandleFunc("/v1/admin/logs/stream", b.HandleAdminLogsStream)
	mux.HandleFunc("/v1/admin/logs/files", b.HandleAdminLogFiles)
	mux.HandleFunc("/v1/admin/logs/file", b.HandleAdminLogFile)
	mux.HandleFunc("/v1/admin/cards", b.HandleAdminCards)
	mux.HandleFunc("/v1/admin/api-features", b.handleAdminAPIFeatures)
	mux.HandleFunc("/v1/admin/api-features/preset", b.handleAdminAPIPreset)

	// Public read of card visibility map — anyone rendering the workspace.
	mux.HandleFunc("/v1/cards", b.HandleCards)
	mux.HandleFunc("/v1/api/policy", b.handleAPIPolicy)

	srv := &http.Server{
		Addr:              b.Cfg.ListenAddr,
		Handler:           b.authMiddleware(mux),
		IdleTimeout:       30 * time.Second, // keep-alive idle 30초 후 서버가 먼저 FIN → stale 커넥션 재사용 방지 (localhost 기준)
		ReadHeaderTimeout: 10 * time.Second, // 슬로우로리스 방어 (진행 중 요청에는 영향 없음)
	}
	go func() { <-ctx.Done(); srv.Close(); b.Close() }()

	// Start rendezvous registration loop — runs as long as we have a way to
	// reach the mesh (either via isannd sidecar, or legacy direct RV addr).
	if b.Cfg.OutboundGateway.NodeBridgeAddr != "" || b.Cfg.OutboundGateway.RVControlAddr != "" || b.Cfg.OutboundGateway.RendezvousAddr != "" {
		go b.runRendezvousLoop(ctx)
	}

	if b.Cfg.TLS.Enabled {
		b.Log.Log(glog.Lifecycle, "[control] HTTPS on %s → isannd: %s",
			b.Cfg.ListenAddr, b.Cfg.OutboundGateway.URL())
		return srv.ListenAndServeTLS(b.Cfg.TLS.Cert, b.Cfg.TLS.Key)
	}
	b.Log.Log(glog.Lifecycle, "[control] HTTP on %s → isannd: %s",
		b.Cfg.ListenAddr, b.Cfg.OutboundGateway.URL())
	return srv.ListenAndServe()
}

// fetchFromIsanndGate proxies a /gate/v1/* request to isannd's
// /internal/gate/* endpoint, preserving ETag / Cache-Control / status.
//
// When isannd reports the gate is unconfigured (X-Isannd-Gate: unconfigured),
// responds with HTTP 200 + emptyFallback so the UI sees "no items" rather
// than 5xx. Callers pass the per-endpoint empty body shape (e.g. "[]" or
// '{"items":[],"total":0}'). emptyFallback == nil ⇒ propagate 5xx as-is
// (used for write endpoints like scan-url where empty isn't meaningful).
func (b *Broker) fetchFromIsanndGate(w http.ResponseWriter, r *http.Request, endpoint string, emptyFallback []byte) {
	w.Header().Set("Content-Type", "application/json")
	base := b.Cfg.OutboundGateway.URL()
	url := base + "/internal/gate" + endpoint
	if q := r.URL.RawQuery; q != "" {
		sep := "?"
		if strings.Contains(endpoint, "?") {
			sep = "&"
		}
		url += sep + q
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, url, r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	for _, h := range []string{"If-None-Match", "Content-Type", "Accept"} {
		if v := r.Header.Get(h); v != "" {
			proxyReq.Header.Set(h, v)
		}
	}

	resp, err := http.DefaultClient.Do(proxyReq)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Isannd-Gate") == "unconfigured" && emptyFallback != nil {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(emptyFallback)
		return
	}

	for _, h := range []string{"ETag", "Cache-Control", "Content-Type"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Access-Control-Expose-Headers", "ETag")
	w.WriteHeader(resp.StatusCode)
	if resp.StatusCode != http.StatusNotModified {
		_, _ = io.Copy(w, resp.Body)
	}
}

// HandleAdminStatus returns monitoring data for broker mode.
func (b *Broker) HandleAdminStatus(w http.ResponseWriter, r *http.Request) {
	tunnel.AdminCORS(w)
	if r.Method == http.MethodOptions {
		return
	}

	b.CfgMu.RLock()
	cfg := b.Cfg
	b.CfgMu.RUnlock()

	json.NewEncoder(w).Encode(map[string]any{
		"uptime_seconds":    int(time.Since(b.StartTime).Seconds()),
		"mode":              string(cfg.Mode),
		"proxy_id":          b.NodeIdentity.Address,
		"listen_addr":       cfg.ListenAddr,
		"node_bridge_addr":  cfg.OutboundGateway.NodeBridgeAddr,
		"rv_control_addr":   cfg.OutboundGateway.RVControlAddr,
		"rendezvous_addr":   cfg.OutboundGateway.RendezvousAddr,
		"auth_mode":         b.Auth.Mode,
	})
}
