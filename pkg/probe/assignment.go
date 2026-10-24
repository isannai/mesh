package probe

// assignment.go — this slot's group assignment, pulled from the RV.
//
// The RV cuts time into slots and, at each boundary, sorts the nodes that were
// online into groups and attaches probers to each. A prober learns it has been
// picked from its own isannd's register ack (assign_groups > 0) and then comes
// here for the detail:
//
//	GET /internal/rv/v1/faucet/current?prober=<us>
//
// Through isannd, not straight at the RV — the same route pkg/rvnodes takes for
// the directory. isannd knows which RV this node is registered to, so nothing
// here needs an RV address in its own config, and a node that changes RV keeps
// working with no edit.
//
// 🔴 THE FILTER IS A CONVENIENCE, NOT A PERMISSION. ?prober= narrows the answer
// to our groups; the unfiltered response is one request away and the RV serves
// it to anyone. Reading an assignment is harmless. What needs a key is ANSWERING
// as a prober, and that is the signature in bundle.go.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/isannai/mesh/pkg/faucet"
)

const (
	assignmentPath    = "/internal/rv/v1/faucet/current"
	assignmentTimeout = 15 * time.Second
)

// AssignGroup is one group as the RV renders it.
type AssignGroup struct {
	Members []string `json:"members"`
	Probers []string `json:"probers"`
	// Path is the merkle path from this group's leaf to the root. Empty when
	// the slot holds ONE group — the leaf is then the root already, and an
	// empty path is the correct proof rather than a missing one.
	Path []string `json:"path"`

	// What the group is checked against, from an RV that plans each slot by budget
	// (workspace docs/todo/probe-오라클노드.md 3절). All empty from an older RV, which
	// means the old checks (Planned).
	//
	//	BudgetID  the budget that pays the group: the one its nodes registered in,
	//	          or the system budget. 0 when no system budget is active.
	//	Model     that budget's model hash; empty for the system budget
	//	MinGrade  the lowest GPU memory grade the budget accepts (gpugrade.go); 0 or
	//	          absent means no limit. Only a budget with a model sets it.
	//	Mode      "quiz" from a planning RV, empty from an older one (CheckMode)
	//	MetaHash  the budget's meta hash, kept as the RV sends it
	//	RequireTPM    only TPM-verified nodes may be paid from the budget; absent means
	//	              no requirement (an older RV sends neither this nor RequireNamed)
	//	RequireNamed  only nodes with a live Name-service subdomain may be paid from
	//	              the budget; absent means no requirement
	BudgetID     uint64 `json:"budget_id,omitempty"`
	Model        string `json:"model,omitempty"`
	MinGrade     int    `json:"min_grade,omitempty"`
	Mode         string `json:"mode,omitempty"`
	MetaHash     string `json:"meta_hash,omitempty"`
	RequireTPM   bool   `json:"require_tpm,omitempty"`
	RequireNamed bool   `json:"require_named,omitempty"`
}

// ModeQuiz is the one way a group is checked now: the quiz (quiz.go). The RV used
// to name other modes (an oracle node's answer as the reference). It no longer
// does, and this prober does not read them.
const ModeQuiz = "quiz"

// CheckMode is how a node of this group is checked: quiz for a group from an
// older RV, which names no mode.
func (g AssignGroup) CheckMode() string {
	if g.Mode == "" {
		return ModeQuiz
	}
	return g.Mode
}

// Assignment is one slot as the RV renders it.
//
// ChainID and FaucetAddr are the deployment that RV issues vouchers for. A
// ticket signs them, and the RV refuses a claim whose pair is not its own, so
// they are taken from here (ticketDeployment). An RV older than the field sends
// neither and leaves them zero.
type Assignment struct {
	RV         string        `json:"rv"`
	ChainID    uint64        `json:"chain_id"`
	FaucetAddr string        `json:"faucet_addr"`
	SlotSec    int           `json:"slot_sec"`
	Epoch      int64         `json:"epoch"`
	Root       string        `json:"root"`
	N          int           `json:"n"`
	Groups     []AssignGroup `json:"groups"`

	root faucet.Hash // parsed once, on fetch
}

// Root32 is the parsed root. Every bundle signs over it, so parsing once on
// arrival keeps a malformed root from being discovered per-shot.
func (a Assignment) Root32() faucet.Hash { return a.root }

// GroupOf returns the index of the group holding addr, or -1.
//
// Which group a member is in decides which bundle it is shown. Every group in
// this response is one we were assigned, so a member found here is one we are
// meant to check.
func (a Assignment) GroupOf(addr string) int {
	addr = strings.ToLower(strings.TrimSpace(addr))
	for i, g := range a.Groups {
		for _, m := range g.Members {
			if strings.EqualFold(strings.TrimSpace(m), addr) {
				return i
			}
		}
	}
	return -1
}

// Planned reports whether the RV grouped this slot by budget registration. Then
// the group, not the node's model hash, decides which budget a ticket names.
func (a Assignment) Planned() bool {
	for _, g := range a.Groups {
		if g.Mode != "" {
			return true
		}
	}
	return false
}

// Stale reports whether this assignment has outlived its slot.
//
// Computed from OUR clock rather than trusted from the response: an RV that
// stops answering leaves the last assignment sitting in memory, and firing a
// previous slot's bundle at a node produces a rejection that looks like the
// node misbehaving.
func (a Assignment) Stale(now time.Time) bool {
	sec := a.SlotSec
	if sec <= 0 {
		sec = faucet.SlotSeconds
	}
	return faucet.EpochAt(now.Unix(), sec) != a.Epoch
}

// FetchAssignment pulls this prober's groups for the current slot.
//
// ok=false with no error means "no assignment for us right now" — the RV has no
// slot, or we are not a prober in it. That is an ordinary state, not a fault:
// an operator removing a line from the RV's faucet.json is exactly how a prober
// is retired, and it must not read as an outage.
func FetchAssignment(isanndURL, proberAddr string) (Assignment, bool, error) {
	proberAddr = strings.ToLower(strings.TrimSpace(proberAddr))
	if proberAddr == "" {
		return Assignment{}, false, fmt.Errorf("no prober address to ask for")
	}

	endpoint := strings.TrimRight(isanndURL, "/") + assignmentPath +
		"?prober=" + url.QueryEscape(proberAddr)

	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return Assignment{}, false, fmt.Errorf("fetch assignment: %w", err)
	}
	c := &http.Client{Timeout: assignmentTimeout}
	resp, err := c.Do(req)
	if err != nil {
		return Assignment{}, false, fmt.Errorf("fetch assignment: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return Assignment{}, false, fmt.Errorf("fetch assignment: read body: %w", err)
	}
	if resp.StatusCode == http.StatusNotFound {
		// The RV distinguishes "no slot" from "not you" only in prose; both are
		// absences and neither is actionable here.
		return Assignment{}, false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return Assignment{}, false, fmt.Errorf("fetch assignment: %s: %s", resp.Status, bodySnippet(body))
	}

	var a Assignment
	if err := json.Unmarshal(body, &a); err != nil {
		return Assignment{}, false, fmt.Errorf("fetch assignment: %w", err)
	}
	if a.Epoch == 0 || len(a.Groups) == 0 {
		return Assignment{}, false, nil
	}
	root, err := faucet.ParseHash(a.Root)
	if err != nil {
		return Assignment{}, false, fmt.Errorf("fetch assignment: unreadable root %q: %w", a.Root, err)
	}
	a.root = root
	return a, true, nil
}

// bodySnippet trims an error body to something a log line can carry.
func bodySnippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "..."
	}
	return s
}
