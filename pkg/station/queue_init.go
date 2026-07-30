// Phase 7 of the queue migration (docs/TODO/queue-migration-phases.md):
// build Provider's queue infrastructure at boot time. The Provider holds a
// single Storage + QueueManager pair. The QueueManager's Factory dispatches
// each ServiceEntry to either MakeManagedProcess (engine-runner spawns) or
// MakeExternalProcess (vLLM-style external metrics-driven backpressure).
//
// Routes that surface this infrastructure to broker (POST /v1/jobs etc.)
// are wired separately in jobs_handler.go and stream.go's orchestrator
// dispatcher (the latter happens once HTTP route injection lands — see
// Phase 7 follow-up notes in queue-migration-phases.md).
package station

import (
	"context"
	"log"
	"time"

	"github.com/isannai/mesh/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/station/queue"
	"github.com/isannai/mesh/pkg/setup"
	"github.com/isannai/mesh/pkg/tunnel"
)

// initQueueSubsystem builds and starts the queue Manager + Storage from cfg.
// Returns nil Manager + nil Storage when queue is disabled (no OutputDir AND
// no services with queue overrides).
//
// onJobChange is invoked on every job state transition (Submit / dequeue /
// runJob done|failed). Phase 3 wires it to Provider.NotifyJobChange so the
// heartbeat loop flushes immediately. Pass nil to disable event-driven push.
// event ∈ {"received","started","completed","failed"}; pending/running are
// the queue's snapshot at the moment of the event (completed/failed exclude self).
//
// The returned Manager is wired to ctx; canceling it drains all per-service
// workers in tandem. The returned Storage may be nil when memory-only.
func initQueueSubsystem(ctx context.Context, cfg tunnel.Config, packagesDir string, onJobChange func(serviceName, event, jobID string, pending, running int)) (*queue.Manager, *queue.Storage) {
	storage := initStorage(cfg)

	// Factory maps each ServiceEntry to its Config + ProcessFunc, blending
	// service config (cfg.Services[i].Queue) with engine manifest defaults
	// (manifests/engines/<engine>.json's queue.default_*).
	factory := func(svc setup.ServiceEntry) (queue.Config, queue.ProcessFunc) {
		m := loadServiceManifest(svc, packagesDir)
		qcfg := queue.BuildConfig(svc, m)
		qcfg.OnJobChange = onJobChange

		// Auto-derive the worker count from the engine's own slot variable
		// (.env PARALLEL / MAX_NUM_SEQS, named by manifest.queue.concurrency_env)
		// UNLESS the operator pinned it in provider.json. The engine .env is the
		// single source of truth for parallelism, so the queue follows it and
		// never forwards more than the engine can serve at once — the operator
		// tunes one place. Applied here, before the external-monitor branch, so
		// vLLM's metrics-driven backpressure capacity uses the same value.
		// (Read once at queue creation — see GetOrCreate; changing .env later
		// needs an engine restart + provider reload to take effect.)
		if svc.Queue == nil || svc.Queue.Concurrency <= 0 {
			if n := engineEnvConcurrency(manifest.InstallRoot(), svc, m); n > 0 {
				qcfg.Concurrency = n
			}
		}

		// Manifest opted this service out of the queue (streaming/long-lived
		// like webdav/terminal). Return nil ProcessFunc so Manager spawns
		// no worker — the queue stays dormant. Dispatch path (stream.go)
		// detects q.IsDisabled() and reverse-proxies directly.
		if qcfg.Disabled {
			return qcfg, nil
		}

		// Per-service storage: nil when SaveToDisk=false (vLLM streaming) so
		// dispatcher.Save() short-circuits to memory mode.
		var perSvcStorage *queue.Storage
		if qcfg.SaveToDisk {
			perSvcStorage = storage
		}

		opts := queue.DispatchOptions{
			Storage: perSvcStorage,
		}
		// Stream (sentence-chunk) support: pass the manifest's SSE delta path so
		// the processor can extract tokens when a job runs in stream mode (M3).
		if m != nil && m.API.Run != nil {
			opts.StreamPath = m.API.Run.Result.StreamPath
		}

		// Default engine-call timeout so a stalled engine can't wedge the worker
		// (docs/bugs/2026-07-30-queue-worker-wedge-on-stream-stall.md). Text
		// (llm/vllm) turns are idle-bounded at 60s of no new chunk; image gen
		// (sd) is a slow total-bounded call at 600s. Overridable per request
		// via ?timeout=.
		opts.Timeout = 60 * time.Second
		if m != nil && m.API.Run != nil && m.API.Run.Result.Modality == "image" {
			opts.Timeout = 600 * time.Second
		}

		// External engines (vLLM, future Ollama/TGI) get the metrics-driven
		// backpressure wrapper. Managed engines (sd.cpp, llama.cpp via
		// engine-runner) use the simpler forward.
		if isExternalLauncher(svc, m) {
			monitor := queue.NewExternalMonitor(svc, m, qcfg.Concurrency)
			if monitor != nil {
				go monitor.Run(ctx)
			}
			return qcfg, queue.MakeExternalProcess(svc, monitor, opts)
		}
		return qcfg, queue.MakeManagedProcess(svc, opts)
	}

	mgr := queue.NewManager(ctx, factory)
	return mgr, storage
}

// initStorage configures the Provider's result directory + cleanup. Returns
// nil when no output dir is set — disk persistence is opt-in.
//
// On startup, runs CleanupOrphans to evict files older than OutputTTLSec
// (= the operator-declared retention window). Without this, every Provider
// crash leaks the previous session's results onto disk, since the queue's
// in-memory cleanup hooks were never invoked.
func initStorage(cfg tunnel.Config) *queue.Storage {
	dir := cfg.Queue.OutputDir
	if dir == "" {
		return nil
	}
	ttl := time.Duration(cfg.Queue.OutputTTLSec) * time.Second
	s, err := queue.NewStorage(dir, ttl)
	if err != nil {
		log.Printf("[station] queue storage init failed: %v — running memory-only", err)
		return nil
	}
	if removed, err := s.CleanupOrphans(); err != nil {
		log.Printf("[station] queue orphan cleanup error: %v", err)
	} else if removed > 0 {
		log.Printf("[station] queue cleaned %d orphan result files", removed)
	}
	return s
}

// isExternalLauncher reports whether svc is delegated to an external engine
// (vLLM-class) rather than spawned by IANN's engine-runner. Drives the
// Factory's choice between MakeManagedProcess and MakeExternalProcess.
func isExternalLauncher(svc setup.ServiceEntry, m *manifest.Manifest) bool {
	// First preference: explicit Type marker on the service entry (matches
	// pollServiceExternal's existing branch). Falls through to manifest's
	// launcher field when Type is empty.
	if svc.Type == "vllm" || svc.Type == "external" {
		return true
	}
	if m != nil && m.Launcher == "external" {
		return true
	}
	return false
}
