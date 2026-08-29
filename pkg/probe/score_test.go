package probe

import (
	"strings"
	"testing"
)

// The cases that decided the rule. Each one is a phrasing an honest node
// actually produces, or an attack the rule has to survive.
func TestWordsMatch(t *testing.T) {
	cases := []struct {
		name  string
		draft string
		got   string
		want  bool
	}{
		// Neither side is reliably the longer one, which is why containment
		// has to run in both directions.
		{"node adds a word", "Delhi", "New Delhi", true},
		{"node drops a word", "Washington D.C.", "Washington", true},
		{"trailing detail", "Ottawa", "Ottawa, Canada", true},
		{"case and punctuation", "South America", "south america.", true},

		// 🔴 Accented capitals are common, and which SIDE carries the accent is
		// a coin toss: the model writes the draft, the node writes the answer,
		// independently. Both are stripped so it does not matter.
		{"accent on the draft", "Bogotá", "Bogota", true},
		{"accent on the answer", "Bogota", "Bogotá", true},
		{"accents on both", "Brasília", "Brasília", true},
		{"accent inside a phrase", "San José", "san jose", true},
		{"nordic", "Reykjavík", "Reykjavik", true},
		// Folding must not make DIFFERENT places match.
		{"folding does not merge distinct names", "Bogotá", "Bogotan", false},

		// 🔴 Sharing one word is not a match. Place names collide on head
		// words constantly, and this pair is the reason the rule is
		// containment and not overlap.
		{"shared head word", "New Delhi", "New York", false},
		{"shared head word 2", "Cape Town", "Cape Canaveral", false},

		// 🔴 The length gate. Without it a node answering with a list of
		// capitals contains the draft for every geography question and passes
		// the category knowing nothing.
		{"word salad", "Paris", "Paris London Berlin Madrid Rome Tokyo", false},
		{"salad with the answer last", "Tokyo", "Paris London Berlin Madrid Tokyo", false},
		// Three words for a one-word draft is still inside the gate — that is
		// the allowance short honest phrasings need. A fourth word is over it,
		// and that ceiling is deliberate: colours and leg counts have answer
		// spaces small enough for a handful of words to cover them.
		{"gate boundary", "Paris", "it is Paris", true},
		{"one word past the gate", "Paris", "The capital is Paris", false},

		{"plain wrong", "Paris", "London", false},
		{"empty answer", "Paris", "", false},
		{"whitespace only", "Paris", "   ", false},
		// The stop sequence usually ends generation at the newline, but an
		// engine that ignores `stop` must not be judged on its essay.
		{"second line ignored", "Paris", "Paris\nParis is the capital of France.", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wordsMatch(c.draft, c.got); got != c.want {
				t.Errorf("wordsMatch(%q, %q) = %v, want %v", c.draft, c.got, got, c.want)
			}
		})
	}
}

// Math never goes through the word rule. Containment on digits would accept
// "192" for "92", and math is the one category whose answer is certain — it
// must be the strongest, not the weakest.
func TestMathMatches(t *testing.T) {
	cases := []struct {
		draft string
		got   string
		want  bool
	}{
		{"92", "92", true},
		{"92", "The answer is 92", true},
		{"92", " 92.", true},
		{"92", "192", false},
		{"92", "920", false},
		{"92", "9", false},
		{"92", "ninety-two", false}, // no digits to compare
		{"92", "", false},
	}
	for _, c := range cases {
		if got := mathMatches(c.draft, c.got); got != c.want {
			t.Errorf("mathMatches(%q, %q) = %v, want %v", c.draft, c.got, got, c.want)
		}
	}
}

// Score must route by category — a math question landing in the word rule is
// exactly the "192 passes as 92" hole.
func TestScoreRoutesByCategory(t *testing.T) {
	if v := Score(Question{Category: CatMath, Draft: "92"}, "192", 2); v != VerdictFail {
		t.Errorf("math 192 vs 92 = %s, want fail", v)
	}
	if v := Score(Question{Category: CatMath, Draft: "92"}, "92", 2); v != VerdictPass {
		t.Errorf("math 92 vs 92 = %s, want pass", v)
	}
	if v := Score(Question{Category: CatGeography, Draft: "Delhi"}, "New Delhi", 3); v != VerdictPass {
		t.Errorf("geography New Delhi vs Delhi = %s, want pass", v)
	}
}

// A node cut off by our own token cap has not answered wrongly — it has not
// finished answering. Scoring the prefix would score the cap.
func TestScoreTruncation(t *testing.T) {
	q := Question{Category: CatGeography, Draft: "Ouagadougou"}

	if v := Score(q, "Ouagadou", probeMaxTokens); v != VerdictTruncated {
		t.Errorf("cut-off answer = %s, want truncated", v)
	}
	// Wrong AND short: the node had room to answer and answered wrongly.
	if v := Score(q, "Paris", 2); v != VerdictFail {
		t.Errorf("short wrong answer = %s, want fail", v)
	}
	// 🔴 Correct then rambling past the cap is still correct. Checking
	// truncation before the match would discard shots the node got right.
	if v := Score(q, "Ouagadougou", probeMaxTokens); v != VerdictPass {
		t.Errorf("correct answer that hit the cap = %s, want pass", v)
	}
	// An engine that reports no usage must not have every wrong answer excused.
	if v := Score(q, "Paris", 0); v != VerdictFail {
		t.Errorf("no usage reported = %s, want fail", v)
	}
}

// 🔴 The prompt ends in "A:" and the node is meant to continue it. An engine
// that applies a chat template starts a FRESH reply instead, so the label comes
// back inside the answer — our own text, charged to the node as a word it never
// chose, out of the two the gate allows.
func TestStripAnswerLabel(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"bare label", "A:澳元", "澳元"},
		{"label with a space", "A: Paris", "Paris"},
		{"spelled out", "Answer: Paris", "Paris"},
		{"lowercase", "a: paris", "paris"},
		// The engines that echo the label are often the ones typing CJK
		// punctuation, so the full-width colon is not an exotic case.
		{"full-width colon", "A：東京", "東京"},
		{"leading space", "  A: Paris", "Paris"},

		// Everything else is the node's own text and must survive untouched.
		{"no label", "Paris", "Paris"},
		{"two words", "Cape Town", "Cape Town"},
		{"colon too far in", "The answer is: Paris", "The answer is: Paris"},
		{"a time is not a label", "10:30", "10:30"},
		{"another word before the colon", "AB: Paris", "AB: Paris"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := strings.TrimSpace(stripAnswerLabel(c.in)); got != c.want {
				t.Errorf("stripAnswerLabel(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// The label matters because it is counted, not because it is ugly: one phantom
// word is half the allowance a two-word draft has.
func TestScoreIgnoresEchoedLabel(t *testing.T) {
	q := Question{Category: CatGeography, Draft: "New Delhi"}

	// Five words with the label, four without — and the gate allows draft+2.
	if v := Score(q, "A: it is New Delhi", 6); v != VerdictPass {
		t.Errorf("echoed label = %s, want pass", v)
	}
	// Stripping it must not turn a wrong answer into a right one.
	if v := Score(q, "A: New York", 4); v != VerdictFail {
		t.Errorf("echoed label on a wrong answer = %s, want fail", v)
	}
}
