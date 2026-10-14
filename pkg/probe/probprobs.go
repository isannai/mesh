package probe

// probprobs.go: asking a node for an answer WITH its per-token candidates.
//
// The reference check (refcheck.go) compares a node's answer with the reference
// nodes' answer token by token: the text of each token and the probabilities of
// the candidates the engine considered at that position (workspace
// docs/todo/probe.md 「응답 읽기」). The quiz path (fire.go) reads only the text.
//
// The request goes to the llama.cpp run variant on /completion that the engine
// manifest declares for this (GLink package-stage .../engines/llama/manifest.json,
// 2026-10-14): a raw prompt, n_predict, temperature 0, n_probs and stop. The
// station returns the engine's body untouched for a job that asks for n_probs
// (mesh pkg/station/queue bufferedJob). A node whose manifest predates the
// variant has no way to answer, and its check fails as no_probs.
//
// Engines spell the candidates differently, and llama.cpp changed its own format
// over time, so every known spelling is read:
//
//	llama.cpp now      completion_probabilities[i] = {token, logprob, top_logprobs[{token, logprob}]}
//	llama.cpp sampled  completion_probabilities[i] = {token, prob, top_probs[{token, prob}]}
//	llama.cpp before   completion_probabilities[i] = {content, probs[{tok_str, prob}]}
//	OpenAI             choices[0].logprobs.content[i] = {token, logprob, top_logprobs[...]}
//
// A log probability is turned into a probability (exp) so every spelling compares
// on one scale.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/isannai/mesh/pkg/engine/manifest"
)

// The request of a reference check (workspace docs/todo/probe.md 「요청」).
const (
	refPath    = "/completion"
	refPredict = 8
	refProbs   = 5
)

var refStop = []string{"."}

// errNoProbeRun: the node's engine declares no run that returns candidates.
var errNoProbeRun = errors.New("the service declares no /completion run with n-probs")

// TokenProbs is one generated token and the candidates at its position.
type TokenProbs struct {
	Text  string             `json:"t"`
	Cands map[string]float64 `json:"c"` // candidate text -> probability
}

// ProbAnswer is an answer with its candidates, up to where the answer ends.
type ProbAnswer struct {
	Text   string       `json:"text"`
	Tokens []TokenProbs `json:"tokens"`
}

// ProbeRun finds (and caches) the node service's run that returns candidates.
// nil with no error when the service has none.
func (f *Firer) ProbeRun(nodeID, svc string) (*manifest.RunSpec, error) {
	key := nodeID + "|" + svc
	f.mu.Lock()
	if f.probeRuns == nil {
		f.probeRuns = map[string]*manifest.RunSpec{}
	}
	if rs, ok := f.probeRuns[key]; ok {
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
	var set struct {
		Default  *manifest.RunSpec  `json:"default"`
		Variants []manifest.RunSpec `json:"variants"`
	}
	var found *manifest.RunSpec
	if bytes.Contains(body, []byte(`"variants"`)) && json.Unmarshal(body, &set) == nil {
		for i := range set.Variants {
			if set.Variants[i].Path == refPath && declares(&set.Variants[i], "n-probs") {
				found = &set.Variants[i]
				break
			}
		}
	}
	f.mu.Lock()
	f.probeRuns[key] = found
	f.mu.Unlock()
	return found, nil
}

func declares(rs *manifest.RunSpec, name string) bool {
	for _, p := range rs.Params {
		if p.Name == name {
			return true
		}
	}
	return false
}

// SubmitProbs asks one node for an answer with its candidates.
func (f *Firer) SubmitProbs(t Target, prompt string) (SubmitResult, error) {
	rs, err := f.ProbeRun(t.Node.ID, t.Service.Name)
	if err != nil {
		return SubmitResult{Outcome: OutcomeRefused}, err
	}
	if rs == nil {
		return SubmitResult{Outcome: OutcomeRefused}, errNoProbeRun
	}
	run := map[string]any{"prompt": prompt}
	set := func(name string, v any) {
		if declares(rs, name) {
			run[name] = v
		}
	}
	set("n-predict", refPredict)
	set("temperature", 0)
	set("n-probs", refProbs)
	set("stop", refStop)
	return f.submitJob(t.Node.ID, t.Service.Name, rs.Path, run, t.Probe)
}

// CollectProbs waits for a job and reads its answer with candidates. outcome is
// OutcomeAnswered when the job finished; the answer may still carry no tokens
// (an engine that ignored n_probs).
func (f *Firer) CollectProbs(nodeID, jobID string, deadline time.Duration) (ProbAnswer, string, error) {
	base := f.nodeBase(nodeID)
	outcome, err := f.await(base, jobID, deadline)
	if outcome != OutcomeAnswered {
		return ProbAnswer{}, outcome, err
	}
	body, code, err := f.get(base+"/v1/jobs/"+url.PathEscape(jobID)+"/result", 60*time.Second)
	if err != nil {
		return ProbAnswer{}, OutcomeRefused, err
	}
	if code != http.StatusOK {
		return ProbAnswer{}, OutcomeRefused, fmt.Errorf("job result: HTTP %d", code)
	}
	a, err := parseTokenProbs(body)
	if err != nil {
		return ProbAnswer{}, OutcomeRefused, err
	}
	return a, OutcomeAnswered, nil
}

// rawCand / rawTok cover every spelling in the file comment.
type rawCand struct {
	Token   *string  `json:"token"`
	TokStr  *string  `json:"tok_str"`
	Prob    *float64 `json:"prob"`
	Logprob *float64 `json:"logprob"`
}

type rawTok struct {
	rawCand
	Content     *string   `json:"content"` // the token text, in the old llama.cpp spelling
	TopLogprobs []rawCand `json:"top_logprobs"`
	TopProbs    []rawCand `json:"top_probs"`
	Probs       []rawCand `json:"probs"`
}

func (c rawCand) text() (string, bool) {
	switch {
	case c.Token != nil:
		return *c.Token, true
	case c.TokStr != nil:
		return *c.TokStr, true
	}
	return "", false
}

func (c rawCand) prob() (float64, bool) {
	switch {
	case c.Prob != nil:
		return *c.Prob, true
	case c.Logprob != nil:
		return math.Exp(*c.Logprob), true
	}
	return 0, false
}

// parseTokenProbs reads an engine answer into text and per-token candidates.
func parseTokenProbs(body []byte) (ProbAnswer, error) {
	var native struct {
		Content *string  `json:"content"`
		Probs   []rawTok `json:"completion_probabilities"`
	}
	var oai struct {
		Choices []struct {
			Text    string `json:"text"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Logprobs struct {
				Content []rawTok `json:"content"`
			} `json:"logprobs"`
		} `json:"choices"`
	}
	var (
		text string
		toks []rawTok
	)
	switch {
	case json.Unmarshal(body, &native) == nil && native.Content != nil:
		text, toks = *native.Content, native.Probs
	case json.Unmarshal(body, &oai) == nil && len(oai.Choices) > 0:
		c := oai.Choices[0]
		text = c.Text
		if text == "" {
			text = c.Message.Content
		}
		toks = c.Logprobs.Content
	default:
		return ProbAnswer{}, fmt.Errorf("answer is neither a llama.cpp nor an OpenAI completion")
	}

	out := ProbAnswer{Text: text}
	acc := ""
	for _, rt := range toks {
		tok, ok := rt.text()
		if !ok && rt.Content != nil {
			tok, ok = *rt.Content, true
		}
		if !ok || tok == "" {
			continue // the end-of-text token carries no text
		}
		// 🔴 Only the tokens that make up the answer count (probe.md 「판정」): a
		// stop token and anything after the text ends are not the node's answer.
		if !strings.HasPrefix(text, acc+tok) {
			break
		}
		acc += tok
		cands := map[string]float64{}
		list := rt.TopLogprobs
		if len(list) == 0 {
			list = rt.TopProbs
		}
		if len(list) == 0 {
			list = rt.Probs
		}
		for _, c := range list {
			ct, okT := c.text()
			cp, okP := c.prob()
			if okT && okP {
				cands[ct] = cp
			}
		}
		if len(cands) == 0 {
			if p, okP := rt.prob(); okP {
				cands[tok] = p
			}
		}
		out.Tokens = append(out.Tokens, TokenProbs{Text: tok, Cands: cands})
		if acc == text {
			break
		}
	}
	return out, nil
}
