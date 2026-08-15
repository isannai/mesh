package probe

// generate.go — refilling the question queue from the prober's own engine.
//
// math is produced in code; the other three categories are written by a local
// model. That asymmetry is on purpose: a program can be certain about
// arithmetic and cannot be certain about geography, so the model's output is
// treated as a DRAFT answer and never as ground truth. Scoring (a later
// milestone) settles disagreements by asking other nodes, and when they side
// with the node under test the QUESTION is thrown away, not the node.
//
// Generation goes through isannd's local inference proxy, so it needs no node
// routing and no anonymous posture — this is the prober's own engine.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// batchTarget is how many questions one generation call asks for.
const batchTarget = 20

// Generator refills the queue.
type Generator struct {
	IsanndURL string
	Service   string // the prober's own text service, e.g. "llm-api"
	Client    *http.Client
}

// NewGenerator builds a Generator. Service empty ⇒ only math is produced.
func NewGenerator(isanndURL, service string) *Generator {
	return &Generator{
		IsanndURL: strings.TrimRight(isanndURL, "/"),
		Service:   strings.TrimSpace(service),
		Client:    &http.Client{Timeout: 3 * time.Minute},
	}
}

// FillMath returns n freshly generated arithmetic questions.
func FillMath(n int, rng *rand.Rand) []Question {
	out := make([]Question, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, newMathQuestion(rng))
	}
	return out
}

// HasEngine reports whether LLM-backed categories can be produced.
func (g *Generator) HasEngine() bool { return g.Service != "" }

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

	raw, err := g.complete(spec.prompt)
	if err != nil {
		return nil, err
	}
	qs := parseBatch(cat, raw)
	if len(qs) == 0 {
		// Not an error the caller should retry immediately: models return
		// unusable batches now and then, and hammering the engine over it
		// would starve the prober's own inference.
		return nil, fmt.Errorf("generated batch for %q had no usable lines", cat)
	}
	return qs, nil
}

// complete runs one blocking completion against the prober's own engine.
//
// `?timeout=` makes isannd wait for the job instead of returning a job id, so
// generation is a single call. That mode exists for exactly this (see
// admin_infer.go clampTimeoutSecs) and avoids a second polling loop here.
func (g *Generator) complete(prompt string) (string, error) {
	payload, err := json.Marshal(map[string]any{
		"service": g.Service,
		"run": map[string]any{
			"prompt":      prompt,
			"max_tokens":  1024, // 20 lines of "<question> | <answer>"
			"temperature": 0.8,  // variety across batches; the answers are drafts anyway
		},
	})
	if err != nil {
		return "", err
	}

	endpoint := g.IsanndURL + "/internal/api/infer/svc/" + url.PathEscape(g.Service) + "/v1/jobs?timeout=150"
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

	var oa struct {
		Choices []struct {
			Text    string `json:"text"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &oa); err == nil && len(oa.Choices) > 0 {
		if s := strings.TrimSpace(oa.Choices[0].Text); s != "" {
			return s, nil
		}
		if s := strings.TrimSpace(oa.Choices[0].Message.Content); s != "" {
			return s, nil
		}
	}
	return "", fmt.Errorf("generate: no text in reply")
}
