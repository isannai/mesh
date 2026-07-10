package control

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/daesob/http3proxy/pkg/glog"
	"github.com/daesob/http3proxy/pkg/setup"
)

// SearchIndex caches RV /v1/nodes data and exposes typed lookups for the
// marketplace search. It is lazy-loaded on first /v1/search/nodes request and
// refreshed using ETag — same pattern as proxyToRendezvous.
//
// The index is rebuilt from scratch on every refresh (no incremental updates)
// since RV /v1/nodes is small (one entry per online provider) and refreshes
// happen at most once per cacheTTL.
type SearchIndex struct {
	mu       sync.RWMutex
	cachedAt time.Time
	etag     string
	nodes    []searchNode
}

// cacheTTL: how long the index is reused before another RV fetch is attempted.
// Heartbeat cadence is 5-10s, so 5s gives near-real-time matches without
// hammering RV on every keystroke. Caller flow already throttles by requiring
// explicit Search trigger (Enter / button) so concurrent requests are rare.
const searchCacheTTL = 5 * time.Second

// searchNode is the flattened per-node view used by the index. We keep only
// the fields the marketplace UI cares about; the full RV response shape is
// preserved in the byID map for /v1/search/nodes responses that want to
// echo back richer data.
type searchNode struct {
	ID           string
	OwnerAddress string
	Online       bool
	Hardware     *searchHardware
	Services     []searchService
	// Raw is the full /v1/nodes entry for this node, decoded as a generic
	// map so we can pass through fields the index doesn't care about
	// (e.g. tpm_verified, ek_cert_issuer) when echoing results.
	Raw map[string]interface{}
}

type searchHardware struct {
	GPU      string `json:"gpu,omitempty"`
	GPUModel string `json:"gpu_model,omitempty"`
	VRAMGB   int    `json:"vram_gb,omitempty"`
	GPUCount int    `json:"gpu_count,omitempty"`
}

type searchService struct {
	Name           string            `json:"name,omitempty"`
	Type           string            `json:"type,omitempty"`
	Model          string            `json:"model,omitempty"`
	ModelHash      string            `json:"model_hash,omitempty"`
	ModelOriginURL string            `json:"model_origin_url,omitempty"`
	Engine         string            `json:"engine,omitempty"`
	Inspect        map[string]string `json:"inspect,omitempty"`
	QueueDepth     int               `json:"queue_depth,omitempty"`
	TotalJobsDone  int               `json:"total_jobs_done,omitempty"`
	AvgJobSec      float64           `json:"avg_job_sec,omitempty"`
}

// SearchResult is what /v1/search/nodes returns to the browser. Mirrors the
// shape documented in docs/TODO/search page/search-design.md.
type SearchResult struct {
	DetectedType    string                   `json:"detected_type"`
	QueryNormalized string                   `json:"query_normalized,omitempty"`
	Matches         []map[string]interface{} `json:"matches"`
}

// detectQueryType applies regex tests in priority order. The first match
// wins; "text" is the default fallback when nothing else fits.
func detectQueryType(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return "text"
	}
	// EVM address: 0x + 40 hex.
	if matched, _ := regexp.MatchString(`(?i)^0x[a-f0-9]{40}$`, q); matched {
		return "address"
	}
	// SHA256 hash: 64 hex (with or without sha256: prefix).
	cleaned := strings.TrimPrefix(strings.ToLower(q), "sha256:")
	if matched, _ := regexp.MatchString(`^[a-f0-9]{64}$`, cleaned); matched {
		return "hash"
	}
	// Node ID convention: node-...
	if matched, _ := regexp.MatchString(`(?i)^node-[\w-]+$`, q); matched {
		return "node"
	}
	// Engine name (well-known list — extend as engines are added).
	switch strings.ToLower(q) {
	case "vllm", "sd.cpp", "llama.cpp", "sd-api", "llm-api":
		return "engine"
	}
	// GPU model heuristic: starts with RTX / GTX / A100 / H100 / etc.
	if matched, _ := regexp.MatchString(`(?i)^(rtx|gtx|a\d+|h\d+|tesla|titan)\s`, q); matched {
		return "gpu"
	}
	return "text"
}

// normalizeQuery strips prefixes / lowercases according to detected type so
// callers can compare against indexed values directly.
func normalizeQuery(q, qtype string) string {
	q = strings.TrimSpace(q)
	switch qtype {
	case "hash":
		return setup.NormalizeSHA256(q)
	case "address":
		return strings.ToLower(q)
	case "engine", "node", "gpu":
		return strings.ToLower(q)
	default:
		return strings.ToLower(q)
	}
}

// fetchRVNodes calls the configured rendezvous /v1/nodes endpoint. Mirrors
// the transport selection in proxyToRendezvous (HTTPS → http3, plain → http)
// so behavior matches the existing proxy path.
func (b *Broker) fetchRVNodes(ctx context.Context, etag string) (data []byte, newETag string, status int, err error) {
	return b.fetchRVPath(ctx, "/v1/nodes?role=provider", etag)
}

// fetchRVMetrics calls /v1/metrics for the volatile per-service metrics
// (queue_depth, total_jobs_done, avg_job_sec, status) that /v1/nodes
// intentionally strips. Same transport selection as fetchRVNodes.
func (b *Broker) fetchRVMetrics(ctx context.Context) ([]byte, error) {
	body, _, status, err := b.fetchRVPath(ctx, "/v1/metrics?role=provider", "")
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, nil
	}
	return body, nil
}

func (b *Broker) fetchRVPath(ctx context.Context, path string, etag string) (data []byte, newETag string, status int, err error) {
	b.CfgMu.RLock()
	bridgeBase := b.Cfg.OutboundGateway.URL()
	b.CfgMu.RUnlock()
	if bridgeBase == "" {
		return nil, "", http.StatusServiceUnavailable, nil
	}

	// Same routing as proxyToRendezvous — go through the local isannd
	// node-bridge listener at /internal/rv/* rather than dialing RV REST
	// directly. The bridge handles TLS termination + reverse proxy +
	// source-IP guard.
	url := bridgeBase + "/internal/rv" + path
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", 0, err
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, resp.StatusCode, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", 0, err
	}
	return body, resp.Header.Get("ETag"), resp.StatusCode, nil
}

// refreshIndex pulls /v1/nodes from RV and rebuilds the search index. Uses
// ETag to short-circuit when RV hasn't changed since the last refresh.
// Concurrent calls are safe; the second waits on the first via mu.
func (idx *SearchIndex) refresh(ctx context.Context, b *Broker) error {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	// Cache hit — another goroutine refreshed within TTL while we waited.
	if time.Since(idx.cachedAt) < searchCacheTTL && idx.nodes != nil {
		return nil
	}

	body, newETag, status, err := b.fetchRVNodes(ctx, idx.etag)
	if err != nil {
		return err
	}

	// /v1/nodes 304 path — static info (hardware / services list / ownership)
	// unchanged, but the volatile per-service metrics still need re-overlay.
	// Reuse the cached node skeletons and only refresh metrics. Without this
	// the search card's `total_jobs_done` / `queue_depth` stay frozen at the
	// first refresh whenever /v1/nodes ETag holds steady (which is the common
	// case — provider only re-registers on conf change or restart).
	if status == http.StatusNotModified {
		if idx.nodes != nil {
			if metricsBody, err := b.fetchRVMetrics(ctx); err == nil && len(metricsBody) > 0 {
				mergeMetricsIntoNodes(idx.nodes, metricsBody)
			}
		}
		idx.cachedAt = time.Now()
		return nil
	}
	if status != http.StatusOK {
		return nil
	}

	// /v1/nodes returns a JSON array of node objects.
	var raw []map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return err
	}
	nodes := make([]searchNode, 0, len(raw))
	for _, r := range raw {
		n := flattenNode(r)
		nodes = append(nodes, n)
	}

	// /v1/nodes is intentionally static — volatile per-service metrics
	// (queue_depth, total_jobs_done, avg_job_sec) live in /v1/metrics. We
	// fetch metrics in parallel-ish and merge into the per-service entries
	// so the search card stats row matches the my-nodes / nodes pages.
	if metricsBody, err := b.fetchRVMetrics(ctx); err == nil && len(metricsBody) > 0 {
		mergeMetricsIntoNodes(nodes, metricsBody)
	}

	idx.nodes = nodes
	idx.etag = newETag
	idx.cachedAt = time.Now()
	return nil
}

// mergeMetricsIntoNodes folds /v1/metrics per-service rows into the
// already-flattened nodes. /v1/metrics returns a flat row array — one
// entry per (node_id, service) pair — so we index by that composite
// key, then walk each node's services and copy the matching metrics.
func mergeMetricsIntoNodes(nodes []searchNode, metricsBody []byte) {
	var rows []map[string]interface{}
	if err := json.Unmarshal(metricsBody, &rows); err != nil {
		return
	}
	// key = nodeID + "\x00" + serviceName
	byKey := map[string]map[string]interface{}{}
	for _, row := range rows {
		nodeID, _ := row["node_id"].(string)
		svcName, _ := row["service"].(string)
		if nodeID == "" || svcName == "" {
			continue
		}
		byKey[nodeID+"\x00"+svcName] = row
	}
	for ni := range nodes {
		for si := range nodes[ni].Services {
			m := byKey[nodes[ni].ID+"\x00"+nodes[ni].Services[si].Name]
			if m == nil {
				continue
			}
			if v, ok := m["queue_depth"].(float64); ok {
				nodes[ni].Services[si].QueueDepth = int(v)
			}
			if v, ok := m["total_jobs_done"].(float64); ok {
				nodes[ni].Services[si].TotalJobsDone = int(v)
			}
			if v, ok := m["avg_job_sec"].(float64); ok {
				nodes[ni].Services[si].AvgJobSec = v
			}
		}
	}
}

// flattenNode extracts the marketplace-relevant fields from one /v1/nodes
// entry. Missing / unexpected fields silently default to zero values — this
// is best-effort projection, not strict validation.
//
// Field mapping mirrors web/control/src/pages/nodes/index.jsx parseGateNode:
//   - id        ← node_id || id
//   - hardware  ← gpus[] array (each {name, vram_gb}) — single string `gpu`
//                 was an older shape the existing code never used.
func flattenNode(raw map[string]interface{}) searchNode {
	id, _ := raw["node_id"].(string)
	if id == "" {
		id, _ = raw["id"].(string)
	}
	owner, _ := raw["owner_address"].(string)
	online, _ := raw["online"].(bool)

	n := searchNode{
		ID:           id,
		OwnerAddress: strings.ToLower(owner),
		Online:       online,
		Raw:          raw,
	}

	// Hardware can come in two shapes: a JSON object (struct in RV) or a
	// JSON-encoded string (legacy gate path). Decode the string form first.
	hwRaw := raw["hardware"]
	if s, ok := hwRaw.(string); ok && s != "" {
		var decoded map[string]interface{}
		if err := json.Unmarshal([]byte(s), &decoded); err == nil {
			hwRaw = decoded
		}
	}
	if hw, ok := hwRaw.(map[string]interface{}); ok {
		out := &searchHardware{}
		// Preferred: gpus[] array — first GPU defines the model+vram for
		// search purposes (multi-GPU systems are rare in this network).
		if gpus, ok := hw["gpus"].([]interface{}); ok && len(gpus) > 0 {
			out.GPUCount = len(gpus)
			if g0, ok := gpus[0].(map[string]interface{}); ok {
				if name, _ := g0["name"].(string); name != "" {
					out.GPU = name
					out.GPUModel = name
				}
				if v, ok := g0["vram_gb"].(float64); ok {
					out.VRAMGB = int(v)
				}
			}
		}
		// Fallback for older shape with single `gpu` string.
		if out.GPU == "" {
			if v, _ := hw["gpu"].(string); v != "" {
				out.GPU = v
				out.GPUModel = v
			}
		}
		if out.VRAMGB == 0 {
			if v, ok := hw["vram_gb"].(float64); ok {
				out.VRAMGB = int(v)
			}
		}
		if out.GPU != "" || out.VRAMGB > 0 {
			n.Hardware = out
		}
	}

	// Services can also arrive as JSON string. Decode first if so.
	svcsRaw := raw["services"]
	if s, ok := svcsRaw.(string); ok && s != "" {
		var decoded []interface{}
		if err := json.Unmarshal([]byte(s), &decoded); err == nil {
			svcsRaw = decoded
		}
	}
	if svcs, ok := svcsRaw.([]interface{}); ok {
		for _, s := range svcs {
			sm, ok := s.(map[string]interface{})
			if !ok {
				continue
			}
			ss := searchService{}
			ss.Name, _ = sm["name"].(string)
			ss.Type, _ = sm["type"].(string)
			ss.Model, _ = sm["model"].(string)
			if h, _ := sm["model_hash"].(string); h != "" {
				ss.ModelHash = setup.NormalizeSHA256(h)
			}
			ss.ModelOriginURL, _ = sm["model_origin_url"].(string)
			// Engine: prefer explicit field, fall back to service name.
			if e, _ := sm["engine"].(string); e != "" {
				ss.Engine = e
			} else {
				ss.Engine = ss.Name
			}
			// Inspect: pass through the per-service options map (ctx_size,
			// max_model_len, etc.) so the search UI can show config badges.
			if insp, ok := sm["inspect"].(map[string]interface{}); ok && len(insp) > 0 {
				flat := map[string]string{}
				for k, v := range insp {
					if s, ok := v.(string); ok {
						flat[k] = s
					}
				}
				if len(flat) > 0 {
					ss.Inspect = flat
				}
			}
			// Per-service runtime stats — used by the search card to show
			// pending / done / avg latency rolled up across services.
			if v, ok := sm["queue_depth"].(float64); ok {
				ss.QueueDepth = int(v)
			}
			if v, ok := sm["total_jobs_done"].(float64); ok {
				ss.TotalJobsDone = int(v)
			}
			if v, ok := sm["avg_job_sec"].(float64); ok {
				ss.AvgJobSec = v
			}
			n.Services = append(n.Services, ss)
		}
	}

	return n
}

// search applies type-specific matching against the cached index. Always
// returns a non-nil slice (empty when no matches). Returns "match objects"
// shaped for the marketplace UI (search-design.md schema) — node_id, gpu,
// matched_models, match_type, queue, etc. — not the raw /v1/nodes entry.
// The UI only needs the convenience fields; passing raw blobs forced the
// frontend to know RV's internal shape, which led to empty cards when the
// keys didn't match.
func (idx *SearchIndex) search(qtype, q string) []map[string]interface{} {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	out := make([]map[string]interface{}, 0)
	if q == "" {
		// Empty query — return all online nodes. Useful for default browse.
		for _, n := range idx.nodes {
			out = append(out, buildMatch(n, qtype, q))
		}
		return out
	}
	for _, n := range idx.nodes {
		if matchNode(n, qtype, q) {
			out = append(out, buildMatch(n, qtype, q))
		}
	}
	return out
}

// buildMatch synthesizes a UI-friendly match object from the indexed node.
// Pulls out the fields the marketplace search results page reads
// (node_id, owner_address, gpu, engine, matched_models, ...) so the UI
// doesn't have to dig into the raw /v1/nodes blob.
//
// `matched_models` is the subset of services that triggered the hit — for
// hash queries it's just the service whose model_hash matched; for other
// query types it's all loaded models on the node.
func buildMatch(n searchNode, qtype, q string) map[string]interface{} {
	m := map[string]interface{}{
		"node_id":       n.ID,
		"owner_address": n.OwnerAddress,
		"online":        n.Online,
		"match_type":    matchTypeFor(qtype),
	}

	if n.Hardware != nil {
		gpu := map[string]interface{}{}
		if n.Hardware.GPU != "" {
			gpu["model"] = n.Hardware.GPU
		}
		if n.Hardware.VRAMGB > 0 {
			gpu["vram_gb"] = n.Hardware.VRAMGB
		}
		if n.Hardware.GPUCount > 0 {
			gpu["count"] = n.Hardware.GPUCount
		}
		if len(gpu) > 0 {
			m["gpu"] = gpu
		}
	}

	// Service-derived fields. For hash queries we filter to just the
	// service that matched; everything else gets all loaded models.
	loaded := []map[string]interface{}{}
	matched := []map[string]interface{}{}
	var firstEngine string
	for _, s := range n.Services {
		entry := map[string]interface{}{}
		if s.Model != "" {
			entry["name"] = s.Model
		}
		if s.ModelHash != "" {
			entry["hash"] = s.ModelHash
		}
		if s.ModelOriginURL != "" {
			entry["model_origin_url"] = s.ModelOriginURL
		}
		if s.Engine != "" {
			entry["engine"] = s.Engine
		}
		if s.Type != "" {
			entry["type"] = s.Type
		}
		if s.Name != "" {
			entry["service"] = s.Name
		}
		if len(s.Inspect) > 0 {
			entry["inspect"] = s.Inspect
		}
		if len(entry) == 0 {
			continue
		}
		loaded = append(loaded, entry)
		if firstEngine == "" && s.Engine != "" {
			firstEngine = s.Engine
		}
		if qtype == "hash" && s.ModelHash == q {
			matched = append(matched, entry)
		}
	}
	if firstEngine != "" {
		m["engine"] = firstEngine
	}
	if len(loaded) > 0 {
		m["loaded_models"] = loaded
	}
	if qtype == "hash" {
		m["matched_models"] = matched
	} else {
		// For non-hash queries every loaded model is conceptually "matched".
		m["matched_models"] = loaded
	}

	// Aggregated runtime stats across all services on this node — pending
	// queue, jobs done, weighted-average job seconds. UI shows them as
	// "X pending · N done · avg Y" on the search card.
	pending, done := 0, 0
	weightedAvg := 0.0
	for _, s := range n.Services {
		pending += s.QueueDepth
		done += s.TotalJobsDone
		if s.AvgJobSec > 0 && s.TotalJobsDone > 0 {
			weightedAvg += s.AvgJobSec * float64(s.TotalJobsDone)
		}
	}
	stats := map[string]interface{}{}
	if pending > 0 || done > 0 {
		stats["pending"] = pending
		stats["done"] = done
		if done > 0 && weightedAvg > 0 {
			stats["avg_sec"] = weightedAvg / float64(done)
		}
	}
	if len(stats) > 0 {
		m["stats"] = stats
	}

	// Pass-through useful fields from the raw RV entry. Skip if missing —
	// older providers may not emit them.
	if v, ok := n.Raw["status"].(string); ok && v != "" {
		m["status"] = v
	}
	if v, ok := n.Raw["conn_status"].(string); ok && v != "" {
		m["conn_status"] = v
	}
	if v, ok := n.Raw["tpm_verified"].(bool); ok && v {
		m["tpm_verified"] = true
	}
	if v, ok := n.Raw["ek_cert_issuer"].(string); ok && v != "" {
		m["ek_cert_issuer"] = v
	}
	if v, ok := n.Raw["emblem"].(string); ok && v != "" {
		m["emblem"] = v
	}
	// started_at is the node's first-register timestamp (RFC3339). Lets the
	// broker UI compute uptime client-side without an extra RV roundtrip.
	if v, ok := n.Raw["started_at"].(string); ok && v != "" {
		m["started_at"] = v
	}
	return m
}

// matchTypeFor maps the query type onto the design-doc's match_type values.
// "hash" → "hash_exact" (we only return exact matches; fuzzy hash hits don't
// exist). Everything else maps 1:1.
func matchTypeFor(qtype string) string {
	if qtype == "hash" {
		return "hash_exact"
	}
	return qtype
}

func matchNode(n searchNode, qtype, q string) bool {
	switch qtype {
	case "address":
		return strings.EqualFold(n.OwnerAddress, q)
	case "node":
		return strings.EqualFold(n.ID, q)
	case "hash":
		for _, s := range n.Services {
			if s.ModelHash == q {
				return true
			}
		}
		return false
	case "engine":
		for _, s := range n.Services {
			if strings.EqualFold(s.Engine, q) || strings.EqualFold(s.Name, q) || strings.EqualFold(s.Type, q) {
				return true
			}
		}
		return false
	case "gpu":
		if n.Hardware == nil {
			return false
		}
		return strings.Contains(strings.ToLower(n.Hardware.GPU), q)
	default:
		// "text" — fuzzy match across model name / node id / owner / gpu / engine.
		ql := strings.ToLower(q)
		if strings.Contains(strings.ToLower(n.ID), ql) {
			return true
		}
		if strings.Contains(n.OwnerAddress, ql) {
			return true
		}
		if n.Hardware != nil && strings.Contains(strings.ToLower(n.Hardware.GPU), ql) {
			return true
		}
		for _, s := range n.Services {
			if strings.Contains(strings.ToLower(s.Model), ql) {
				return true
			}
			if strings.Contains(strings.ToLower(s.Name), ql) {
				return true
			}
		}
		return false
	}
}

// handleSearchNodes serves GET /v1/search/nodes — the unified marketplace
// search endpoint. Type detection is automatic when ?type=auto (default) and
// can be overridden via the type param.
//
// Limit caps the result count (default 50, max 200) so a wildcard search on
// a busy broker doesn't dump the entire node table.
func (b *Broker) handleSearchNodes(idx *SearchIndex) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		qtype := r.URL.Query().Get("type")
		if qtype == "" || qtype == "auto" {
			qtype = detectQueryType(q)
		}
		normalized := normalizeQuery(q, qtype)

		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		defer cancel()
		if err := idx.refresh(ctx, b); err != nil {
			// Refresh failure is non-fatal — serve stale data if any. This
			// keeps the UI responsive when RV is briefly down.
			b.Log.Log(glog.Error, "[control] search index refresh: %v", err)
		}

		matches := idx.search(qtype, normalized)

		// Enforce limit so callers can paginate / cap large result sets.
		limit := 50
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > 200 {
			limit = 200
		}
		if len(matches) > limit {
			matches = matches[:limit]
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(SearchResult{
			DetectedType:    qtype,
			QueryNormalized: normalized,
			Matches:         matches,
		})
	}
}
