package probe

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/rvnodes"
)

// The tier of a service: the parameter count the node reports first (the core, so a
// mixture of experts counts by what it runs), then the size in the model's name, and
// the small tier when there is neither.
func TestServiceTier(t *testing.T) {
	gb := func(n float64) *rvnodes.ModelClass { return &rvnodes.ModelClass{ParamsCore: uint64(n * 1e9)} }
	for _, c := range []struct {
		name string
		svc  rvnodes.Service
		want int
	}{
		{"1.5B", rvnodes.Service{ModelClass: gb(1.5)}, tierSmall},
		{"6.7B", rvnodes.Service{ModelClass: gb(6.7)}, tierSmall},
		{"7.6B (a 7B)", rvnodes.Service{ModelClass: gb(7.6)}, tierMiddle},
		{"8.0B", rvnodes.Service{ModelClass: gb(8.0)}, tierMiddle},
		{"12.2B", rvnodes.Service{ModelClass: gb(12.2)}, tierMiddle},
		{"14.77B (a 14B)", rvnodes.Service{ModelClass: gb(14.77)}, tierLarge},
		{"32B", rvnodes.Service{ModelClass: gb(32)}, tierLarge},
		{"a mixture of experts counts by its core", rvnodes.Service{ModelClass: &rvnodes.ModelClass{ParamsCore: 3e9, ParamsTotal: 30e9}}, tierSmall},
		{"no core, the total", rvnodes.Service{ModelClass: &rvnodes.ModelClass{ParamsTotal: 15e9}}, tierLarge},
		{"the count beats a misleading name", rvnodes.Service{Model: "qwen-70b", ModelClass: gb(1.5)}, tierSmall},
		{"no count, the name", rvnodes.Service{Model: "qwen3-14b-uncensored-q4_k_m"}, tierLarge},
		{"an empty count, the name", rvnodes.Service{Model: "llama-3.1-8b", ModelClass: &rvnodes.ModelClass{}}, tierMiddle},
		{"neither", rvnodes.Service{Model: "phi-mini"}, tierSmall},
	} {
		if got := serviceTier(c.svc); got != c.want {
			t.Errorf("%s: tier %d, want %d", c.name, got, c.want)
		}
	}
}

// A question made for a tier is taken only by that tier; the ones for every tier by all.
func TestTakeQuestionByTier(t *testing.T) {
	s := newTestStore(t)
	now := time.Now()
	qs := []Question{
		{Category: CatBase, Tier: tierMiddle, Q: "What is 100 in binary?", Draft: "1100100"},
		{Category: CatBase, Tier: tierLarge, Q: "What is 200 in octal?", Draft: "310"},
		{Category: CatAddSub, Tier: 0, Q: "What is 1 + 2?", Draft: "3"},
	}
	if _, err := s.AddQuestions(qs, now); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.TakeQuestion(CatBase, tierSmall, now); ok {
		t.Error("the small tier took a question made for another")
	}
	q, ok, err := s.TakeQuestion(CatBase, tierLarge, now)
	if err != nil || !ok || q.Tier != tierLarge || q.Draft != "310" {
		t.Errorf("the large tier got %+v, %v, %v", q, ok, err)
	}
	if _, ok, _ := s.TakeQuestion(CatBase, tierLarge, now); ok {
		t.Error("the same question was taken twice")
	}
	for _, tier := range []int{tierSmall, tierMiddle, tierLarge} {
		if _, ok, _ := s.TakeQuestion(CatAddSub, tier, now); tier == tierSmall && !ok {
			t.Error("a question for every tier was not taken by the small tier")
		} else if tier != tierSmall && ok {
			t.Error("the one question was taken twice")
		}
	}
	depth, err := s.QueueDepthTier()
	if err != nil {
		t.Fatal(err)
	}
	if depth[CatBase][tierMiddle] != 1 || depth[CatBase][tierLarge] != 0 {
		t.Errorf("depth by tier = %v", depth)
	}
}

// A database from before the tier column opens, keeps its questions, and they are for
// every tier.
func TestOldQuestionTableGetsATier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE question (id INTEGER PRIMARY KEY AUTOINCREMENT, category TEXT NOT NULL, q TEXT NOT NULL,
		   draft_answer TEXT NOT NULL, fewshot TEXT NOT NULL, created_at INTEGER NOT NULL, consumed_at INTEGER)`,
		`INSERT INTO question(category,q,draft_answer,fewshot,created_at) VALUES('math','What is 1 + 1?','2','[]',1)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	q, ok, err := s.TakeQuestion(CatMath, tierLarge, time.Now())
	if err != nil || !ok || q.Tier != 0 || q.Draft != "2" {
		t.Errorf("the old question = %+v, %v, %v", q, ok, err)
	}
}

// An empty queue is not an empty round: a code-written kind that ran dry is made on the
// spot, in the tier's own mix, and nothing is asked twice.
func TestTakeQuestionsMakesWhatRanDry(t *testing.T) {
	p := compareProber(t, 1)
	for tier := tierSmall; tier <= tierLarge; tier++ {
		own := map[Category]bool{}
		for _, e := range codeMix[tier] {
			own[e.cat] = true
		}
		qs := p.takeQuestions(12, tier, time.Now())
		if len(qs) != 12 {
			t.Fatalf("tier %d: %d questions, want 12", tier, len(qs))
		}
		seen := map[string]bool{}
		for _, q := range qs {
			if !own[q.Category] {
				t.Errorf("tier %d was asked a %q question", tier, q.Category)
			}
			if q.Category == CatBase && q.Tier != tier {
				t.Errorf("tier %d got a base question made for tier %d", tier, q.Tier)
			}
			if seen[q.Q] {
				t.Errorf("tier %d was asked %q twice", tier, q.Q)
			}
			seen[q.Q] = true
			if q.ID == 0 {
				t.Errorf("%q has no queue id, so no shot could point at it", q.Q)
			}
		}
	}
}

// The model-written kinds come in only while the writer has questions waiting, and then
// take the configured share.
func TestModelQuestionsFollowTheWritersStock(t *testing.T) {
	modelCount := func(qs []Question) int {
		n := 0
		for _, q := range qs {
			if q.Category == CatGeography || q.Category == CatElement || q.Category == CatCurrency {
				n++
			}
		}
		return n
	}
	stock := func(p *Prober) {
		var qs []Question
		for _, g := range genSpecs {
			for i := 0; i < 200; i++ {
				qs = append(qs, Question{Category: g.cat, Q: "Question " + string(g.cat) + " " + time.Duration(i).String(), Draft: "x"})
			}
		}
		if _, err := p.store.AddQuestions(qs, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	p := compareProber(t, 1)
	if n := modelCount(p.takeQuestions(200, tierMiddle, time.Now())); n != 0 {
		t.Errorf("%d model-written questions with no writer, want none", n)
	}

	stock(p)
	const draw = 600
	n := modelCount(p.takeQuestions(draw, tierMiddle, time.Now()))
	if n < draw*22/100 || n > draw*38/100 {
		t.Errorf("%d of %d from the writer, want about 30%%", n, draw)
	}

	zero := 0
	p.cfg.ModelQuestionPct = &zero
	if n := modelCount(p.takeQuestions(200, tierMiddle, time.Now())); n != 0 {
		t.Errorf("%d model-written questions at a share of 0, want none", n)
	}
}

// Each tier's nodes are drawn for with that tier's mix, and nodes of one tier that share
// a /24 get different questions.
func TestAssignQuestionsByTier(t *testing.T) {
	p := compareProber(t, 6)
	sizes := []float64{1.5, 1.5, 7.6, 7.6, 14.8, 14.8}
	for i := range p.targets {
		svc := p.targets[i].Service
		svc.ModelClass = &rvnodes.ModelClass{ParamsCore: uint64(sizes[i] * 1e9)}
		p.targets[i].Service = svc
	}
	groups := [][]Target{p.targets} // one /24 holding all six

	assigned := p.assignQuestions(groups, time.Now())
	if len(assigned) != 6 {
		t.Fatalf("%d of 6 targets were given a question", len(assigned))
	}
	byTier := map[int]map[string]bool{}
	for _, tg := range p.targets {
		q, ok := assigned[targetKey(tg)]
		if !ok {
			t.Fatalf("%s got no question", short(tg.Node.ID))
		}
		tier := serviceTier(tg.Service)
		own := false
		for _, e := range codeMix[tier] {
			own = own || e.cat == q.Category
		}
		if !own {
			t.Errorf("a tier %d node was asked a %q question", tier, q.Category)
		}
		if q.Category == CatBase && q.Tier != tier {
			t.Errorf("a tier %d node was asked a base question made for tier %d", tier, q.Tier)
		}
		if byTier[tier] == nil {
			byTier[tier] = map[string]bool{}
		}
		if byTier[tier][q.Q] {
			t.Errorf("two tier %d nodes in one /24 got %q", tier, q.Q)
		}
		byTier[tier][q.Q] = true
	}
}
