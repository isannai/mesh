package queue

import (
	"time"

	"github.com/daesob/http3proxy/pkg/engine/manifest"
	"github.com/daesob/http3proxy/pkg/setup"
)

// Resolution priority for every queue knob:
//
//	1. service config  (conf/provider.json's services[].queue.*)   — instance override
//	2. manifest        (manifests/engines/*.json's queue.default_*) — engine default
//	3. code fallback                                                — safety net
//
// service config uses int / *bool fields; "0 / nil" means "unspecified, defer
// to next layer". manifest uses int / bool; for those, "0" reaches the queue
// directly (e.g. vLLM's default_ttl_sec=0 means "evict on next gc tick").
// The code fallback is only consulted when both upper layers are unspecified.

// resolveMaxQueue returns the active pending+running cap for svc. 0 = unlimited.
func resolveMaxQueue(svc setup.ServiceEntry, m *manifest.Manifest) int {
	if svc.Queue != nil && svc.Queue.MaxQueue > 0 {
		return svc.Queue.MaxQueue
	}
	if m != nil && m.Queue.MaxPending > 0 {
		return m.Queue.MaxPending
	}
	return 0
}

// resolveConcurrency returns the active worker count. Floor of 1 keeps tests
// safe even when nobody set anything.
//
// NOTE: the provider's queue factory (pkg/station/queue_init.go) layers the
// engine .env slot value (PARALLEL / MAX_NUM_SEQS, via manifest.concurrency_env)
// ON TOP of this, between the service-config override and the manifest default
// — so the effective priority is: provider.json override > engine .env slot >
// manifest default_concurrency > 1. That layer needs filesystem access (the
// engine .env), which is why it lives in the provider package, not here.
func resolveConcurrency(svc setup.ServiceEntry, m *manifest.Manifest) int {
	if svc.Queue != nil && svc.Queue.Concurrency > 0 {
		return svc.Queue.Concurrency
	}
	if m != nil && m.Queue.Concurrency > 0 {
		return m.Queue.Concurrency
	}
	return 1
}

// resolveMaxDone returns the LRU cap for done/failed jobs.
func resolveMaxDone(svc setup.ServiceEntry, m *manifest.Manifest) int {
	if svc.Queue != nil && svc.Queue.MaxDone > 0 {
		return svc.Queue.MaxDone
	}
	if m != nil && m.Queue.MaxDone > 0 {
		return m.Queue.MaxDone
	}
	return 100
}

// resolveTTL returns done/failed retention. Manifest is authoritative — even
// 0 reaches Queue (vLLM's "evict on next gc" semantics). Only when neither
// service config nor manifest has a value does the 1-hour fallback apply.
func resolveTTL(svc setup.ServiceEntry, m *manifest.Manifest) time.Duration {
	if svc.Queue != nil && svc.Queue.TTLSec > 0 {
		return time.Duration(svc.Queue.TTLSec) * time.Second
	}
	if m != nil {
		return time.Duration(m.Queue.DoneTTLSec) * time.Second
	}
	return time.Hour
}

// resolveSaveToDisk uses pointer semantics for service config to distinguish
// "explicit false" (override to memory-only) from "unset" (defer to manifest).
func resolveSaveToDisk(svc setup.ServiceEntry, m *manifest.Manifest) bool {
	if svc.Queue != nil && svc.Queue.SaveToDisk != nil {
		return *svc.Queue.SaveToDisk
	}
	if m != nil {
		return m.Queue.SaveToDisk
	}
	return false
}

// BuildConfig assembles a queue.Config by merging service config over manifest
// defaults, with safe fallbacks when both layers are absent. Pass m=nil for
// services that have no associated manifest (purely service-config driven).
//
// Production code wires this into Manager.NewManager via the Factory:
//
//	factory := func(svc setup.ServiceEntry) (queue.Config, queue.ProcessFunc) {
//	    m := loadManifest(svc.Manifest)
//	    return queue.BuildConfig(svc, m), buildProcess(svc, m)
//	}
func BuildConfig(svc setup.ServiceEntry, m *manifest.Manifest) Config {
	return Config{
		ServiceName: svc.Name,
		Concurrency: resolveConcurrency(svc, m),
		MaxQueue:    resolveMaxQueue(svc, m),
		MaxDone:     resolveMaxDone(svc, m),
		DoneTTL:     resolveTTL(svc, m),
		SaveToDisk:  resolveSaveToDisk(svc, m),
		Disabled:    m != nil && !m.Queue.IsEnabled(), // manifest-only, no conf override
	}
}
