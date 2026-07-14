package control

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/isannai/mesh/pkg/control/apipolicy"
	"github.com/isannai/mesh/pkg/installclient"
	"github.com/isannai/mesh/pkg/pipeline"
	"github.com/isannai/mesh/pkg/pipeline/entities"
	"github.com/isannai/mesh/pkg/tunnel"
)

// Broker is the Control Center: it serves the console SPA and reverse-proxies
// /node/<id>/* to provider nodes over HTTP through the isannd node-bridge.
type Broker struct {
	*tunnel.Base

	// Installer client for local software installation
	InstallClient *installclient.InstallClient

	// Pipeline runner (Go-native, replaces Node.js iann-vm).
	pipelineRegistry *pipeline.Registry
	pipelineRunner   *pipeline.Runner
	pipelineJobs     *pipeline.JobStore

	// FullSync delta tracking (Phase 1 refine).
	regMu       sync.Mutex
	regSent     bool
	lastVersion string
	lastBinHash string
	lastOwner   string
	lastLAddr   string

	// API policy snapshot. Atomic pointer so live updates from
	// /v1/admin/api-features swap atomically without locking the request path.
	policy atomic.Pointer[apipolicy.Policy]

	// isanndCli — single shared TCP NLB client to isannd's rv-control
	// listener. Lazy-initialised via isanndClient(). Sharing means one
	// TCP conn per backend — RV's last-writer-wins controlConns map
	// would otherwise close earlier conns when a new hello arrives.
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
	// Stops a credential-less broker from FullSync-spamming the RV every
	// heartbeat under protected mode. 0 = no backoff.
	rejectBackoffUntilMs atomic.Int64
}

// isanndClient returns the shared IsanndClient, creating it on first call.
func (b *Broker) isanndClient() *tunnel.IsanndClient {
	b.isanndCliMu.Lock()
	defer b.isanndCliMu.Unlock()
	if b.isanndCli == nil {
		b.isanndCli = tunnel.NewIsanndClient(b.Cfg.OutboundGateway.URL(), b.Cfg.OutboundGateway.ControlHostPort())
	}
	return b.isanndCli
}

// rebuildPolicy refreshes the API policy snapshot from current Cfg.APIFeatures.
// Called at startup and after PUT /v1/admin/api-features.
func (b *Broker) rebuildPolicy() {
	b.CfgMu.RLock()
	raw := b.Cfg.APIFeatures
	b.CfgMu.RUnlock()
	features := make(map[string]apipolicy.FeatureToggle, len(raw))
	for k, v := range raw {
		features[k] = apipolicy.FeatureToggle{Enabled: v.Enabled}
	}
	b.policy.Store(apipolicy.New(features))
}

// Policy returns the current API policy snapshot. Always non-nil after New().
func (b *Broker) Policy() *apipolicy.Policy {
	p := b.policy.Load()
	if p == nil {
		// Defensive — should not happen if New() was called.
		return apipolicy.New(nil)
	}
	return p
}

// New creates a new Broker. Initializes the pipeline runner and async job
// store so /v1/pipeline/* routes are ready when ServeHTTP starts.
func New(base *tunnel.Base) *Broker {
	b := &Broker{
		Base:          base,
		InstallClient: installclient.New(),
	}

	reg := pipeline.NewRegistry()
	entities.RegisterBuiltins(reg)
	b.pipelineRegistry = reg
	// BaseURL is resolved per-request via pipelineSelfBaseURL; we pass an
	// empty string here and the runner will use whatever the broker's
	// listen address resolves to at request time. For now we set it to
	// the configured listen address; New() runs once at startup.
	b.pipelineRunner = pipeline.NewRunner(reg, b.pipelineSelfBaseURL(), base.Log)
	// No remote NodeCaller is wired: AI pipeline entities (llmNode/sdNode) that
	// call a provider node used the direct broker→provider QUIC tunnel, now
	// retired (isannd fronts all node traffic). Reimplement over the isannd
	// node-bridge — mirror handleNodeProxy — if remote pipeline inference is
	// wanted; non-AI pipeline nodes run without a NodeCaller.
	b.pipelineJobs = pipeline.NewJobStore(pipeline.JobStoreConfig{})
	b.rebuildPolicy()
	return b
}

// Close releases resources owned by the broker. Currently stops the
// pipeline job store's background GC loop.
func (b *Broker) Close() {
	if b.pipelineJobs != nil {
		b.pipelineJobs.Close()
	}
}

// isHashedAsset returns true for Vite build outputs with content hash in filename.
// e.g. index-DhQ3f1kA.js, index-BxN1G9rO.css
var hashedAssetRe = regexp.MustCompile(`\.[0-9a-zA-Z]{8,}\.(js|css)$`)

func isHashedAsset(name string) bool {
	return hashedAssetRe.MatchString(name)
}

// brokerSPA serves the React SPA build directory, falling back to index.html for client-side routing.
func (b *Broker) brokerSPA(webDir string) http.Handler {
	indexPath := filepath.Join(webDir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API paths should not be handled here
		if strings.HasPrefix(r.URL.Path, "/v1/") || strings.HasPrefix(r.URL.Path, "/node/") ||
			strings.HasPrefix(r.URL.Path, "/gate/") || strings.HasPrefix(r.URL.Path, "/rendezvous/") ||
			strings.HasPrefix(r.URL.Path, "/health") || strings.HasPrefix(r.URL.Path, "/info") ||
			strings.HasPrefix(r.URL.Path, "/node-id") {
			http.NotFound(w, r)
			return
		}
		// Serve static file if it exists (not directory)
		path := filepath.Join(webDir, filepath.Clean(r.URL.Path))
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			// Hashed assets (Vite build: *.js, *.css with hash) → long cache
			// index.html and other files → no cache
			name := filepath.Base(path)
			if isHashedAsset(name) {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
			}
			http.ServeFile(w, r, path)
			return
		}
		// Fallback: serve index.html directly for SPA routing
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data, err := os.ReadFile(indexPath)
		if err != nil {
			http.Error(w, "index.html not found", http.StatusNotFound)
			return
		}
		w.Write(data)
	})
}

// (Node.js iann-vm reverse proxy and child-process spawn removed —
// pipeline-runner is now in-process via pkg/pipeline.)
