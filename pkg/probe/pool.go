package probe

// pool.go — the round-robin over helper nodes (question writers, CLIP judges).
//
// The prober does not run these engines itself. A prober is a small machine
// whose job is to ask questions, not to host a 14B model or a CLIP validator,
// so both are delegated to allied nodes named in the config. Several may be
// listed; they are tried in turn and a failure moves to the next.
//
// WHY ALLIES AND NOT ANY PUBLIC NODE
//
// A question writer learns every question before it is asked, and a judge
// decides outright whether a node gets paid. Neither is a role you hand to a
// stranger — these entries name nodes the operator runs or trusts, and isannd
// admits the prober to a protected one automatically by attaching the active
// inference-access credential.
//
// WHAT A COOLDOWN IS FOR
//
// Without one, a dead entry at the head of the list is knocked on first every
// single round. The cooldown is not a health model — nothing is probed to see
// whether it recovered — it just stops a known-bad entry from being the first
// thing tried for a while.

import (
	"strings"
	"sync"
	"time"
)

// poolCooldown is how long a failed entry is skipped.
//
// Long enough that a down node is not retried every round (rounds are a minute
// and refills hourly), short enough that a restarted ally is picked back up
// without operator action.
const poolCooldown = 10 * time.Minute

// selfRefs are the spellings that mean "this node".
//
// Three of them because a config is written by hand and all three are natural
// to reach for. They are folded to one internal value so nothing downstream has
// to know there was more than one.
var selfRefs = map[string]bool{"": true, "this": true, "local": true, "self": true}

// poolEntry is one helper: which node, and which service on it.
type poolEntry struct {
	// Node is "this" for the local node, or a node id / favorite alias. It is
	// NOT resolved here — isannd resolves both aliases and self-references on
	// the /node/ path, so parsing it a second time would be a second place to
	// get it wrong.
	Node string
	// Service is the service name to call on that node.
	Service string
}

// IsSelf reports whether this entry means the local node.
func (e poolEntry) IsSelf() bool { return e.Node == selfNode }

// selfNode is the single internal spelling every self-reference folds to.
const selfNode = "this"

// String is what appears in logs — "this/llm-api", "0xabc…/llm-api".
func (e poolEntry) String() string { return e.Node + "/" + e.Service }

// parsePool turns configured entries into a pool.
//
// Item syntax is "<node>" or "<node>/<service>". The service is usually the
// same across allies, so the common case is just the node and the default
// carries it; a slash overrides it for that one entry. Blank items are dropped
// rather than treated as self — an empty STRING in a list reads as an editing
// slip, while an absent LIST is the deliberate "use this node" default and is
// handled by the config layer.
func parsePool(items []string, defaultService string) []poolEntry {
	out := make([]poolEntry, 0, len(items))
	for _, raw := range items {
		item := strings.TrimSpace(raw)
		if item == "" {
			continue
		}
		node, svc := item, defaultService
		if i := strings.LastIndex(item, "/"); i >= 0 {
			node = strings.TrimSpace(item[:i])
			if s := strings.TrimSpace(item[i+1:]); s != "" {
				svc = s
			}
		}
		if selfRefs[strings.ToLower(node)] {
			node = selfNode
		}
		if svc == "" {
			continue // nothing to call
		}
		out = append(out, poolEntry{Node: node, Service: svc})
	}
	return out
}

// pool hands out helper entries in round-robin order, skipping ones that
// recently failed.
type pool struct {
	mu      sync.Mutex
	entries []poolEntry
	cursor  int
	// downUntil is the cooldown expiry per entry index. Absent = available.
	downUntil map[int]time.Time
}

func newPool(entries []poolEntry) *pool {
	return &pool{entries: entries, downUntil: map[int]time.Time{}}
}

// Len is how many entries were configured, cooldowns included.
func (p *pool) Len() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// Empty reports whether the pool has nothing configured at all — the
// "explicitly turned off" state, which is different from "everything is
// currently in cooldown".
func (p *pool) Empty() bool { return p.Len() == 0 }

// Take walks the pool once from the cursor and calls fn for each available
// entry until one succeeds. It returns the entry that worked.
//
// One PASS, not a retry loop: every entry is tried at most once per call, so a
// caller cannot spin. If they all fail, the caller falls back — for questions
// that means arithmetic, for judging it means the image track sits this round
// out.
//
// The cursor advances past whichever entry succeeded, so consecutive calls
// spread load rather than hammering the first healthy one.
func (p *pool) Take(now time.Time, fn func(poolEntry) error) (poolEntry, error) {
	if p == nil || len(p.entries) == 0 {
		return poolEntry{}, errNoPoolEntries
	}
	n := len(p.entries)
	var firstErr error
	tried := 0

	for i := 0; i < n; i++ {
		p.mu.Lock()
		idx := (p.cursor + i) % n
		entry := p.entries[idx]
		until, down := p.downUntil[idx]
		p.mu.Unlock()

		if down && now.Before(until) {
			continue
		}
		tried++
		if err := fn(entry); err != nil {
			p.mu.Lock()
			p.downUntil[idx] = now.Add(poolCooldown)
			p.mu.Unlock()
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		p.mu.Lock()
		delete(p.downUntil, idx)
		p.cursor = (idx + 1) % n
		p.mu.Unlock()
		return entry, nil
	}

	if tried == 0 {
		// Everything is still cooling down. Distinct from "they all just
		// failed": nothing was called, so there is no error to report and no
		// point logging a failure that did not happen this round.
		return poolEntry{}, errAllCoolingDown
	}
	return poolEntry{}, firstErr
}

// poolError is a sentinel for the two "nothing was usable" outcomes.
type poolError string

func (e poolError) Error() string { return string(e) }

const (
	errNoPoolEntries  = poolError("no helper nodes configured")
	errAllCoolingDown = poolError("every helper node is in cooldown")
)
