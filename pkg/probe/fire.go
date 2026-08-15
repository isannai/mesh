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
	// probeMaxTokens is 64, NOT the 4-8 that "one word answers" suggests.
	// Truncating at 4 tokens cuts multi-token proper nouns ("Phys|ic|ist") and
	// fails honest nodes; the short answer comes from the stop sequence
	// instead. It also gives tokens-per-second something to measure.
	probeMaxTokens = 64
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

	payload, err := json.Marshal(map[string]any{"service": t.Service.Name, "run": run})
	if err != nil {
		return SubmitResult{Outcome: OutcomeRefused}, err
	}
	endpoint := f.nodeBase(t.Node.ID) + "/svc/" + url.PathEscape(t.Service.Name) + "/v1/jobs"
	body, code, err := f.post(endpoint, payload, 30*time.Second)
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

// get / post issue anonymous requests through isannd. No session header — see
// the file comment.
func (f *Firer) get(endpoint string, timeout time.Duration) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, 0, err
	}
	return f.do(req, timeout)
}

func (f *Firer) post(endpoint string, payload []byte, timeout time.Duration) ([]byte, int, error) {
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
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
