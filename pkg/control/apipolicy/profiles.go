package apipolicy

// Presets — bulk on/off recipes the owner can apply from Settings → API.
//
// Settings UI shows individual feature toggles (cards.json style); presets
// are convenience buttons that flip many toggles at once.
var Presets = map[string]map[string]bool{
	// Public broker — node management endpoints stay open because per-node
	// wallet auth on the provider side already gates them.
	"central": {
		FeatureInfo:               true,
		FeatureNodeDiscovery:      true,
		FeatureGateProxy:          true,
		FeatureAuthVerify:         true,
		FeatureMyNodes:            true,
		FeaturePipeline:           true,
		FeatureNodeProxySvc:       true,
		FeatureNodeProxyProvider:  true,
		FeatureNodeProxyInstaller: true,
		FeatureNodeProxyService:   true,
	},
	// Local agent — full access.
	"personal": {
		FeatureInfo:               true,
		FeatureNodeDiscovery:      true,
		FeatureGateProxy:          true,
		FeatureAuthVerify:         true,
		FeatureMyNodes:            true,
		FeaturePipeline:           true,
		FeatureNodeProxySvc:       true,
		FeatureNodeProxyProvider:  true,
		FeatureNodeProxyInstaller: true,
		FeatureNodeProxyService:   true,
	},
}

// DefaultPreset is what a fresh broker boots with when api_features is
// missing entirely. "central" is the safer choice — opt-in is required for
// new features.
const DefaultPreset = "central"
