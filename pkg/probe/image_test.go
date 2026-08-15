package probe

import (
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/rvnodes"
)

// 🔴 The rule the whole image track rests on: an alternative must come from a
// DIFFERENT category than the pick. Draw it from the same one and CLIP is asked
// to tell a fox from a wolf, which it cannot do — an honest node then fails and
// it looks like a model problem.
func TestAlternativesComeFromOtherCategories(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	// Which category each subject belongs to.
	catOf := map[string]string{}
	for cat, vals := range slots.Subject {
		for _, v := range vals {
			catOf[v] = cat
		}
	}

	for i := 0; i < 300; i++ {
		o, ok := NewImageOrder(rng)
		if !ok {
			t.Fatal("draw failed")
		}
		var subj Check
		for _, c := range o.Checks {
			if c.Label == "subject" {
				subj = c
			}
		}
		// Recover the drawn values from the captions ("a photo of a fox").
		pick := strings.TrimPrefix(subj.Expect, "a photo of ")
		pickCat := catOf[stripArticle(pick)]
		if pickCat == "" {
			t.Fatalf("subject %q is not in the table", pick)
		}
		for _, alt := range subj.Alternatives {
			a := stripArticle(strings.TrimPrefix(alt, "a photo of "))
			if catOf[a] == pickCat {
				t.Fatalf("alternative %q shares category %q with the pick %q", a, pickCat, pick)
			}
		}
	}
}

func stripArticle(s string) string {
	s = strings.TrimSpace(s)
	for _, a := range []string{"an ", "a "} {
		if strings.HasPrefix(s, a) {
			return s[len(a):]
		}
	}
	return s
}

// "macro shot" was removed because CLIP cannot separate it from "close-up" —
// they are the same picture, so it showed up as honest nodes failing.
func TestCompositionHasNoNearSynonyms(t *testing.T) {
	for _, c := range slots.Composition {
		if strings.Contains(strings.ToLower(c), "macro") {
			t.Errorf("composition %q is indistinguishable from close-up", c)
		}
	}
	if len(slots.Composition) < 3 {
		t.Errorf("composition needs 3 values so a pick has two alternatives, got %d", len(slots.Composition))
	}
}

// A caption in broken English is scored unpredictably by CLIP, which surfaces
// as an honest node failing. Prepositions are per-value for that reason.
func TestEnvironmentPrepositions(t *testing.T) {
	cases := map[string]string{
		"a river bank": "a fox on a river bank",
		"underwater":   "a fox underwater",
		"a valley":     "a fox in a valley",
	}
	for env, want := range cases {
		if got := inEnvironment("a fox", env); got != want {
			t.Errorf("inEnvironment(%q) = %q, want %q", env, got, want)
		}
	}
}

// Every order must carry the three required checks — a missing one silently
// removes an axis from the verdict.
func TestOrderCarriesRequiredChecks(t *testing.T) {
	rng := rand.New(rand.NewSource(9))
	o, ok := NewImageOrder(rng)
	if !ok {
		t.Fatal("draw failed")
	}
	seen := map[string]bool{}
	for _, c := range o.Checks {
		seen[c.Label] = true
		if c.Expect == "" || len(c.Alternatives) != 2 {
			t.Errorf("check %q: expect=%q alts=%v", c.Label, c.Expect, c.Alternatives)
		}
		for _, a := range c.Alternatives {
			if a == c.Expect {
				t.Errorf("check %q lists its own expectation as an alternative", c.Label)
			}
		}
	}
	for label := range requiredLabels {
		if !seen[label] {
			t.Errorf("order is missing the required check %q", label)
		}
	}
	// Four tags: colour+subject, environment, composition, style. More dilutes
	// attention and each element is followed LESS well.
	if n := strings.Count(o.Prompt, ",") + 1; n != 4 {
		t.Errorf("prompt has %d tags, want 4: %q", n, o.Prompt)
	}
}

// The image track must not fire at nodes that only serve text, and vice versa.
func TestImageTargetsPickImageServices(t *testing.T) {
	nodes := []rvnodes.Node{
		{ID: "text", Addr: "203.0.113.1:1", AuthMode: "public",
			Services: []rvnodes.Service{{Name: "llm-api", ServerReady: true}}},
		{ID: "img", Addr: "203.0.113.2:1", AuthMode: "public",
			Services: []rvnodes.Service{{Name: "sd-api", ServerReady: true}}},
		{ID: "loading", Addr: "203.0.113.3:1", AuthMode: "public",
			Services: []rvnodes.Service{{Name: "sd-api", ServerLoading: true}}},
		{ID: "protected", Addr: "203.0.113.4:1", AuthMode: "protected",
			Services: []rvnodes.Service{{Name: "sd-api", ServerReady: true}}},
	}
	got := imageTargets(nodes, nil)
	if len(got) != 1 || got[0].Node.ID != "img" {
		t.Fatalf("got %+v, want only the ready public sd node", got)
	}
	if txt := eligible(nodes, nil); len(txt) != 1 || txt[0].Node.ID != "text" {
		t.Fatalf("text targets = %+v, want only the llm node", txt)
	}
}

// 🔴 The architecture decides the size, and the wrong one wrecks the picture
// rather than merely slowing it down: SDXL at 512 produces duplicated subjects
// and broken anatomy, SD 1.5 at 1024 does the same in reverse. Either way an
// honest node fails for our mistake.
func TestSizeFollowsArchitecture(t *testing.T) {
	p := &Prober{cfg: Config{}}

	// The DECLARED architecture, which the node stamps onto the register frame
	// from its own package.json. This is the path that should normally decide.
	for _, a := range []string{"sdxl", "sd3", "flux", "SDXL"} {
		if sizes := p.imageParams(a, ""); sizes[0] != "1024x1024" {
			t.Errorf("arch %q got %v, want the XL family", a, sizes)
		}
	}
	// 🔴 Exactly one size, not merely a list starting with it. The non-square
	// 512 resolutions are legal for SD 1.5 but cost enough extra VRAM that a
	// 4GB card spills to system memory and blows the deadline — recorded as a
	// timeout against a node that did nothing wrong.
	for _, a := range []string{"sd15", "pony"} {
		sizes := p.imageParams(a, "")
		if len(sizes) != 1 || sizes[0] != "512x512" {
			t.Errorf("arch %q got %v, want exactly [512x512]", a, sizes)
		}
	}

	// 🔴 A declaration outranks the name. This node says sd15 while its file is
	// called "...xl-merge"; honouring the name here would send 1024 to a 512
	// model on the strength of a substring.
	if sizes := p.imageParams("sd15", "my-xl-merge"); sizes[0] != "512x512" {
		t.Errorf("declared sd15 with an xl-ish name got %v, want the 512 family", sizes)
	}
	if sizes := p.imageParams("sdxl", "v1-5-pruned-emaonly"); sizes[0] != "1024x1024" {
		t.Errorf("declared sdxl with a 1.5-ish name got %v, want the XL family", sizes)
	}

	// The name fallback, for nodes on an isannd older than the one that stamps
	// model_arch and for models registered without an architecture.
	xl := []string{"sd_xl_base_1.0", "sdxl-turbo", "juggernautXL_v9", "playground-v2.5", "flux1-dev"}
	for _, m := range xl {
		sizes := p.imageParams("", m)
		if sizes[0] != "1024x1024" {
			t.Errorf("%q got %v, want the XL family", m, sizes)
		}
	}

	for _, m := range []string{"v1-5-pruned-emaonly", "dreamshaper_8", "realisticVision_v6", ""} {
		sizes := p.imageParams("", m)
		if sizes[0] != "512x512" {
			t.Errorf("%q got %v, want the 512 family", m, sizes)
		}
	}
}

// An explicit config wins over both the declaration and the guess — the
// operator knows their own fleet, and this is the escape hatch when a node
// declares an architecture its hardware cannot actually render at.
func TestConfiguredSizesWinOverTheGuess(t *testing.T) {
	p := &Prober{cfg: Config{ImageSizes: []string{"640x640"}}}
	sizes := p.imageParams("sdxl", "sd_xl_base_1.0")
	if len(sizes) != 1 || sizes[0] != "640x640" {
		t.Errorf("sizes = %v, want the configured list", sizes)
	}
}

// 🔴 `steps` must NOT be in the run block. The engine manifest declares it with
// a default, so omitting the field is what makes the engine's own default
// apply. Sending a number — any number, including the same 20 — makes the
// prober the authority on a value it does not own, and freezes it the day an
// engine changes its default.
func TestRunBlockOmitsSteps(t *testing.T) {
	p := &Prober{cfg: Config{}, rng: rand.New(rand.NewSource(3))}
	o, ok := NewImageOrder(p.rng)
	if !ok {
		t.Fatal("draw failed")
	}
	run := p.imageRun(o, rvnodes.Service{ModelArch: "sd15"}, p.rng)
	if _, present := run["steps"]; present {
		t.Errorf("run block carries steps=%v; the manifest default must decide", run["steps"])
	}
	for _, k := range []string{"prompt", "size", "seed"} {
		if _, present := run[k]; !present {
			t.Errorf("run block is missing %q", k)
		}
	}
	if run["size"] != "512x512" {
		t.Errorf("size = %v, want 512x512 for a declared sd15", run["size"])
	}
}

// The deadline comes from what the node reports about itself. A fixed one had
// every shot at a 371s node recorded as a timeout while it drew each picture
// correctly — the number was ours, not the node's.
func TestImageDeadlineFollowsReportedSpeed(t *testing.T) {
	m, err := rvnodes.DecodeMetrics([]byte(`[
		{"node_id":"S:0xAAA","service":"sd-api","avg_job_sec":370.9,"running_count":0},
		{"node_id":"S:0xBBB","service":"sd-api","avg_job_sec":20,"running_count":0},
		{"node_id":"S:0xCCC","service":"sd-api","avg_job_sec":5000,"running_count":0}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	p := &Prober{cfg: Config{}, metrics: m}

	// 370.9 x 2 = 741.8s, inside the band.
	if got := p.imageDeadline("s:0xaaa", "sd-api"); got != 741*time.Second {
		t.Errorf("371s node got %v, want ~742s", got)
	}
	// Fast node still gets the floor — a mean means half the jobs are slower.
	if got := p.imageDeadline("S:0xBBB", "sd-api"); got != minImageDeadline {
		t.Errorf("fast node got %v, want the floor %v", got, minImageDeadline)
	}
	// Absurdly slow node is capped rather than blocking a round for an hour.
	if got := p.imageDeadline("S:0xCCC", "sd-api"); got != maxImageDeadline {
		t.Errorf("slow node got %v, want the ceiling %v", got, maxImageDeadline)
	}
	// Unknown is not fast.
	if got := p.imageDeadline("S:0xZZZ", "sd-api"); got != minImageDeadline {
		t.Errorf("unknown node got %v, want the floor", got)
	}
	// The operator's explicit value outranks the computation.
	pc := &Prober{cfg: Config{ImageDeadline: 60}, metrics: m}
	if got := pc.imageDeadline("s:0xaaa", "sd-api"); got != 60*time.Second {
		t.Errorf("configured deadline got %v, want 60s", got)
	}
}

// A node still working on the last order must not be given another. The
// deadline expiring does not stop the node; firing again only queues behind
// work we have already stopped waiting for.
func TestBusyNodesAreSkipped(t *testing.T) {
	m, err := rvnodes.DecodeMetrics([]byte(`[
		{"node_id":"S:0xAAA","service":"sd-api","running_count":1},
		{"node_id":"S:0xBBB","service":"sd-api","queue_depth":2},
		{"node_id":"S:0xCCC","service":"sd-api","running_count":0,"queue_depth":0}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	p := &Prober{metrics: m}
	svc := rvnodes.Service{Name: "sd-api"}
	due := []Target{
		{Node: rvnodes.Node{ID: "S:0xAAA"}, Service: svc}, // running
		{Node: rvnodes.Node{ID: "S:0xBBB"}, Service: svc}, // queued
		{Node: rvnodes.Node{ID: "S:0xCCC"}, Service: svc}, // idle
		{Node: rvnodes.Node{ID: "S:0xZZZ"}, Service: svc}, // unknown
	}
	got := p.dropBusy(due)
	// Unknown is fired at: one flaky metrics poll must not stop the track.
	if len(got) != 2 || got[0].Node.ID != "S:0xCCC" || got[1].Node.ID != "S:0xZZZ" {
		t.Fatalf("kept %+v, want only the idle and the unknown node", got)
	}
}

// Node ids arrive in mixed case in the SAME response — the RV echoes the
// checksummed form on some rows and lowercase on others. A case-sensitive
// lookup silently finds nothing, and every deadline quietly falls back.
func TestMetricLookupIsCaseInsensitive(t *testing.T) {
	m, err := rvnodes.DecodeMetrics([]byte(`[{"node_id":"S:0x8fF81256","service":"SD-API","avg_job_sec":100}]`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Get("s:0x8ff81256", "sd-api"); !ok {
		t.Error("lowercase lookup missed a checksummed row")
	}
	if _, ok := m.Get("S:0x8FF81256", "Sd-Api"); !ok {
		t.Error("uppercase lookup missed it")
	}
}
