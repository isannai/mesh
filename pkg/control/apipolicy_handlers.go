package control

import (
	"encoding/json"
	"log"
	"net/http"

	"github.com/isannai/mesh/pkg/control/apipolicy"
	"github.com/daesob/http3proxy/pkg/tunnel"
)

// handleAPIPolicy returns the current effective API policy. Public — clients
// use it to hide UI features that won't reach the server.
//
// GET /v1/api/policy
//
//	→ {"enabled_features":["info","node_discovery",...], "all_features":[...]}
func (b *Broker) handleAPIPolicy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	pol := b.Policy()
	json.NewEncoder(w).Encode(map[string]any{
		"enabled_features": pol.EnabledFeatures(),
		"all_features":     apipolicy.AllFeatures,
		"presets":          presetNames(),
	})
}

// handleAdminAPIFeatures persists per-feature toggles. Owner-only via the
// existing /v1/admin/* middleware gate. Body shape mirrors /v1/admin/cards:
//
//	{"features": {"feature_name": {"enabled": true}, ...}}
func (b *Broker) handleAdminAPIFeatures(w http.ResponseWriter, r *http.Request) {
	tunnel.AdminCORS(w)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPut {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Features map[string]tunnel.CardConfig `json:"features"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	if req.Features == nil {
		req.Features = map[string]tunnel.CardConfig{}
	}
	b.CfgMu.Lock()
	b.Cfg.APIFeatures = req.Features
	cfg := b.Cfg
	b.CfgMu.Unlock()
	rt := tunnel.RuntimeOverride{Cards: cfg.Cards, APIFeatures: cfg.APIFeatures}
	if err := tunnel.SaveRuntime(cfg, rt); err != nil {
		log.Printf("[control] api-features save failed: %v", err)
		http.Error(w, `{"error":"save failed: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	b.rebuildPolicy()
	json.NewEncoder(w).Encode(map[string]any{
		"status":   "ok",
		"features": req.Features,
	})
}

// handleAdminAPIPreset bulk-applies one of the known presets (central /
// personal). Convenience for the Settings UI's preset buttons.
//
// POST /v1/admin/api-features/preset  body={"name":"central"}
func (b *Broker) handleAdminAPIPreset(w http.ResponseWriter, r *http.Request) {
	tunnel.AdminCORS(w)
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	preset, ok := apipolicy.Presets[req.Name]
	if !ok {
		http.Error(w, `{"error":"unknown preset"}`, http.StatusBadRequest)
		return
	}
	features := make(map[string]tunnel.CardConfig, len(preset))
	for k, v := range preset {
		features[k] = tunnel.CardConfig{Enabled: v}
	}
	b.CfgMu.Lock()
	b.Cfg.APIFeatures = features
	cfg := b.Cfg
	b.CfgMu.Unlock()
	rt := tunnel.RuntimeOverride{Cards: cfg.Cards, APIFeatures: cfg.APIFeatures}
	if err := tunnel.SaveRuntime(cfg, rt); err != nil {
		log.Printf("[control] api-features preset save failed: %v", err)
		http.Error(w, `{"error":"save failed: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	b.rebuildPolicy()
	json.NewEncoder(w).Encode(map[string]any{
		"status":   "ok",
		"preset":   req.Name,
		"features": features,
	})
}

func presetNames() []string {
	out := make([]string, 0, len(apipolicy.Presets))
	for k := range apipolicy.Presets {
		out = append(out, k)
	}
	return out
}
