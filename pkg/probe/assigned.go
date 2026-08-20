package probe

// assigned.go — turning an assignment into shots.
//
// Two sources, each answering a different question, and neither can answer the
// other's:
//
//	assignment  WHO to check. Addresses, drawn by the RV at the slot boundary
//	            and fixed into a merkle root. Nothing about reachability.
//	directory   HOW to reach them. Node id, address, which services are up and
//	            which model each runs. The RV cannot know this — it only knows
//	            who was registered when the snapshot was taken.
//
// So the directory is filtered DOWN to the assignment, never the other way
// round. A node in our group with no ready service is skipped this round; a
// node with a ready service that is not in our group is somebody else's to
// check.
//
// 🔴 This is what replaced the uptime ladder. Shots used to be earned by being
// present for 3h, 6h, 9h, 12h, 15h of a day, measured by this prober's own
// hourly polls, because there was no other way to tell a node that stayed up
// from one that flickered. The slot answers that now: eligibility is "were you
// registered at the boundary", and it is the RV that decides, once, for
// everyone. A prober no longer has an opinion about who deserves a shot.

import (
	"log"
	"strings"
	"time"
)

// assignedTargets keeps only the targets this prober was assigned, and attaches
// each one's proof.
//
// A target whose bundle cannot be built is DROPPED rather than fired at unproven.
// An unproven shot still costs the node a real inference, and once the free gate
// exists it would be spending that node's paid capacity while proving nothing —
// so a signing failure has to stop the shot, not degrade it.
func (p *Prober) assignedTargets(in []Target) []Target {
	if !p.hasAssign {
		return nil
	}
	if p.assign.Stale(time.Now()) {
		// The slot ended while we were between polls. Firing a previous slot's
		// bundle produces a rejection that reads like the NODE misbehaving,
		// which is the most expensive kind of wrong log line to leave behind.
		log.Printf("[probe] epoch %d has passed — holding fire until the next assignment", p.assign.Epoch)
		return nil
	}

	out := make([]Target, 0, len(in))
	failed := 0
	for _, t := range in {
		addr := strings.ToLower(strings.TrimSpace(nodeAddressOf(t.Node.ID)))
		if addr == "" {
			continue
		}
		// A prober is an ordinary member of its own group — the RV seats it
		// like anyone else, because the leaf has to hash the group as it really
		// is. It just cannot be its own evidence: firing at itself means signing
		// for a machine it controls, which is not a measurement under any
		// reading, and the merkle and the signature both still check out.
		//
		// 🔴 The exception is FireAtSelf, and it must be honoured HERE too. That
		// flag exists for a single-PC setup with nowhere else to aim, and
		// eligible() already respects it through p.exclude — so blocking self
		// unconditionally at this second gate would leave the flag switched on
		// and doing nothing, which is worse than not having it.
		if addr == p.self && !p.cfg.FireAtSelf {
			continue
		}
		gi := p.assign.GroupOf(addr)
		if gi < 0 {
			continue // online and healthy, but not ours this slot
		}
		hdr, err := BuildProbeHeader(p.assign, gi, addr, p.signKey)
		if err != nil {
			log.Printf("[probe] cannot prove a check of %s: %v", short(t.Node.ID), err)
			failed++
			continue
		}
		t.Probe = hdr
		out = append(out, t)
	}
	if failed > 0 {
		log.Printf("[probe] %d target(s) skipped: no proof could be built", failed)
	}
	return out
}
