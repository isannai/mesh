package probe

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

// A bank is checked by hand, so a typo in it fails honest nodes. These are the
// mistakes a table can hold without anyone noticing: a repeated question, an example
// that is also a question, a question that answers itself, an answer in the wrong
// shape.
func TestBanksAreWellFormed(t *testing.T) {
	shape := map[Category]*regexp.Regexp{
		CatVerb:   regexp.MustCompile(`^[a-z]+$`),
		CatSIUnit: regexp.MustCompile(`^[a-z]+$`),
		CatISO:    regexp.MustCompile(`^[A-Z]{3}$`),
		CatDomain: regexp.MustCompile(`^\.[a-z]{2}$`),
	}
	minimum := map[Category]int{CatVerb: 90, CatSIUnit: 24, CatISO: 55, CatDomain: 70}

	for _, b := range banks {
		t.Run(string(b.cat), func(t *testing.T) {
			if len(b.facts) < minimum[b.cat] {
				t.Errorf("%d facts, want at least %d", len(b.facts), minimum[b.cat])
			}
			if len(b.fewshot) != 2 {
				t.Fatalf("%d examples, want 2", len(b.fewshot))
			}
			seen := map[string]bool{}
			for _, f := range b.facts {
				if f.subject == "" || f.answer == "" {
					t.Errorf("empty fact %+v", f)
				}
				if !shape[b.cat].MatchString(f.answer) {
					t.Errorf("answer %q for %q is not the shape of this bank", f.answer, f.subject)
				}
				if seen[f.subject] {
					t.Errorf("%q is in the table twice", f.subject)
				}
				seen[f.subject] = true
				q := strings.Replace(b.ask, "%s", f.subject, 1)
				for _, ex := range b.fewshot {
					if ex.Q == q {
						t.Errorf("the example %q is also a question", ex.Q)
					}
				}
				// A question that answers itself measures reading, not knowledge.
				if strings.EqualFold(f.subject, f.answer) {
					t.Errorf("%q answers itself", f.subject)
				}
			}
			for _, ex := range b.fewshot {
				if !shape[b.cat].MatchString(ex.A) {
					t.Errorf("the example answer %q is not the shape of this bank", ex.A)
				}
			}
		})
	}
}

// The question is made from the table, in the form its examples have, and every
// bank is among the code categories the queue is refilled from.
func TestBankQuestions(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	for _, b := range banks {
		spec, ok := codeSpecFor(b.cat)
		if !ok {
			t.Errorf("%s is not a code category", b.cat)
			continue
		}
		for i := 0; i < 50; i++ {
			q := spec.generate(rng, tierSmall)
			if q.Category != b.cat || q.Draft == "" || len(q.Fewshot) != 2 {
				t.Fatalf("%s: bad question %+v", b.cat, q)
			}
			if strings.Contains(q.Q, "%") {
				t.Fatalf("%s: question not filled in: %q", b.cat, q.Q)
			}
		}
	}
}

// How answers are scored, on the shapes these banks have.
func TestBankAnswersAreScored(t *testing.T) {
	cases := []struct {
		cat    Category
		draft  string
		answer string
		want   string
	}{
		{CatDomain, ".jp", ".jp", VerdictPass},
		{CatDomain, ".jp", "jp", VerdictPass},        // the dot is punctuation
		{CatDomain, ".jp", "It is .jp", VerdictPass}, // two extra words are allowed
		{CatDomain, ".jp", ".de", VerdictFail},
		{CatISO, "JPY", "jpy", VerdictPass},
		{CatISO, "JPY", "JPY.", VerdictPass},
		{CatISO, "JPY", "USD", VerdictFail},
		{CatVerb, "swam", "swam", VerdictPass},
		{CatVerb, "swam", "swimmed", VerdictFail},
		{CatSIUnit, "newton", "Newton", VerdictPass},
		{CatSIUnit, "newton", "N", VerdictFail},
	}
	for _, c := range cases {
		q := Question{Category: c.cat, Draft: c.draft}
		if got := Score(q, c.answer, 3); got != c.want {
			t.Errorf("%s %q vs %q: %s, want %s", c.cat, c.draft, c.answer, got, c.want)
		}
	}
}

// Answers are scored as word sets, so an answer that is an ordinary English word
// would be matched by a reply that merely starts "It is". Those are not asked.
func TestDomainBankLeavesOutEnglishWords(t *testing.T) {
	for _, f := range domainBank.facts {
		switch f.answer {
		case ".it", ".is", ".in", ".no", ".be", ".at", ".my", ".to", ".so", ".do", ".or", ".as", ".on", ".us", ".me":
			t.Errorf("%s (%s) is an English word and would match a reply that says it", f.subject, f.answer)
		}
	}
}
