package probe

// generate.go — refilling the question queue from allied nodes.
//
// math is produced in code; the other three categories are written by a model.
// That asymmetry is on purpose: a program can be certain about arithmetic and
// cannot be certain about geography, so the model's output is treated as a
// DRAFT answer and never as ground truth.
//
// WHERE THE MODEL RUNS
//
// Not necessarily here. A prober is a small machine whose job is to ask
// questions, and requiring it to also host a 14B model would exclude most of
// the machines that could do the asking. So writers are named in the config and
// tried in round-robin; "this" is one possible entry, not the assumption.
//
// 🔴 A writer learns every question before it is asked. That is why these are
// ALLIED nodes — ones the operator runs or trusts — and not any public node off
// the directory. The floor if a writer is compromised is arithmetic: it is
// generated here, fresh per shot, and cannot leak in advance.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// batchTarget is how many arithmetic questions one refill produces. The
// model-written categories use genBatchLines instead — they lose two lines to
// the few-shot pair, which code-generated math does not.
const batchTarget = 20

// Generator refills the queue, using whichever allied node answers.
type Generator struct {
	IsanndURL string
	Client    *http.Client

	// pool is the round-robin over configured writers. A prober is a small
	// machine and usually hosts no model at all, so this normally names an
	// allied node rather than "this".
	pool *pool
	// firer reaches remote entries. Same path and same anonymous posture as a
	// probe shot — the only difference is what is in the run block.
	firer *Firer
}

// NewGenerator builds a Generator over the configured writer entries. No
// entries ⇒ only arithmetic is produced, which needs no engine at all.
func NewGenerator(isanndURL string, entries []poolEntry) *Generator {
	base := strings.TrimRight(isanndURL, "/")
	return &Generator{
		IsanndURL: base,
		Client:    &http.Client{Timeout: 3 * time.Minute},
		pool:      newPool(entries),
		firer:     NewFirer(base),
	}
}

// FillCode returns n freshly generated questions of a code-written category.
//
// Duplicates are not filtered here — the store drops them on insert, and
// filtering twice would only hide how often the generator repeats itself.
func FillCode(spec codeSpec, n int, rng *rand.Rand) []Question {
	out := make([]Question, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, spec.gen(rng))
	}
	return out
}

// FillMath returns n freshly generated arithmetic questions.
func FillMath(n int, rng *rand.Rand) []Question {
	return FillCode(codeSpecs[0], n, rng)
}

// HasEngine reports whether LLM-backed categories can be produced at all.
func (g *Generator) HasEngine() bool { return !g.pool.Empty() }

// Writers describes the configured pool, for log lines.
func (g *Generator) Writers() []string {
	if g.pool == nil {
		return nil
	}
	return describePool(g.pool.entries)
}

// FillCategory asks the local model for a batch of one category.
//
// The first two usable lines become the few-shot pair and are dropped as
// questions — their answers are already shown in the prompt, so asking them
// would test copying rather than knowledge. See parseBatch.
func (g *Generator) FillCategory(cat Category) ([]Question, error) {
	if !g.HasEngine() {
		return nil, fmt.Errorf("no generator service configured")
	}
	var spec *genSpec
	for i := range genSpecs {
		if genSpecs[i].cat == cat {
			spec = &genSpecs[i]
			break
		}
	}
	if spec == nil {
		return nil, fmt.Errorf("category %q is not model-generated", cat)
	}

	// One pass over the pool. An entry that cannot produce a usable batch is a
	// failure like any other — the node answered, but with nothing we can ask —
	// so it hands over to the next writer and goes into cooldown.
	var qs []Question
	used, err := g.pool.Take(time.Now(), func(e poolEntry) error {
		batch, err := g.fillFrom(e, cat, spec.topic)
		if err != nil {
			return err
		}
		qs = batch
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", cat, err)
	}
	log.Printf("[probe] %d %s questions from %s", len(qs), cat, used)
	return qs, nil
}

// fillFrom asks one writer for a batch.
func (g *Generator) fillFrom(e poolEntry, cat Category, topic string) ([]Question, error) {
	raw, err := g.complete(e, genSystem, topic)
	if err != nil {
		return nil, err
	}
	qs := parseBatch(cat, raw)
	if len(qs) == 0 {
		// The output contract may simply never have arrived. `system` is an
		// OPTIONAL manifest parameter — llama declares it, another engine need
		// not, and a station drops keys its schema does not know. The model
		// would then see the bare topic line and answer it in prose, which
		// parses to nothing.
		//
		// So retry once with the contract folded into the prompt. That is the
		// lowest-common-denominator form every text engine accepts, and it
		// costs one extra call on a batch that was already lost. It matters
		// more now that writers are remote: we do not control what engine an
		// ally runs.
		raw, err = g.complete(e, "", genSystem+"\n\n"+topic)
		if err != nil {
			return nil, err
		}
		qs = parseBatch(cat, raw)
	}
	if len(qs) == 0 {
		return nil, fmt.Errorf("batch from %s had no usable lines", e)
	}
	return qs, nil
}

// genRun is the run block for one generation call.
//
// An empty system is omitted rather than sent blank, so the retry path in
// fillFrom produces a genuinely prompt-only request instead of one carrying an
// empty system message.
func genRun(system, prompt string) map[string]any {
	run := map[string]any{
		"prompt":      prompt,
		"max_tokens":  1024, // 22 lines of "<question> | <answer>" fits easily
		"temperature": 0.8,  // variety across batches; the answers are drafts anyway
	}
	if system != "" {
		run["system"] = system
	}
	return run
}

// complete runs one generation call against a writer, local or remote.
//
// The two halves differ only in how isannd is asked to wait. Neither carries a
// session token: a background process cannot depend on `isann auth unlock`, and
// for a protected ally isannd attaches the active inference-access credential
// on the way out by itself (outbound.go attachInferCredential) — which is why
// there is no auth code here at all.
func (g *Generator) complete(e poolEntry, system, prompt string) (string, error) {
	if e.IsSelf() {
		return g.completeLocal(e.Service, system, prompt)
	}
	return g.completeRemote(e, system, prompt)
}

// completeLocal asks this node's own engine, blocking.
//
// `?timeout=` makes isannd wait for the job instead of returning a job id, so
// generation is a single call. That mode exists for exactly this (see
// admin_infer.go clampTimeoutSecs) and it is NOT available on the peer path,
// which is why the remote half polls instead.
func (g *Generator) completeLocal(service, system, prompt string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"service": service,
		"run":     genRun(system, prompt),
	})
	if err != nil {
		return "", err
	}

	endpoint := g.IsanndURL + "/internal/api/infer/svc/" + url.PathEscape(service) + "/v1/jobs?timeout=150"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("generate: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return "", fmt.Errorf("generate: HTTP %d", resp.StatusCode)
	}
	if s := textFromReply(body); s != "" {
		return s, nil
	}
	return "", fmt.Errorf("generate: no text in reply")
}

// genDeadline is how long an allied writer gets to produce a batch.
//
// Generous: twenty-two lines is a much longer generation than a probe answer,
// and the ally may be serving other traffic. It costs nothing to wait — refills
// run in the background, off the firing loop.
const genDeadline = 4 * time.Minute

// completeRemote asks an allied node, submit then poll.
//
// Exactly the path a probe shot takes (`/node/s:<id>/svc/<svc>/v1/jobs`), so
// discovery, NAT traversal and the HTTP/3 hop are isannd's problem and none of
// it is reimplemented here.
func (g *Generator) completeRemote(e poolEntry, system, prompt string) (string, error) {
	res, err := g.firer.SubmitRun(e.Node, e.Service, genRun(system, prompt))
	if err != nil {
		return "", fmt.Errorf("generate via %s: %w", e, err)
	}
	// A full queue is not "this writer is broken", but it is still a reason to
	// try the next one rather than sit and wait for a busy ally.
	if res.Outcome != OutcomeSubmitted {
		return "", fmt.Errorf("generate via %s: %s", e, res.Outcome)
	}
	fetched, err := g.firer.Collect(e.Node, res.JobID, genDeadline)
	if err != nil {
		return "", fmt.Errorf("generate via %s: %w", e, err)
	}
	if fetched.Outcome != OutcomeAnswered {
		return "", fmt.Errorf("generate via %s: %s", e, fetched.Outcome)
	}
	if s := strings.TrimSpace(fetched.Answer); s != "" {
		return s, nil
	}
	return "", fmt.Errorf("generate via %s: no text in reply", e)
}

// textFromReply pulls generated text out of the OpenAI-shaped reply bodies
// engines use. Returns "" when there is none.
func textFromReply(body []byte) string {
	var oa struct {
		Choices []struct {
			Text    string `json:"text"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &oa); err != nil || len(oa.Choices) == 0 {
		return ""
	}
	if s := strings.TrimSpace(oa.Choices[0].Text); s != "" {
		return s
	}
	return strings.TrimSpace(oa.Choices[0].Message.Content)
}
