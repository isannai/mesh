package control

import (
	"context"
	"log"
	"net"
	"strings"
	"time"

	"github.com/isannai/mesh/pkg/setup"
	"github.com/isannai/mesh/pkg/signal"
	"github.com/isannai/mesh/pkg/tunnel"
)

// Fallback cadences used when RV's register_ack has not yet been received
// (cold start, or RV permanently down). Once an ack arrives, the atomics
// in Broker take over via effective*Interval helpers. Match the defaults
// in pkg/rendezvous/server.go so behavior is identical between "no RV
// available" and "RV available with default config".
const (
	brokerFallbackPingIntervalSec     = 10
	brokerFallbackRegisterIntervalSec = 300
	// registerRejectBackoff throttles re-register after an RV admission denial
	// (protected mode, no/invalid credential) so the broker doesn't spam the RV
	// every heartbeat. Retries resume at ~this cadence until admitted.
	registerRejectBackoff = 60 * time.Second
)

// runRendezvousLoop starts the broker's RV connection. register/ping
// goroutines both keep retrying through isannd's NLB pipe — if RV is
// down, attempts fail and we log + sleep; when RV returns the next
// successful ack updates the atomic cadences in Broker so subsequent
// ticks use RV's chosen value.
func (b *Broker) runRendezvousLoop(ctx context.Context) {
	b.startIsanndForwarder(ctx)
	b.startPingLoop(ctx)
	<-ctx.Done()
}

// startIsanndForwarder — periodic fullSync register through isannd's NLB
// to RV. Cadence comes from RV's register_ack (atomic field on Broker);
// before the first ack arrives, brokerFallbackRegisterIntervalSec applies.
//
// broker 는 Hardware / Services 같은 변동 정적 필드가 없어서 항상
// fullSync (서명 + 정적 필드 echo) — payload ≈ 수백 bytes, 부담 적음.
//
// Also installs the OnPush ack handler that picks up PingIntervalSec and
// RegisterIntervalSec on every register_ack. The 5 holepunch fields in
// the same ack are intentionally ignored here — isannd already consumed
// them in nlb_listener.go's TypeAck interception.
func (b *Broker) startIsanndForwarder(ctx context.Context) {
	cli := b.isanndClient()

	cli.OnPush(signal.TypeAck, func(msg *tunnel.RendezvousMsg) {
		if msg.PingIntervalSec > 0 {
			prev := b.pingIntervalSec.Swap(int32(msg.PingIntervalSec))
			if prev != int32(msg.PingIntervalSec) {
				log.Printf("[control] RV ping cadence updated: %ds → %ds", prev, msg.PingIntervalSec)
			}
		}
		if msg.RegisterIntervalSec > 0 {
			prev := b.registerIntervalSec.Swap(int32(msg.RegisterIntervalSec))
			if prev != int32(msg.RegisterIntervalSec) {
				log.Printf("[control] RV register cadence updated: %ds → %ds", prev, msg.RegisterIntervalSec)
			}
		}
	})

	// RV rejected our register (protected mode admission). Back off
	// re-register so a credential-less broker doesn't spam the RV every
	// heartbeat. The broker always FullSyncs, so no regSent reset is needed.
	cli.OnPush(signal.TypeError, func(msg *tunnel.RendezvousMsg) {
		if !strings.Contains(msg.Addr, "admission denied") {
			return
		}
		b.rejectBackoffUntilMs.Store(time.Now().Add(registerRejectBackoff).UnixMilli())
		log.Printf("[control] RV admission denied (%s) — backing off re-register %s", msg.Addr, registerRejectBackoff)
	})

	log.Printf("[control] isannd forwarder → %s (cadences controlled by RV; fallback register=%ds ping=%ds)",
		b.Cfg.OutboundGateway.URL(), brokerFallbackRegisterIntervalSec, brokerFallbackPingIntervalSec)

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
			if until := b.rejectBackoffUntilMs.Load(); until > 0 {
				if remain := time.Until(time.UnixMilli(until)); remain > 0 {
					next = remain
					continue
				}
			}
			msg := b.buildRegisterMsg(true)
			if msg == nil {
				log.Printf("[control] isannd register: payload nil — skip")
			} else if err := cli.SendRegister(ctx, msg); err != nil {
				log.Printf("[control] isannd register: %v", err)
			} else {
				log.Printf("[control] register forwarded to isannd (id=%s role=%s)", msg.ID, msg.Role)
			}
			next = b.effectiveRegisterInterval()
		}
	}()
}

// effectivePingInterval returns the RV-dictated ping cadence if available,
// otherwise the hardcoded fallback. Called per-tick so RV updates take
// effect on the very next cycle.
func (b *Broker) effectivePingInterval() time.Duration {
	if v := b.pingIntervalSec.Load(); v > 0 {
		return time.Duration(v) * time.Second
	}
	return brokerFallbackPingIntervalSec * time.Second
}

// effectiveRegisterInterval mirrors effectivePingInterval for register
// cadence. RV's register_ack carries RegisterIntervalSec; before it
// arrives, we fall back to a built-in default.
func (b *Broker) effectiveRegisterInterval() time.Duration {
	if v := b.registerIntervalSec.Load(); v > 0 {
		return time.Duration(v) * time.Second
	}
	return brokerFallbackRegisterIntervalSec * time.Second
}

// buildRegisterMsg returns the broker's register message. When fullSync is
// true, static fields + ECDSA signature over RegisterDigest are included.
func (b *Broker) buildRegisterMsg(fullSync bool) *tunnel.RendezvousMsg {
	b.CfgMu.RLock()
	listenAddr := b.Cfg.ListenAddr
	b.CfgMu.RUnlock()

	version := setup.ControlVersion
	binHash := setup.SelfHash()
	localAddr := getLANAddr(listenAddr)

	msg := &tunnel.RendezvousMsg{
		V:        1,
		Type:     "register",
		Role:     "control",
		ID:       "C:" + b.NodeIdentity.Address,
		CertHash: b.CertHash,
		FullSync: fullSync,
	}
	// When operator declares an external dial target (NAT bypass /
	// loopback workaround for co-hosted RV+broker setups), surface it so
	// RV stores AddrManual=true and peers dial that addr instead of the
	// TCP control source IP (which would be 127.0.0.1 when broker shares
	// the host with RV).
	if ext := b.Cfg.ExternalAddr; ext != "" {
		msg.Addr = ext
	}
	if fullSync {
		msg.LocalAddr = localAddr
		msg.Version = version
		msg.BinHash = binHash
		// OwnerAddress + register signature are stamped by isannd as it relays
		// this frame to RV (nlb_listener.go) — isannd holds the owner identity
		// and the hardware node key, so the broker ships no auth.json owner and
		// never signs.
	}
	return msg
}

// startPingLoop sends a periodic liveness ping to isannd to keep the
// broker→isannd TCP control pipe alive. Without this, isannd's outbound
// peer lookups fail with "no backend control conn open" whenever the
// pipe is dead (cold start before first send, or after isannd restart).
//
// Cadence comes from RV's register_ack (PingIntervalSec); before the
// first ack arrives, brokerFallbackPingIntervalSec applies.
func (b *Broker) startPingLoop(ctx context.Context) {
	cli := b.isanndClient()
	nodeID := "C:" + b.NodeIdentity.Address

	go func() {
		timer := time.NewTimer(b.effectivePingInterval())
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				// Re-arm with the (possibly updated) interval before each tick.
				timer.Reset(b.effectivePingInterval())
				ts := time.Now().UnixMilli()
				sig, sigErr := b.NodeIdentity.Sign(tunnel.PingDigest("control", ts))
				if sigErr != nil {
					log.Printf("[control] ping sign: %v", sigErr)
					continue
				}
				needRegister, err := cli.SendHeartbeat(ctx, &tunnel.HeartbeatPing{
					NodeID:      nodeID,
					Role:        "control",
					TimestampMs: ts,
					Signature:   sig,
				})
				if err != nil {
					log.Printf("[control] ping: %v", err)
					continue
				}
				if needRegister {
					if until := b.rejectBackoffUntilMs.Load(); until > 0 && time.Now().UnixMilli() < until {
						// Admission denied recently — skip the immediate re-register
						// so we don't spam the RV every heartbeat. The periodic
						// register loop still retries at its slower cadence.
						log.Printf("[control] need_register ignored — admission-denied backoff active")
					} else {
						msg := b.buildRegisterMsg(true)
						if msg == nil {
							continue
						}
						if rerr := cli.SendRegister(ctx, msg); rerr != nil {
							log.Printf("[control] re-register on need_register: %v", rerr)
						} else {
							log.Printf("[control] re-registered after RV need_register (id=%s)", msg.ID)
						}
					}
				}
			}
		}
	}()
}

// QUIC signal client + FullSync rotation loop removed — broker's
// register / heartbeat / session refresh now ride the NLB IsanndClient
// path established by startIsanndForwarder + startPingLoop.

// getLANAddr returns the LAN IP:port for the given listen address.
//
// The IP is the host's real internet-routable interface — found by asking
// the OS which source address it would use to reach a public addr (a UDP
// "dial" sends nothing, it just resolves the route). This avoids the old
// bug where enumerating net.Interfaces() and taking the first non-loopback
// IPv4 picked a *virtual* adapter (WSL vEthernet 172.25.224.x, Hyper-V,
// VirtualBox) that has no real route — that wrong LAN candidate (e.g.
// 172.25.224.1) then got gossiped to peers and broke same-LAN dials.
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
// WSL2 (172.16/12 Hyper-V default switch range, e.g. 172.25.224.x),
// link-local APIPA (169.254/16). Best-effort — the route-aware path above
// is the real fix; this only guards the enumeration fallback.
func isVirtualAdapterIP(ip net.IP) bool {
	if ip.IsLinkLocalUnicast() { // 169.254/16
		return true
	}
	v4 := ip.To4()
	if v4 == nil {
		return false
	}
	// 172.16.0.0/12 — Hyper-V / WSL2 NAT switch lives here (172.25.224.x
	// observed). Real home/office LANs use 192.168/16 or 10/8; 172.16/12 is
	// rare on physical nets, so skipping it in the fallback is safe enough.
	return v4[0] == 172 && v4[1] >= 16 && v4[1] <= 31
}
