package station

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/daesob/http3proxy/pkg/glog"
	"github.com/isannai/mesh/pkg/station/queue"
	"github.com/daesob/http3proxy/pkg/setup"
	"github.com/daesob/http3proxy/pkg/signal"
	"github.com/daesob/http3proxy/pkg/tunnel"
)

// initStaticHardware caches the provider's static hardware spec once,
// used by FullSync register payloads.
func (p *Provider) initStaticHardware() {
	p.hwStaticOnce.Do(func() {
		hw := setup.DetectHardwareStatic("")
		p.hwStatic = &hw
	})
}

// getLANAddr returns the LAN IP:port for the given listen address.
//
// The IP is the host's real internet-routable interface — found by asking the
// OS which source address it would use to reach a public addr (a UDP "dial"
// sends nothing, it just resolves the route). This avoids the bug where
// enumerating net.Interfaces() and taking the first non-loopback IPv4 picked a
// *virtual* adapter (WSL vEthernet 172.25.224.x, Hyper-V, VirtualBox) that has
// no real route — that wrong LAN candidate then got gossiped to peers and broke
// same-LAN dials (provider nodes advertised 172.25.224.1, unreachable by peers).
//
// NOTE: broker/rendezvous.go carries an identical copy — keep the two in sync.
func getLANAddr(listenAddr string) string {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return ""
	}
	if host != "" && host != "0.0.0.0" {
		return listenAddr
	}
	// Primary: route-aware source IP via a UDP dial to a public addr (no
	// packets are sent; the kernel just picks the outbound interface).
	if conn, derr := net.Dial("udp", "8.8.8.8:80"); derr == nil {
		la, ok := conn.LocalAddr().(*net.UDPAddr)
		_ = conn.Close()
		if ok && la.IP != nil && !la.IP.IsLoopback() && la.IP.To4() != nil {
			return la.IP.String() + ":" + port
		}
	}
	// Fallback: enumerate interfaces, skipping loopback / down / known
	// virtual-adapter ranges so a WSL/Hyper-V address isn't chosen.
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := iface.Addrs()
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.IsLoopback() || ip.To4() == nil || isVirtualAdapterIP(ip) {
				continue
			}
			return ip.String() + ":" + port
		}
	}
	return ""
}

// isVirtualAdapterIP reports whether ip falls in a range commonly used by
// host-only / hypervisor virtual adapters that carry no internet route:
// WSL2 (172.16/12 Hyper-V default switch range, e.g. 172.25.224.x) and
// link-local APIPA (169.254/16). Best-effort — the route-aware path above is
// the real fix; this only guards the enumeration fallback.
func isVirtualAdapterIP(ip net.IP) bool {
	if ip.IsLinkLocalUnicast() { // 169.254/16
		return true
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	// 172.16.0.0/12 — Hyper-V / WSL2 NAT switch lives here. Real home/office
	// LANs use 192.168/16 or 10/8; 172.16/12 is rare on physical nets.
	return v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31
}

// buildRegisterMsg collects current hardware/service state and assembles a
// RendezvousMsg. Pure-ish (no network IO), used by both the legacy UDP path
// and the Phase 2 QUIC signaling path.
func (p *Provider) buildRegisterMsg() *tunnel.RendezvousMsg {
	p.CfgMu.RLock()
	svcs := p.Cfg.Services
	proxyID := p.NodeIdentity.Address
	expose := p.Cfg.ExposeHardwareInfo
	listenAddr := p.Cfg.ListenAddr
	emblem := p.Cfg.Emblem
	cfgPath := p.Cfg.ConfigFile
	p.CfgMu.RUnlock()

	// Initialize as empty slice (not nil) so the wire format always carries
	// a `services` field, even when every service is disabled. This lets RV
	// overwrite its cached list down to empty.
	services := []setup.ServiceInfo{}
	for _, svc := range svcs {
		if !svc.IsEnabled() {
			// Disabled services must NOT appear in the register payload — RV
			// would otherwise keep showing them in /v1/nodes long after the
			// owner toggled them off. Heartbeat already honours this; the
			// register path was the missing piece.
			continue
		}
		info, alive, busy, apiBody := pollServiceWithAPI(svc, p.PackagesDir)
		if !alive {
			// Stopped services still need a card on broker UI with a Start
			// button. Carry the manifest's launcher so the UI knows whether
			// to render Start/Stop (docker) or "externally managed"
			// (external) — pollServiceWithAPI doesn't run for !alive so we
			// load the manifest directly here. ServerLoading은 PID 트래킹
			// (engine-runner 시절) 신호 — 컨테이너 패턴엔 의미 없어서 false.
			launcher := ""
			if mf := loadServiceManifest(svc, p.PackagesDir); mf != nil {
				launcher = mf.Launcher
				if launcher == "" {
					launcher = "external"
				}
			}
			services = append(services, setup.ServiceInfo{
				Name:     svc.Name,
				Type:     svc.Type,
				Engine:   svc.Engine,
				Launcher: launcher,
			})
			continue
		}

		// Capacity (static): resolved from conf overrides + manifest defaults.
		// Sent at register; broker reads via /v1/nodes for dispatch sizing.
		mf := loadInspectManifest(svc, cfgPath, p.PackagesDir)
		if mf != nil {
			qcfg := queue.BuildConfig(svc, mf)
			info.MaxQueue = qcfg.MaxQueue
			info.Concurrency = qcfg.Concurrency
			info.QueueDisabled = qcfg.Disabled
		}

		// Inspect: resolve manifest-declared options into a flat map carried
		// inside ServiceInfo. The register path is the only place this is
		// populated — heartbeat skips it (volatile-only) so per-tick load
		// stays small.
		if mf != nil && mf.Inspect != nil {
			confJSON := loadServiceConfJSON(svc, cfgPath)
			profileValues := loadActiveProfileValues(svc, cfgPath)
			vals, labels, order := extractInspect(mf, confJSON, apiBody, profileValues, svc)
			info.Inspect = vals
			info.InspectLabels = labels
			info.InspectOrder = order
		}

		services = append(services, info)
		_ = busy
	}

	hw := p.buildHardwareForMsg(expose)
	version := setup.StationVersion
	binHash := setup.SelfHash()
	localAddr := getLANAddr(listenAddr)
	hwHash := hashHardware(hw)

	// Delta logic: omit static fields that haven't changed since last register.
	// FullSync is driven by: first register after boot, static-field change,
	// or session rotation (scheduleFullSync signals by clearing regSent).
	p.regMu.Lock()
	p.regSeq++
	seq := p.regSeq
	fullSync := !p.regSent
	staticChanged := emblem != p.lastEmblem ||
		version != p.lastVersion ||
		binHash != p.lastBinHash ||
		localAddr != p.lastLAddr ||
		hwHash != p.lastHWHash
	if staticChanged {
		fullSync = true
	}
	if fullSync {
		p.lastEmblem = emblem
		p.lastVersion = version
		p.lastBinHash = binHash
		p.lastLAddr = localAddr
		p.lastHWHash = hwHash
		p.regSent = true
	}
	p.regMu.Unlock()

	msg := &tunnel.RendezvousMsg{
		V:        1,
		Type:     "register",
		Role:     "station",
		ID:       "S:" + proxyID,
		CertHash: p.CertHash,
		// AuthMode intentionally unset here: isannd stamps it fresh from
		// conf/auth.json as the register is relayed to RV (nlb_listener.go), so
		// an `isann auth mode` change lands without a proxy restart. The
		// startup-cached p.Auth.Mode would be stale, so the provider no longer
		// reads it for the advertisement.
		Services: services, // always — contains per-service runtime state
		Seq:      seq,
		FullSync: fullSync,
	}
	// Operator-declared external dial target (NAT bypass — port-forwarded
	// host or static public IP). When set, RV stores AddrManual=true so
	// punch learning doesn't overwrite it.
	if ext := p.Cfg.ExternalAddr; ext != "" {
		msg.Addr = ext
	}
	if fullSync {
		msg.LocalAddr = localAddr
		msg.Version = version
		msg.BinHash = binHash
		msg.Emblem = emblem
		msg.Hardware = hw
		// OwnerAddress, the register signature, and TPM evidence (EK cert / AK
		// name) are stamped by isannd as it relays this frame to RV
		// (nlb_listener.go). isannd is the single owner-identity + hardware
		// node-key authority — "session in → signature out" — so the backend
		// ships no auth.json owner and never signs. hwHash above stays for
		// delta change-detection only.
	}

	// Include pending TPM challenge response
	p.tpmMu.Lock()
	if len(p.tpmResponse) > 0 {
		msg.TPMResponse = p.tpmResponse
		p.tpmResponse = nil
	}
	p.tpmMu.Unlock()

	return msg
}

// hashHardware returns a short fingerprint for HardwareSpec change detection.
// Uses fnv-1a over JSON-marshaled bytes.
func hashHardware(hw *setup.HardwareSpec) string {
	if hw == nil {
		return ""
	}
	data, err := json.Marshal(hw)
	if err != nil {
		return ""
	}
	const offset uint64 = 14695981039346656037
	const prime uint64 = 1099511628211
	h := offset
	for _, b := range data {
		h ^= uint64(b)
		h *= prime
	}
	return fmt.Sprintf("%016x", h)
}


// Fallback cadences used when RV's register_ack has not yet been received
// (cold start, or RV permanently down). Once an ack arrives, the atomics
// in Provider take over via effective*Interval helpers. Match the defaults
// in pkg/rendezvous/server.go so behavior is identical between "no RV
// available" and "RV available with default config".
const (
	providerFallbackPingIntervalSec     = 5
	providerFallbackRegisterIntervalSec = 300
	// registerRejectBackoff throttles re-register after an RV admission denial
	// (protected mode, no/invalid credential) so the node doesn't FullSync-spam
	// the RV every heartbeat. Retries resume at ~this cadence until admitted.
	registerRejectBackoff = 60 * time.Second
)

// startIsanndForwarder — periodic register through isannd's NLB to RV.
// Cadence comes from RV's register_ack (atomic field on Provider); before
// the first ack arrives, providerFallbackRegisterIntervalSec applies.
//
// fullSync vs non-fullSync is decided by buildRegisterMsg's delta logic:
//   - 첫 사이클 (regSent=false): fullSync
//   - 정적 필드 (Version / BinHash / Owner / LocalAddr / HWHash / Emblem)
//     변화 시: fullSync 자동 승격
//   - 그 외: non-fullSync delta (RV 가 cached value merge)
// register 페이로드는 ≈ 수백 bytes, 부담 적음.
//
// Also installs the OnPush ack handler that picks up PingIntervalSec and
// RegisterIntervalSec on every register_ack. The 5 holepunch fields in
// the same ack are intentionally ignored here — isannd already consumed
// them in nlb_listener.go's TypeAck interception.
func (p *Provider) startIsanndForwarder(ctx context.Context) {
	cli := p.isanndClient()

	cli.OnPush(signal.TypeAck, func(msg *tunnel.RendezvousMsg) {
		if msg.PingIntervalSec > 0 {
			prev := p.pingIntervalSec.Swap(int32(msg.PingIntervalSec))
			if prev != int32(msg.PingIntervalSec) {
				p.Log.Log(glog.Lifecycle, "[station] RV ping cadence updated: %ds → %ds", prev, msg.PingIntervalSec)
			}
		}
		if msg.RegisterIntervalSec > 0 {
			prev := p.registerIntervalSec.Swap(int32(msg.RegisterIntervalSec))
			if prev != int32(msg.RegisterIntervalSec) {
				p.Log.Log(glog.Lifecycle, "[station] RV register cadence updated: %ds → %ds", prev, msg.RegisterIntervalSec)
			}
		}
	})

	// RV rejected our register (protected mode admission). Back off
	// re-register and force the next register to be a FullSync so it
	// re-presents the (possibly newly-installed) credential + static fields
	// instead of a delta — otherwise we'd FullSync-spam the RV every heartbeat.
	cli.OnPush(signal.TypeError, func(msg *tunnel.RendezvousMsg) {
		if !strings.Contains(msg.Addr, "admission denied") {
			return
		}
		p.rejectBackoffUntilMs.Store(time.Now().Add(registerRejectBackoff).UnixMilli())
		p.regMu.Lock()
		p.regSent = false
		p.regMu.Unlock()
		p.Log.Log(glog.Lifecycle, "[station] RV admission denied (%s) — backing off re-register %s; next register fullSync", msg.Addr, registerRejectBackoff)
	})

	p.Log.Log(glog.Lifecycle, "[station] isannd forwarder → %s (cadences controlled by RV; fallback register=%ds ping=%ds)",
		p.Cfg.OutboundGateway.URL(), providerFallbackRegisterIntervalSec, providerFallbackPingIntervalSec)

	go func() {
		// First register: immediate. Subsequent: per effectiveRegisterInterval().
		next := time.Duration(0)
		for {
			select {
			case <-time.After(next):
			case <-ctx.Done():
				return
			}
			// Admission-denied backoff: don't re-register (and re-reject) every
			// interval while the RV is refusing us. Wait out the backoff, then
			// retry — the heartbeat need_register path honors the same window.
			if until := p.rejectBackoffUntilMs.Load(); until > 0 {
				if remain := time.Until(time.UnixMilli(until)); remain > 0 {
					next = remain
					continue
				}
			}
			msg := p.buildRegisterMsg() // delta 로직이 fullSync 여부 결정
			if msg == nil {
				p.Log.Log(glog.Connection, "[station] isannd register: payload nil — skip")
			} else if err := cli.SendRegister(ctx, msg); err != nil {
				p.Log.Log(glog.Connection, "[station] isannd register: %v", err)
			} else {
				p.Log.Log(glog.Lifecycle, "[station] register forwarded to isannd (id=%s role=%s fullSync=%v svcs=%d)",
					msg.ID, msg.Role, msg.FullSync, len(msg.Services))
			}
			next = p.effectiveRegisterInterval()
		}
	}()
}

// effectivePingInterval returns the RV-dictated ping cadence if available,
// otherwise the hardcoded fallback. Called per-tick so RV updates take
// effect on the very next cycle.
func (p *Provider) effectivePingInterval() time.Duration {
	if v := p.pingIntervalSec.Load(); v > 0 {
		return time.Duration(v) * time.Second
	}
	return providerFallbackPingIntervalSec * time.Second
}

// effectiveRegisterInterval mirrors effectivePingInterval for register
// cadence. RV's register_ack carries RegisterIntervalSec; before it
// arrives, we fall back to a built-in default.
func (p *Provider) effectiveRegisterInterval() time.Duration {
	if v := p.registerIntervalSec.Load(); v > 0 {
		return time.Duration(v) * time.Second
	}
	return providerFallbackRegisterIntervalSec * time.Second
}

// metricsDebounceWindow — coalesce window for queue lifecycle callbacks.
// A burst of service-state changes within this window collapses into one
// batched push. 100ms is short enough to feel real-time on the UI yet
// long enough to absorb the typical "all services transition at the same
// tick" scenario the user saw in logs.
const metricsDebounceWindow = 100 * time.Millisecond

// notifyMetricsChange schedules a batched metrics push after the debounce
// window. Multiple calls within the window all reset the timer so the
// push fires once, ~100ms after the last call. Safe for concurrent use.
func (p *Provider) notifyMetricsChange(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	p.metricsBatchMu.Lock()
	defer p.metricsBatchMu.Unlock()
	if p.metricsBatchTimer != nil {
		p.metricsBatchTimer.Stop()
	}
	p.metricsBatchTimer = time.AfterFunc(metricsDebounceWindow, func() {
		p.pushAllMetrics(ctx)
	})
}

// buildServiceMetric polls one service and returns its metric snapshot.
// Extracted so pushAllMetrics can build the batch row-by-row without
// repeating the status-decision tree per service.
func (p *Provider) buildServiceMetric(svc setup.ServiceEntry, nowMs int64) tunnel.ServiceMetrics {
	info, alive, _ := pollService(svc, p.PackagesDir)
	queueDepth, runningCount, totalJobsDone, avgJobSec, lastSubmittedAt, runningJobID := p.pollServiceQueue(svc, p.PackagesDir)

	// Status decision tree — explicit fall-through to "loading" when the
	// backend is alive but its /health response is still ambiguous (both
	// `server:false` and `server_loading:false`). Treating "alive but
	// not-yet-ready" as loading keeps the UI quiet during transients.
	status := "stopped"
	if alive {
		status = "loading"
		if info.ServerReady {
			status = "idle"
			if runningCount > 0 || runningJobID != "" {
				status = "busy"
			}
		}
	}
	return tunnel.ServiceMetrics{
		Service:       svc.Name,
		Status:        status,
		QueueDepth:    uint32(queueDepth),
		RunningCount:  uint32(runningCount),
		TotalJobsDone: uint64(totalJobsDone),
		AvgJobSec:     float32(avgJobSec),
		LastJobMs:     lastSubmittedAt,
		RunningJobID:  runningJobID,
		TimestampMs:   nowMs,
	}
}

// metricHasValue reports whether a service metric carries information
// worth pushing. A row is treated as empty (no value) when the service
// is fully stopped AND every counter / running-job slot is zero —
// repeatedly re-sending that row each 10s tick is pure noise. Active
// states (idle / busy / loading) and stopped-but-with-history (e.g.
// total_jobs_done > 0) still ride along.
func metricHasValue(m tunnel.ServiceMetrics) bool {
	if m.Status == "idle" || m.Status == "busy" || m.Status == "loading" {
		return true
	}
	if m.QueueDepth > 0 || m.RunningCount > 0 || m.TotalJobsDone > 0 {
		return true
	}
	if m.AvgJobSec > 0 || m.LastJobMs > 0 || m.RunningJobID != "" {
		return true
	}
	return false
}

// pushAllMetrics enumerates every enabled service and sends one batched
// service_event frame. Fires after the debounce window in
// notifyMetricsChange; also called from startServiceStatusLoop's periodic
// tick so the batch picks up gradual changes (e.g. a service drifting
// from loading → idle) that didn't originate from queue callbacks.
//
// Rows with no value (stopped + zero counters) are dropped from the
// batch. When every row is dropped, the frame still ships with an empty
// `[]` MetricsBatch so the receiver gets a positive "I'm alive, nothing
// to report" signal rather than silent absence.
func (p *Provider) pushAllMetrics(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	p.CfgMu.RLock()
	services := append([]setup.ServiceEntry(nil), p.Cfg.Services...)
	p.CfgMu.RUnlock()

	nowMs := time.Now().UnixMilli()
	batch := make([]tunnel.ServiceMetrics, 0, len(services))
	for _, svc := range services {
		if !svc.IsEnabled() {
			continue
		}
		m := p.buildServiceMetric(svc, nowMs)
		if !metricHasValue(m) {
			continue
		}
		batch = append(batch, m)
	}
	cli := p.isanndClient()
	nodeID := "S:" + p.NodeIdentity.Address
	if err := cli.SendMetricsBatch(ctx, nodeID, "station", batch); err != nil {
		p.Log.Log(glog.Debug, "[station] push metrics batch: %v", err)
		return
	}
	if len(batch) == 0 {
		p.Log.Log(glog.Debug, "[station] push metrics batch: empty (no service has value)")
		return
	}
	// One log line per push instead of N — matches the new send pattern.
	var summary strings.Builder
	for i, m := range batch {
		if i > 0 {
			summary.WriteString(", ")
		}
		fmt.Fprintf(&summary, "%s=%s/q%d/r%d", m.Service, m.Status, m.QueueDepth, m.RunningCount)
	}
	p.Log.Log(glog.Debug, "[station] push metrics batch (%d): %s", len(batch), summary.String())
}

// startPingLoop sends a periodic liveness ping to isannd's
// /internal/rv/heartbeat. Cadence comes from RV's register_ack
// (PingIntervalSec); before the first ack arrives,
// providerFallbackPingIntervalSec applies. Each tick = one POST. Errors
// logged + ignored — next tick recovers automatically when isannd is
// back up.
func (p *Provider) startPingLoop(ctx context.Context) {
	cli := p.isanndClient()
	nodeID := "S:" + p.NodeIdentity.Address

	go func() {
		timer := time.NewTimer(p.effectivePingInterval())
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				// Re-arm with the (possibly updated) interval before each tick.
				timer.Reset(p.effectivePingInterval())
				ts := time.Now().UnixMilli()
				sig, sigErr := p.NodeIdentity.Sign(tunnel.PingDigest("station", ts))
				if sigErr != nil {
					p.Log.Log(glog.Connection, "[station] ping sign: %v", sigErr)
					continue
				}
				needRegister, err := cli.SendHeartbeat(ctx, &tunnel.HeartbeatPing{
					NodeID:      nodeID,
					Role:        "station",
					TimestampMs: ts,
					Signature:   sig,
				})
				if err != nil {
					p.Log.Log(glog.Connection, "[station] ping: %v", err)
					continue
				}
				p.Log.Log(glog.Connection, "[station] ping → isannd")
				if needRegister {
					if until := p.rejectBackoffUntilMs.Load(); until > 0 && time.Now().UnixMilli() < until {
						// Admission denied recently — skip the immediate re-register
						// so we don't FullSync-spam the RV every heartbeat. The
						// periodic register loop still retries at its slower cadence.
						p.Log.Log(glog.Connection, "[station] need_register ignored — admission-denied backoff active")
					} else {
						// RV restart recovery: force fullSync so the next
						// buildRegisterMsg emits a signed payload — RV's TCP
						// control path rejects non-fullSync registers.
						p.regMu.Lock()
						p.regSent = false
						p.regMu.Unlock()
						msg := p.buildRegisterMsg()
						if msg == nil {
							continue
						}
						if rerr := cli.SendRegister(ctx, msg); rerr != nil {
							p.Log.Log(glog.Lifecycle, "[station] re-register on need_register: %v", rerr)
						} else {
							p.Log.Log(glog.Lifecycle, "[station] re-registered after RV need_register (id=%s)", msg.ID)
						}
					}
				}
			}
		}
	}()
}

// startServiceStatusLoop pushes a metrics snapshot for every enabled
// service every 1 second to isannd. isannd caches the latest in memory so
// the unsigned conn /ping (pong) serves peers a near-real-time view, and
// throttles its OWN forward to RV to a slower cadence (rvMetricsForwardInterval,
// nlb_listener.go). So: provider → isannd 1s (fresh cache), isannd → RV 10s.
// Bridges the gap between event-driven push (queue lifecycle only) and what
// peers / the UI need (service ready / loading / stopped transitions).
//
// One batched send per tick — pushAllMetrics enumerates all enabled
// services and emits a single service_event frame with the full snapshot.
func (p *Provider) startServiceStatusLoop(ctx context.Context) {
	interval := 1 * time.Second
	p.Log.Log(glog.Lifecycle, "[station] service status loop every %s", interval)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				p.pushAllMetrics(ctx)
			}
		}
	}()
}
