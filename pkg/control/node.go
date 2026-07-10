package control

import (
	"net/http"
	"strings"
	"time"

	"github.com/isannai/mesh/pkg/control/apipolicy"
	"github.com/daesob/http3proxy/pkg/glog"
)

// extractSubscribePollFromSvcRest peels /v1/jobs/subscribe/{id} or
// /v1/jobs/poll/{id} out of the per-service rest path (the part after
// /svc/{svcName}). Returns the jobID + true on match; "", false otherwise.
// Used by handleNodeProxy to rewrite legacy progress URLs onto the
// provider's JobsHandler at /v1/jobs/{id}.
func extractSubscribePollFromSvcRest(svcRest string) (string, bool) {
	for _, prefix := range []string{"/v1/jobs/subscribe/", "/v1/jobs/poll/"} {
		if strings.HasPrefix(svcRest, prefix) {
			jobID := strings.TrimPrefix(svcRest, prefix)
			if jobID != "" {
				return jobID, true
			}
		}
	}
	return "", false
}

func (b *Broker) handleNodeProxy(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/node/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "invalid path: expected /node/{nodeID}/{service}/...", http.StatusBadRequest)
		return
	}

	nodeID := parts[0]
	rest := "/" + parts[1]

	// service is the first segment of rest for the policy gate (e.g.
	// "installer", "provider", "svc"). Owner sigs already bypassed in
	// auth middleware; reaching here means the per-service feature flag
	// applies.
	serviceParts := strings.SplitN(parts[1], "/", 2)
	service := serviceParts[0]
	if feature := apipolicy.NodeProxyFeature(service); feature != "" {
		if !b.Policy().IsEnabled(feature) {
			http.Error(w, `{"error":"feature_disabled","feature":"`+feature+`"}`, http.StatusForbidden)
			return
		}
	}

	// Legacy SSE/poll URL rewrite. broker UI calls
	//   /node/{id}/svc/{svcName}/v1/jobs/subscribe/{jobID}   (was SSE)
	//   /node/{id}/svc/{svcName}/v1/jobs/poll/{jobID}        (was 1-shot poll)
	// In the new architecture broker is a thin reverse proxy; the
	// provider's JobsHandler owns progress at /v1/jobs/{jobID}. Strip
	// the svc/{name} prefix + subscribe/poll suffix and let the proxy
	// hit the provider HTTP server directly.
	if service == "svc" && len(serviceParts) == 2 {
		svcRest := "/" + strings.SplitN(serviceParts[1], "/", 2)[1] // strip {svcName}/
		if jobID, ok := extractSubscribePollFromSvcRest(svcRest); ok {
			rest = "/v1/jobs/" + jobID
		}
	}

	start := time.Now()
	b.Log.Log(glog.Request, "[control] → %s /node/.../%s", r.Method, rest)

	// Convert nodeID prefix to lowercase for isannd's outbound routing
	// (it expects "s:0x..." / "c:0x...").
	pColon := strings.SplitN(nodeID, ":", 2)
	if len(pColon) != 2 {
		http.Error(w, "invalid nodeID format", http.StatusBadRequest)
		return
	}
	isanndNodeID := strings.ToLower(pColon[0]) + ":" + pColon[1]

	isanndBase := b.Cfg.OutboundGateway.URL()
	if isanndBase == "" {
		http.Error(w, "outbound_gateway.addr not configured", http.StatusInternalServerError)
		return
	}
	targetURL := isanndBase + "/node/" + isanndNodeID + rest
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, r.Body)
	if err != nil {
		http.Error(w, "node proxy request: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for k, vv := range r.Header {
		// Skip hop-by-hop headers; Host is reset by http.NewRequest.
		if k == "Connection" || k == "Keep-Alive" || k == "Proxy-Authenticate" ||
			k == "Proxy-Authorization" || k == "Te" || k == "Trailer" ||
			k == "Transfer-Encoding" || k == "Upgrade" {
			continue
		}
		for _, v := range vv {
			proxyReq.Header.Add(k, v)
		}
	}

	resp, err := http.DefaultClient.Do(proxyReq)
	if err != nil {
		http.Error(w, `{"error":"node proxy: `+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Stream-friendly copy — flush on every chunk so SSE responses don't
	// buffer.
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

	b.Log.Log(glog.Request, "[control] ← %s /node/.../%s (%dms)", r.Method, rest, time.Since(start).Milliseconds())
}
