package apipolicy

// FeatureToggle is the per-feature on/off entry stored in broker.json.
// Same shape as tunnel.CardConfig but kept local to avoid an import cycle.
type FeatureToggle struct {
	Enabled bool `json:"enabled"`
}

// Policy is an immutable snapshot of which features are enabled.
type Policy struct {
	enabled map[string]bool
}

// New builds a Policy from the raw config map (key = feature name).
//
// Resolution rules:
//   - If features map has the feature → use its Enabled flag
//   - Else fall back to the DefaultPreset's value
//   - FeatureInfo is force-enabled (health/info must always work)
func New(features map[string]FeatureToggle) *Policy {
	preset := Presets[DefaultPreset]
	enabled := make(map[string]bool, len(AllFeatures))
	for _, f := range AllFeatures {
		if v, ok := features[f]; ok {
			enabled[f] = v.Enabled
		} else if pv, ok := preset[f]; ok {
			enabled[f] = pv
		}
	}
	enabled[FeatureInfo] = true
	return &Policy{enabled: enabled}
}

// IsEnabled reports whether a given feature is currently active.
// Unknown feature names return false (fail-closed).
func (p *Policy) IsEnabled(feature string) bool {
	if feature == "" {
		return true // ungated routes always pass
	}
	return p.enabled[feature]
}

// IsRouteAllowed maps a request to its feature and returns whether it is
// allowed under the current policy. Routes with no feature are allowed.
func (p *Policy) IsRouteAllowed(method, path string) bool {
	f := FeatureForRoute(method, path)
	return p.IsEnabled(f)
}

// EnabledFeatures returns the list of enabled feature names in stable order.
func (p *Policy) EnabledFeatures() []string {
	out := make([]string, 0, len(p.enabled))
	for _, f := range AllFeatures {
		if p.enabled[f] {
			out = append(out, f)
		}
	}
	return out
}
