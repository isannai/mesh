package probe

// slots.go — building an image order and the questions to judge it by, from
// one draw of the slot table.
//
// A text probe has a right answer. An image does not, so the question becomes
// "is this picture BOUND to the order that was placed" — and that is asked
// element by element: was the subject drawn, is the style right, is the framing
// right. Each element is put to CLIP as the expected phrasing against
// deliberately confusable alternatives.
//
// 🔴 THE ALTERNATIVES COME FROM A DIFFERENT CATEGORY OF THE SAME SLOT. That
// single rule is why the table has categories at all:
//
//	fox (animal)   → piano (instrument) · castle (building)
//	                 NEVER wolf, which is also an animal
//
// Same slot keeps the comparison meaningful (a subject against subjects); a
// different category keeps it decidable. Draw the alternative from the same
// category and CLIP is asked to separate a fox from a wolf, which it cannot do
// reliably — and an honest node fails. Getting this backwards is the single
// most damaging mistake available here, so the drawing code enforces it rather
// than leaving it to whoever writes the table.
//
// TWO FORMATS FROM ONE DRAW
//
//	SD    "red fox, a snowy forest, close-up, realistic photo"     tags
//	CLIP  "a photo of a fox" · "a realistic photo" · …             sentences
//
// Tags for the node because we do not know what it runs: a model fine-tuned on
// Danbooru tags follows tag lists and follows prose poorly, and an honest node
// must not fail for that. Sentences for CLIP because measurement says so —
// "a sleeping cat" scored 0.2878 against 0.2750 for the equivalent tag list.
//
// The node receives ONLY the prompt. It never sees the checks, so it cannot
// author its own alternatives — which would defeat the whole comparison.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
)

//go:embed slots.json
var slotsJSON []byte

// slotTable is the parsed vocabulary.
//
// Subject, style and colour are grouped by category; composition is a flat list
// because its values are already far apart (close-up / wide shot / aerial view)
// and inventing categories for them would buy nothing.
//
// 🔴 "macro shot" was REMOVED from that list. CLIP cannot separate it from
// "close-up" — they are the same picture — so it appeared as an honest node
// failing composition. The 100% measurement behind treating composition as
// required was taken on the other three.
type slotTable struct {
	ArticleOverride map[string]string `json:"_article_override"`
	// EnvironmentPrep is the preposition per environment value. Default "in";
	// a few read as broken English with it ("a koala IN a river bank") and
	// two take none at all ("a koala underwater").
	//
	// Broken English is not cosmetic here — CLIP scores a malformed caption
	// unpredictably, so it would show up as an honest node failing.
	EnvironmentPrep    map[string]string `json:"_environment_prep"`
	EnvironmentPrepDef string            `json:"_environment_prep_default"`
	Subject         map[string][]string `json:"subject"`
	Style           map[string][]string `json:"style"`
	Color           map[string][]string `json:"color"`
	Environment     map[string][]string `json:"environment"`
	Composition     []string            `json:"composition"`
}

// slots is the table, parsed once at startup. A malformed embed is a build-time
// mistake, not a runtime condition, so it panics rather than degrading.
var slots = mustLoadSlots()

func mustLoadSlots() *slotTable {
	var t slotTable
	if err := json.Unmarshal(slotsJSON, &t); err != nil {
		panic("probe: slots.json: " + err.Error())
	}
	if len(t.Subject) < 2 || len(t.Style) < 2 || len(t.Composition) < 3 {
		panic("probe: slots.json is too small to draw contrasting alternatives from")
	}
	return &t
}

// ImageOrder is one drawn image probe: what to ask the node for, and what to
// ask CLIP about the result.
type ImageOrder struct {
	// Prompt is the tag line sent to the node. The ONLY thing it receives.
	Prompt string
	// Checks are the element-wise comparisons, kept here and shown to nobody
	// but the validator.
	Checks []Check
}

// article returns "a" or "an" for a word.
//
// The vowel rule is wrong often enough to need a table — "an hourglass" is
// spelled with a consonant and "a ukulele" with a vowel — so the overrides in
// slots.json win. A wrong article does not fail a check on its own, but it
// makes the caption read as broken English, which is exactly the sort of input
// CLIP scores unpredictably.
func article(word string) string {
	w := strings.ToLower(strings.TrimSpace(word))
	if head := strings.Fields(w); len(head) > 0 {
		if a, ok := slots.ArticleOverride[head[0]]; ok {
			return a
		}
	}
	if w == "" {
		return "a"
	}
	if strings.ContainsRune("aeiou", rune(w[0])) {
		return "an"
	}
	return "a"
}

// np returns the noun phrase for a subject: "a fox", "an elephant".
func np(subject string) string { return article(subject) + " " + subject }

// inEnvironment renders "<subject phrase> <prep> <environment>", with the
// preposition the environment actually takes.
func inEnvironment(subjectPhrase, env string) string {
	prep := slots.EnvironmentPrepDef
	if prep == "" {
		prep = "in"
	}
	if p, ok := slots.EnvironmentPrep[env]; ok {
		prep = p
	}
	if prep == "" {
		return subjectPhrase + " " + env // "a koala underwater"
	}
	return subjectPhrase + " " + prep + " " + env
}

// drawWithAlternatives picks one value from `groups` and two alternatives, each
// from a DIFFERENT category than the pick.
//
// Returns ok=false when there are not at least three categories to draw from —
// better to leave the check out than to emit one whose alternatives come from
// the pick's own category.
func drawWithAlternatives(groups map[string][]string, rng *rand.Rand) (pick string, alts []string, ok bool) {
	cats := make([]string, 0, len(groups))
	for c, vals := range groups {
		if len(vals) > 0 {
			cats = append(cats, c)
		}
	}
	if len(cats) < 3 {
		return "", nil, false
	}
	// Sorted then shuffled: map iteration order is random per run in Go, and a
	// draw that depends on it cannot be reproduced from a seed.
	sortStrings(cats)
	rng.Shuffle(len(cats), func(i, j int) { cats[i], cats[j] = cats[j], cats[i] })

	own := cats[0]
	vals := groups[own]
	pick = vals[rng.Intn(len(vals))]
	for _, c := range cats[1:] {
		if len(alts) == 2 {
			break
		}
		v := groups[c]
		alts = append(alts, v[rng.Intn(len(v))])
	}
	return pick, alts, len(alts) == 2
}

// sortStrings is a tiny insertion sort — the slices here are a handful of
// category names, and pulling in sort for that is not worth the import.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// NewImageOrder draws one image probe.
//
// The prompt carries four tags — colour+subject, environment, composition,
// style. Four is deliberate: adding more dilutes attention and each element is
// followed LESS well, so a longer order makes an honest node look worse.
func NewImageOrder(rng *rand.Rand) (ImageOrder, bool) {
	subject, subjectAlts, ok := drawWithAlternatives(slots.Subject, rng)
	if !ok {
		return ImageOrder{}, false
	}
	style, styleAlts, ok := drawWithAlternatives(slots.Style, rng)
	if !ok {
		return ImageOrder{}, false
	}
	color, colorAlts, ok := drawWithAlternatives(slots.Color, rng)
	if !ok {
		return ImageOrder{}, false
	}
	env, envAlts, ok := drawWithAlternatives(slots.Environment, rng)
	if !ok {
		return ImageOrder{}, false
	}
	// Composition has no categories: its values are already far apart, so the
	// alternatives are simply the others.
	ci := rng.Intn(len(slots.Composition))
	comp := slots.Composition[ci]
	var compAlts []string
	for i, c := range slots.Composition {
		if i != ci && len(compAlts) < 2 {
			compAlts = append(compAlts, c)
		}
	}

	order := ImageOrder{
		// Tag form. Colour rides on the subject because that is how it is
		// written in an SD prompt, and separating them ("red, fox") reads as
		// two subjects.
		Prompt: strings.Join([]string{
			color + " " + subject,
			env,
			comp,
			style,
		}, ", "),
	}

	// 🔴 The captions name the REAL subject, every time. Generalising it
	// ("an animal in a snowy forest") breaks the moment the subject slot yields
	// an object — a piano is not an animal. Isolation comes from expect and its
	// alternatives differing in ONE slot, not from removing words: a shared word
	// raises every candidate's baseline equally and cancels in the softmax.
	order.Checks = []Check{
		{
			Label:        "subject",
			Expect:       "a photo of " + np(subject),
			Alternatives: []string{"a photo of " + np(subjectAlts[0]), "a photo of " + np(subjectAlts[1])},
		},
		{
			Label:        "style",
			Expect:       article(style) + " " + style,
			Alternatives: []string{article(styleAlts[0]) + " " + styleAlts[0], article(styleAlts[1]) + " " + styleAlts[1]},
		},
		{
			Label:  "composition",
			Expect: article(comp) + " " + comp + " of " + np(subject),
			Alternatives: []string{
				article(compAlts[0]) + " " + compAlts[0] + " of " + np(subject),
				article(compAlts[1]) + " " + compAlts[1] + " of " + np(subject),
			},
		},
		// colour and environment are recorded but NOT required. A correct image
		// scores about 66% on background colour, and SD often refuses an
		// unnatural colour outright or bleeds it into the background — failing
		// an honest node on either would be the worst outcome available.
		{
			Label:        "color",
			Expect:       article(color) + " " + color + " " + subject,
			Alternatives: []string{article(colorAlts[0]) + " " + colorAlts[0] + " " + subject, article(colorAlts[1]) + " " + colorAlts[1] + " " + subject},
		},
		{
			// 🔴 Drawn, not hardcoded. Fixed alternatives ("on a beach", "in a
			// kitchen") collide the moment the environment slot yields one of
			// them, and CLIP is then asked to choose between two identical
			// captions — a check that decides nothing while looking like it did.
			Label:  "environment",
			Expect: inEnvironment(np(subject), env),
			Alternatives: []string{
				inEnvironment(np(subject), envAlts[0]),
				inEnvironment(np(subject), envAlts[1]),
			},
		},
	}
	return order, true
}

// String renders an order for eyeballing — the shape `probe -slots N` prints.
func (o ImageOrder) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "SD    %s\n", o.Prompt)
	for _, c := range o.Checks {
		req := " "
		if requiredLabels[c.Label] {
			req = "*"
		}
		fmt.Fprintf(&b, "  %s %-12s %-40s vs %s\n",
			req, c.Label, c.Expect, strings.Join(c.Alternatives, " · "))
	}
	return b.String()
}

// PrintImageOrders draws n orders and prints them.
//
// Seeded from a fixed value rather than the clock: the point is to inspect the
// TABLE, and a draw that changes every run cannot be compared against the last
// one after an edit.
func PrintImageOrders(n int) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < n; i++ {
		o, ok := NewImageOrder(rng)
		if !ok {
			fmt.Printf("[%d] slot table too small to draw from\n", i+1)
			continue
		}
		fmt.Printf("[%d] %s\n", i+1, o)
	}
	fmt.Println("* = required (subject / style / composition). colour and environment are recorded, not enforced.")
}
