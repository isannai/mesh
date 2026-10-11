package probe

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/faucet"
	"github.com/isannai/mesh/pkg/rvnodes"
)

// votes builds a cohort: "<slash24>:<key>", key "" is a node that did not answer.
func votes(spec ...string) []cmpVote {
	var out []cmpVote
	for i, s := range spec {
		net, key, _ := strings.Cut(s, ":")
		out = append(out, cmpVote{Node: fmt.Sprintf("n%d", i), Slash24: net, Key: key})
	}
	return out
}

func decisionsOf(m map[string]string, n int) string {
	var parts []string
	for i := 0; i < n; i++ {
		parts = append(parts, m[fmt.Sprintf("n%d", i)])
	}
	return strings.Join(parts, " ")
}

// 🔴 More than half wins, a draw drops nobody, fewer than three are not compared,
// and a /24 is one voice. These are the rules a round is judged by.
func TestDecideCohort(t *testing.T) {
	cases := []struct {
		name  string
		votes []cmpVote
		want  string
	}{
		{"four of five agree", votes("a:x", "b:x", "c:x", "d:x", "e:y"), "win win win win lose"},
		{"two answered: too few to judge", votes("a:x", "b:y"), "skip skip"},
		{"two answered and one silent: still too few", votes("a:x", "b:y", "c:"), "skip skip skip"},
		{"exactly half is a draw", votes("a:x", "b:x", "c:y", "d:y"), "skip skip skip skip"},
		{"three different answers", votes("a:x", "b:y", "c:z"), "skip skip skip"},
		{"two, one, one, one: the largest is not half", votes("a:x", "b:x", "c:y", "d:z", "e:w"), "skip skip skip skip skip"},
		{"silent member loses once the round is judged", votes("a:x", "b:x", "c:x", "d:"), "win win win lose"},
		{"a minority of one is dropped", votes("a:x", "b:x", "c:y"), "win win lose"},
		{
			// Five machines behind one /24 are one voice: three honest nodes on three
			// networks outweigh them, although the farm has more nodes.
			"a farm is one voice",
			votes("farm:f", "farm:f", "farm:f", "farm:f", "farm:f", "h1:x", "h2:x", "h3:x"),
			"lose lose lose lose lose win win win",
		},
		{
			// Two farm nodes and one honest node: the farm's voice is one, the honest
			// one is one, neither is more than half.
			"farm against one honest node",
			votes("farm:f", "farm:f", "h1:x"),
			"skip skip skip",
		},
		{"unknown networks each count", votes(":x", ":x", ":y"), "win win lose"},
	}
	for _, c := range cases {
		got := decisionsOf(decideCohort(c.votes), len(c.votes))
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAnswerKey(t *testing.T) {
	if got := answerKey("  The  LIGHTHOUSE\nwas   quiet. "); got != "the lighthouse was quiet." {
		t.Errorf("answerKey = %q", got)
	}
	if answerKey("   ") != "" {
		t.Error("an empty answer must have an empty key")
	}
	long := strings.Repeat("ab ", 40)
	if n := len([]rune(answerKey(long))); n != compareKeyRunes {
		t.Errorf("key is %d runes, want %d", n, compareKeyRunes)
	}
	// Two answers that agree at the start and drift later are the same answer.
	if answerKey(strings.Repeat("x", compareKeyRunes)+" tail one") != answerKey(strings.Repeat("x", compareKeyRunes)+" tail two") {
		t.Error("the end of an answer must not count")
	}
}

// The prompt is the same for everyone in a cohort in a round and different for
// every other cohort and every other round.
func TestComparePrompt(t *testing.T) {
	k := cohortKey{Epoch: 165900, Group: 2, Model: "0x" + qwen14Hex}
	if comparePrompt(k, 1) != comparePrompt(k, 1) {
		t.Fatal("the prompt must be the same each time it is built")
	}
	seen := map[string]bool{comparePrompt(k, 1): true}
	for name, p := range map[string]string{
		"round 2":       comparePrompt(k, 2),
		"next slot":     comparePrompt(cohortKey{Epoch: 165901, Group: 2, Model: k.Model}, 1),
		"another group": comparePrompt(cohortKey{Epoch: 165900, Group: 3, Model: k.Model}, 1),
		"another model": comparePrompt(cohortKey{Epoch: 165900, Group: 2, Model: "0x" + strings.Repeat("ab", 32)}, 1),
	} {
		if seen[p] {
			t.Errorf("%s got the prompt of another cohort", name)
		}
		seen[p] = true
	}
	if p := comparePrompt(k, 1); !strings.Contains(p, "number") || !strings.HasSuffix(p, ".") {
		t.Errorf("prompt = %q", p)
	}
}

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
	held.Round, held.Outcome, held.Key = 0, OutcomeHeld, ""
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
	if got, _ := s.OpenCompare(7, 0); len(got) != 1 || got[0].Outcome != OutcomeHeld {
		t.Errorf("held tickets = %+v", got)
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
	// Held tickets are not counted: only the rounds are.
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
		models:       map[string]budgetRef{"0x" + qwen14Hex: {"2", "14.768", 14768307200}},
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
	mu      sync.Mutex
	prompts []string
	asked   map[string]int           // submits per node id
	answer  func(node string) string // the text a node writes
	refuse  map[string]bool          // nodes that reject the submit
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
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"text": c.answer(id)}},
			"usage":   map[string]any{"prompt_tokens": 30, "completion_tokens": 12},
		})
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "done"})
	}
}

func TestBuildCohorts(t *testing.T) {
	// Two nodes: below the minimum, nobody is compared.
	if got := compareProber(t, 2).buildCohorts(); len(got) != 0 {
		t.Errorf("a group of two formed a cohort: %v", got)
	}
	// Three: all three are in it.
	p := compareProber(t, 3)
	got := p.buildCohorts()
	if len(got) != 3 {
		t.Fatalf("cohort has %d nodes, want 3", len(got))
	}
	for id, k := range got {
		if k.Epoch != compareEpoch || k.Group != 0 || k.Model != "0x"+qwen14Hex {
			t.Errorf("%s: %+v", id, k)
		}
	}
	// A node whose reported size does not fit is not in it, and leaves two.
	bad := compareProber(t, 3)
	bad.targets[0].Service.ModelClass = &rvnodes.ModelClass{ParamsCore: 1777088000}
	bad.targets[0].Node.Services = []rvnodes.Service{bad.targets[0].Service}
	if got := bad.buildCohorts(); len(got) != 0 {
		t.Errorf("a node that does not fit still counted: %v", got)
	}
	// The switch turns it off.
	off := compareProber(t, 3)
	off.cfg.NoCompare = true
	if len(off.buildCohorts()) != 0 {
		t.Error("NoCompare did not stop the cohorts")
	}
}

// The two sampling windows are the first and the last half hour of the firing
// window: 0:30 to 1:00 and 2:00 to 2:30 of a three hour slot.
func TestCompareWindows(t *testing.T) {
	w := compareWindows(faucet.SlotSeconds, 30*time.Minute, 30*time.Minute)
	want := [2][2]time.Duration{{30 * time.Minute, time.Hour}, {2 * time.Hour, 2*time.Hour + 30*time.Minute}}
	if w != want {
		t.Errorf("windows = %v, want %v", w, want)
	}
	// A firing window too short for two half hours is split in half.
	short := compareWindows(3600, 10*time.Minute, 10*time.Minute) // 10m .. 50m
	if short[0][1] != 30*time.Minute || short[1][0] != 30*time.Minute {
		t.Errorf("short windows = %v", short)
	}

	p := compareProber(t, 3)
	start := compareSlotStart()
	for _, c := range []struct {
		at    time.Duration
		round int
	}{
		{0, 0}, {29 * time.Minute, 0}, {30 * time.Minute, 1}, {59*time.Minute + 59*time.Second, 1},
		{time.Hour, 0}, {90 * time.Minute, 0}, {2 * time.Hour, 2}, {2*time.Hour + 29*time.Minute, 2},
		{2*time.Hour + 30*time.Minute, 0}, {2*time.Hour + 45*time.Minute, 0},
	} {
		if got := p.compareRoundAt(start.Add(c.at)); got != c.round {
			t.Errorf("at %v: round %d, want %d", c.at, got, c.round)
		}
	}
}

// The moment a node is asked is random inside the window, stays the same for the
// slot, and stops short of the window's end so no node falls between two ticks.
func TestCompareOffset(t *testing.T) {
	p := compareProber(t, 3)
	seen := map[time.Duration]bool{}
	for i := 0; i < 50; i++ {
		id := fmt.Sprintf("S:0x%040x", i)
		off := p.compareOffset(1, id, compareWindow)
		if off < 0 || off >= compareWindow-2*time.Minute {
			t.Fatalf("offset %v is outside the window", off)
		}
		if again := p.compareOffset(1, id, compareWindow); again != off {
			t.Fatalf("the offset changed between ticks: %v then %v", off, again)
		}
		seen[off] = true
	}
	if len(seen) < 25 {
		t.Errorf("only %d distinct moments for 50 nodes: not random", len(seen))
	}
	// A new slot draws again.
	p.compareOffset(1, "S:0x01", compareWindow)
	p.assign.Epoch++
	if p.compareAtEpoch == p.assign.Epoch {
		t.Fatal("test setup")
	}
	p.compareOffset(1, "S:0x01", compareWindow)
	if p.compareAtEpoch != p.assign.Epoch {
		t.Error("the offsets of the last slot were kept")
	}
}

// 🔴 Each cohort node is asked once per round, in its window, and never asked
// again: not when it failed, not on the next tick.
func TestFireCompareAsksOncePerRound(t *testing.T) {
	node := &compareNode{answer: func(string) string { return "the lighthouse stood alone" },
		refuse: map[string]bool{}}
	srv := httptest.NewServer(node)
	defer srv.Close()

	p := compareProber(t, 3)
	p.firer = NewFirer(srv.URL)
	start := compareSlotStart()
	node.refuse[p.targets[2].Node.ID] = true // this one will not take the request

	// Outside both windows nothing is sent.
	p.fireCompare(start.Add(10 * time.Minute))
	p.fireCompare(start.Add(90 * time.Minute))
	if len(node.prompts) != 0 {
		t.Fatalf("asked outside the windows: %d prompts", len(node.prompts))
	}

	// The end of window 1: every node's random moment has come.
	p.fireCompare(start.Add(59*time.Minute + 50*time.Second))
	if len(node.prompts) != 3 {
		t.Fatalf("round 1 sent %d prompts, want 3", len(node.prompts))
	}
	want := comparePrompt(cohortKey{Epoch: compareEpoch, Group: 0, Model: "0x" + qwen14Hex}, 1)
	for _, pr := range node.prompts {
		if pr != want {
			t.Errorf("prompt %q, want the cohort's round 1 prompt", pr)
		}
	}
	rows, _ := p.store.OpenCompare(compareEpoch, 1)
	if len(rows) != 3 {
		t.Fatalf("recorded %d rows, want 3", len(rows))
	}
	for _, r := range rows {
		silent := r.NodeID == p.targets[2].Node.ID
		if silent && (r.Key != "" || r.Outcome == OutcomeAnswered) {
			t.Errorf("the node that refused has an answer: %+v", r)
		}
		if !silent && (r.Key != "the lighthouse stood alone" || r.Outcome != OutcomeAnswered) {
			t.Errorf("answer not recorded: %+v", r)
		}
	}

	// No second try: not the failed one, not anyone.
	p.fireCompare(start.Add(59*time.Minute + 55*time.Second))
	if len(node.prompts) != 3 {
		t.Errorf("round 1 was asked again: %d prompts", len(node.prompts))
	}

	// Round 2 asks everyone once more, with its own prompt.
	p.fireCompare(start.Add(2*time.Hour + 29*time.Minute + 50*time.Second))
	if len(node.prompts) != 6 {
		t.Fatalf("after round 2 %d prompts, want 6", len(node.prompts))
	}
	if node.prompts[5] == want || node.prompts[5] != comparePrompt(cohortKey{Epoch: compareEpoch, Group: 0, Model: "0x" + qwen14Hex}, 2) {
		t.Errorf("round 2 used %q", node.prompts[5])
	}
}

// Nothing is sent when the switch is off or the group is too small.
func TestFireCompareStaysQuiet(t *testing.T) {
	node := &compareNode{answer: func(string) string { return "x" }}
	srv := httptest.NewServer(node)
	defer srv.Close()
	at := compareSlotStart().Add(59*time.Minute + 50*time.Second)

	small := compareProber(t, 2)
	small.firer = NewFirer(srv.URL)
	small.fireCompare(at)

	off := compareProber(t, 3)
	off.firer = NewFirer(srv.URL)
	off.cfg.NoCompare = true
	off.fireCompare(at)

	if len(node.prompts) != 0 {
		t.Errorf("sent %d prompts", len(node.prompts))
	}
}

// The quiz holds the ticket and sends nothing; the sentence requests have their
// own windows.
func TestDeferToCompareHoldsTheTicket(t *testing.T) {
	node := &compareNode{answer: func(string) string { return "x" }}
	srv := httptest.NewServer(node)
	defer srv.Close()

	p := compareProber(t, 3)
	p.firer = NewFirer(srv.URL)
	p.cohorts = p.buildCohorts()

	if !p.deferToCompare(p.targets[0]) {
		t.Fatal("a node in a cohort was not held")
	}
	if len(node.prompts) != 0 {
		t.Errorf("holding sent %d requests", len(node.prompts))
	}
	held, _ := p.store.OpenCompare(compareEpoch, 0)
	if len(held) != 1 || held[0].Outcome != OutcomeHeld || held[0].NodeID != p.targets[0].Node.ID {
		t.Fatalf("held = %+v", held)
	}

	// Not in a cohort: issued at once.
	stranger := p.targets[0]
	stranger.Node.ID = "S:0x" + strings.Repeat("f", 40)
	if p.deferToCompare(stranger) {
		t.Error("a node outside every cohort was held")
	}
	// A cohort left over from another slot does not hold anything.
	p.assign.Epoch++
	if p.deferToCompare(p.targets[1]) {
		t.Error("a stale cohort held a ticket")
	}
}

// compareRound writes a round's answers: key per target index, "" = no answer.
func compareRound(t *testing.T, p *Prober, round int, keys ...string) {
	t.Helper()
	for i, k := range keys {
		tg := p.targets[i]
		outcome := OutcomeAnswered
		if k == "" {
			outcome = OutcomeTimeout
		}
		err := p.store.RecordCompare(CompareRow{
			Epoch: compareEpoch, Group: 0, Model: "0x" + qwen14Hex, NodeID: tg.Node.ID, Round: round,
			Service: "llm", Slash24: tg.Slash24, AskedAt: compareSlotStart().Add(time.Hour), Key: k, Outcome: outcome,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func holdTickets(t *testing.T, p *Prober, idx ...int) {
	t.Helper()
	for _, i := range idx {
		tg := p.targets[i]
		err := p.store.RecordCompare(CompareRow{
			Epoch: compareEpoch, Group: 0, Model: "0x" + qwen14Hex, NodeID: tg.Node.ID, Round: 0,
			Service: "llm", Slash24: tg.Slash24, AskedAt: compareSlotStart().Add(time.Hour), Outcome: OutcomeHeld,
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func issuedBy(p *Prober, into *[]string) {
	p.issueFn = func(tg Target, _ time.Time, _ string) { *into = append(*into, tg.Node.ID) }
}

// 🔴 A ticket needs the quiz AND both rounds. Failing either round is no ticket,
// and the rounds are judged when their window has closed, not before.
func TestJudgeAndSettleNeedsBothRounds(t *testing.T) {
	p := compareProber(t, 5)
	var issued []string
	issuedBy(p, &issued)
	start := compareSlotStart()

	holdTickets(t, p, 0, 1, 2, 3, 4)
	// Round 1: node 4 is the odd one out. Round 2: node 3 is.
	compareRound(t, p, 1, "a1", "a1", "a1", "a1", "b1")
	compareRound(t, p, 2, "a2", "a2", "a2", "b2", "a2")

	// While the first window is still open nothing is judged.
	p.judgeRounds(start.Add(59 * time.Minute))
	p.settleTickets(start.Add(59 * time.Minute))
	if open, _ := p.store.OpenCompare(compareEpoch, 1); len(open) != 5 {
		t.Fatal("round 1 was judged before its window closed")
	}

	// Just after it: round 1 is judged, round 2 is not, no ticket yet.
	p.judgeRounds(start.Add(time.Hour + 2*time.Minute))
	p.settleTickets(start.Add(time.Hour + 2*time.Minute))
	if open, _ := p.store.OpenCompare(compareEpoch, 1); len(open) != 0 {
		t.Error("round 1 was not judged once its window closed")
	}
	if open, _ := p.store.OpenCompare(compareEpoch, 2); len(open) != 5 {
		t.Error("round 2 was judged early")
	}
	if len(issued) != 0 {
		t.Fatalf("tickets issued before round 2 was judged: %v", issued)
	}

	// After the second window: round 2 is judged and the tickets go out.
	p.judgeRounds(start.Add(2*time.Hour + 31*time.Minute))
	p.settleTickets(start.Add(2*time.Hour + 31*time.Minute))
	if len(issued) != 3 {
		t.Fatalf("issued %d tickets, want the 3 that failed neither round: %v", len(issued), issued)
	}
	for _, id := range issued {
		if id == p.targets[3].Node.ID || id == p.targets[4].Node.ID {
			t.Errorf("%s failed a round and was paid", id)
		}
	}
	if tally, _ := p.store.CompareStats(p.targets[4].Node.ID, start.Add(-time.Hour)); tally.Lose != 1 {
		t.Errorf("the lost round was not recorded: %+v", tally)
	}

	// A second pass finds nothing left to decide.
	p.judgeRounds(start.Add(2*time.Hour + 40*time.Minute))
	p.settleTickets(start.Add(2*time.Hour + 40*time.Minute))
	if len(issued) != 3 {
		t.Errorf("a settled ticket was issued again: %v", issued)
	}
}

// A node that gave no answer in a round that was judged fails it.
func TestSilentNodeFailsAJudgedRound(t *testing.T) {
	p := compareProber(t, 4)
	var issued []string
	issuedBy(p, &issued)
	holdTickets(t, p, 0, 1, 2, 3)
	compareRound(t, p, 1, "a", "a", "a", "") // node 3 timed out
	compareRound(t, p, 2, "a", "a", "a", "a")

	at := compareSlotStart().Add(2*time.Hour + 31*time.Minute)
	p.judgeRounds(at)
	p.settleTickets(at)
	if len(issued) != 3 {
		t.Fatalf("issued %v, want the three that answered", issued)
	}
	for _, id := range issued {
		if id == p.targets[3].Node.ID {
			t.Error("the node that did not answer was paid")
		}
	}
}

// A round too small or too split to judge pays everybody.
func TestUnjudgedRoundsPayEveryone(t *testing.T) {
	p := compareProber(t, 4)
	var issued []string
	issuedBy(p, &issued)
	holdTickets(t, p, 0, 1, 2, 3)
	// Round 1: only two answered. Round 2: a 2-2 draw.
	compareRound(t, p, 1, "a", "b", "", "")
	compareRound(t, p, 2, "a", "a", "b", "b")

	at := compareSlotStart().Add(2*time.Hour + 31*time.Minute)
	p.judgeRounds(at)
	p.settleTickets(at)
	if len(issued) != 4 {
		t.Fatalf("issued %d tickets, want all 4: %v", len(issued), issued)
	}
}

// A node never asked in a round has nothing against it; a quiz that finishes
// after the settle is settled on the next tick.
func TestSettleHandlesLateQuizAndMissingRounds(t *testing.T) {
	p := compareProber(t, 4)
	var issued []string
	issuedBy(p, &issued)
	start := compareSlotStart()

	holdTickets(t, p, 0, 1, 2)
	compareRound(t, p, 1, "a", "a", "a")
	compareRound(t, p, 2, "a", "a", "a")
	at := start.Add(2*time.Hour + 31*time.Minute)
	p.judgeRounds(at)
	p.settleTickets(at)
	if len(issued) != 3 {
		t.Fatalf("issued %v", issued)
	}

	// Node 3's quiz passes only now and it was never asked in either round.
	holdTickets(t, p, 3)
	p.settleTickets(at.Add(time.Minute))
	if len(issued) != 4 || issued[3] != p.targets[3].Node.ID {
		t.Errorf("the late quiz was not settled: %v", issued)
	}
}

// The switch stops the whole check, and the slot ending stops it too.
func TestSettleDoesNothingWhenOff(t *testing.T) {
	p := compareProber(t, 3)
	var issued []string
	issuedBy(p, &issued)
	holdTickets(t, p, 0, 1, 2)
	at := compareSlotStart().Add(2*time.Hour + 31*time.Minute)

	p.cfg.NoCompare = true
	p.judgeRounds(at)
	p.settleTickets(at)
	if len(issued) != 0 {
		t.Errorf("issued %v with the check off", issued)
	}

	p.cfg.NoCompare = false
	p.settleTickets(compareSlotStart().Add(4 * time.Hour)) // another slot by then
	if len(issued) != 0 {
		t.Errorf("settled a slot that was over: %v", issued)
	}
}
