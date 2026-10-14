package probe

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/faucet"
	"github.com/isannai/mesh/pkg/rvnodes"
)

func TestCompareStore(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	at := time.Now()
	row := CompareRow{Epoch: 7, Group: 1, Model: "0xaa", NodeID: "S:0x01", Round: 1, Service: "llm", Slash24: "10.0.0",
		AskedAt: at, Key: "the lighthouse", Answer: "The lighthouse stood.", Outcome: OutcomeAnswered}
	if err := s.RecordCompare(row); err != nil {
		t.Fatal(err)
	}
	// The first answer stands.
	again := row
	again.Key = "something else"
	if err := s.RecordCompare(again); err != nil {
		t.Fatal(err)
	}
	r2 := row
	r2.Round = 2 // the same node in the other round is a row of its own
	other := row
	other.Group, other.NodeID = 2, "S:0x02" // another group, kept apart
	held := row
	held.Round, held.Outcome, held.Key = 0, OutcomeSettled, ""
	for _, r := range []CompareRow{r2, other, held} {
		if err := s.RecordCompare(r); err != nil {
			t.Fatal(err)
		}
	}

	open1, err := s.OpenCompare(7, 1)
	if err != nil || len(open1) != 2 {
		t.Fatalf("round 1 open = %d rows, err %v", len(open1), err)
	}
	if open1[0].Key != "the lighthouse" || open1[0].Group != 1 {
		t.Errorf("first answer was replaced: %+v", open1[0])
	}
	if got, _ := s.OpenCompare(7, 0); len(got) != 1 || got[0].Outcome != OutcomeSettled {
		t.Errorf("settled tickets = %+v", got)
	}
	if got, _ := s.OpenCompare(8, 1); len(got) != 0 {
		t.Error("another slot's rows were returned")
	}
	asked, _ := s.CompareAsked(7, 2)
	if len(asked) != 1 || !asked["S:0x01"] {
		t.Errorf("asked in round 2 = %v", asked)
	}

	if err := s.SetCompareDecision(open1[0], DecisionLose); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCompareDecision(r2, DecisionWin); err != nil {
		t.Fatal(err)
	}
	dec, _ := s.CompareDecisions(7)
	if d := dec["S:0x01"]; d.Round1 != DecisionLose || d.Round2 != DecisionWin {
		t.Errorf("decisions = %+v", d)
	}
	// Settled tickets are not counted: only the rounds are.
	if err := s.SetCompareDecision(held, DecisionLose); err != nil {
		t.Fatal(err)
	}
	tally, err := s.CompareStats("S:0x01", at.Add(-time.Hour))
	if err != nil || tally.Lose != 1 || tally.Win != 1 {
		t.Errorf("tally = %+v err %v", tally, err)
	}

	if n, _ := s.PruneCompare(at.Add(time.Hour)); n != 4 {
		t.Errorf("pruned %d rows, want 4", n)
	}
}

func TestPruneShots(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	old := Shot{FiredAt: time.Now().AddDate(0, 0, -40), NodeID: "S:0x01", Outcome: OutcomeAnswered}
	fresh := Shot{FiredAt: time.Now(), NodeID: "S:0x02", Outcome: OutcomeAnswered}
	for _, sh := range []Shot{old, fresh} {
		if _, err := s.RecordShot(sh); err != nil {
			t.Fatal(err)
		}
	}
	n, err := s.PruneShots(time.Now().AddDate(0, 0, -30))
	if err != nil || n != 1 {
		t.Fatalf("pruned %d, err %v, want the one old shot", n, err)
	}
}

const compareEpoch = int64(165900)

func compareSlotStart() time.Time {
	return time.Unix(faucet.SlotStartAt(compareEpoch, faucet.SlotSeconds), 0)
}

// compareProber is a prober with one group of `n` nodes on the 14.768B model, each
// on its own /24, and a live budget for that model.
func compareProber(t *testing.T, n int) *Prober {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "probe.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	svc := rvnodes.Service{Name: "llm", ModelHash: qwen14, ModelClass: &rvnodes.ModelClass{ParamsCore: 14768307200}}
	p := &Prober{
		cfg:   Config{ObservationDays: 30, FireLeadSec: 1800, FireTailSec: 1800, FireIntervalSec: 60, TextDeadline: 5},
		store: s, firer: NewFirer("http://127.0.0.1:1"), hasAssign: true,
		rng:          rand.New(rand.NewSource(1)),
		models:       map[string]budgetRef{"0x" + qwen14Hex: {"2"}},
		budgetsRead:  true,
		systemBudget: "1",
	}
	grp := AssignGroup{}
	for i := 0; i < n; i++ {
		addr := fmt.Sprintf("0x%040x", i+1)
		grp.Members = append(grp.Members, addr)
		node := rvnodes.Node{ID: "S:" + addr, Services: []rvnodes.Service{svc}}
		p.targets = append(p.targets, Target{Node: node, Service: svc, Slash24: fmt.Sprintf("10.0.%d", i)})
	}
	p.assign = Assignment{SlotSec: faucet.SlotSeconds, Epoch: compareEpoch, Groups: []AssignGroup{grp}}
	return p
}

// compareNode is a node bridge that answers each node with a text of the test's
// choosing and keeps what it was asked.
type compareNode struct {
	mu       sync.Mutex
	prompts  []string
	asked    map[string]int                   // submits per node id
	answer   func(node string) string         // the text a node writes
	byPrompt func(node, prompt string) string // if set, used instead of answer
	last     map[string]string                // the last prompt each node was sent
	refuse   map[string]bool                  // nodes that reject the submit
}

func (c *compareNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	node := strings.TrimPrefix(path, "/node/")
	if i := strings.Index(node, "/"); i >= 0 {
		node = node[:i]
	}
	switch {
	case strings.HasSuffix(path, "/run-schema"):
		_ = json.NewEncoder(w).Encode(map[string]any{
			"default": manifest.RunSpec{Path: "/v1/completions", Params: []manifest.RunParam{
				{Name: "prompt", Type: "string", Required: true},
				{Name: "max_tokens", Type: "int"},
				{Name: "temperature", Type: "float"},
				{Name: "stop", Type: "string"},
			}},
			"variants": []any{},
		})
	case strings.HasSuffix(path, "/v1/jobs") && r.Method == http.MethodPost:
		var body map[string]map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		c.mu.Lock()
		if c.asked == nil {
			c.asked = map[string]int{}
		}
		c.asked[node]++
		prompt, _ := body["run"]["prompt"].(string)
		c.prompts = append(c.prompts, prompt)
		if c.last == nil {
			c.last = map[string]string{}
		}
		c.last[node] = prompt
		refused := c.refuse[node]
		c.mu.Unlock()
		if refused {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"job_id": "job-" + node, "queue_max": 8})
	case strings.HasSuffix(path, "/result"):
		id := strings.TrimSuffix(path, "/result")
		id = id[strings.LastIndex(id, "/job-")+len("/job-"):]
		text := ""
		if c.byPrompt != nil {
			c.mu.Lock()
			prompt := c.last[id]
			c.mu.Unlock()
			text = c.byPrompt(id, prompt)
		} else {
			text = c.answer(id)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"text": text}},
			"usage":   map[string]any{"prompt_tokens": 30, "completion_tokens": 12},
		})
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "done"})
	}
}

func issuedBy(p *Prober, into *[]string) {
	p.issueFn = func(tg Target, _ time.Time, _ string) { *into = append(*into, tg.Node.ID) }
}
