package probe

import (
	"encoding/json"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fullChecks is a complete, well-formed order — every required label with both
// alternatives. Tests about failover and parsing need one because clipRun
// refuses an incomplete order outright; they are not testing that rule.
func fullChecks() []Check {
	return []Check{
		{Label: "subject", Expect: "a photo of a fox", Alternatives: []string{"a photo of a banjo", "a photo of a jacket"}},
		{Label: "background", Expect: "a fox on a red background", Alternatives: []string{"a fox on a blue background", "a fox on a green background"}},
		{Label: "composition", Expect: "a front view of a fox", Alternatives: []string{"a close-up of a fox", "a wide shot of a fox"}},
	}
}

// verdictBody builds a validator reply with the given per-check outcomes.
func verdictBody(t *testing.T, pass bool, checks map[string]bool) string {
	t.Helper()
	type chk struct {
		Label      string  `json:"label"`
		Confidence float64 `json:"confidence"`
		Pass       bool    `json:"pass"`
	}
	body := map[string]any{"pass": pass, "model": "clip-vit-base-patch32", "version": "1.0.0"}
	var list []chk
	for label, ok := range checks {
		list = append(list, chk{Label: label, Pass: ok, Confidence: 0.9})
	}
	body["checks"] = list
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The validator's own `pass` is an AND over every check, so one optional
// element failing sinks an otherwise correct image. The required/optional split
// has to live on our side.
func TestRequiredPass(t *testing.T) {
	cases := []struct {
		name   string
		pass   bool
		checks map[string]bool
		want   bool
	}{
		{
			// SD often will not paint an unnatural colour, and a correct image
			// scores ~66% on background colour. Requiring those fails honest
			// nodes, so the top-level false is ignored.
			name:   "optional failure does not sink the image",
			pass:   false,
			checks: map[string]bool{"subject": true, "background": true, "composition": false},
			want:   true,
		},
		{
			name:   "required failure fails",
			pass:   false,
			checks: map[string]bool{"subject": false, "background": true},
			want:   false,
		},
		{
			// 🔴 An empty verdict must not read as a pass — otherwise a
			// validator answering `{}` mints tickets for everything.
			name:   "no required checks is not a pass",
			pass:   true,
			checks: map[string]bool{"composition": true},
			want:   false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var j Judgement
			if err := json.Unmarshal([]byte(verdictBody(t, c.pass, c.checks)), &j); err != nil {
				t.Fatal(err)
			}
			if got := j.RequiredPass(); got != c.want {
				t.Errorf("RequiredPass() = %v, want %v", got, c.want)
			}
		})
	}
}

// stationStub models what a station ACTUALLY does with a validate job, which is
// the thing the old stub got wrong and the reason a broken call shipped:
//
//	POST /v1/jobs            202 {"job_id":…}      NOT the verdict
//	GET  /v1/jobs/<id>       {"status":"done"}
//	GET  /v1/jobs/<id>/result the verdict
//
// verdict is served on the result leg; a nil verdict makes the submit leg fail
// with 500 so failover can be exercised.
func stationStub(t *testing.T, seen *[]string, verdictFor func(path string) string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/result"):
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(verdictFor(p)))
		case strings.Contains(p, "/v1/jobs/"):
			w.Write([]byte(`{"status":"done"}`))
		default:
			if seen != nil {
				*seen = append(*seen, p)
			}
			if verdictFor(p) == "" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"job_id":"j1"}`))
		}
	}
}

// newLocalValidator points a Validator at a stub isannd.
func newLocalValidator(t *testing.T, entries []string, h http.HandlerFunc) *Validator {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	v := NewValidator(srv.URL, parsePool(entries, "clip-api"))
	v.Deadline = 5 * time.Second
	return v
}

// 🔴 The rule this file exists for. A clean pass:false is an ANSWER: if it
// triggered failover, a list of validators would become "ask until one says
// yes" and adding judges would raise the pass rate.
func TestFailVerdictDoesNotFailOver(t *testing.T) {
	var submits []string
	body := verdictBody(t, false, map[string]bool{"subject": false, "background": true})
	v := newLocalValidator(t, []string{"this", "this"},
		stationStub(t, &submits, func(string) string { return body }))

	j, err := v.Validate("aGVsbG8=", fullChecks())
	if err != nil {
		t.Fatalf("a fail verdict was reported as an error: %v", err)
	}
	if j.RequiredPass() {
		t.Error("a failing subject check passed")
	}
	if len(submits) != 1 {
		t.Errorf("%d validators were asked; one image gets one verdict", len(submits))
	}
}

// A validator that cannot judge is a different thing, and does fail over.
func TestBrokenValidatorFailsOver(t *testing.T) {
	var paths []string
	good := verdictBody(t, true, map[string]bool{"subject": true, "background": true})
	v := newLocalValidator(t, []string{"this/broken", "this/good"},
		stationStub(t, &paths, func(p string) string {
			if strings.Contains(p, "broken") {
				return ""
			}
			return good
		}))

	j, err := v.Validate("aGVsbG8=", fullChecks())
	if err != nil {
		t.Fatal(err)
	}
	if !j.RequiredPass() {
		t.Error("the working validator's pass was lost")
	}
	if len(paths) != 2 {
		t.Fatalf("paths = %v; the broken entry should have handed over", paths)
	}
	if j.Validator != "this/good" {
		t.Errorf("Validator = %q, want the entry that answered", j.Validator)
	}
}

// A body with an `error` field, or with no checks at all, is not a verdict.
// Recording either as a fail would let a broken judge condemn every node.
func TestMalformedVerdictIsNotAFail(t *testing.T) {
	for _, body := range []string{`{"error":"image decode failed"}`, `{"pass":true}`, `{}`} {
		v := newLocalValidator(t, []string{"this"}, func(w http.ResponseWriter, r *http.Request) {
			w.Write([]byte(body))
		})
		if _, err := v.Validate("aGVsbG8=", fullChecks()); err == nil {
			t.Errorf("%s was accepted as a verdict", body)
		}
	}
}

// No clips configured is a chosen state, not an error to log every round.
func TestValidatorDisabled(t *testing.T) {
	v := NewValidator("http://127.0.0.1:8443", parsePool(nil, "clip-api"))
	if v.Enabled() {
		t.Error("a validator with no entries reports enabled")
	}
	if _, err := v.Validate("x", []Check{{Label: "subject"}}); err != errNoPoolEntries {
		t.Errorf("err = %v, want errNoPoolEntries", err)
	}
	var nilV *Validator
	if nilV.Enabled() {
		t.Error("a nil validator reports enabled")
	}
}

// 🔴 The run block must speak the MANIFEST's param names, not the engine's body
// shape. The station assembles the engine body from flat subject/style/
// composition params; a block written as {image_base64, checks} matches no
// declared param, so every field is dropped and the validator is handed an
// empty image with empty expectations — silently, with nothing erroring.
func TestClipRunUsesManifestParamNames(t *testing.T) {
	o, ok := NewImageOrder(rand.New(rand.NewSource(11)))
	if !ok {
		t.Fatal("draw failed")
	}
	run, err := clipRun("QkFTRTY0", o.Checks)
	if err != nil {
		t.Fatal(err)
	}

	if run["image"] != "QkFTRTY0" {
		t.Errorf("image = %v, want the base64 under the manifest name %q", run["image"], "image")
	}
	for _, engineShape := range []string{"image_base64", "checks"} {
		if _, present := run[engineShape]; present {
			t.Errorf("run carries %q — that is the engine body shape, not a declared param", engineShape)
		}
	}
	// Every required label, each with both alternatives.
	for label := range requiredLabels {
		for _, k := range []string{label, label + "-alt", label + "-alt2"} {
			if s, _ := run[k].(string); s == "" {
				t.Errorf("run is missing %q", k)
			}
		}
	}
	// colour and environment have no manifest slot; sending them would do
	// nothing, and they never decide a verdict anyway.
	for _, k := range []string{"composition", "color", "environment"} {
		if _, present := run[k]; present {
			t.Errorf("run carries %q, which the manifest does not declare", k)
		}
	}
}

// A missing required check must stop the call, not produce a verdict that looks
// complete while one axis was never examined.
func TestClipRunRejectsAnIncompleteOrder(t *testing.T) {
	// Everything the order carries EXCEPT a required label. The validator would
	// happily answer on what it got, and the verdict would look complete while
	// the one axis that decides was never examined.
	noSubject := []Check{
		{Label: "composition", Expect: "a front view of a fox", Alternatives: []string{"a close-up of a fox", "a wide shot of a fox"}},
		{Label: "background", Expect: "a fox on a red background", Alternatives: []string{"a fox on a blue background", "a fox on a green background"}},
	}
	if _, err := clipRun("x", noSubject); err == nil {
		t.Error("clipRun accepted an order with no subject check")
	}

	// And the mirror: the required set is the whole bar, so an order carrying it
	// is complete even though other labels are absent.
	if _, err := clipRun("x", []Check{
		{Label: "subject", Expect: "a photo of a fox", Alternatives: []string{"a photo of a banjo", "a photo of a jacket"}},
		{Label: "background", Expect: "a fox on a red background", Alternatives: []string{"a fox on a blue background", "a fox on a green background"}},
	}); err != nil {
		t.Errorf("clipRun refused an order that carries every required label: %v", err)
	}
}

// 🔴 A submission receipt is not a verdict. POST /v1/jobs answers 202 with
// {"job_id":…} — `?timeout=` does not make the station block — so the reply has
// to be polled, not parsed. Reading the receipt surfaced as "verdict carries no
// checks" while CLIP was judging every picture correctly and its answer was
// simply never fetched.
func TestVerdictIsPolledNotTakenFromTheSubmitReply(t *testing.T) {
	var legs []string
	good := verdictBody(t, true, map[string]bool{"subject": true, "background": true})
	v := newLocalValidator(t, []string{"this"}, func(w http.ResponseWriter, r *http.Request) {
		legs = append(legs, r.Method+" "+r.URL.Path)
		switch {
		case strings.HasSuffix(r.URL.Path, "/result"):
			w.Write([]byte(good))
		case strings.Contains(r.URL.Path, "/v1/jobs/"):
			w.Write([]byte(`{"status":"done"}`))
		default:
			w.WriteHeader(http.StatusAccepted)
			w.Write([]byte(`{"job_id":"j1"}`))
		}
	})

	j, err := v.Validate("aGVsbG8=", fullChecks())
	if err != nil {
		t.Fatal(err)
	}
	if !j.RequiredPass() {
		t.Error("the verdict was lost")
	}
	// Submit, status, result — all three legs, in that order.
	if len(legs) != 3 {
		t.Fatalf("legs = %v, want submit + status + result", legs)
	}
	if !strings.HasSuffix(legs[2], "/result") {
		t.Errorf("last leg was %q; the verdict must come from /result", legs[2])
	}
}

// A submit reply with neither a job id nor a verdict leaves nothing to poll and
// nothing to read, so it must fail over rather than be mistaken for a judgement.
func TestSubmitWithoutJobIDFailsOver(t *testing.T) {
	v := newLocalValidator(t, []string{"this"}, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte(`{}`))
	})
	if _, err := v.Validate("aGVsbG8=", fullChecks()); err == nil {
		t.Error("a submit reply with no job_id was accepted as a verdict")
	}
}
