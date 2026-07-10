// Package apipolicy implements feature-based API gating for the broker.
//
// Each broker deployment (central / personal) has a different set of
// features that should be exposed. Features are semantic groups of routes
// (e.g. "node_proxy_installer" → /node/{id}/installer) so config stays
// stable when paths are refactored, and missing-feature defaults are
// fail-closed (new features are disabled until a profile opts them in).
//
// See docs/confirm/broker-config/api-policy.md for the full design.
package apipolicy

// Feature names. Keep in sync with docs/confirm/broker-config/api-policy.md §4.
const (
	FeatureInfo               = "info"
	FeatureNodeDiscovery      = "node_discovery"
	FeatureGateProxy          = "gate_proxy"
	FeatureAuthVerify         = "auth_verify"
	FeatureMyNodes            = "my_nodes"
	FeaturePipeline           = "pipeline"
	FeatureNodeProxySvc       = "node_proxy_svc"
	FeatureNodeProxyProvider  = "node_proxy_provider"
	FeatureNodeProxyInstaller = "node_proxy_installer"
	FeatureNodeProxyService   = "node_proxy_service" // /node/{id}/service/{name}/{action} — start/stop lifecycle
)

// AllFeatures lists every feature in stable order. Used for /v1/api/policy.
var AllFeatures = []string{
	FeatureInfo,
	FeatureNodeDiscovery,
	FeatureGateProxy,
	FeatureAuthVerify,
	FeatureMyNodes,
	FeaturePipeline,
	FeatureNodeProxySvc,
	FeatureNodeProxyProvider,
	FeatureNodeProxyInstaller,
	FeatureNodeProxyService,
}

// RouteSpec maps an HTTP route to a feature. Method "*" matches any method.
// Path with trailing "/" matches by prefix.
type RouteSpec struct {
	Method string
	Path   string
}

// FeatureRoutes lists the static (top-level) routes per feature.
//
// /node/{id}/{service}/* is split per-service inside the node-proxy handler
// instead of being matched here — see NodeProxyFeature.
var FeatureRoutes = map[string][]RouteSpec{
	FeatureInfo: {
		{"GET", "/health"}, {"GET", "/info"}, {"GET", "/node-id"},
	},
	FeatureNodeDiscovery: {
		{"GET", "/v1/nodes"},
		{"GET", "/v1/metrics"},
		{"GET", "/rendezvous/v1/nodes"},
		{"GET", "/rendezvous/v1/metrics"},
	},
	FeatureGateProxy: {
		{"GET", "/gate/v1/rendezvous"},
		{"GET", "/gate/v1/nodes"},
		{"GET", "/gate/v1/software"},
		{"GET", "/gate/v1/software/package"},
		{"POST", "/gate/v1/software/scan-url"},
		{"POST", "/gate/v1/software/scan-file"},
	},
	FeatureAuthVerify: {{"POST", "/v1/auth/verify"}},
	FeatureMyNodes:    {{"*", "/v1/my-nodes/"}},
	FeaturePipeline: {
		{"POST", "/v1/pipeline/execute"},
		{"GET", "/v1/pipeline/jobs"},
		{"GET", "/v1/pipeline/jobs/"},
		{"GET", "/v1/pipeline/entities"},
	},
}

// NodeProxyFeature returns the feature name for a node-proxy sub-service.
// Called by handleNodeProxy after parsing /node/{id}/{service}/...
//
// Returns "" for unknown service — caller should treat as deny.
func NodeProxyFeature(service string) string {
	switch service {
	case "svc":
		return FeatureNodeProxySvc
	case "provider":
		return FeatureNodeProxyProvider
	case "installer":
		return FeatureNodeProxyInstaller
	case "service":
		return FeatureNodeProxyService
	}
	return ""
}

// FeatureForRoute returns the feature name that gates a given (method, path)
// pair, or "" if the route is not feature-gated (always allowed).
//
// Match strategy:
//   - exact match wins
//   - then prefix match where RouteSpec.Path ends with "/"
//   - method "*" matches any method
//
// /node/{id}/{service}/* is intentionally NOT matched here; the broker's
// node-proxy handler must call NodeProxyFeature with the parsed service.
func FeatureForRoute(method, path string) string {
	for feature, specs := range FeatureRoutes {
		for _, s := range specs {
			if s.Method != "*" && s.Method != method {
				continue
			}
			if s.Path == path {
				return feature
			}
			if len(s.Path) > 0 && s.Path[len(s.Path)-1] == '/' && len(path) >= len(s.Path) && path[:len(s.Path)] == s.Path {
				return feature
			}
		}
	}
	return ""
}
