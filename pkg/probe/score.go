package probe

// score.go — deciding whether an answer matches what was expected.
//
// Two comparisons, because the two kinds of question differ in what is known:
//
//	math       the answer is CERTAIN (generated in code), so it is compared as a
//	           number and must match exactly.
//	the rest   the expected answer is a model-written DRAFT, and the node's
//	           phrasing is its own. Compared as a set of words, with containment
//	           in EITHER direction counting as a match.
//
// The bidirectional rule exists because neither side is reliably the longer
// one: a draft of "Delhi" meets a node saying "New Delhi", and a draft of
// "Washington D.C." meets a node saying "Washington". Requiring one fixed
// direction fails whichever case it was not written for.
//
// 🔴 What makes containment safe is the LENGTH GATE, not the containment rule.
// See wordsMatch.

import (
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

// Verdict is the outcome of scoring one answer.
//
// VerdictTruncated is neither a pass nor a fail. It says the node was still
// speaking when the token budget ran out, so what came back is not its answer —
// it is a prefix of its answer, and judging a prefix is judging our own cap.
const (
	VerdictPass      = "pass"
	VerdictFail      = "fail"
	VerdictTruncated = "truncated"
)

// Score compares a node's answer against the question's expected answer.
//
// completionTokens is what the node reported generating; hitting the ceiling
// exactly is what marks a cut-off answer. Zero means the engine reported
// nothing, which is treated as "not truncated" — an engine that omits usage
// must not have every wrong answer excused.
func Score(q Question, answer string, completionTokens int) string {
	matched := false
	if q.Category == CatMath {
		matched = mathMatches(q.Draft, answer)
	} else {
		matched = wordsMatch(q.Draft, answer)
	}
	if matched {
		// A match despite the cap is still a match: the answer arrived before
		// the budget ran out even if the node kept going afterwards.
		return VerdictPass
	}
	// 🔴 Only now. Checking truncation FIRST would throw away shots that
	// answered correctly and then rambled past the limit.
	if completionTokens > 0 && completionTokens >= probeMaxTokens {
		return VerdictTruncated
	}
	return VerdictFail
}

// maxExtraWords is how many words a node may add beyond the draft's own count.
//
// 🔴 This gate is what makes containment usable at all. Without a cap, a node
// that answers with a list of the world's capitals contains the draft for every
// geography question and passes them all with no knowledge whatsoever —
// probeMaxTokens is 64, which is room for about fifty words.
//
// Two, and it stays tight because the salad bites hardest where the answer
// space is SMALL. Capitals have hundreds of answers, so even a six-word list
// covers almost nothing — but colours have about ten and leg counts have four,
// and there a handful of words would cover the category outright.
//
// The cost is that "The capital is Paris" (four words for a one-word draft)
// fails while "it is Paris" passes. That is accepted: the few-shot pair shows
// one-word answers and temperature is 0, so a full sentence is the unusual
// case, not the norm being priced in.
const maxExtraWords = 2

// wordsMatch reports whether draft and answer match as word sets.
//
// Word sets, NOT substrings. "192" contains "92" as a substring but is a
// different number, and the same trap catches place names — a substring rule
// would let "Cape Canaveral" satisfy "Cape Town".
//
// Containment, NOT overlap. Sharing one word is far too weak: "New York" and
// "New Delhi" share "new", and place names collide on head words like new,
// san, saint, port and north constantly. One side's words must ALL appear in
// the other's.
func wordsMatch(draft, answer string) bool {
	want := words(draft)
	got := words(answer)
	if len(want) == 0 || len(got) == 0 {
		return false
	}
	if len(got) > len(want)+maxExtraWords {
		return false
	}
	return subset(want, got) || subset(got, want)
}

// subset reports whether every word of a appears in b.
func subset(a, b []string) bool {
	in := make(map[string]bool, len(b))
	for _, w := range b {
		in[w] = true
	}
	for _, w := range a {
		if !in[w] {
			return false
		}
	}
	return true
}

// foldDiacritics is the transformer that strips accent marks.
//
// 🔴 Without it "Bogotá" and "Bogota" are different strings and an honest node
// fails. Accented capitals are not a rare case — Bogotá, Brasília, Asunción,
// San José, Reykjavík — and which side carries the accent is a coin toss: the
// model writes the draft and the node writes the answer, independently.
//
// NFD splits a letter into its base plus a combining mark, and unicode.Mn is
// exactly the class of those marks, so removing them leaves the base letters.
// This is asymmetric-safe: stripping BOTH sides means it does not matter which
// one had the accent.
var foldDiacritics = transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// words normalises text into comparable tokens.
//
// Only the FIRST LINE is read. The stop sequence usually ends generation there,
// but a node whose engine ignores `stop` would otherwise be judged on an
// explanatory paragraph it was never asked for.
func words(s string) []string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	// Accents come off before the lowercasing, so "É" and "e" meet in the
	// middle. A transform failure leaves the text as it was — a comparison on
	// the original beats refusing to score at all.
	if folded, _, err := transform.String(foldDiacritics, s); err == nil {
		s = folded
	}
	s = strings.ToLower(s)
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// mathMatches compares two arithmetic answers as numbers.
//
// The draft is generated in code and therefore certain, so this is an exact
// comparison and deliberately does not fall back to the word rule: containment
// on digits would accept "192" for "92".
//
// The node's answer may still be wrapped in words ("The answer is 92"), so the
// first number in it is taken. Anything after that is ignored rather than
// failed — an engine that appends a stray token should not cost a correct node
// its shot.
func mathMatches(draft, answer string) bool {
	want, ok := firstNumber(draft)
	if !ok {
		return false
	}
	got, ok := firstNumber(answer)
	if !ok {
		return false
	}
	return want == got
}

// firstNumber pulls the first integer out of s.
func firstNumber(s string) (int, bool) {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = s[:i]
	}
	start := -1
	for i, r := range s {
		if unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			n, err := strconv.Atoi(s[start:i])
			return n, err == nil
		}
	}
	if start >= 0 {
		n, err := strconv.Atoi(s[start:])
		return n, err == nil
	}
	return 0, false
}
