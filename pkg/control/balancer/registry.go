// Package balancer implements broker-side node registry + routing.
//
// Architecture:
//
//	RV (rendezvous) is the source of truth. The broker never queries it in
//	the hot path. Two background goroutines keep a local Registry in sync:
//
//	  staticPoller   GET /v1/nodes          every 30s (ETag-friendly)
//	  metricsPoller  GET /v1/nodes/metrics  every 1s
//
//	Game requests resolve against the local Registry (<1ms), then the
//	SlotTracker reserves a concurrent slot on the picked node before the
//	broker forwards the call over the existing QUIC tunnel.
package balancer

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Status mirrors heartbeatpb.Status as a short string form used by the HTTP
// layer. Lower-cased to match what RV /v1/nodes/metrics emits.
type Status string

const (
	StatusUnknown Status = ""
	StatusIdle    Status = "idle"
	StatusBusy    Status = "busy"
	StatusLoading Status = "loading"
	StatusStopped Status = "stopped"
)

// GPUSpec / CPUSpec / RAMSpec are the static subset consumed by routing.
// We deliberately keep only fields that affect node selection; anything
// else stays in the raw JSON for UI consumption.
type GPUSpec struct {
	Name        string  `json:"name,omitempty"`
	VramTotalGB float64 `json:"vram_total_gb,omitempty"`
}

type HardwareSpec struct {
	CPUName string    `json:"cpu_name,omitempty"`
	GPUs    []GPUSpec `json:"gpus,omitempty"`
	RAMGB   float64   `json:"ram_gb,omitempty"`
}

// ServiceStatic is the routing-relevant static info for one service on a node.
type ServiceStatic struct {
	Version   string
	Model     string
	ModelHash string
	MaxSlots  int32 // 동시 처리 워커 수 (= ServiceInfo.Concurrency); 0 → treat as 1
	MaxQueue  int32 // pending+running 합계 한도 (= ServiceInfo.MaxQueue); 0 = unlimited
	Ready     bool  // model loaded (server_ready)
}

// NodeStatic is the slowly-changing half of the node record. Cloned on write
// so concurrent readers see a stable snapshot without locks.
type NodeStatic struct {
	ID           string
	Role         string
	Addr         string
	OwnerAddress string
	Version      string
	CertHash     string
	Hardware     HardwareSpec
	Services     map[string]*ServiceStatic
}

// ServiceMetric is the per-service volatile state derived from UDP heartbeats.
type ServiceMetric struct {
	Status        Status
	QueueDepth    uint32
	RunningCount  uint32 // 동시 실행 중인 작업 수 (heartbeat: running_count)
	TotalJobsDone uint64
	AvgJobSec     float32
	LastJobMs     int64
	RunningJobID  string
	UpdatedAt     time.Time
}

// NodeMetrics bundles live state for one node.
type NodeMetrics struct {
	ID       string
	Online   bool
	LastSeen time.Time
	Services map[string]*ServiceMetric
}

// Registry holds both halves keyed by nodeID. Designed for frequent reads
// and occasional writes (30s static, 1s metrics).
type Registry struct {
	static  sync.Map // nodeID → *NodeStatic
	metrics sync.Map // nodeID → *NodeMetrics

	// Optional indexes for fast candidate lookup. Rebuilt after every
	// static refresh. O(1) on the hot path; O(N) on the refresh path
	// (which runs out-of-band every 30s).
	idxMu     sync.RWMutex
	byService map[string][]string            // service_name → [nodeID,...]
	byModel   map[string]map[string][]string // service_name → model → [nodeID,...]

	// version bumps on each static refresh so callers can detect stale
	// snapshot references if they hold them beyond a single request.
	version atomic.Uint64
}

// NewRegistry returns an empty registry with initialized indexes.
func NewRegistry() *Registry {
	return &Registry{
		byService: make(map[string][]string),
		byModel:   make(map[string]map[string][]string),
	}
}

// ReplaceStatic atomically replaces the static half with the supplied set.
// Nodes absent from `nodes` are removed. Indexes are rebuilt.
func (r *Registry) ReplaceStatic(nodes []*NodeStatic) {
	// Build new maps up front to minimize write lock time.
	fresh := make(map[string]*NodeStatic, len(nodes))
	byService := make(map[string][]string)
	byModel := make(map[string]map[string][]string)

	for _, n := range nodes {
		fresh[n.ID] = n
		for svcName, svc := range n.Services {
			byService[svcName] = append(byService[svcName], n.ID)
			model := svc.Model
			if byModel[svcName] == nil {
				byModel[svcName] = make(map[string][]string)
			}
			if model != "" {
				byModel[svcName][model] = append(byModel[svcName][model], n.ID)
			}
		}
	}

	// Replace static entries: delete missing, upsert present.
	seen := make(map[string]bool, len(fresh))
	for id, n := range fresh {
		r.static.Store(id, n)
		seen[id] = true
	}
	r.static.Range(func(k, _ any) bool {
		id := k.(string)
		if !seen[id] {
			r.static.Delete(id)
			// also purge metrics for dropped nodes
			r.metrics.Delete(id)
		}
		return true
	})

	r.idxMu.Lock()
	r.byService = byService
	r.byModel = byModel
	r.idxMu.Unlock()

	r.version.Add(1)
}

// ReplaceMetrics atomically replaces the metrics half with the supplied set.
// Metrics for nodes absent from `metrics` are removed (treated as offline).
func (r *Registry) ReplaceMetrics(metrics []*NodeMetrics) {
	seen := make(map[string]bool, len(metrics))
	for _, m := range metrics {
		r.metrics.Store(m.ID, m)
		seen[m.ID] = true
	}
	r.metrics.Range(func(k, _ any) bool {
		id := k.(string)
		if !seen[id] {
			r.metrics.Delete(id)
		}
		return true
	})
}

// GetStatic returns a node's static snapshot (or nil if unknown).
func (r *Registry) GetStatic(nodeID string) *NodeStatic {
	v, ok := r.static.Load(nodeID)
	if !ok {
		return nil
	}
	return v.(*NodeStatic)
}

// GetMetrics returns a node's live metrics snapshot (or nil).
func (r *Registry) GetMetrics(nodeID string) *NodeMetrics {
	v, ok := r.metrics.Load(nodeID)
	if !ok {
		return nil
	}
	return v.(*NodeMetrics)
}

// Query expresses the node-selection criteria for routing.
type Query struct {
	Service string // e.g. "llm-api" — required
	Model   string // optional: exact match on ServiceStatic.Model
	GPU     string // optional: substring match on hardware GPU name (e.g. "4070")
	Status  Status // optional: only nodes whose service metric matches
	MinVRAM float64
}

// Find returns node IDs matching q. Cheap (index lookup + linear filter).
func (r *Registry) Find(q Query) []string {
	if q.Service == "" {
		return nil
	}

	// Candidate set from service (and optional model) index.
	r.idxMu.RLock()
	var candidates []string
	if q.Model != "" {
		if byModel := r.byModel[q.Service]; byModel != nil {
			candidates = append(candidates, byModel[q.Model]...)
		}
	} else {
		candidates = append(candidates, r.byService[q.Service]...)
	}
	r.idxMu.RUnlock()

	if len(candidates) == 0 {
		return nil
	}

	out := candidates[:0]
	for _, id := range candidates {
		ns := r.GetStatic(id)
		if ns == nil {
			continue
		}
		svc, ok := ns.Services[q.Service]
		if !ok || !svc.Ready {
			continue
		}
		if q.GPU != "" && !matchGPU(ns.Hardware.GPUs, q.GPU) {
			continue
		}
		if q.MinVRAM > 0 && maxVRAM(ns.Hardware.GPUs) < q.MinVRAM {
			continue
		}
		if q.Status != StatusUnknown {
			nm := r.GetMetrics(id)
			if nm == nil {
				continue
			}
			sm := nm.Services[q.Service]
			if sm == nil || sm.Status != q.Status {
				continue
			}
		}
		out = append(out, id)
	}
	return out
}

// Version returns the monotonic counter bumped on each static refresh.
// Callers may use it to invalidate derived caches.
func (r *Registry) Version() uint64 {
	return r.version.Load()
}

func matchGPU(gpus []GPUSpec, needle string) bool {
	needle = strings.ToLower(needle)
	for _, g := range gpus {
		if strings.Contains(strings.ToLower(g.Name), needle) {
			return true
		}
	}
	return false
}

func maxVRAM(gpus []GPUSpec) float64 {
	var max float64
	for _, g := range gpus {
		if g.VramTotalGB > max {
			max = g.VramTotalGB
		}
	}
	return max
}
