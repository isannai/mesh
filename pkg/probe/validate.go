package probe

// validate.go — asking a CLIP validator whether an image matches what was
// ordered.
//
// Same shape as the question writers: the prober does not run CLIP itself, it
// names allied validators in the config and takes them in round-robin. The
// trust requirement is a notch higher here though — a writer only LEARNS the
// questions, while a judge decides outright whether a node gets paid.
//
// 🔴 WHAT IS AND IS NOT A FAILOVER
//
//	transport error · 5xx · error in the body · no `pass` field   → next validator
//	a clean verdict of pass:false                                 → NOT a failure
//
// The second one is the whole reason this file is separate from the generator's
// error handling. `pass:false` is an ANSWER. Treating it as a failure would
// make a list of validators into "keep asking until one says yes", so adding
// judges would raise the pass rate — precisely backwards. One image gets one
// verdict from one validator; the round-robin spreads images across validators,
// it never re-asks about the same image.
//
// WHY THERE IS NO HOLDING QUEUE
//
// If every validator is down the image track simply does not run that round.
// A ticket is signed at fire time, so a verdict that arrives hours later has no
// ticket left to affect — and other probers are firing at the same nodes
// anyway, so the redundancy already exists one layer up. Scoring later would
// mean a second visit to deliver a replacement ticket, which is not worth it.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Check is one element of an image judgement: the phrasing that should win,
// and the confusable alternatives it must beat.
//
// Alternatives come from the SAME slot as the expected value, so they are
// genuinely confusable by construction. Feeding easy wrong answers makes even a
// mismatched image pass at 99.9%, which reads as accuracy and is not.
type Check struct {
	Label        string   `json:"label"`
	Expect       string   `json:"expect"`
	Alternatives []string `json:"alternatives"`
}

// Judgement is one validator's verdict on one image.
type Judgement struct {
	// Pass is the validator's own top-level AND across every check. The prober
	// does not use it directly — see RequiredPass.
	Pass   bool `json:"pass"`
	Checks []struct {
		Label      string  `json:"label"`
		Confidence float64 `json:"confidence"`
		Pass       bool    `json:"pass"`
	} `json:"checks"`
	Reason string `json:"reason"`
	// Model and Version pin the judging criteria. Two validators can only be
	// compared when both match, so they are stored with the verdict rather than
	// assumed constant.
	Model   string `json:"model"`
	Version string `json:"version"`
	// Validator is the pool entry that answered. Filled in by Validate.
	Validator string `json:"-"`
}

// requiredLabels are the checks that must pass for an image to count.
//
// subject and background decide. Composition is drawn, ordered and recorded,
// but no longer gates a ticket.
//
// 🔴 background REPLACED the old subject-colour check, and the difference is
// where the paint goes. "an amber crocodile" measured 66% on correct images:
// SD refuses an unnatural subject colour about as often as it obeys, and when
// it obeys it bleeds the colour into the background anyway. "on an amber
// background" has neither problem — a flat backdrop is the easiest thing in a
// prompt to satisfy, it fills most of the frame, and CLIP reads it far more
// reliably than a place the subject stands in front of and half occludes.
//
// ⚠️ THE CLIP ENGINE MUST DECLARE THE PARAM. The station maps run params
// through the manifest's body template and DROPS anything undeclared, without
// an error (see clipRun). If `background`, `background-alt` and
// `background-alt2` are not in the clip manifest, this check does not happen
// and every image passes on subject alone — silently. Verify on a real verdict
// that a `background` entry comes back in `checks` before trusting it.
//
// 🔴 STYLE AND COMPOSITION WERE REQUIRED AND ARE NOT ANY MORE. Both measured
// well in calibration, but calibration ran against images a capable model had
// produced. In the field an honest node with a plain checkpoint draws the right
// subject and simply does not render the asked-for art style or framing, and
// whole shots were failing on that alone — style at 0.03, composition at 0.06
// on images whose subject was correct.
//
// Style is gone from the order entirely. Composition is still asked for in the
// prompt (always a front view now, never the "aerial view" a plain checkpoint
// will not produce) and still recorded, but no longer scored.
//
// ⚠️ WHAT THIS COSTS. Subject is the only axis left, so an image probe now
// asks one question instead of three. Subject is drawn from 535 values and
// changes every shot, which is what keeps a cached picture from answering it —
// but a node that pre-generated one image per subject would pass, and nothing
// here would notice. The check is a deterrent now, not a proof.
//
// 🔴 COLOUR CANNOT TAKE THE EMPTY SLOT, AND THE REASON IS NOT TASTE. The clip
// manifest declares flat subject/style/composition params and nothing else, so
// a colour check is DROPPED on the way out (see clipRun) — the verdict comes
// back without it and the image passes on subject alone. Silently. Promoting
// colour here would weaken the check while looking like it tightened it.
// Declaring the params on the clip engine is the prerequisite, not this line.
var requiredLabels = map[string]bool{"subject": true, "background": true}

// RequiredPass reports whether the checks that matter passed.
//
// The validator's own top-level `pass` is an AND over EVERY check, so one
// optional element failing would sink an otherwise correct image. The
// required/optional split lives here rather than in the validator so that the
// validator stays a plain judge with no policy in it.
func (j Judgement) RequiredPass() bool {
	seen := 0
	for _, c := range j.Checks {
		if !requiredLabels[c.Label] {
			continue
		}
		seen++
		if !c.Pass {
			return false
		}
	}
	// No required check came back at all: that is a malformed verdict, not a
	// pass. Returning true here would let a validator that answers `{}` mint
	// tickets for everything.
	return seen > 0
}

// Validator judges images through allied CLIP nodes.
type Validator struct {
	pool  *pool
	firer *Firer
	// Deadline is how long one judgement may take. CLIP is ~90ms of compute,
	// so anything longer is queueing at the ally.
	Deadline time.Duration
}

// NewValidator builds a Validator over the configured clip entries. No entries
// means the image track does not run.
func NewValidator(isanndURL string, entries []poolEntry) *Validator {
	return &Validator{
		pool:     newPool(entries),
		firer:    NewFirer(strings.TrimRight(isanndURL, "/")),
		Deadline: 60 * time.Second,
	}
}

// Enabled reports whether any validator is configured at all.
func (v *Validator) Enabled() bool { return v != nil && !v.pool.Empty() }

// Judges describes the configured pool, for the boot log.
func (v *Validator) Judges() []string {
	if v == nil || v.pool == nil {
		return nil
	}
	return describePool(v.pool.entries)
}

// Validate judges one image against one set of checks.
//
// imageB64 is the downsampled image, base64. Not a URL: a validator that
// fetches arbitrary URLs is an SSRF hole, and the prober already holds the
// bytes.
func (v *Validator) Validate(imageB64 string, checks []Check) (Judgement, error) {
	if !v.Enabled() {
		return Judgement{}, errNoPoolEntries
	}
	if len(checks) == 0 {
		return Judgement{}, fmt.Errorf("validate: no checks")
	}

	var out Judgement
	used, err := v.pool.Take(time.Now(), func(e poolEntry) error {
		j, err := v.askOne(e, imageB64, checks)
		if err != nil {
			return err
		}
		out = j
		return nil
	})
	if err != nil {
		return Judgement{}, err
	}
	out.Validator = used.String()
	return out, nil
}

// clipRun builds the run block in the shape the clip manifest declares.
//
// 🔴 THESE ARE MANIFEST PARAM NAMES, NOT THE ENGINE BODY. The station maps run
// params through the manifest's body template — `${image}` becomes
// `image_base64`, and the three checks are ASSEMBLED there from flat
// subject/style/composition params. A run block written in the engine's own
// shape (`image_base64`, `checks`) has no declared param to match, so
// BuildBody drops every field and the validator receives an empty image with
// empty expectations. Nothing errors on the way; it just cannot judge.
//
// Only the required labels travel. Composition is recorded for humans but never
// decides a verdict (see slots.go), so sending it would ask the validator to
// judge something nobody reads.
func clipRun(imageB64 string, checks []Check) (map[string]any, error) {
	run := map[string]any{
		"image": imageB64,
		// margin 0 means "the expected phrasing only has to come first". The
		// contrast is what discriminates, not the size of the lead.
		"margin": 0.0,
	}
	// Each label maps to <label>, <label>-alt, <label>-alt2.
	for _, c := range checks {
		if !requiredLabels[c.Label] {
			continue
		}
		run[c.Label] = c.Expect
		for i, alt := range c.Alternatives {
			switch i {
			case 0:
				run[c.Label+"-alt"] = alt
			case 1:
				run[c.Label+"-alt2"] = alt
			}
		}
	}
	// A required label missing here means the order was built wrong, and the
	// validator would answer on the ones it did get — a verdict that looks
	// complete while an axis was never examined.
	for label := range requiredLabels {
		if _, ok := run[label]; !ok {
			return nil, fmt.Errorf("validate: order carries no %s check", label)
		}
	}
	return run, nil
}

// askOne puts the question to one validator.
//
// 🔴 The error return is reserved for "this validator did not judge". A verdict
// of pass:false is returned with err == nil, so the pool never fails over on
// it — see the file comment.
func (v *Validator) askOne(e poolEntry, imageB64 string, checks []Check) (Judgement, error) {
	run, err := clipRun(imageB64, checks)
	if err != nil {
		return Judgement{}, err
	}

	var body []byte
	if e.IsSelf() {
		body, err = v.askLocal(e.Service, run)
	} else {
		body, err = v.askRemote(e, run)
	}
	if err != nil {
		return Judgement{}, err
	}

	// An `error` field means the validator itself refused — a bad image, a
	// malformed check list. That is a failure of this validator, so the caller
	// moves on rather than recording a verdict nobody made.
	var errBody struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &errBody) == nil && errBody.Error != "" {
		return Judgement{}, fmt.Errorf("validate via %s: %s", e, errBody.Error)
	}

	var j Judgement
	if err := json.Unmarshal(body, &j); err != nil {
		return Judgement{}, fmt.Errorf("validate via %s: parse verdict: %w", e, err)
	}
	// A body with no checks is not a verdict. Distinguishing this from an
	// honest fail is what stops a broken validator from silently failing every
	// node it is asked about.
	if len(j.Checks) == 0 {
		return Judgement{}, fmt.Errorf("validate via %s: verdict carries no checks", e)
	}
	return j, nil
}

// askLocal calls a validator on this node, blocking.
// 🔴 SUBMIT THEN POLL, even locally. This used to POST with `?timeout=120` and
// return the reply, on the assumption that the route blocks until the job
// finishes. It does not — the station answers 202 with `{"job_id":…}`
// immediately. The prober was parsing a submission receipt as a verdict, which
// surfaced as "verdict carries no checks" while CLIP was judging every picture
// correctly and its answer was simply never read.
func (v *Validator) askLocal(service string, run map[string]any) ([]byte, error) {
	payload, err := json.Marshal(map[string]any{"service": service, "run": run})
	if err != nil {
		return nil, err
	}
	jobsBase := v.firer.IsanndURL + "/internal/api/infer/svc/" + url.PathEscape(service) + "/v1/jobs"
	body, code, err := v.firer.post(jobsBase, payload, v.Deadline, "") // ally call, not a shot: nothing to prove
	if err != nil {
		return nil, fmt.Errorf("validate: %w", err)
	}
	if code != 200 && code != 202 {
		return nil, fmt.Errorf("validate: HTTP %d", code)
	}
	var sub struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(body, &sub); err != nil || sub.JobID == "" {
		// No job id and no checks either — nothing to poll and nothing to read.
		return nil, fmt.Errorf("validate: submit reply carries no job_id")
	}
	out, err := v.firer.AwaitJSON(jobsBase, sub.JobID, v.Deadline)
	if err != nil {
		return nil, fmt.Errorf("validate: %w", err)
	}
	return out, nil
}

// askRemote calls an allied validator, submit then poll — the same path a probe
// shot takes.
func (v *Validator) askRemote(e poolEntry, run map[string]any) ([]byte, error) {
	res, err := v.firer.SubmitRun(e.Node, e.Service, run)
	if err != nil {
		return nil, fmt.Errorf("validate via %s: %w", e, err)
	}
	// A full queue is not "this judge is broken", but it is still a reason to
	// hand the image to another one instead of waiting on a busy ally. CLIP
	// runs at concurrency 1 on a standard install, so this happens under burst.
	if res.Outcome != OutcomeSubmitted {
		return nil, fmt.Errorf("validate via %s: %s", e, res.Outcome)
	}
	// AwaitJSON, not Collect: Collect pulls a completion STRING out of the
	// OpenAI chat shape for the text track, which would throw a judgement away.
	// A verdict is a JSON document and has to arrive as one.
	out, err := v.firer.AwaitJSON(v.firer.nodeBase(e.Node)+"/v1/jobs", res.JobID, v.Deadline)
	if err != nil {
		return nil, fmt.Errorf("validate via %s: %w", e, err)
	}
	return out, nil
}
