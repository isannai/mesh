package probe

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// Multiplication is banned because 3B-class models get two-digit products
// wrong, which would measure model size instead of liveness. Negative results
// are avoided because the minus sign is an extra token and an extra way for a
// correct node to look wrong.
func TestMathQuestionShape(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 500; i++ {
		q := newMathQuestion(rng)
		if q.Category != CatMath {
			t.Fatalf("category = %q", q.Category)
		}
		if strings.ContainsAny(q.Q, "*x×") {
			t.Fatalf("multiplication generated: %q", q.Q)
		}
		if !strings.Contains(q.Q, "+") && !strings.Contains(q.Q, "-") {
			t.Fatalf("neither + nor -: %q", q.Q)
		}
		n, err := strconv.Atoi(q.Draft)
		if err != nil {
			t.Fatalf("draft %q is not a number (%q)", q.Draft, q.Q)
		}
		if n < 0 {
			t.Fatalf("negative answer %d for %q", n, q.Q)
		}
		if len(q.Fewshot) != 2 {
			t.Fatalf("want 2 few-shot examples, got %d", len(q.Fewshot))
		}
	}
}

// The generated answer must actually be the answer — a generator that drifts
// from its own arithmetic would fail every honest node.
func TestMathAnswersAreCorrect(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		q := newMathQuestion(rng)
		var a, b int
		var op string
		if strings.Contains(q.Q, "+") {
			op = "+"
		} else {
			op = "-"
		}
		if _, err := fmtSscan(q.Q, op, &a, &b); err != nil {
			t.Fatalf("cannot parse %q: %v", q.Q, err)
		}
		want := a + b
		if op == "-" {
			want = a - b
		}
		if got, _ := strconv.Atoi(q.Draft); got != want {
			t.Fatalf("%q: draft %s, want %d", q.Q, q.Draft, want)
		}
	}
}

// fmtSscan pulls the two operands out of "What is A op B?".
func fmtSscan(q, op string, a, b *int) (int, error) {
	s := strings.TrimPrefix(q, "What is ")
	s = strings.TrimSuffix(s, "?")
	parts := strings.Split(s, " "+op+" ")
	if len(parts) != 2 {
		return 0, errParse
	}
	var err error
	if *a, err = strconv.Atoi(strings.TrimSpace(parts[0])); err != nil {
		return 0, err
	}
	if *b, err = strconv.Atoi(strings.TrimSpace(parts[1])); err != nil {
		return 0, err
	}
	return 2, nil
}

var errParse = strconv.ErrSyntax

// The measured prompt is a single space-joined line ending in "A:", with the
// lead-in "Give only the answer" — NOT "answer with one word only", which
// truncated legitimate two-word answers like "South America".
func TestBuildPrompt(t *testing.T) {
	q := Question{
		Category: CatGeography,
		Q:        "Which continent contains Egypt?",
		Draft:    "Africa",
		Fewshot: []QA{
			{Q: "What is the capital of France?", A: "Paris"},
			{Q: "Which continent is Japan in?", A: "Asia"},
		},
	}
	got := q.BuildPrompt()
	want := "Give only the answer. No explanation. " +
		"Q: What is the capital of France? A: Paris " +
		"Q: Which continent is Japan in? A: Asia " +
		"Q: Which continent contains Egypt? A:"
	if got != want {
		t.Errorf("prompt mismatch\ngot:  %q\nwant: %q", got, want)
	}
	if strings.Contains(got, "\n") {
		t.Error("prompt must be one line — the stop sequence is the first newline")
	}
	if strings.Contains(strings.ToLower(got), "one word") {
		t.Error(`"one word" phrasing truncates two-word answers`)
	}
}

// The first two usable lines become the few-shot pair and must NOT also be
// asked: their answers are already in the prompt.
func TestParseBatchReservesFewshot(t *testing.T) {
	raw := `What is the capital of France? | Paris
Which continent is Japan in? | Asia
What is the capital of Peru? | Lima
Which continent contains Egypt? | Africa`

	qs := parseBatch(CatGeography, raw)
	if len(qs) != 2 {
		t.Fatalf("want 2 questions (4 lines minus 2 examples), got %d", len(qs))
	}
	for _, q := range qs {
		if len(q.Fewshot) != 2 {
			t.Fatalf("question carries %d examples", len(q.Fewshot))
		}
		if q.Q == "What is the capital of France?" || q.Q == "Which continent is Japan in?" {
			t.Errorf("example %q was also asked as a question", q.Q)
		}
		if q.Category != CatGeography {
			t.Errorf("category = %q", q.Category)
		}
	}
}

// Models add numbering and prose no matter how the prompt is worded; one bad
// line must not cost the whole batch.
func TestParseBatchSkipsJunk(t *testing.T) {
	raw := `Here are your questions:
1. What is the capital of France? | Paris
Which continent is Japan in? | Asia
this line has no separator
3) What is the capital of Peru? | Lima
Bad one? | ` + strings.Repeat("x", 60) + `
What colour is the sky? | blue`

	qs := parseBatch(CatGeography, raw)
	if len(qs) != 2 {
		t.Fatalf("want 2 usable questions, got %d: %+v", len(qs), qs)
	}
	for _, q := range qs {
		if strings.HasPrefix(q.Q, "3)") || strings.HasPrefix(q.Q, "1.") {
			t.Errorf("numbering not stripped: %q", q.Q)
		}
	}
}

// Fewer than three usable lines cannot spare two for examples.
func TestParseBatchTooFew(t *testing.T) {
	if qs := parseBatch(CatAnimal, "a | b\nc | d"); qs != nil {
		t.Errorf("want nil for a 2-line batch, got %+v", qs)
	}
}

// Examples are model-written and will eventually contain whatever character a
// delimiter format picked, so the codec has to be JSON.
func TestFewshotRoundTrip(t *testing.T) {
	in := []QA{{Q: `a | b "c"`, A: "d,e"}, {Q: "f", A: "g"}}
	out := decodeFewshot(encodeFewshot(in))
	if len(out) != 2 || out[0].Q != in[0].Q || out[0].A != in[0].A || out[1].Q != in[1].Q {
		t.Errorf("round trip lost data: %+v", out)
	}
	if got := decodeFewshot("not json"); got != nil {
		t.Errorf("bad JSON should decode to nil, got %+v", got)
	}
}

// math carries the largest share because it is the only category whose answer
// is certain — it never needs a re-check panel.
func TestCategoryMix(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	counts := map[Category]int{}
	const n = 20000
	for i := 0; i < n; i++ {
		counts[pickCategory(rng)]++
	}
	if counts[CatMath] < n*40/100 || counts[CatMath] > n*60/100 {
		t.Errorf("math share = %d/%d, want roughly half", counts[CatMath], n)
	}
	for _, c := range []Category{CatGeography, CatAnimal, CatColor} {
		if counts[c] == 0 {
			t.Errorf("category %q never selected", c)
		}
		if counts[c] > counts[CatMath] {
			t.Errorf("category %q (%d) outweighs math (%d)", c, counts[c], counts[CatMath])
		}
	}
}

// Every generation prompt must demand the one-line "<q> | <a>" format, or
// parseBatch has nothing to work with.
func TestGenSpecsDemandLineFormat(t *testing.T) {
	seen := map[Category]bool{}
	for _, s := range genSpecs {
		seen[s.cat] = true
		if !strings.Contains(s.prompt, "<question> | <answer>") {
			t.Errorf("%q prompt does not specify the line format", s.cat)
		}
		if !strings.Contains(s.prompt, "20") {
			t.Errorf("%q prompt does not ask for a batch", s.cat)
		}
	}
	for _, c := range []Category{CatGeography, CatAnimal, CatColor} {
		if !seen[c] {
			t.Errorf("no generation prompt for %q", c)
		}
	}
	if seen[CatMath] {
		t.Error("math must be generated in code, not by a model")
	}
}
