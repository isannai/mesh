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

// A sequence's answer is an ordinary number, so a guesser gets nothing — which
// is the whole reason the category exists. Geometric runs are avoided: by the
// fifth term they are four digits, i.e. the multiplication the mix excludes.
func TestSequenceQuestions(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 300; i++ {
		q := newSequenceQuestion(rng)
		if q.Category != CatSequence {
			t.Fatalf("category = %q", q.Category)
		}
		body := strings.TrimSuffix(strings.TrimPrefix(q.Q, "What comes next: "), "?")
		parts := strings.Split(body, ", ")
		if len(parts) != 4 {
			t.Fatalf("want 4 shown terms, got %d in %q", len(parts), q.Q)
		}
		nums := make([]int, 4)
		for j, p := range parts {
			n, err := strconv.Atoi(p)
			if err != nil {
				t.Fatalf("term %q is not a number in %q", p, q.Q)
			}
			nums[j] = n
		}
		step := nums[1] - nums[0]
		for j := 2; j < 4; j++ {
			if nums[j]-nums[j-1] != step {
				t.Fatalf("run is not arithmetic: %q", q.Q)
			}
		}
		want := nums[3] + step
		if got, _ := strconv.Atoi(q.Draft); got != want {
			t.Fatalf("%q: draft %s, want %d", q.Q, q.Draft, want)
		}
	}
}

// Powers of ten only. A factor like 2.54 would invite rounding disagreements
// and fail honest nodes; appending zeros is something even a small model does
// reliably, which is what separates this from banned multiplication.
func TestUnitsQuestions(t *testing.T) {
	rng := rand.New(rand.NewSource(13))
	for i := 0; i < 300; i++ {
		q := newUnitsQuestion(rng)
		if q.Category != CatUnits {
			t.Fatalf("category = %q", q.Category)
		}
		n, err := strconv.Atoi(q.Draft)
		if err != nil {
			t.Fatalf("draft %q is not a number (%q)", q.Draft, q.Q)
		}
		// Every factor is a power of ten and every operand is 2..99, so the
		// answer is at least 20 and always ends in a zero.
		if n < 20 || n%10 != 0 {
			t.Fatalf("%q: answer %d is not a power-of-ten conversion", q.Q, n)
		}
	}
	// The table itself must stay powers of ten — this is the property the
	// certainty of the answer rests on.
	for _, c := range unitConversions {
		if c.factor != 10 && c.factor != 100 && c.factor != 1000 {
			t.Errorf("%s→%s factor %d is not a power of ten", c.from, c.to, c.factor)
		}
	}
}

// Every code-written category must actually be wired to a generator, and must
// produce its own category — a copy-paste slip here would quietly fill one
// queue while starving another.
func TestCodeSpecsProduceTheirCategory(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	for _, spec := range codeSpecs {
		q := spec.gen(rng)
		if q.Category != spec.cat {
			t.Errorf("codeSpec %q generated category %q", spec.cat, q.Category)
		}
		if strings.TrimSpace(q.Q) == "" || strings.TrimSpace(q.Draft) == "" {
			t.Errorf("codeSpec %q produced an empty question or answer: %+v", spec.cat, q)
		}
		if len(q.Fewshot) != 2 {
			t.Errorf("codeSpec %q carries %d examples, want 2", spec.cat, len(q.Fewshot))
		}
	}
}

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
	if qs := parseBatch(CatGeography, "a | b\nc | d"); qs != nil {
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

// The code-written categories carry the majority, because theirs are the only
// answers that are certain — they never need a re-check panel, and they keep
// working when every question writer is unreachable.
func TestCategoryMix(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	counts := map[Category]int{}
	const n = 20000
	for i := 0; i < n; i++ {
		counts[pickCategory(rng)]++
	}

	code := 0
	for _, spec := range codeSpecs {
		if counts[spec.cat] == 0 {
			t.Errorf("code category %q never selected", spec.cat)
		}
		code += counts[spec.cat]
	}
	if code < n*45/100 || code > n*70/100 {
		t.Errorf("code-written share = %d/%d, want a clear majority", code, n)
	}

	for _, spec := range genSpecs {
		if counts[spec.cat] == 0 {
			t.Errorf("category %q never selected", spec.cat)
		}
	}

	// 🔴 The categories removed for having a guessable answer space must not
	// creep back: "how many legs" has four real answers and colour has ten.
	for _, gone := range []Category{"animal", "color"} {
		if counts[gone] > 0 {
			t.Errorf("%q is back in the mix; its answer space is small enough to guess", gone)
		}
	}
}

// The output contract lives in the shared system message now, and it is the
// only thing that makes a batch parseable — FillCategory also folds it into the
// prompt on the retry path, so it has to carry the format on its own.
func TestGenSystemDemandsLineFormat(t *testing.T) {
	if !strings.Contains(genSystem, "<question> | <answer>") {
		t.Error("the system contract does not specify the line format")
	}
	if !strings.Contains(genSystem, strconv.Itoa(genBatchLines)) {
		t.Errorf("the system contract does not ask for %d lines", genBatchLines)
	}
	// parseBatch spends two lines on the few-shot pair, so a batch of 20 would
	// only yield 18 questions.
	if genBatchLines < 22 {
		t.Errorf("genBatchLines = %d; two lines go to the few-shot pair", genBatchLines)
	}
}

// Every model-written category needs a topic, and math must not have one.
func TestGenSpecsCoverCategories(t *testing.T) {
	seen := map[Category]bool{}
	for _, s := range genSpecs {
		seen[s.cat] = true
		if strings.TrimSpace(s.topic) == "" {
			t.Errorf("%q has no topic", s.cat)
		}
	}
	for _, c := range []Category{CatGeography, CatElement, CatCurrency} {
		if !seen[c] {
			t.Errorf("no generation prompt for %q", c)
		}
	}
	if seen[CatMath] {
		t.Error("math must be generated in code, not by a model")
	}
}
