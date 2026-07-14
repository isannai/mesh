package station

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/isannai/mesh/pkg/glog"
	"github.com/isannai/mesh/pkg/setup"
	"github.com/isannai/mesh/pkg/signal"
	"github.com/isannai/mesh/pkg/tunnel"
)

// runServiceWatcher is the Phase 1-C lifecycle state machine. Every second
// it polls registered services plus running-process list, compares against
// the cached state, and pushes a `service_event` frame to the signal
// client on transitions:
//
//	<nothing>  → starting   (pid appeared / ServerLoading=true)
//	starting   → ready      (/health returned ready=true)
//	ready      → stopped    (pid gone / alive=false)
//	starting   → stopped    (pid disappeared before ready)
//
// The watcher does NOT push `running` liveness each second — that's the
// UDP heartbeat's job. It only announces transitions.
func (p *Provider) runServiceWatcher(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	// probeCache caches the last probe result per service so we can honor
	// each manifest's ready_check.interval_ms — the ticker fires every 1s
	// but a service declaring interval_ms=10000 should only get hit once
	// every 10s. Reconcile still runs every tick with the cached snapshot
	// so phase transitions surface immediately the next probe lands.
	probeCache := map[string]probeEntry{}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.tickServiceWatcher(probeCache)
		}
	}
}

// probeEntry is the per-service slot in the watcher's probe cache.
type probeEntry struct {
	lastAt time.Time
	info   setup.ServiceInfo
	alive  bool
}

func (p *Provider) tickServiceWatcher(probeCache map[string]probeEntry) {
	p.CfgMu.RLock()
	svcs := append([]setup.ServiceEntry(nil), p.Cfg.Services...)
	p.CfgMu.RUnlock()

	observed := map[string]bool{}
	for _, svc := range svcs {
		if !svc.IsEnabled() {
			// Disabled — auto-stop the docker container if it's still up.
			// dockerStop is idempotent (no-op if not running) so we can fire
			// unconditionally without an extra ps lookup.
			if svc.IsManagedLocally() {
				svcCopy := svc
				go func(svc setup.ServiceEntry) {
					if err := p.dockerStop(svc); err != nil {
						p.Log.Log(glog.Debug, "[station] auto-stop on disable: %s: %v", svc.Name, err)
					}
				}(svcCopy)
			}
			continue
		}
		observed[svc.Name] = true

		// Gate the actual HTTP probe by the manifest's ready_check.interval_ms.
		// Within the window we reuse the cached snapshot so reconcile still
		// runs every tick (phase changes surface immediately the next probe
		// lands), but we don't burn HTTP calls on services that declared a
		// longer interval (e.g. sd-server's 10s).
		interval := time.Second
		if m := loadServiceManifest(svc, p.PackagesDir); m != nil && m.ReadyCheck.IntervalMS > 0 {
			interval = time.Duration(m.ReadyCheck.IntervalMS) * time.Millisecond
		}

		var info setup.ServiceInfo
		var alive bool
		if last, ok := probeCache[svc.Name]; ok && time.Since(last.lastAt) < interval {
			info, alive = last.info, last.alive
		} else {
			info, alive, _ = pollService(svc, p.PackagesDir)
			probeCache[svc.Name] = probeEntry{lastAt: time.Now(), info: info, alive: alive}
		}
		// PID 인자는 옛 engine-runner 시절 zombie 감지용. 컨테이너 패턴에선
		// 의미 없음 — 0 으로 고정. reconcileService 는 alive / info 만으로
		// phase 결정.
		p.reconcileService(svc, alive, info, 0)
	}

	// Evict cache entries for services that disappeared from cfg.
	for name := range probeCache {
		if !observed[name] {
			delete(probeCache, name)
		}
	}

	// Services previously tracked but no longer in cfg → treat as stopped.
	p.svcStateMu.Lock()
	stale := make([]string, 0)
	for name := range p.svcStates {
		if !observed[name] {
			stale = append(stale, name)
		}
	}
	p.svcStateMu.Unlock()
	for _, name := range stale {
		p.reconcileService(setup.ServiceEntry{Name: name}, false, setup.ServiceInfo{Name: name}, 0)
	}
}

func (p *Provider) reconcileService(svc setup.ServiceEntry, alive bool, info setup.ServiceInfo, runningPID int) {
	var nextPhase string
	if !svc.IsManagedLocally() {
		// Externally-managed engines (vllm, future ollama/tgi...) have no
		// IANN-owned pidfile — phase is decided purely by HTTP reachability.
		switch {
		case alive && info.ServerReady:
			nextPhase = "ready"
		case alive:
			nextPhase = "starting" // reachable but model not loaded yet
		default:
			nextPhase = "stopped"
		}
	} else {
		switch {
		case alive && info.ServerReady:
			nextPhase = "ready"
		case alive || info.ServerLoading || runningPID > 0:
			nextPhase = "starting"
		default:
			nextPhase = "stopped"
		}
	}

	p.svcStateMu.Lock()
	prev, had := p.svcStates[svc.Name]
	if !had && nextPhase == "stopped" {
		// Never seen, still not running — nothing to announce.
		p.svcStateMu.Unlock()
		return
	}
	if had && prev.phase == nextPhase && prev.childPID == info.ChildPID && prev.model == info.Model {
		// No transition.
		p.svcStateMu.Unlock()
		return
	}

	p.svcStates[svc.Name] = serviceState{
		phase:     nextPhase,
		childPID:  info.ChildPID,
		model:     info.Model,
		updatedAt: time.Now(),
	}
	p.svcStateMu.Unlock()

	p.emitServiceEvent(svc, nextPhase, info, runningPID)
}

func (p *Provider) emitServiceEvent(svc setup.ServiceEntry, phase string, info setup.ServiceInfo, runningPID int) {
	name := svc.Name
	event := ""
	payload := map[string]any{}
	switch phase {
	case "starting":
		event = "service.starting"
		payload["child_pid"] = info.ChildPID
		if info.ChildPID == 0 && runningPID > 0 {
			payload["child_pid"] = runningPID
		}
		payload["child_name"] = info.ChildName
		payload["version"] = info.Version
		payload["bin_hash"] = info.BinHash
		payload["model"] = info.Model
		payload["model_hash"] = info.ModelHash
	case "ready":
		event = "service.ready"
		payload["child_pid"] = info.ChildPID
		payload["version"] = info.Version
		payload["bin_hash"] = info.BinHash
		payload["model"] = info.Model
		payload["model_hash"] = info.ModelHash
	case "stopped":
		event = "service.stopped"
		payload["reason"] = "exited"
	}
	if event == "" {
		return
	}

	// Attach inspect data on starting/ready transitions so RV gets the
	// configured options (ctx_size, gpu_layers, model_default, …) without
	// waiting for the next 24h FullSync register cycle.
	if phase == "starting" || phase == "ready" {
		p.CfgMu.RLock()
		cfgPath := p.Cfg.ConfigFile
		p.CfgMu.RUnlock()
		if mf := loadInspectManifest(svc, cfgPath, p.PackagesDir); mf != nil && mf.Inspect != nil {
			confJSON := loadServiceConfJSON(svc, cfgPath)

			// External engines expose some inspect fields only via their HTTP
			// API (vLLM /v1/models → max_model_len, served name, root). Probe
			// once so from="api" rows get populated; without this, ready_event
			// only carries value-inline + service-options fields and the UI
			// shows partial inspect until next FullSync.
			var apiBody []byte
			if !svc.IsManagedLocally() && mf.ReadyCheck.URL != "" {
				url := resolveAddrTemplate(mf.ReadyCheck.URL, svc.Addr)
				c := &http.Client{Timeout: 2 * time.Second}
				if resp, err := c.Get(url); err == nil {
					if resp.StatusCode == http.StatusOK {
						apiBody, _ = io.ReadAll(resp.Body)
					}
					resp.Body.Close()
				}
			}

			profileValues := loadActiveProfileValues(svc, cfgPath)
			vals, labels, order := extractInspect(mf, confJSON, apiBody, profileValues, svc)
			if len(vals) > 0 {
				payload["inspect"] = vals
				payload["inspect_labels"] = labels
				payload["inspect_order"] = order
			}
		}
	}

	msg := &tunnel.RendezvousMsg{
		V:           1,
		Type:        signal.TypeServiceEvent,
		Role:        "station",
		ID:          "S:" + p.NodeIdentity.Address,
		Event:       event,
		ServiceName: name,
		ServiceInfo: payload,
	}
	// Route lifecycle events through the shared NLB IsanndClient — the
	// legacy QUIC signal client is gone. isannd forwards the frame
	// byte-verbatim to RV's TCP control listener.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli := p.isanndClient()
	if err := cli.SendFrame(ctx, msg); err != nil {
		p.Log.Log(glog.Debug, "[station] service_event push skipped: %v", err)
	} else {
		p.Log.Log(glog.Lifecycle, "[station] service_event → %s %s", event, name)
	}
}
