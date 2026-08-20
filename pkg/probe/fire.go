package probe

// fire.go — submitting a question to a node and collecting the answer.
//
// The prober does not open a connection to the node. It calls its OWN isannd,
// which does the RV lookup, the hole punch and the HTTP/3 round trip:
//
//	POST http://127.0.0.1:8443/node/s:<id>/svc/<svc>/v1/jobs
//
// This is the same path `isann infer --nodes <id>` takes. Nothing about
// discovery or NAT traversal is reimplemented here.
//
// 🔴 NO SESSION TOKEN goes on these calls. The probe must arrive at the target
// as an anonymous caller, because that is what a public node's free tier
// admits and what the design says it should exercise. isannd converts a session
// token into a wallet signature on the way out (outbound.go), so leaving it on
// would quietly turn every probe into an identified request and change what is
// being measured.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/isannai/mesh/pkg/engine/manifest"
)

// Measured generation settings (llama-faucet-probe.md §6).
const (
	// probeMaxTokens is a BACKSTOP, not what makes answers short — the stop
	// sequence ends generation at the first newline, so a node that behaves
	// normally never reaches this at all. What the number decides is only what
	// happens to an answer that keeps going.
	//
	// NOT the 4-8 that "one word answers" suggests: that truncates multi-token
	// proper nouns and fails honest nodes. "Ouagadougou" and "Antananarivo" run
	// to six or seven tokens on a Llama tokeniser, and a leading space token
	// eats one more. 16 clears the longest capital name with room to spare.
	//
	// It is not the defence against a node answering with a LIST either — that
	// is the word gate in score.go (maxExtraWords), which caps a one-word draft
	// at three words no matter how many tokens were allowed.
	//
	// A shot cut off here is recorded as VerdictTruncated rather than a failure,
	// so setting this too low shows up in the log instead of quietly failing
	// honest nodes.
	probeMaxTokens = 16
	// Deterministic — the same question must not become easier or harder
	// depending on sampling luck.
	probeTemperature = 0
)

// probeStop ends generation at the first newline or the next "Q:".
var probeStop = []string{"\n", "Q:"}

// SubmitResult is what a submit attempt produced.
type SubmitResult struct {
	JobID    string
	Status   int
	Outcome  string
	QueueMax int
}

// Firer submits and collects probes through the local isannd.
type Firer struct {
	IsanndURL string
	Client    *http.Client

	mu      sync.Mutex
	schemas map[string]*manifest.RunSpec // "<nodeID>|<svc>" → run schema
}

// NewFirer builds a Firer. The client carries no timeout of its own; each call
// sets its own deadline, because a submit and a 90-second result wait are not
// the same kind of wait.
func NewFirer(isanndURL string) *Firer {
	return &Firer{
		IsanndURL: strings.TrimRight(isanndURL, "/"),
		Client:    &http.Client{},
		schemas:   map[string]*manifest.RunSpec{},
	}
}

// nodeBase is the isannd path prefix that routes to one node.
//
// The "s:" prefix names the station role. `isann infer` derives the same thing
// (nodePathPrefix); duplicated rather than exported because it is two lines and
// the CLI's copy lives in package main.
func (f *Firer) nodeBase(nodeID string) string {
	id := strings.TrimSpace(nodeID)
	if !strings.Contains(id, ":") {
		id = "s:" + id
	}
	return f.IsanndURL + "/node/" + id
}

// RunSchema fetches (and caches) the run schema for one node's service.
//
// Cached per node+service rather than per service: two nodes can run different
// versions of the same engine, and the schema is what says which parameter
// names exist. Guessing "prompt" and "max_tokens" would work until it silently
// did not.
func (f *Firer) RunSchema(nodeID, svc string) (*manifest.RunSpec, error) {
	key := nodeID + "|" + svc
	f.mu.Lock()
	if rs, ok := f.schemas[key]; ok {
		f.mu.Unlock()
		return rs, nil
	}
	f.mu.Unlock()

	endpoint := f.nodeBase(nodeID) + "/svc/" + url.PathEscape(svc) + "/run-schema?variants=1"
	body, code, err := f.get(endpoint, 15*time.Second)
	if err != nil {
		return nil, err
	}
	if code != http.StatusOK {
		return nil, fmt.Errorf("run-schema: HTTP %d", code)
	}

	// New stations answer {"default":…,"variants":[…]}; older ones a bare
	// RunSpec. "variants" never appears in a bare RunSpec, so it discriminates.
	var rs *manifest.RunSpec
	if bytes.Contains(body, []byte(`"variants"`)) {
		var set struct {
			Default *manifest.RunSpec `json:"default"`
		}
		if err := json.Unmarshal(body, &set); err != nil {
			return nil, fmt.Errorf("parse run-schema set: %w", err)
		}
		if set.Default == nil {
			return nil, fmt.Errorf("service %q has no default run schema", svc)
		}
		rs = set.Default
	} else {
		var one manifest.RunSpec
		if err := json.Unmarshal(body, &one); err != nil {
			return nil, fmt.Errorf("parse run-schema: %w", err)
		}
		rs = &one
	}

	f.mu.Lock()
	f.schemas[key] = rs
	f.mu.Unlock()
	return rs, nil
}

// buildRun turns a question into the `run` map for a submit.
//
// Parameter NAMES come from the schema, not from constants here: an engine
// declares what it accepts, and sending a key it does not know is at best
// ignored and at worst rejected. Only the values are ours.
func buildRun(rs *manifest.RunSpec, q Question) map[string]any {
	run := map[string]any{}
	has := map[string]bool{}
	for _, p := range rs.Params {
		has[p.Name] = true
	}

	// The prompt parameter is whatever the schema calls it. "prompt" is the
	// common name; fall back to the first required string parameter so an
	// engine that names it differently still gets the question.
	promptKey := ""
	switch {
	case has["prompt"]:
		promptKey = "prompt"
	default:
		for _, p := range rs.Params {
			if p.Type == "string" && p.Required {
				promptKey = p.Name
				break
			}
		}
	}
	if promptKey == "" {
		return nil // nothing to put the question in
	}
	run[promptKey] = q.BuildPrompt()

	// The rest are best-effort: set them when the engine declares them, skip
	// them otherwise. A missing max_tokens costs measurement quality, not
	// correctness, so it must not fail the shot.
	if has["max_tokens"] {
		run["max_tokens"] = probeMaxTokens
	}
	if has["temperature"] {
		run["temperature"] = probeTemperature
	}
	if has["stop"] {
		run["stop"] = probeStop
	}
	return run
}

// Submit sends one question to one node.
//
// A 429 is NOT an error: it means the node is alive and its queue is full,
// which is a different statement from "the node did not respond". Recording it
// as a failure would make a busy node look dead.
func (f *Firer) Submit(t Target, q Question) (SubmitResult, error) {
	rs, err := f.RunSchema(t.Node.ID, t.Service.Name)
	if err != nil {
		return SubmitResult{Outcome: OutcomeRefused}, err
	}
	run := buildRun(rs, q)
	if run == nil {
		return SubmitResult{Outcome: OutcomeRefused},
			fmt.Errorf("service %q declares no prompt parameter", t.Service.Name)
	}

	return f.SubmitRunProbe(t.Node.ID, t.Service.Name, run, t.Probe)
}

// SubmitRun posts an arbitrary run block to one node's service.
//
// Split out of Submit so the question GENERATOR can reach an allied node the
// same way — same path, same anonymous posture, same 429 handling. The only
// difference between asking a node a quiz question and asking an ally to write
// twenty is what goes in the run map.
func (f *Firer) SubmitRun(nodeID, svc string, run map[string]any) (SubmitResult, error) {
	return f.SubmitRunProbe(nodeID, svc, run, "")
}

// SubmitRunProbe is SubmitRun with a probe proof attached.
//
// Split rather than folded into SubmitRun because the two kinds of call are
// genuinely different: a SHOT is a prober checking a node it was assigned, and
// carries proof of that; an ALLY call (question generation, clip validation)
// is this prober asking a friend for work and proves nothing. Threading an
// empty string through the ally paths would invite someone to fill it in.
func (f *Firer) SubmitRunProbe(nodeID, svc string, run map[string]any, probe string) (SubmitResult, error) {
	payload, err := json.Marshal(map[string]any{"service": svc, "run": run})
	if err != nil {
		return SubmitResult{Outcome: OutcomeRefused}, err
	}
	endpoint := f.nodeBase(nodeID) + "/svc/" + url.PathEscape(svc) + "/v1/jobs"
	body, code, err := f.post(endpoint, payload, 30*time.Second, probe)
	if err != nil {
		return SubmitResult{Outcome: OutcomeRefused}, err
	}

	switch {
	case code == http.StatusTooManyRequests:
		return SubmitResult{Status: code, Outcome: OutcomeQueueFull}, nil
	case code != http.StatusAccepted && code != http.StatusOK:
		return SubmitResult{Status: code, Outcome: OutcomeRefused},
			fmt.Errorf("submit: HTTP %d", code)
	}

	var resp struct {
		JobID    string `json:"job_id"`
		QueueMax int    `json:"queue_max"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return SubmitResult{Status: code, Outcome: OutcomeRefused}, fmt.Errorf("parse submit reply: %w", err)
	}
	if resp.JobID == "" {
		return SubmitResult{Status: code, Outcome: OutcomeRefused}, fmt.Errorf("submit reply carries no job_id")
	}
	return SubmitResult{JobID: resp.JobID, Status: code, Outcome: OutcomeSubmitted, QueueMax: resp.QueueMax}, nil
}

// FetchResult is a collected answer.
type FetchResult struct {
	Answer           string
	PromptTokens     int
	CompletionTokens int
	Outcome          string
}

// Collect polls a job to completion and returns its answer.
//
// The deadline is the node's allowance, not a network timeout: a node running a
// 70B model legitimately needs longer than one running a 3B, and a fixed cap
// would penalise the larger machine for being larger.
func (f *Firer) Collect(nodeID, jobID string, deadline time.Duration) (FetchResult, error) {
	base := f.nodeBase(nodeID)
	stop := time.Now().Add(deadline)

	// Same backoff shape as the CLI's poll (cmd/isann/infer.go): quick at
	// first because short answers finish fast, then easing off so a slow node
	// is not polled hundreds of times.
	wait := 500 * time.Millisecond
	const maxWait = 3 * time.Second

	for {
		body, code, err := f.get(base+"/v1/jobs/"+url.PathEscape(jobID), 15*time.Second)
		if err != nil {
			return FetchResult{Outcome: OutcomeRefused}, err
		}
		if code != http.StatusOK {
			return FetchResult{Outcome: OutcomeRefused}, fmt.Errorf("job status: HTTP %d", code)
		}
		var st struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(body, &st); err != nil {
			return FetchResult{Outcome: OutcomeRefused}, fmt.Errorf("parse job status: %w", err)
		}

		switch st.Status {
		case "done":
			return f.result(base, jobID)
		case "failed":
			// The node ran and could not finish. That is the node's answer,
			// not a transport problem, so it is recorded rather than retried.
			return FetchResult{Outcome: OutcomeRefused}, fmt.Errorf("job failed: %s", st.Error)
		}

		if time.Now().After(stop) {
			return FetchResult{Outcome: OutcomeTimeout}, nil
		}
		time.Sleep(wait)
		if wait < maxWait {
			wait *= 2
		}
	}
}

// result reads the finished body and pulls out the answer and token counts.
func (f *Firer) result(base, jobID string) (FetchResult, error) {
	body, code, err := f.get(base+"/v1/jobs/"+url.PathEscape(jobID)+"/result", 60*time.Second)
	if err != nil {
		return FetchResult{Outcome: OutcomeRefused}, err
	}
	if code != http.StatusOK {
		return FetchResult{Outcome: OutcomeRefused}, fmt.Errorf("job result: HTTP %d", code)
	}

	// Engines differ in where the text sits, so try the OpenAI shapes and fall
	// back to the raw body. Scoring is a later milestone and reads answer_raw,
	// so storing something imperfect beats storing nothing.
	var oa struct {
		Choices []struct {
			Text    string `json:"text"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	res := FetchResult{Outcome: OutcomeAnswered}
	if err := json.Unmarshal(body, &oa); err == nil {
		res.PromptTokens = oa.Usage.PromptTokens
		res.CompletionTokens = oa.Usage.CompletionTokens
		if len(oa.Choices) > 0 {
			if s := strings.TrimSpace(oa.Choices[0].Text); s != "" {
				res.Answer = s
			} else {
				res.Answer = strings.TrimSpace(oa.Choices[0].Message.Content)
			}
		}
	}
	if res.Answer == "" {
		res.Answer = strings.TrimSpace(string(body))
	}
	if len(res.Answer) > answerLimit {
		res.Answer = res.Answer[:answerLimit]
	}
	return res, nil
}

// answerLimit caps what is stored. Scoring reads the first few tokens; keeping
// a little more helps a human see what went wrong, and keeping a whole runaway
// generation helps nobody.
const answerLimit = 512

// imageLimit bounds a collected image.
//
// 🔴 An image CANNOT go through Collect. That path truncates at answerLimit
// (512 bytes) because a text answer longer than that is a runaway generation —
// but a 512x512 PNG is half a megabyte of base64, so the same rule would return
// a corrupted stub and every image probe would fail for a reason nobody could
// see.
//
// A limit is still needed, just a different one: the reply comes from a node
// that may be hostile, and "however many bytes it feels like sending" is not an
// acceptable allocation. 8MB leaves an order of magnitude over a 512² PNG.
const imageLimit = 8 << 20

// errJobPending says the node accepted the job and has not finished it. It is
// not a failure and the caller must not record one: the shot stays open and the
// next round asks again.
var errJobPending = fmt.Errorf("job not finished yet")

// PeekImage asks ONCE whether an image job is done, and returns the picture as
// base64 if it is.
//
// 🔴 IT DOES NOT WAIT. Waiting here is what made a busy node look like a broken
// one: the round blocked for the whole deadline, gave up, recorded a failure,
// and the node finished the picture seconds later with nobody to hand it to. A
// 4GB card spent hours in that loop — every shot a timeout, every picture drawn
// correctly, every one discarded. Now the round moves on and the NEXT one picks
// the answer up, so the only thing a slow node costs us is patience.
//
// errJobPending means "ask again later" and is the ordinary case, not an error
// worth logging.
//
// The base64 string goes to the validator undecoded: the engine produces base64
// and CLIP accepts base64, so decoding here would only be a chance to corrupt it.
func (f *Firer) PeekImage(nodeID, jobID string) (string, error) {
	base := f.nodeBase(nodeID)
	body, code, err := f.get(base+"/v1/jobs/"+url.PathEscape(jobID), 15*time.Second)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("job status: HTTP %d", code)
	}
	var st struct {
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &st); err != nil {
		return "", fmt.Errorf("parse job status: %w", err)
	}
	switch st.Status {
	case "done":
		return f.imageResult(base, jobID)
	case "failed":
		return "", fmt.Errorf("job failed: %s", st.Error)
	}
	return "", errJobPending
}

// QueueStats is the station's own view of one service's queue.
//
// pending and running are kept apart deliberately. A job we add now waits behind
// both, but they answer different questions: `pending 0 / running 1` is a node
// mid-picture that will free up, while `pending 1 / running 0` has not even
// started the one in front of us. The combined queue_depth other endpoints
// report cannot tell those apart.
type QueueStats struct {
	Pending          int     `json:"pending"`
	Running          int     `json:"running"`
	EstimatedWaitSec int     `json:"estimated_wait_sec"`
	AvgJobSec        float64 `json:"avg_job_sec"`
}

// Idle reports whether a shot fired now would start immediately.
func (q QueueStats) Idle() bool { return q.Pending == 0 && q.Running == 0 }

// QueueStats reads a node's live queue over the same P2P path everything else
// uses. isannd's isInferencePeerPath already covers /v1/queue, so the request
// carries the caller signature without any special handling here.
//
// 🔴 The RV's /v1/metrics carries the same numbers and must NOT be used for
// this. It is a heartbeat snapshot, and one SD picture takes longer than the
// heartbeat interval — by the time a stale gauge says "idle" the queue has
// turned over completely. A gauge that is wrong exactly when it matters is
// worse than no gauge, because it reads as certainty.
func (f *Firer) QueueStats(nodeID, service string) (QueueStats, error) {
	endpoint := f.nodeBase(nodeID) + "/v1/queue/stats?service=" + url.QueryEscape(service)
	body, code, err := f.get(endpoint, 15*time.Second)
	if err != nil {
		return QueueStats{}, err
	}
	if code != http.StatusOK {
		return QueueStats{}, fmt.Errorf("queue stats: HTTP %d", code)
	}
	var qs QueueStats
	if err := json.Unmarshal(body, &qs); err != nil {
		return QueueStats{}, fmt.Errorf("parse queue stats: %w", err)
	}
	return qs, nil
}

// AwaitJSON polls a job to completion and returns its result body verbatim.
//
// 🔴 SUBMITTING IS NOT ANSWERING. `POST /v1/jobs` replies 202 with
// `{"job_id":…}` — even with `?timeout=`, which the station does not honour on
// this route. Reading that reply as the answer is how the validator call failed
// with "verdict carries no checks": CLIP had judged the picture correctly and
// the prober was parsing the submission receipt.
//
// Verbatim because the caller wants the engine's own JSON. Collect() exists for
// the text path and pulls a completion string out of the OpenAI shape, which
// would discard a judgement wholesale.
//
// jobsBase is the prefix up to and including `/v1/jobs` — the local infer proxy
// and the node bridge expose it at different depths, and both are used.
func (f *Firer) AwaitJSON(jobsBase, jobID string, deadline time.Duration) ([]byte, error) {
	stop := time.Now().Add(deadline)
	wait := 500 * time.Millisecond
	const maxWait = 3 * time.Second

	for {
		body, code, err := f.get(jobsBase+"/"+url.PathEscape(jobID), 15*time.Second)
		if err != nil {
			return nil, err
		}
		if code != http.StatusOK {
			return nil, fmt.Errorf("job status: HTTP %d", code)
		}
		var st struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(body, &st); err != nil {
			return nil, fmt.Errorf("parse job status: %w", err)
		}
		switch st.Status {
		case "done":
			out, code, err := f.getLarge(jobsBase+"/"+url.PathEscape(jobID)+"/result", 60*time.Second)
			if err != nil {
				return nil, err
			}
			if code != http.StatusOK {
				return nil, fmt.Errorf("job result: HTTP %d", code)
			}
			return out, nil
		case "failed":
			return nil, fmt.Errorf("job failed: %s", st.Error)
		}
		if time.Now().After(stop) {
			return nil, fmt.Errorf("job did not finish in time")
		}
		time.Sleep(wait)
		if wait < maxWait {
			wait *= 2
		}
	}
}

// imageResult reads the finished body and returns the image as base64.
//
// 🔴 THE STATION SERVES DECODED BYTES, NOT JSON. `/v1/jobs/<id>/result` answers
// `Content-Type: image/png` with the PNG itself:
//
//	89 50 4e 47 0d 0a 1a 0a  …
//
// The manifest's `result.content_path: data[0].b64_json` describes how the
// STATION parses the ENGINE's reply — it is not the shape the station passes on.
// Reading it as the client contract made every collected image fail with
// "reply carries no image" 87 seconds after a picture had been drawn correctly.
//
// Both shapes are accepted anyway. The station is one hop, and a node reached
// another way (or a future engine whose station forwards the JSON verbatim) can
// still answer in the OpenAI shape.
func (f *Firer) imageResult(base, jobID string) (string, error) {
	body, code, ctype, err := f.getLargeTyped(base+"/v1/jobs/"+url.PathEscape(jobID)+"/result", 120*time.Second)
	if err != nil {
		return "", err
	}
	if code != http.StatusOK {
		return "", fmt.Errorf("job result: HTTP %d", code)
	}

	// Raw image bytes — the normal path. Decided on Content-Type rather than by
	// sniffing the first bytes, so a truncated or corrupt image reports as
	// exactly that instead of silently falling through to "no image".
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(ctype)), "image/") {
		if len(body) == 0 {
			return "", fmt.Errorf("job result: %s with an empty body", ctype)
		}
		return base64.StdEncoding.EncodeToString(body), nil
	}

	// The OpenAI images shape.
	var oa struct {
		Data []struct {
			B64 string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &oa); err == nil && len(oa.Data) > 0 {
		if s := strings.TrimSpace(oa.Data[0].B64); s != "" {
			return s, nil
		}
	}
	// No image where one was promised is the node's answer, not a parse
	// problem to paper over — it is recorded as a failed shot. The content type
	// goes in the message: without it this said nothing about what DID arrive,
	// which is the whole question when a picture was drawn and then lost.
	return "", fmt.Errorf("reply carries no image (content-type %q, %d bytes)", ctype, len(body))
}

// getLarge is get with the image body limit.
func (f *Firer) getLarge(endpoint string, timeout time.Duration) ([]byte, int, error) {
	body, code, _, err := f.getLargeTyped(endpoint, timeout)
	return body, code, err
}

// getLargeTyped is getLarge plus the Content-Type header.
//
// The type is the only thing that distinguishes a delivered image from a JSON
// envelope carrying one, and the two arrive on the same endpoint from different
// hops. Guessing from the bytes would mean a corrupt image reads as "not an
// image" rather than as a corrupt one.
func (f *Firer) getLargeTyped(endpoint string, timeout time.Duration) ([]byte, int, string, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, "", err
	}
	c := *f.Client
	c.Timeout = timeout
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, "", err
	}
	defer resp.Body.Close()
	ctype := resp.Header.Get("Content-Type")
	body, err := io.ReadAll(io.LimitReader(resp.Body, imageLimit))
	if err != nil {
		return nil, resp.StatusCode, ctype, err
	}
	return body, resp.StatusCode, ctype, nil
}

// get / post issue anonymous requests through isannd. No session header — see
// the file comment.
func (f *Firer) get(endpoint string, timeout time.Duration) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	return f.do(req, timeout)
}

// post sends a job. probe, when non-empty, carries the prober's proof.
//
// 🔴 The proof is a HEADER and the body is untouched, so the request the node
// sees is byte-identical to one from any other caller. A probe that changed the
// payload would stop measuring what an ordinary user experiences.
func (f *Firer) post(endpoint string, payload []byte, timeout time.Duration, probe string) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if probe != "" {
		req.Header.Set(ProbeHeader, probe)
	}
	return f.do(req, timeout)
}

func (f *Firer) do(req *http.Request, timeout time.Duration) ([]byte, int, error) {
	c := *f.Client
	c.Timeout = timeout
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}
