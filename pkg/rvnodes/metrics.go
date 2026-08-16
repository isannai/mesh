package rvnodes

// metrics.go — the RV's per-service volatile view (`/v1/metrics`).
//
// /v1/nodes deliberately carries only static facts; anything that moves between
// registers was split out here so the two views can never disagree. That split
// is why a caller who needs to know whether a node is BUSY RIGHT NOW has to read
// this endpoint as well — the directory will never tell it.
//
// Two questions are answered here, and both matter to a prober:
//
//	avg_job_sec   how long this node actually takes, measured by the node
//	running/queue whether it is mid-job at this instant
//
// The first is the only honest basis for a response deadline. A fixed one
// expresses our patience, not the node's health: a 4GB card measured at 371s per
// image was being cut off at 300s, so every shot recorded a timeout against a
// node that was drawing the picture correctly and did in fact finish it.
//
// The second stops us stacking work on a node that has not finished the last
// thing we asked for.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ServiceMetric is one row of /v1/metrics.
//
// The RV emits a row per (node, service) plus a bare per-node row with an empty
// Service carrying only connection liveness — hence the Service check in Get.
type ServiceMetric struct {
	NodeID  string `json:"node_id"`
	Service string `json:"service"`
	// Status is the engine's own word: "idle" / "busy". Not relied on for the
	// busy decision — RunningCount and QueueDepth are the numbers behind it and
	// they cannot disagree with themselves.
	Status        string  `json:"status,omitempty"`
	QueueDepth    int     `json:"queue_depth,omitempty"`
	RunningCount  int     `json:"running_count,omitempty"`
	TotalJobsDone int     `json:"total_jobs_done,omitempty"`
	AvgJobSec     float64 `json:"avg_job_sec,omitempty"`
	RunningJobID  string  `json:"running_job_id,omitempty"`
}

// There is deliberately no Busy() here any more. These numbers come from the
// RV's heartbeat, and one image job outlasts the heartbeat interval — by the
// time this snapshot reads "idle" the node's queue has turned over completely.
// "Is it busy right now" is asked of the node itself, per shot, over the P2P
// path (probe.Firer.QueueStats). What this poll is still good for is sizing:
// AvgJobSec says how long that node's work usually takes.

// Metrics is a lookup over one /v1/metrics poll.
type Metrics struct {
	byKey map[string]ServiceMetric
}

// metricKey normalises the lookup key.
//
// 🔴 Node ids arrive in MIXED CASE — the RV echoes the checksummed EOA on some
// rows ("S:0x8fF81256…") and the lowercase form on others, in the SAME response.
// A case-sensitive map silently misses half of them, and the symptom is simply
// that no metric is ever found: deadlines quietly fall back and the busy check
// quietly passes. Nothing errors.
func metricKey(nodeID, service string) string {
	return strings.ToLower(strings.TrimSpace(nodeID)) + "|" + strings.ToLower(strings.TrimSpace(service))
}

// Get returns the metric for one node's service. ok=false when the RV has not
// heard anything about it — a caller must treat that as "unknown", never as
// "idle" or "fast".
func (m *Metrics) Get(nodeID, service string) (ServiceMetric, bool) {
	if m == nil || m.byKey == nil {
		return ServiceMetric{}, false
	}
	v, ok := m.byKey[metricKey(nodeID, service)]
	return v, ok
}

// FetchMetrics reads /v1/metrics through the same isannd node-bridge Fetch uses.
//
// Loopback-guarded and session-free — this path is one of the two named
// exceptions in isannd's internal auth gate, alongside /v1/nodes. See Fetch.
func FetchMetrics(isanndURL string) (*Metrics, error) {
	endpoint := strings.TrimRight(isanndURL, "/") + bridgePath + "/v1/metrics"
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	c := &http.Client{Timeout: fetchTimeout}
	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("metrics: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("metrics: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics: %s: %s", resp.Status, snippet(body))
	}
	return DecodeMetrics(body)
}

// DecodeMetrics parses a /v1/metrics body. Same two shapes as /v1/nodes — a
// bare array, or wrapped once paging is in play.
func DecodeMetrics(body []byte) (*Metrics, error) {
	var rows []ServiceMetric
	trimmed := strings.TrimLeft(string(body), " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(body, &rows); err != nil {
			return nil, fmt.Errorf("decode metric array: %w", err)
		}
	} else {
		var paged struct {
			Metrics []ServiceMetric `json:"metrics"`
			Nodes   []ServiceMetric `json:"nodes"`
		}
		if err := json.Unmarshal(body, &paged); err != nil {
			return nil, fmt.Errorf("decode metric page: %w", err)
		}
		rows = paged.Metrics
		if len(rows) == 0 {
			rows = paged.Nodes
		}
	}
	m := &Metrics{byKey: make(map[string]ServiceMetric, len(rows))}
	for _, r := range rows {
		if strings.TrimSpace(r.Service) == "" {
			// The per-node liveness row. It has no service to key on and
			// carries none of the numbers this package exists to read.
			continue
		}
		m.byKey[metricKey(r.NodeID, r.Service)] = r
	}
	return m, nil
}
