package probe

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var sixDigits = regexp.MustCompile(`^[1-9]\d{5}$`)

// A code is six digits, never starts with a zero, and is not the same twice in a
// row: every shot needs one of its own.
func TestNewOptIsSixDigitsAndNew(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 2000; i++ {
		c := newOpt()
		if !sixDigits.MatchString(c) {
			t.Fatalf("code %q is not six digits starting 1-9", c)
		}
		seen[c] = true
	}
	// 2000 draws from 900000: a handful of repeats is chance, hundreds is a stuck source.
	if len(seen) < 1950 {
		t.Errorf("only %d different codes in 2000 draws", len(seen))
	}
}

// The codes shown in the worked examples are six digits, differ from each other
// and from the shot's own, and are the same every time the same prompt is built.
func TestExampleOptDiffersAndIsStable(t *testing.T) {
	for i := 0; i < 500; i++ {
		opt := newOpt()
		a, b := exampleOpt(opt, 0), exampleOpt(opt, 1)
		if !sixDigits.MatchString(a) || !sixDigits.MatchString(b) {
			t.Fatalf("example codes %q %q for %s", a, b, opt)
		}
		if a == b || a == opt || b == opt {
			t.Fatalf("codes collide for %s: %s %s", opt, a, b)
		}
		if exampleOpt(opt, 0) != a || exampleOpt(opt, 1) != b {
			t.Fatalf("example codes are not stable for %s", opt)
		}
	}
}

func TestSplitOpt(t *testing.T) {
	cases := []struct {
		in        string
		opt, rest string
		ok        bool
	}{
		{"123456: Paris", "123456", "Paris", true},
		{"123456:Paris", "123456", "Paris", true},
		{"  123456 : Paris  ", "123456", "Paris", true},
		{"123456： Paris", "123456", "Paris", true},   // full width colon
		{"123456: Paris.", "123456", "Paris.", true}, // the trailing stop is the scorer's business
		{"123456: New Delhi\nsecond line", "123456", "New Delhi", true},
		{"\n123456: Paris", "123456", "Paris", true},
		{"A: 123456: Paris", "123456", "Paris", true}, // an echoed label
		{"123456:", "123456", "", true},               // a code with no answer is still a code
		{"Paris", "", "", false},
		{"12345: Paris", "", "", false},    // five digits
		{"1234567: Paris", "", "", false},  // seven digits
		{"123456 Paris", "", "", false},    // no colon
		{"Paris 123456: x", "", "", false}, // not at the front
		{"", "", "", false},
	}
	for _, c := range cases {
		opt, rest, ok := splitOpt(c.in)
		if ok != c.ok || opt != c.opt || rest != c.rest {
			t.Errorf("splitOpt(%q) = (%q, %q, %v), want (%q, %q, %v)", c.in, opt, rest, ok, c.opt, c.rest, c.ok)
		}
	}
}

// A question with a code is passed only when the code that comes back is the one
// that went out and the answer after it is right.
func TestScoreWithOpt(t *testing.T) {
	paris := Question{Category: CatDomain, Q: "What is the internet country code of France?", Draft: ".fr", Opt: "483917"}
	math := Question{Category: CatAddSub, Q: "What is 31 + 44?", Draft: "75", Opt: "483917"}

	cases := []struct {
		name   string
		q      Question
		answer string
		tokens int
		want   string
	}{
		{"code and answer right", paris, "483917: .fr", 9, VerdictPass},
		{"trailing stop is fine", paris, "483917: .fr.", 9, VerdictPass},
		{"math, code and answer right", math, "483917: 75", 9, VerdictPass},
		{"math, wrapped in words", math, "483917: The answer is 75", 12, VerdictPass},
		{"right answer, no code", math, "75", 3, VerdictFail},
		{"right answer, another code", math, "483918: 75", 9, VerdictFail},
		{"right answer, an old code from an example", math, "481903: 75", 9, VerdictFail},
		{"right code, wrong answer", math, "483917: 76", 9, VerdictFail},
		{"right code, nothing after it", math, "483917:", 8, VerdictFail},
		{"five digit code", math, "48391: 75", 9, VerdictFail},
		{"empty reply", math, "", 0, VerdictFail},
		// Still speaking when the budget ran out, and the code never came: our cap.
		{"cut off before the code", math, "Let me think about this question for a moment", probeMaxTokens, VerdictTruncated},
		// Cut off after a good code but with a wrong answer: the existing rule.
		{"cut off after the code", math, "483917: 76 and then it kept going", probeMaxTokens, VerdictTruncated},
		// A right answer after the right code passes even when it ran past the cap.
		{"right even past the cap", math, "483917: 75", probeMaxTokens, VerdictPass},
	}
	for _, c := range cases {
		if got := Score(c.q, c.answer, c.tokens); got != c.want {
			t.Errorf("%s: Score(%q) = %s, want %s", c.name, c.answer, got, c.want)
		}
	}

	// A question with no code is scored exactly as before.
	plain := Question{Category: CatAddSub, Q: "What is 31 + 44?", Draft: "75"}
	if got := Score(plain, "75", 3); got != VerdictPass {
		t.Errorf("plain answer to a plain question = %s", got)
	}
}

// The prompt of a question with a code ends on the arrow after the shot's own
// code, shows the question's two examples with codes of their own, and never
// contains the shot's code anywhere but where it is asked.
func TestBuildPromptWithOpt(t *testing.T) {
	q := Question{
		Category: CatDomain, Q: "What is the internet country code of Japan?", Draft: ".jp",
		Fewshot: []QA{
			{Q: "What is the internet country code of Germany?", A: ".de"},
			{Q: "What is the internet country code of France?", A: ".fr"},
		},
		Opt: "483917",
	}
	p := q.BuildPrompt()
	if !strings.HasSuffix(p, "Question number 483917, What is the internet country code of Japan? ->") {
		t.Errorf("the prompt does not end on the shot's own question: %q", p)
	}
	if !strings.HasPrefix(p, "Answer in exactly this form: <6 digit number>: <short answer>.") {
		t.Errorf("lead-in: %q", p)
	}
	if !strings.Contains(p, "Answer in English.") {
		t.Errorf("the language instruction was lost: %q", p)
	}
	if n := strings.Count(p, "483917"); n != 1 {
		t.Errorf("the shot's code appears %d times, want once (the question): %q", n, p)
	}
	// Each example shows a code of its own, repeated in front of its answer.
	e0, e1 := exampleOpt(q.Opt, 0), exampleOpt(q.Opt, 1)
	for _, w := range []string{
		"Question number " + e0 + ", What is the internet country code of Germany? -> " + e0 + ": .de.",
		"Question number " + e1 + ", What is the internet country code of France? -> " + e1 + ": .fr.",
	} {
		if !strings.Contains(p, w) {
			t.Errorf("missing example %q in %q", w, p)
		}
	}
	// The same prompt every time the same code is asked.
	if q.BuildPrompt() != p {
		t.Error("the prompt is not stable for one code")
	}
	// No stop sequence of the probe's appears in it by accident.
	for _, s := range append(append([]string{}, probeStop...), probeStopCapped...) {
		if strings.Contains(p, s) {
			t.Errorf("the prompt contains the stop sequence %q: %q", s, p)
		}
	}

	// Without a code the prompt is the old one, byte for byte.
	q.Opt = ""
	old := q.BuildPrompt()
	if !strings.HasPrefix(old, "Give only the answer. No explanation. Answer in English. ") || !strings.HasSuffix(old, " A:") {
		t.Errorf("the plain prompt changed: %q", old)
	}
}

// A shot goes out with its own code, the node's code is checked, and the code is
// kept with the shot: a node that writes it passes, one that does not fails.
func TestFireOneAsksForTheCodeAndChecksIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		noCode bool
		want   string
	}{
		{"a node that writes the code", false, VerdictPass},
		{"a node that leaves the code out", true, VerdictFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &compareNode{answer: func(string) string { return "2" }, noCode: tc.noCode}
			srv := httptest.NewServer(node)
			defer srv.Close()

			p := quizProber(t, "10.0.1")
			p.firer = NewFirer(srv.URL)
			var stats roundStats
			q := Question{ID: 1, Category: CatMath, Q: "What is 1 + 1?", Draft: "2",
				Fewshot: []QA{{Q: "What is 12 + 7?", A: "19"}, {Q: "What is 40 - 15?", A: "25"}}}
			p.fireOne(p.targets[0], q, 1, &stats)

			shots, err := p.store.QuizShots(compareEpoch)
			if err != nil || len(shots) != 1 {
				t.Fatalf("shots = %+v, err %v", shots, err)
			}
			if shots[0].Verdict != tc.want {
				t.Errorf("verdict = %q, want %q", shots[0].Verdict, tc.want)
			}

			// The prompt that went out asks for a code, and the same code was stored.
			if len(node.prompts) != 1 {
				t.Fatalf("%d prompts went out", len(node.prompts))
			}
			code := promptCode(node.prompts[0])
			if !sixDigits.MatchString(code) {
				t.Fatalf("no code in the prompt: %q", node.prompts[0])
			}
			var stored string
			if err := p.store.db.QueryRow(`SELECT COALESCE(opt,'') FROM shot`).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != code {
				t.Errorf("stored code %q, asked for %q", stored, code)
			}
		})
	}
}

// Two shots of the same question, even to the same node, carry different codes.
func TestEachShotHasItsOwnCode(t *testing.T) {
	node := &compareNode{answer: func(string) string { return "2" }}
	srv := httptest.NewServer(node)
	defer srv.Close()

	p := quizProber(t, "10.0.1", "10.0.2")
	p.firer = NewFirer(srv.URL)
	var stats roundStats
	q := Question{ID: 1, Category: CatMath, Q: "What is 1 + 1?", Draft: "2"}
	p.fireOne(p.targets[0], q, 1, &stats)
	p.fireOne(p.targets[1], q, 1, &stats)

	if len(node.prompts) != 2 {
		t.Fatalf("%d prompts went out", len(node.prompts))
	}
	a, b := promptCode(node.prompts[0]), promptCode(node.prompts[1])
	if a == "" || b == "" || a == b {
		t.Errorf("codes %q and %q: each shot needs its own", a, b)
	}
}
