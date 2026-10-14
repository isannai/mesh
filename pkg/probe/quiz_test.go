package probe

import (
	"fmt"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/rvnodes"
)

// quizProber is a prober with one node per entry, each entry the node's /24: two
// nodes given the same prefix share a connection.
func quizProber(t *testing.T, nets ...string) *Prober {
	t.Helper()
	p := compareProber(t, 0)
	svc := rvnodes.Service{Name: "llm", ModelHash: qwen14, ModelClass: &rvnodes.ModelClass{ParamsCore: 14768307200}}
	grp := AssignGroup{}
	for i, n := range nets {
		addr := fmt.Sprintf("0x%040x", i+1)
		grp.Members = append(grp.Members, addr)
		node := rvnodes.Node{ID: "S:" + addr, Services: []rvnodes.Service{svc}}
		p.targets = append(p.targets, Target{Node: node, Service: svc, Slash24: n})
	}
	p.assign.Groups = []AssignGroup{grp}
	return p
}

func idsOfTargets(ts []Target) map[string]bool {
	out := map[string]bool{}
	for _, t := range ts {
		out[t.Node.ID] = true
	}
	return out
}

// 🔴 The moment is drawn per /24, not per node. Every node behind one connection
// is asked together, which is the whole point of asking a /24 at once: a farm
// sharing one GPU answers one at a time fine and fails all at once.
func TestDueQuizAsksANetworkTogether(t *testing.T) {
	p := quizProber(t, "10.0.1", "10.0.1", "10.0.2", "10.0.3", "10.0.4", "10.0.5")
	open := compareSlotStart().Add(30 * time.Minute)

	became := map[string]int{} // first 10-second step at which a node was due
	for step := 0; step < 180; step++ {
		for id := range idsOfTargets(p.dueQuiz(open.Add(time.Duration(step)*10*time.Second), 1, nil)) {
			if _, ok := became[id]; !ok {
				became[id] = step
			}
		}
	}
	if len(became) != 6 {
		t.Fatalf("%d of 6 nodes were due inside the window: %v", len(became), became)
	}
	a, b := p.targets[0].Node.ID, p.targets[1].Node.ID
	if became[a] != became[b] {
		t.Errorf("two nodes of one /24 were asked at different moments: step %d and %d", became[a], became[b])
	}
	distinct := map[int]bool{}
	for _, step := range became {
		distinct[step] = true
	}
	if len(distinct) < 3 {
		t.Errorf("five networks were all asked at %d moments: not random", len(distinct))
	}
}

// Nobody is asked at the instant the window opens unless their moment is zero, and
// a node that has been due stays due until it is asked.
func TestDueQuizWaitsForItsMoment(t *testing.T) {
	var nets []string
	for i := 0; i < 40; i++ {
		nets = append(nets, fmt.Sprintf("10.1.%d", i))
	}
	p := quizProber(t, nets...)
	open := compareSlotStart().Add(30 * time.Minute)

	if got := p.dueQuiz(open, 1, nil); len(got) > 3 {
		t.Errorf("%d of 40 networks were due the moment the window opened", len(got))
	}
	half := p.dueQuiz(open.Add(14*time.Minute), 1, nil)
	if len(half) < 8 || len(half) > 32 {
		t.Errorf("%d of 40 networks due halfway through the window", len(half))
	}
	later := idsOfTargets(p.dueQuiz(open.Add(20*time.Minute), 1, nil))
	for id := range idsOfTargets(half) {
		if !later[id] {
			t.Fatalf("%s was due and then was not", id)
		}
	}
	if end := p.dueQuiz(open.Add(29*time.Minute+50*time.Second), 1, nil); len(end) != 40 {
		t.Errorf("%d of 40 due at the end of the window, want all", len(end))
	}
}

// A node is asked once in a round, and the count is the round's own: having been
// asked in the first window does not stop the second.
func TestDueQuizAsksOncePerRound(t *testing.T) {
	p := quizProber(t, "10.0.1", "10.0.2")
	end1 := compareSlotStart().Add(59*time.Minute + 50*time.Second)
	end2 := compareSlotStart().Add(2*time.Hour + 29*time.Minute + 50*time.Second)

	if got := p.dueQuiz(end1, 1, nil); len(got) != 2 {
		t.Fatalf("%d due at the end of round 1, want 2", len(got))
	}
	asked := map[string]int{p.targets[0].Node.ID: 1}
	got := p.dueQuiz(end1, 1, asked)
	if len(got) != 1 || got[0].Node.ID != p.targets[1].Node.ID {
		t.Errorf("due after one was asked = %+v, want only the other", got)
	}
	// Asked in round 1 says nothing about round 2.
	if got := p.dueQuiz(end2, 2, nil); len(got) != 2 {
		t.Errorf("%d due in round 2, want 2", len(got))
	}
	// Between the windows there is no round.
	if p.dueQuiz(end1, 0, nil) != nil || p.dueQuiz(end1, 3, nil) != nil {
		t.Error("a round that is not 1 or 2 had nodes due")
	}
}

// The text track keeps a node that also serves pictures, so the picture track
// leaves it alone for the whole slot.
func TestClaimTextNodes(t *testing.T) {
	p := quizProber(t, "10.0.1", "10.0.2")
	counts := map[string]int{"image-only": 0}
	p.claimTextNodes(counts)
	for _, tg := range p.targets {
		if counts[tg.Node.ID] == 0 {
			t.Errorf("%s was not claimed", tg.Node.ID)
		}
	}
	if counts["image-only"] != 0 {
		t.Error("a node the text track does not serve was claimed")
	}
}

// A shot is recorded with the round it was fired in, and a pass issues nothing:
// the slot is decided as a whole, later.
func TestFireOneRecordsTheRoundAndIssuesNothing(t *testing.T) {
	node := &compareNode{answer: func(string) string { return "2" }}
	srv := httptest.NewServer(node)
	defer srv.Close()

	p := quizProber(t, "10.0.1")
	p.firer = NewFirer(srv.URL)
	var issued []string
	issuedBy(p, &issued)

	var stats roundStats
	q := Question{ID: 1, Category: CatMath, Q: "What is 1 + 1?", Draft: "2"}
	p.fireOne(p.targets[0], q, 2, &stats)

	shots, err := p.store.QuizShots(compareEpoch)
	if err != nil || len(shots) != 1 {
		t.Fatalf("shots = %+v, err %v", shots, err)
	}
	if s := shots[0]; s.Round != 2 || s.Outcome != OutcomeAnswered || s.Verdict != VerdictPass {
		t.Errorf("shot = %+v, want a passed round 2", s)
	}
	if len(issued) != 0 {
		t.Errorf("a passing shot issued %v", issued)
	}
	if n, _ := p.store.ShotCountsInRound(compareEpoch, 2); n[p.targets[0].Node.ID] != 1 {
		t.Errorf("round 2 count = %v", n)
	}
	if n, _ := p.store.ShotCountsInRound(compareEpoch, 1); len(n) != 0 {
		t.Errorf("round 1 count = %v, want none", n)
	}
	// The whole slot's count, which the picture track reads, sees it too.
	if n, _ := p.store.ShotCountsInSlot(compareEpoch); n[p.targets[0].Node.ID] != 1 {
		t.Errorf("slot count = %v", n)
	}
}

// A slot from the first question to the tickets: nothing before the first window,
// one shot to each node in each window, nothing between, no ticket until a minute
// after the second window, and then one ticket each, once.
func TestAQuizTwiceThenTheTicket(t *testing.T) {
	node := &compareNode{answer: func(string) string { return "2" }}
	srv := httptest.NewServer(node)
	defer srv.Close()

	p := quizProber(t, "10.0.1", "10.0.2")
	p.firer = NewFirer(srv.URL)
	var issued []string
	issuedBy(p, &issued)
	var qs []Question
	for i := 0; i < 12; i++ {
		qs = append(qs, Question{Category: CatMath, Q: fmt.Sprintf("What is %d - %d?", 2+i, i), Draft: "2"})
	}
	if _, err := p.store.AddQuestions(qs, time.Now()); err != nil {
		t.Fatal(err)
	}
	start := compareSlotStart()
	shotsOf := func() int {
		n, _ := p.store.ShotCountsInSlot(compareEpoch)
		return n[p.targets[0].Node.ID] + n[p.targets[1].Node.ID]
	}

	p.fireRoundAt(start.Add(10 * time.Minute)) // before the first window
	p.fireRoundAt(start.Add(29 * time.Minute))
	if shotsOf() != 0 {
		t.Fatalf("%d shots before the first window", shotsOf())
	}

	p.fireRoundAt(start.Add(59*time.Minute + 50*time.Second)) // every moment of round 1 has come
	if r1, _ := p.store.ShotCountsInRound(compareEpoch, 1); len(r1) != 2 {
		t.Fatalf("round 1 asked %v, want both nodes", r1)
	}
	p.fireRoundAt(start.Add(59*time.Minute + 55*time.Second)) // and nobody is asked twice
	if shotsOf() != 2 {
		t.Errorf("%d shots after round 1, want 2", shotsOf())
	}

	p.fireRoundAt(start.Add(90 * time.Minute)) // between the windows
	if shotsOf() != 2 {
		t.Errorf("a shot went out between the windows: %d", shotsOf())
	}

	p.fireRoundAt(start.Add(2*time.Hour + 29*time.Minute + 50*time.Second))
	if r2, _ := p.store.ShotCountsInRound(compareEpoch, 2); len(r2) != 2 {
		t.Fatalf("round 2 asked %v, want both nodes", r2)
	}
	if shotsOf() != 4 {
		t.Errorf("%d shots after round 2, want 4", shotsOf())
	}
	if len(issued) != 0 {
		t.Fatalf("tickets issued before the slot was settled: %v", issued)
	}

	// The second window is over but the margin after it is not.
	p.fireRoundAt(start.Add(2*time.Hour + 30*time.Minute))
	if len(issued) != 0 {
		t.Fatalf("settled inside the margin: %v", issued)
	}
	p.fireRoundAt(start.Add(2*time.Hour + 31*time.Minute))
	if len(issued) != 2 {
		t.Fatalf("issued %v, want one ticket each", issued)
	}
	p.fireRoundAt(start.Add(2*time.Hour + 32*time.Minute))
	if len(issued) != 2 {
		t.Errorf("a settled ticket was issued again: %v", issued)
	}

	// Each node was given a different question in the two rounds.
	rows, err := p.store.db.Query(`SELECT node_id, COUNT(DISTINCT question_id) FROM shot GROUP BY node_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var distinct int
		if err := rows.Scan(&id, &distinct); err != nil {
			t.Fatal(err)
		}
		if distinct != 2 {
			t.Errorf("%s was asked %d different questions, want 2", id, distinct)
		}
	}
}

// When more networks are due than a round takes, the ones that have waited longest
// are asked first, so the ones left over are not the same every slot. A /24 stays
// whole.
func TestDueQuizServesTheLongestWaitingFirst(t *testing.T) {
	var nets []string
	for i := 0; i < 20; i++ {
		nets = append(nets, fmt.Sprintf("10.2.%d", i))
	}
	nets = append(nets, "10.2.0") // a second node on the first network
	p := quizProber(t, nets...)
	end := compareSlotStart().Add(59*time.Minute + 50*time.Second)

	due := p.dueQuiz(end, 1, nil)
	if len(due) != 21 {
		t.Fatalf("%d due, want 21", len(due))
	}
	offset := func(tg Target) time.Duration { return p.sampleOffset("quiz", 1, slash24Key(tg), sampleWindow) }
	for i := 1; i < len(due); i++ {
		if offset(due[i]) < offset(due[i-1]) {
			t.Fatalf("node %d was due earlier than node %d but came after it", i, i-1)
		}
	}

	groups := groupInOrder(due)
	if len(groups) != 20 {
		t.Fatalf("%d groups, want 20 networks", len(groups))
	}
	whole := 0
	for _, g := range groups {
		if len(g) == 2 {
			whole++
			if g[0].Slash24 != "10.2.0" || g[1].Slash24 != "10.2.0" {
				t.Errorf("a group of two that is not the shared /24: %+v", g)
			}
		}
	}
	if whole != 1 {
		t.Errorf("%d groups of two, want the shared /24 kept whole", whole)
	}

	// A batch of 3 takes the 3 networks that have waited longest.
	var all []time.Duration
	for _, g := range groups {
		all = append(all, offset(g[0]))
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	for i, g := range takeBatch(groups, 3) {
		if offset(g[0]) != all[i] {
			t.Errorf("batch member %d has moment %v, want the %d-th earliest %v", i, offset(g[0]), i, all[i])
		}
	}
}

// 🔴 A group of four from an RV that does not group by budget, through a whole
// slot: the quiz goes out in each window, nothing is issued until the second
// window is over, and then every node that passed both rounds is paid. No sentence
// request goes out any more: the majority check between nodes was dropped
// (2026-10-14, compare.go).
func TestAGroupThroughAWholeSlot(t *testing.T) {
	node := &compareNode{byPrompt: func(id, prompt string) string { return "2" }}
	srv := httptest.NewServer(node)
	defer srv.Close()

	p := compareProber(t, 4)
	p.firer = NewFirer(srv.URL)
	var issued []string
	issuedBy(p, &issued)
	var qs []Question
	for i := 0; i < 16; i++ {
		qs = append(qs, Question{Category: CatMath, Q: fmt.Sprintf("What is %d - %d?", 2+i, i), Draft: "2"})
	}
	if _, err := p.store.AddQuestions(qs, time.Now()); err != nil {
		t.Fatal(err)
	}
	start := compareSlotStart()

	p.fireRoundAt(start.Add(59*time.Minute + 50*time.Second))
	p.fireRoundAt(start.Add(2*time.Hour + 29*time.Minute + 50*time.Second))
	quiz, _ := p.store.QuizShots(compareEpoch)
	if len(quiz) != 8 {
		t.Fatalf("%d quiz shots, want 4 nodes in 2 rounds", len(quiz))
	}
	if len(node.prompts) != 8 {
		t.Errorf("%d requests went out, want the 8 quiz questions only", len(node.prompts))
	}
	if len(issued) != 0 {
		t.Fatalf("issued before the slot was settled: %v", issued)
	}

	p.fireRoundAt(start.Add(2*time.Hour + 31*time.Minute))
	if got, want := sortedIDs(issued), nodeIDs(p, 0, 1, 2, 3); got != want {
		t.Fatalf("issued %v, want all four", issued)
	}
	p.fireRoundAt(start.Add(2*time.Hour + 32*time.Minute))
	if len(issued) != 4 {
		t.Errorf("a later tick issued more: %v", issued)
	}
}
