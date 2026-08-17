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
// Subject and colour are grouped by category; composition is a flat list
// because its values are already far apart and inventing categories for them
// would buy nothing.
//
// 🔴 "macro shot" was REMOVED from that list. CLIP cannot separate it from
// "close-up" — they are the same picture — so it appeared as an honest node
// failing composition.
//
// 🔴 STYLE IS GONE ENTIRELY. It was a required check and honest nodes kept
// failing it: a plain SD checkpoint draws the right subject in the right
// framing and simply does not render the asked-for art style. Requiring it
// measured the checkpoint rather than the effort. The slot stays in slots.json
// so an older file still parses, but nothing reads it.
type slotTable struct {
	ArticleOverride map[string]string `json:"_article_override"`
	// EnvironmentPrep is the preposition per environment value. Default "in";
	// a few read as broken English with it ("a koala IN a river bank") and
	// two take none at all ("a koala underwater").
	//
	// Broken English is not cosmetic here — CLIP scores a malformed caption
	// unpredictably, so it would show up as an honest node failing.
	EnvironmentPrep    map[string]string   `json:"_environment_prep"`
	EnvironmentPrepDef string              `json:"_environment_prep_default"`
	Subject            map[string][]string `json:"subject"`
	Style              map[string][]string `json:"style"`
	Color              map[string][]string `json:"color"`
	Environment        map[string][]string `json:"environment"`
	Composition        []string            `json:"composition"`
}

// frontComposition is the framing every image order asks for.
//
// A constant rather than a slot draw: see NewImageOrder. Worded the way an SD
// prompt words it, and the same words go into the caption CLIP scores, so the
// two cannot drift apart.
const frontComposition = "front view"

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
// The prompt carries three tags — colour+subject, environment, composition.
// Short is deliberate: adding more dilutes attention and each element is
// followed LESS well, so a longer order makes an honest node look worse. It was
// four until style came out.
func NewImageOrder(rng *rand.Rand) (ImageOrder, bool) {
	subject, subjectAlts, ok := drawWithAlternatives(slots.Subject, rng)
	if !ok {
		return ImageOrder{}, false
	}
	color, colorAlts, ok := drawWithAlternatives(slots.Color, rng)
	if !ok {
		return ImageOrder{}, false
	}
	// 🔴 COMPOSITION IS FIXED, NOT DRAWN. Every order asks for a front view.
	//
	// The alternatives still vary the caption, so CLIP is still made to choose
	// and the check still decides something. What is given up is that a node
	// knows the answer in advance — which costs little, because the SUBJECT is
	// what a cached picture cannot satisfy, and that is still drawn per shot.
	//
	// What is bought is that honest nodes stop failing on framing. "aerial view"
	// in particular is a shot a plain checkpoint often will not produce, and it
	// was sinking whole rounds.
	comp := frontComposition
	compAlts := []string{"close-up", "wide shot"}

	order := ImageOrder{
		// 🔴 THE COLOUR IS ON THE BACKGROUND, NOT THE SUBJECT.
		//
		// It used to ride the subject ("an amber crocodile") because that is how
		// an SD prompt is written. It measured at 66% on correct images and was
		// never required: SD refuses an unnatural subject colour outright as
		// often as it obeys, and when it obeys it tends to bleed the colour into
		// the background anyway.
		//
		// A background colour has neither problem. A flat backdrop is the easiest
		// thing in the prompt to satisfy, it fills most of the frame, and CLIP
		// reads it far more reliably than a place ("a parking lot") that the
		// subject stands in front of and half occludes.
		//
		// So the slot is still drawn from the same vocabulary, and it is now the
		// candidate for the second required check — once the clip manifest
		// declares a param to carry it. See requiredLabels.
		Prompt: strings.Join([]string{
			subject,
			color + " background",
			comp,
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
			Label:  "composition",
			Expect: article(comp) + " " + comp + " of " + np(subject),
			Alternatives: []string{
				article(compAlts[0]) + " " + compAlts[0] + " of " + np(subject),
				article(compAlts[1]) + " " + compAlts[1] + " of " + np(subject),
			},
		},
		// composition, colour and environment are recorded but NOT required.
		// clipRun only sends the required labels, so none of these reach CLIP —
		// they exist so a failure can be investigated against what was actually
		// ordered, and so the shape is ready if a label is promoted later. See
		// requiredLabels.
		{
			// Phrased as the picture, not as the object: "on a red background"
			// is what CLIP is being asked to see, and the alternatives differ in
			// exactly that one word.
			Label:  "background",
			Expect: np(subject) + " on " + article(color) + " " + color + " background",
			Alternatives: []string{
				np(subject) + " on " + article(colorAlts[0]) + " " + colorAlts[0] + " background",
				np(subject) + " on " + article(colorAlts[1]) + " " + colorAlts[1] + " background",
			},
		},
		// 🔴 ENVIRONMENT IS NO LONGER ORDERED. A place ("a parking lot") competes
		// with the background colour for the same pixels, and asking for both
		// gave SD two contradictory instructions about what is behind the
		// subject. The colour is the one that can be checked, so the place went.
		//
		// inEnvironment and the environment slot are kept: the vocabulary and
		// its per-value prepositions are the expensive part, and this is a
		// choice about what to ORDER, not a decision that places are useless.
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
	fmt.Println("* = required (subject / background). composition is recorded, not enforced.")
}
