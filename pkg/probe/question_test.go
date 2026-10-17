package probe

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// evalExpr reads "What is 3 + 4 x 5?" and returns the value, the product and the
// division first and then the sums, the way the questions mean them. The operands are
// returned too, so a test can check their ranges.
func evalExpr(q string) (value int, operands []int, ops []string, err error) {
	s := strings.TrimSuffix(strings.TrimPrefix(q, "What is "), "?")
	parts := strings.Split(s, " ")
	if len(parts)%2 == 0 {
		return 0, nil, nil, errParse
	}
	for i, p := range parts {
		if i%2 == 0 {
			n, e := strconv.Atoi(p)
			if e != nil {
				return 0, nil, nil, e
			}
			operands = append(operands, n)
		} else {
			ops = append(ops, p)
		}
	}
	// products and divisions bind first
	terms := []int{operands[0]}
	var signs []string
	for i, op := range ops {
		n := operands[i+1]
		switch op {
		case "x":
			terms[len(terms)-1] *= n
		case "/":
			if n == 0 || terms[len(terms)-1]%n != 0 {
				return 0, nil, nil, errParse
			}
			terms[len(terms)-1] /= n
		case "+", "-":
			terms = append(terms, n)
			signs = append(signs, op)
		default:
			return 0, nil, nil, errParse
		}
	}
	value = terms[0]
	for i, sg := range signs {
		if sg == "+" {
			value += terms[i+1]
		} else {
			value -= terms[i+1]
		}
	}
	return value, operands, ops, nil
}

func digitsOf(n int) int { return len(strconv.Itoa(n)) }

// Every arithmetic kind builds its own kind, with the right answer, operands in its
// range, no negative result (the minus sign is an extra token and an extra way for a
// correct node to look wrong), and two examples of its own kind.
func TestArithmeticKinds(t *testing.T) {
	kinds := []struct {
		cat  Category
		gen  func(*rand.Rand) Question
		ops  string // operators in order, joined
		fits func(operands []int) bool
	}{
		{CatAddSub, newSumQuestion, "", func(o []int) bool {
			return digitsOf(o[0]) >= 2 && digitsOf(o[0]) <= 3 && digitsOf(o[1]) >= 2 && digitsOf(o[1]) <= 3
		}},
		{CatMul1, newMulQuestion, "x", func(o []int) bool { return o[0] >= 2 && o[0] <= 9 && digitsOf(o[1]) == 2 }},
		{CatMul2, newMul2Question, "x", func(o []int) bool { return digitsOf(o[0]) == 2 && digitsOf(o[1]) == 2 && o[0] >= 11 && o[1] >= 11 }},
		{CatMul3, newMul3Question, "x", func(o []int) bool { return digitsOf(o[0]) == 3 && o[0] >= 101 && digitsOf(o[1]) == 2 && o[1] >= 11 }},
		{CatMul4, newMul4Question, "x", func(o []int) bool { return digitsOf(o[0]) == 4 && o[0] >= 1001 && digitsOf(o[1]) == 2 && o[1] >= 11 }},
		{CatDiv, newDivQuestion, "/", func(o []int) bool { return o[1] >= 2 && o[1] <= 12 && o[0]/o[1] >= 10 && o[0]/o[1] <= 99 }},
		{CatCompound, newCompoundQuestion, "+x", func(o []int) bool { return o[0] >= 2 && o[0] <= 30 && o[1] >= 2 && o[1] <= 9 && o[2] >= 2 && o[2] <= 9 }},
		{CatCompound2, newCompound2Question, "x+x", func(o []int) bool {
			for _, n := range o {
				if n < 2 || n > 19 {
					return false
				}
			}
			return true
		}},
	}
	rng := rand.New(rand.NewSource(1))
	for _, k := range kinds {
		t.Run(string(k.cat), func(t *testing.T) {
			sawSum, sawDiff := false, false
			for i := 0; i < 400; i++ {
				q := k.gen(rng)
				if q.Category != k.cat {
					t.Fatalf("category = %q", q.Category)
				}
				value, operands, ops, err := evalExpr(q.Q)
				if err != nil {
					t.Fatalf("cannot read %q: %v", q.Q, err)
				}
				if got, _ := strconv.Atoi(q.Draft); got != value {
					t.Fatalf("%q: draft %s, want %d", q.Q, q.Draft, value)
				}
				if value < 0 {
					t.Fatalf("negative answer for %q", q.Q)
				}
				if k.ops != "" && strings.Join(ops, "") != k.ops {
					t.Fatalf("%q: operators %v, want %q", q.Q, ops, k.ops)
				}
				if !k.fits(operands) {
					t.Fatalf("operands out of range: %q", q.Q)
				}
				if len(q.Fewshot) != 2 {
					t.Fatalf("want 2 few-shot examples, got %d", len(q.Fewshot))
				}
				for _, ex := range q.Fewshot {
					exValue, _, exOps, err := evalExpr(ex.Q)
					if err != nil || strconv.Itoa(exValue) != ex.A {
						t.Fatalf("example %q = %q is wrong or unreadable (%v)", ex.Q, ex.A, err)
					}
					if k.ops != "" && strings.Join(exOps, "") != k.ops {
						t.Fatalf("example %q is not of this kind", ex.Q)
					}
				}
				if k.cat == CatAddSub {
					sawSum = sawSum || ops[0] == "+"
					sawDiff = sawDiff || ops[0] == "-"
				}
			}
			if k.cat == CatAddSub && (!sawSum || !sawDiff) {
				t.Error("an addition or a subtraction never came up")
			}
			if !numericKinds[k.cat] {
				t.Errorf("%s is not scored as a number", k.cat)
			}
		})
	}
}

// Base conversions: the tier decides the bases and the size of the number, one direction
// is chosen at random, the answer is the right digits, and the examples are of the same
// base and direction.
func TestBaseQuestions(t *testing.T) {
	cases := []struct {
		tier   int
		bases  map[int]bool
		lo, hi int
	}{
		{tierSmall, map[int]bool{2: true, 8: true}, 10, 99},
		{tierMiddle, map[int]bool{2: true, 8: true, 16: true}, 100, 999},
		{tierLarge, map[int]bool{2: true, 4: true, 8: true, 16: true}, 100, 999},
	}
	baseOf := map[string]int{"binary": 2, "base 4": 4, "octal": 8, "hexadecimal": 16}
	rng := rand.New(rand.NewSource(8))
	for _, c := range cases {
		seenBase, seenDir := map[int]bool{}, map[string]bool{}
		for i := 0; i < 600; i++ {
			q := newBaseQuestion(rng, c.tier)
			if q.Category != CatBase || numericKinds[q.Category] {
				t.Fatalf("tier %d: category %q", c.tier, q.Category)
			}
			var name, dir string
			var n int
			var digits string
			if strings.Contains(q.Q, ", from ") {
				dir = "from"
				i := strings.Index(q.Q, ", from ")
				name = strings.TrimSuffix(q.Q[i+len(", from "):], "?")
				digits = strings.TrimSuffix(strings.TrimPrefix(q.Q, "What is "), " in decimal"+q.Q[strings.Index(q.Q, ","):])
			} else {
				dir = "to"
				body := strings.TrimSuffix(strings.TrimPrefix(q.Q, "What is "), "?")
				i := strings.Index(body, " in ")
				digits, name = body[:i], body[i+len(" in "):]
			}
			base, ok := baseOf[name]
			if !ok || !c.bases[base] {
				t.Fatalf("tier %d: base %q is not for this tier: %q", c.tier, name, q.Q)
			}
			seenBase[base], seenDir[dir] = true, true
			if dir == "to" {
				n, _ = strconv.Atoi(digits)
				if q.Draft != strings.ToUpper(strconv.FormatInt(int64(n), base)) {
					t.Fatalf("%q: draft %s", q.Q, q.Draft)
				}
			} else {
				v, err := strconv.ParseInt(digits, base, 64)
				if err != nil {
					t.Fatalf("%q: %v", q.Q, err)
				}
				n = int(v)
				if q.Draft != strconv.Itoa(n) {
					t.Fatalf("%q: draft %s, want %d", q.Q, q.Draft, n)
				}
			}
			if n < c.lo || n > c.hi {
				t.Fatalf("tier %d: %d is outside %d..%d: %q", c.tier, n, c.lo, c.hi, q.Q)
			}
			if len(q.Fewshot) != 2 {
				t.Fatalf("want 2 examples, got %d", len(q.Fewshot))
			}
			for _, ex := range q.Fewshot {
				if dir == "to" && !strings.HasSuffix(ex.Q, " in "+name+"?") {
					t.Fatalf("%q carries an example of another kind: %q", q.Q, ex.Q)
				}
				if dir == "from" && !strings.HasSuffix(ex.Q, ", from "+name+"?") {
					t.Fatalf("%q carries an example of another kind: %q", q.Q, ex.Q)
				}
			}
		}
		if len(seenBase) != len(c.bases) {
			t.Errorf("tier %d: bases that came up %v, want %v", c.tier, seenBase, c.bases)
		}
		if !seenDir["to"] || !seenDir["from"] {
			t.Errorf("tier %d: a direction never came up: %v", c.tier, seenDir)
		}
	}
}

// The tier is read from the size in the model's name.
func TestModelTier(t *testing.T) {
	for name, want := range map[string]int{
		"Qwen2.5-1.5B-Instruct-Q4_K_M.gguf":  tierSmall,
		"Qwen2.5-0.5B-Instruct":              tierSmall,
		"llama-3.2-3b-instruct":              tierSmall,
		"deepseek-coder-6.7b":                tierSmall,
		"Qwen2.5-7B-Instruct-Q4_K_M":         tierMiddle,
		"llama-3.1-8b":                       tierMiddle,
		"gemma-2-9b-it":                      tierMiddle,
		"mistral-nemo-12b":                   tierMiddle,
		"qwen3-14b-uncensored-q4_k_m":        tierLarge,
		"Qwen2.5-14B-Q4_K_M":                 tierLarge,
		"qwen2.5-32b":                        tierLarge,
		"llama-3.3-70b-instruct":             tierLarge,
		"qwen3-30b-a3b":                      tierLarge, // the first size in the name, not the active one
		"mixtral-8x7b":                       tierMiddle,
		"phi-3-mini-4k-instruct":             tierSmall, // no size in the name
		"":                                   tierSmall,
		"some-model-q8_0":                    tierSmall, // q8 is a quantisation, not a size
		"gemma-3n-e4b":                       tierSmall,
		"Qwen3-235B-A22B":                    tierLarge,
		"model-bf16":                         tierSmall,
		"qwen3-14b-uncensored-q4_k_m.gguf":   tierLarge,
		"Meta-Llama-3.1-70B-Instruct.Q4_K_M": tierLarge,
	} {
		if got := modelTier(name); got != want {
			t.Errorf("modelTier(%q) = %d, want %d", name, got, want)
		}
	}
}

// What each tier is asked among the code-written kinds, and that every tier adds up.
func TestCodeMixTiers(t *testing.T) {
	banks := map[Category]bool{CatVerb: true, CatSIUnit: true, CatISO: true, CatDomain: true}
	for tier := tierSmall; tier <= tierLarge; tier++ {
		total, arithmetic, base := 0, 0, 0
		for _, e := range codeMix[tier] {
			total += e.weight
			if _, ok := codeSpecFor(e.cat); !ok {
				t.Errorf("tier %d asks %q, which has no generator", tier, e.cat)
			}
			switch {
			case banks[e.cat]:
				if e.weight != 5 {
					t.Errorf("tier %d: %s is %d%%, want 5%%", tier, e.cat, e.weight)
				}
			case e.cat == CatBase:
				base = e.weight
				arithmetic += e.weight
			default:
				arithmetic += e.weight
			}
		}
		if total != 100 || arithmetic != 80 {
			t.Errorf("tier %d: %d%% in all, %d%% arithmetic; want 100 and 80", tier, total, arithmetic)
		}
		if base != 20 {
			t.Errorf("tier %d: base conversion is %d%%, want 20%%", tier, base)
		}
	}
	// the ladder: what only the harder tiers ask
	has := func(tier int, cat Category) bool {
		for _, e := range codeMix[tier] {
			if e.cat == cat {
				return true
			}
		}
		return false
	}
	if has(tierSmall, CatAddSub) || !has(tierMiddle, CatAddSub) || has(tierLarge, CatAddSub) {
		t.Error("addition and subtraction belong to the middle tier only")
	}
	if has(tierSmall, CatMul4) || has(tierMiddle, CatMul4) || !has(tierLarge, CatMul4) {
		t.Error("four digit products belong to the large tier only")
	}
	if has(tierSmall, CatCompound2) || has(tierMiddle, CatCompound2) || !has(tierLarge, CatCompound2) {
		t.Error("a x b + c x d belongs to the large tier only")
	}
	// only the base conversions differ by tier
	for _, s := range codeSpecs {
		if s.tiered != (s.cat == CatBase) {
			t.Errorf("%s: tiered = %v", s.cat, s.tiered)
		}
	}
}

// The draw: a tier asks only its own kinds, the model-written kinds join only when the
// writer has stock and then take about the configured share, and a kind that came up
// empty is not drawn again.
func TestPickCategory(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	mixOf := func(tier int) map[Category]bool {
		m := map[Category]bool{}
		for _, e := range codeMix[tier] {
			m[e.cat] = true
		}
		return m
	}
	stock := []Category{CatGeography, CatElement, CatCurrency}
	const n = 20000
	for tier := tierSmall; tier <= tierLarge; tier++ {
		own := mixOf(tier)
		// no writer: code only, in the tier's weights
		counts := map[Category]int{}
		for i := 0; i < n; i++ {
			c, ok := pickCategory(rng, tier, nil, 30, nil)
			if !ok || !own[c] {
				t.Fatalf("tier %d with no stock drew %q (ok %v)", tier, c, ok)
			}
			counts[c]++
		}
		for _, e := range codeMix[tier] {
			want := n * e.weight / 100
			if got := counts[e.cat]; got < want*8/10 || got > want*12/10 {
				t.Errorf("tier %d: %s drawn %d of %d, want about %d", tier, e.cat, got, n, want)
			}
		}
		// a writer: about 30% model-written, the rest the tier's own
		model := 0
		for i := 0; i < n; i++ {
			c, _ := pickCategory(rng, tier, stock, 30, nil)
			if own[c] {
				continue
			}
			model++
			if c != CatGeography && c != CatElement && c != CatCurrency {
				t.Fatalf("tier %d drew %q, which no tier asks", tier, c)
			}
		}
		if model < n*27/100 || model > n*33/100 {
			t.Errorf("tier %d: %d of %d from the writer, want about 30%%", tier, model, n)
		}
	}
	// the share is a setting
	for pct, want := range map[int]int{0: 0, 100: n} {
		got := 0
		for i := 0; i < n; i++ {
			if c, _ := pickCategory(rng, tierMiddle, stock, pct, nil); c == CatGeography || c == CatElement || c == CatCurrency {
				got++
			}
		}
		if got != want {
			t.Errorf("share %d%%: %d of %d from the writer, want %d", pct, got, n, want)
		}
	}
	// dry kinds are left out, and with nothing left there is no draw
	dry := map[Category]bool{CatGeography: true, CatBase: true}
	for i := 0; i < 2000; i++ {
		if c, _ := pickCategory(rng, tierLarge, stock, 50, dry); dry[c] {
			t.Fatalf("drew %q, which came up empty", c)
		}
	}
	all := map[Category]bool{}
	for _, e := range codeMix[tierSmall] {
		all[e.cat] = true
	}
	if _, ok := pickCategory(rng, tierSmall, nil, 30, all); ok {
		t.Error("a draw with every kind dry should say so")
	}
}

// The share of model-written questions is a setting: 30 when absent, 0 to 100 when given.
func TestModelQuestionShare(t *testing.T) {
	pct := func(n int) *int { return &n }
	for _, c := range []struct {
		set  *int
		want int
	}{{nil, 30}, {pct(0), 0}, {pct(10), 10}, {pct(100), 100}, {pct(-5), 0}, {pct(250), 100}} {
		if got := (Config{ModelQuestionPct: c.set}).ModelQuestionShare(); got != c.want {
			t.Errorf("model_question_pct %v: %d, want %d", c.set, got, c.want)
		}
	}
}

var errParse = strconv.ErrSyntax

// A sequence's answer is an ordinary number, so a guesser gets nothing — which
// is the whole reason the category exists. Geometric runs are avoided: by the
// fifth term they are four digits, i.e. the two digit multiplication the mix leaves out.
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
// reliably, which is what separates this from the two digit products left out.
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
		q := spec.generate(rng, tierSmall)
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
	want := "Give only the answer. No explanation. Answer in English. " +
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
	// 🔴 A multilingual node answered "澳元" for Australia's currency: right,
	// and unscorable against a one-word English draft. The few-shot pair being
	// English is a hint a Chinese-first model does not take.
	if !strings.Contains(got, "Answer in English.") {
		t.Error("the prompt must name the language it wants the answer in")
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
