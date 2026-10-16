package probe

// refcheck_test.go: the reference check, from reading an engine's candidates to
// the tickets of a whole slot.

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/engine/manifest"
)

func TestParseTokenProbs(t *testing.T) {
	cases := []struct {
		name, body, text string
		tokens           []string
		top              float64 // the first token's own probability
	}{
		{
			"llama.cpp now, the stop token left out",
			`{"content":" Paris is","completion_probabilities":[
				{"token":" Paris","logprob":-0.105,"top_logprobs":[{"token":" Paris","logprob":-0.105},{"token":" Lyon","logprob":-3}]},
				{"token":" is","logprob":-0.2,"top_logprobs":[{"token":" is","logprob":-0.2}]},
				{"token":".","logprob":-0.01,"top_logprobs":[{"token":".","logprob":-0.01}]}]}`,
			" Paris is", []string{" Paris", " is"}, math.Exp(-0.105),
		},
		{
			"llama.cpp sampled probabilities",
			`{"content":" Paris","completion_probabilities":[{"token":" Paris","prob":0.9,"top_probs":[{"token":" Paris","prob":0.9},{"token":" Lyon","prob":0.05}]}]}`,
			" Paris", []string{" Paris"}, 0.9,
		},
		{
			"llama.cpp before",
			`{"content":" Paris","completion_probabilities":[{"content":" Paris","probs":[{"tok_str":" Paris","prob":0.8},{"tok_str":" Rome","prob":0.1}]}]}`,
			" Paris", []string{" Paris"}, 0.8,
		},
		{
			"OpenAI logprobs",
			`{"choices":[{"text":" Paris","logprobs":{"content":[{"token":" Paris","logprob":-0.5,"top_logprobs":[{"token":" Paris","logprob":-0.5}]}]}}]}`,
			" Paris", []string{" Paris"}, math.Exp(-0.5),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a, err := parseTokenProbs([]byte(c.body))
			if err != nil {
				t.Fatal(err)
			}
			if a.Text != c.text || len(a.Tokens) != len(c.tokens) {
				t.Fatalf("got %q with %d tokens, want %q with %d", a.Text, len(a.Tokens), c.text, len(c.tokens))
			}
			for i, tok := range c.tokens {
				if a.Tokens[i].Text != tok {
					t.Errorf("token %d = %q, want %q", i, a.Tokens[i].Text, tok)
				}
			}
			if got := a.Tokens[0].Cands[c.tokens[0]]; math.Abs(got-c.top) > 1e-9 {
				t.Errorf("first token's probability = %v, want %v", got, c.top)
			}
		})
	}
	if a, err := parseTokenProbs([]byte(`{"choices":[{"text":" Paris"}]}`)); err != nil || len(a.Tokens) != 0 {
		t.Errorf("an answer without candidates: %+v, %v, want text and no tokens", a, err)
	}
}

func answerOf(text string, probs ...float64) ProbAnswer {
	a := ProbAnswer{Text: text}
	for i, w := range strings.SplitAfter(strings.TrimSpace(text), " ") {
		tok := " " + strings.TrimSpace(w)
		p := 0.9
		if i < len(probs) {
			p = probs[i]
		}
		a.Tokens = append(a.Tokens, TokenProbs{Text: tok, Cands: map[string]float64{tok: p, " other": 1 - p}})
	}
	return a
}

func TestCompareAnswer(t *testing.T) {
	ref := medianOf([]ProbAnswer{answerOf(" the old lighthouse")})
	cases := []struct {
		name    string
		a       ProbAnswer
		maxMiss int
		want    string
	}{
		{"the same", answerOf(" the old lighthouse"), 0, ""},
		{"within the tolerance", answerOf(" the old lighthouse", 0.9005), 0, ""},
		{"another answer", answerOf(" a new lighthouse"), 0, reasonText},
		{"a gap past the tolerance", answerOf(" the old lighthouse", 0.85), 0, reasonProbs},
		{"a gap allowed by the miss count", answerOf(" the old lighthouse", 0.85), 2, ""},
		{"no candidates", ProbAnswer{Text: " the old lighthouse"}, 0, reasonNoProbs},
	}
	for _, c := range cases {
		if got, _, _ := compareAnswer(c.a, ref, DefaultRefTolerance, c.maxMiss); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// Same text cut into other tokens: the count differs.
	odd := answerOf(" the old lighthouse")
	odd.Tokens = odd.Tokens[:2]
	if got, _, _ := compareAnswer(odd, ref, DefaultRefTolerance, 0); got != reasonCount {
		t.Errorf("fewer tokens: %q, want %q", got, reasonCount)
	}
	// A candidate the node lists and the reference does not, well above the
	// reference's last entry, is a gap too.
	extra := answerOf(" the old lighthouse")
	extra.Tokens[0].Cands[" sneaky"] = 0.5
	if got, _, _ := compareAnswer(extra, ref, DefaultRefTolerance, 0); got != reasonProbs {
		t.Errorf("an extra strong candidate: %q, want %q", got, reasonProbs)
	}
}

func TestBuildReference(t *testing.T) {
	tol := DefaultRefTolerance
	// One reference: its answer, and it passes.
	ref, evals, err := buildReference(map[string]ProbAnswer{"r1": answerOf(" a b")}, tol, 0)
	if err != nil || ref.Text != " a b" || !evals["r1"] {
		t.Fatalf("one reference: %+v %v %v", ref, evals, err)
	}
	// Two that agree: the median, both pass.
	ref, evals, err = buildReference(map[string]ProbAnswer{"r1": answerOf(" a b", 0.9), "r2": answerOf(" a b", 0.9008)}, tol, 0)
	if err != nil || !evals["r1"] || !evals["r2"] || math.Abs(ref.Tokens[0].Cands[" a"]-0.9004) > 1e-9 || ref.Refs != 2 {
		t.Fatalf("two that agree: %+v %v %v", ref, evals, err)
	}
	// Two different answers: nobody to believe.
	if _, _, err = buildReference(map[string]ProbAnswer{"r1": answerOf(" a b"), "r2": answerOf(" c d")}, tol, 0); err == nil {
		t.Error("two different answers made a reference")
	}
	// Three, one wrong: the two make the value, the third fails its round.
	ref, evals, err = buildReference(map[string]ProbAnswer{"r1": answerOf(" a b"), "r2": answerOf(" a b"), "r3": answerOf(" c d")}, tol, 0)
	if err != nil || ref.Text != " a b" || !evals["r1"] || !evals["r2"] || evals["r3"] {
		t.Fatalf("three, one wrong: %+v %v %v", ref, evals, err)
	}
	// Three with the same text, one pulling away in probability.
	ref, evals, err = buildReference(map[string]ProbAnswer{"r1": answerOf(" a b", 0.9), "r2": answerOf(" a b", 0.9), "r3": answerOf(" a b", 0.7)}, tol, 0)
	if err != nil || evals["r3"] || !evals["r1"] || ref.Refs != 2 {
		t.Fatalf("one pulling away: %+v %v %v", ref, evals, err)
	}
	// A candidate one reference did not list is left out and counted.
	a, b := answerOf(" a b"), answerOf(" a b")
	a.Tokens[0].Cands[" rare"] = 0.0001
	ref, _, _ = buildReference(map[string]ProbAnswer{"r1": a, "r2": b}, tol, 0)
	if _, kept := ref.Tokens[0].Cands[" rare"]; kept || ref.Dropped != 1 {
		t.Errorf("partial candidate kept=%v dropped=%d", kept, ref.Dropped)
	}
}

func TestNewRefPromptIsFresh(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		q := newRefPrompt()
		if seen[q] {
			t.Fatalf("the same question twice: %q", q)
		}
		seen[q] = true
		if !strings.HasPrefix(q, "Q: ") || !strings.HasSuffix(q, "\nA:") {
			t.Fatalf("question %q", q)
		}
	}
}

// probsNode is a node bridge whose default run returns candidates. Each
// node answers a prompt with the text answer(node, prompt), one token a word.
type probsNode struct {
	mu      sync.Mutex
	answer  func(node, prompt string) string
	jobs    map[string][2]string // job id -> node, prompt
	asked   map[string][]string  // node -> prompts, in order
	paths   []string             // the run path each submit named
	noProbs map[string]bool      // nodes whose schema has no candidate run
}

func (c *probsNode) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	node := strings.TrimPrefix(path, "/node/")
	if i := strings.Index(node, "/"); i >= 0 {
		node = node[:i]
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case strings.HasSuffix(path, "/run-schema"):
		def := []manifest.RunParam{{Name: "prompt", Type: "string", Required: true}}
		if !c.noProbs[node] {
			def = append(def, manifest.RunParam{Name: "max-tokens", Type: "int"}, manifest.RunParam{Name: "temperature", Type: "float"},
				manifest.RunParam{Name: "stop", Type: "string"}, manifest.RunParam{Name: "logprobs", Type: "bool"},
				manifest.RunParam{Name: "top-logprobs", Type: "int"})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"default":  manifest.RunSpec{Path: "/v1/chat/completions", Params: def},
			"variants": []manifest.RunSpec{},
		})
	case strings.HasSuffix(path, "/v1/jobs") && r.Method == http.MethodPost:
		var body struct {
			Path string         `json:"path"`
			Run  map[string]any `json:"run"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		prompt, _ := body.Run["prompt"].(string)
		if c.jobs == nil {
			c.jobs, c.asked = map[string][2]string{}, map[string][]string{}
		}
		id := fmt.Sprintf("job%d", len(c.jobs)+1)
		c.jobs[id] = [2]string{node, prompt}
		c.asked[node] = append(c.asked[node], prompt)
		c.paths = append(c.paths, body.Path)
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{"job_id": id, "queue_max": 8})
	case strings.HasSuffix(path, "/result"):
		id := path[strings.LastIndex(strings.TrimSuffix(path, "/result"), "/")+1 : len(path)-len("/result")]
		job := c.jobs[id]
		text := c.answer(job[0], job[1])
		var toks []map[string]any
		for _, w := range strings.Fields(text) {
			tok := " " + w
			toks = append(toks, map[string]any{"token": tok, "logprob": math.Log(0.9),
				"top_logprobs": []map[string]any{{"token": tok, "logprob": math.Log(0.9)}, {"token": " else", "logprob": math.Log(0.05)}}})
		}
		toks = append(toks, map[string]any{"token": ".", "logprob": -0.01})
		_ = json.NewEncoder(w).Encode(map[string]any{"content": " " + strings.Join(strings.Fields(text), " "), "completion_probabilities": toks})
	default:
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "done"})
	}
}

// bridgeAnswer is the answer probsNode gives for text, as the prober reads it.
func bridgeAnswer(text string) ProbAnswer {
	a := ProbAnswer{Text: " " + strings.Join(strings.Fields(text), " ")}
	for _, w := range strings.Fields(text) {
		a.Tokens = append(a.Tokens, TokenProbs{Text: " " + w, Cands: map[string]float64{" " + w: 0.9, " else": 0.05}})
	}
	return a
}

// refProber is compareProber with the slot grouped by budget: one ref group of n
// nodes on budget 2 whose first node is the reference.
func refProber(t *testing.T, n int, bridge http.Handler) (*Prober, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(bridge)
	t.Cleanup(srv.Close)
	p := compareProber(t, n)
	p.firer = NewFirer(srv.URL)
	g := &p.assign.Groups[0]
	g.BudgetID, g.Model, g.GPU, g.Mode = 2, "0x"+qwen14Hex, "NVIDIA GeForce RTX 4090", ModeRef
	g.Engine, g.Profile, g.MetaHash = "llama 0.1.0", "/assets/profile/0xabc/p?version=1", "0x"+strings.Repeat("aa", 32)
	g.Refs = []string{g.Members[0]}
	return p, srv
}

const cheatSuffix = "0000000003" // the third node's address ends so

// 🔴 A ref group through a whole slot: every checked node gets its own question,
// asked of the reference first; nobody gets the quiz; the node that answers
// differently from the reference gets a second question and then nothing; the
// reference and the honest node are paid from the group's budget.
func TestRefGroupThroughAWholeSlot(t *testing.T) {
	bridge := &probsNode{answer: func(node, prompt string) string {
		if strings.HasSuffix(node, cheatSuffix) {
			return "something else entirely here"
		}
		return "the lighthouse stood alone"
	}}
	p, _ := refProber(t, 3, bridge)
	var issued []string
	issuedBy(p, &issued)
	start := compareSlotStart()

	p.fireRoundAt(start.Add(59*time.Minute + 50*time.Second))
	p.fireRoundAt(start.Add(104*time.Minute + 50*time.Second))
	p.fireRoundAt(start.Add(2*time.Hour + 29*time.Minute + 50*time.Second))

	if quiz, _ := p.store.QuizShots(compareEpoch); len(quiz) != 0 {
		t.Errorf("%d quiz shots in a ref group, want none", len(quiz))
	}
	for _, path := range bridge.paths {
		if path != "" {
			t.Fatalf("a submit named run %q, want the default run", path)
		}
	}
	honest, cheat := p.targets[1].Node.ID, p.targets[2].Node.ID
	ref := p.targets[0].Node.ID
	// Questions: one each a round for the honest node, two each a round for the
	// cheat (the second after a text mismatch), each also put to the reference.
	if got := len(bridge.asked[honest]); got != SampleRounds {
		t.Errorf("the honest node was asked %d times, want %d", got, SampleRounds)
	}
	if got := len(bridge.asked[cheat]); got != 2*SampleRounds {
		t.Errorf("the cheat was asked %d times, want %d (a retry each round)", got, 2*SampleRounds)
	}
	seen := map[string]bool{}
	for _, q := range append(append([]string{}, bridge.asked[honest]...), bridge.asked[cheat]...) {
		if seen[q] {
			t.Errorf("question %q went to two nodes", q)
		}
		seen[q] = true
		found := false
		for _, rq := range bridge.asked[ref] {
			if rq == q {
				found = true
			}
		}
		if !found {
			t.Errorf("question %q was not put to the reference", q)
		}
	}
	if len(issued) != 0 {
		t.Fatalf("issued before the slot was settled: %v", issued)
	}

	p.fireRoundAt(start.Add(2*time.Hour + 31*time.Minute))
	if got, want := sortedIDs(issued), nodeIDs(p, 0, 1); got != want {
		t.Fatalf("issued %v, want the reference and the honest node", issued)
	}
	rows, _ := p.store.RefRows(compareEpoch)
	if r := rows[p.targets[2].Node.ID][0]; r == nil || r.Verdict != VerdictFail || r.Reason != reasonText || r.Attempts != 2 {
		t.Errorf("cheat round 1 = %+v, want a text failure after two questions", r)
	}
	if r := rows[p.targets[0].Node.ID][1]; r == nil || r.Role != RoleRef || r.Verdict != VerdictPass {
		t.Errorf("reference round 2 = %+v, want a passing reference row", r)
	}
	// The ticket names the group's budget and model.
	model, _, budget, write, _ := p.ticketFor(p.targets[1])
	if !write || model != "0x"+qwen14Hex || budget != "2" {
		t.Errorf("ticket = %s %s %v, want budget 2's model", model, budget, write)
	}
	// What the references answered was stored for the cache mode.
	if n, _ := p.store.RefCacheCount(refSettingOf(p.assign.Groups[0])); n == 0 {
		t.Error("no question was stored")
	}
}

// With the references offline the RV sends cache mode: a stored question is used,
// the node is held against the stored value, and the row says so.
func TestCacheModeUsesStoredQuestions(t *testing.T) {
	bridge := &probsNode{answer: func(node, prompt string) string { return "the lighthouse stood alone" }}
	p, _ := refProber(t, 3, bridge)
	g := &p.assign.Groups[0]
	g.Mode, g.Refs = ModeCache, nil
	set := refSettingOf(*g)
	ref := medianOf([]ProbAnswer{bridgeAnswer("the lighthouse stood alone")})
	for i := 0; i < 3; i++ {
		if err := p.store.SaveRefCache(set, fmt.Sprintf("Q: stored %d\nA:", i), map[string]ProbAnswer{}, ref, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var issued []string
	issuedBy(p, &issued)
	start := compareSlotStart()
	p.fireRoundAt(start.Add(59*time.Minute + 50*time.Second))

	rows, _ := p.store.RefRows(compareEpoch)
	got := map[string]bool{}
	for i := 0; i < 3; i++ {
		r := rows[p.targets[i].Node.ID][0]
		if r == nil || r.Verdict != VerdictPass || !r.Cached || r.Mode != ModeCache {
			t.Fatalf("node %d round 1 = %+v, want a pass on a stored question", i, r)
		}
		q := bridge.asked[p.targets[i].Node.ID][0]
		if got[q] {
			t.Errorf("stored question %q went to two nodes of the group", q)
		}
		got[q] = true
	}
	// A fourth node in round 1 would find none left: the check fails.
	p.refUsed = nil
	if c, ok := p.takeStored(compareEpoch, 0, 1, *g); !ok || c.ID == 0 {
		t.Error("a fresh round should hand out stored questions again")
	}
}

// A GPU the budget's check info does not list: never asked, no ticket.
func TestUnlistedGroupEarnsNothing(t *testing.T) {
	bridge := &probsNode{answer: func(node, prompt string) string { return "x y" }}
	p, _ := refProber(t, 2, bridge)
	p.assign.Groups[0].Mode, p.assign.Groups[0].Refs = ModeUnlisted, nil
	var issued []string
	issuedBy(p, &issued)
	start := compareSlotStart()
	p.fireRoundAt(start.Add(59*time.Minute + 50*time.Second))
	p.fireRoundAt(start.Add(2*time.Hour + 31*time.Minute))
	if len(bridge.jobs) != 0 || len(issued) != 0 {
		t.Fatalf("asked %d times, issued %v, want nothing", len(bridge.jobs), issued)
	}
	if settled, _ := p.store.SettledTickets(compareEpoch); len(settled) != 2 {
		t.Errorf("%d nodes settled, want both refused once", len(settled))
	}
}

// A node whose engine has no candidate run fails as no_probs; a node running
// another model than the budget's gets no ticket even if it answered right.
func TestRefCheckRefusals(t *testing.T) {
	bridge := &probsNode{
		answer:  func(node, prompt string) string { return "the lighthouse stood alone" },
		noProbs: map[string]bool{},
	}
	p, _ := refProber(t, 3, bridge)
	bridge.noProbs[p.targets[2].Node.ID] = true
	start := compareSlotStart()
	p.fireRoundAt(start.Add(59*time.Minute + 50*time.Second))
	rows, _ := p.store.RefRows(compareEpoch)
	if r := rows[p.targets[2].Node.ID][0]; r == nil || r.Reason != reasonNoProbs {
		t.Errorf("node without candidates = %+v, want %s", r, reasonNoProbs)
	}
	other := p.targets[1]
	other.Service.ModelHash = "sha256:" + strings.Repeat("ab", 32)
	if _, _, _, write, why := p.ticketFor(other); write || why == "" {
		t.Errorf("a node on another model: write %v (%q), want no ticket", write, why)
	}
}

func TestDecideRef(t *testing.T) {
	row := func(v, reason string) *RefRow { return &RefRow{Verdict: v, Reason: reason} }
	pass, fail, air := row(VerdictPass, ""), row(VerdictFail, reasonProbs), row("", "")
	cases := []struct {
		name string
		r    [SampleRounds]*RefRow
		need int
		want ticketCall
	}{
		{"every round passed", [SampleRounds]*RefRow{pass, pass, pass}, 2, callIssue},
		{"one failed, two passed", [SampleRounds]*RefRow{pass, fail, pass}, 2, callIssue},
		{"two failed", [SampleRounds]*RefRow{pass, fail, fail}, 2, callRefuse},
		{"one round only", [SampleRounds]*RefRow{pass, nil, nil}, 2, callRefuse},
		{"enough passed, one in flight", [SampleRounds]*RefRow{pass, pass, air}, 2, callIssue},
		{"in flight could make it", [SampleRounds]*RefRow{pass, air, nil}, 2, callWait},
		{"two failed and one in flight cannot make it", [SampleRounds]*RefRow{fail, fail, air}, 2, callRefuse},
		{"never asked", [SampleRounds]*RefRow{nil, nil, nil}, 2, callNone},
		{"all three needed, one failed", [SampleRounds]*RefRow{pass, pass, fail}, 3, callRefuse},
	}
	for _, c := range cases {
		if got, why := decideRef(c.r, c.need); got != c.want || (got == callRefuse && why == "") {
			t.Errorf("%s: %d %q, want %d", c.name, got, why, c.want)
		}
	}
}
