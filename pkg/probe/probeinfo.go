package probe

// probeinfo.go: asking a node what it runs NOW, before its ticket is written.
//
// The RV checks a node against the budget it registered in when it builds the slot,
// with what the node reported at registration (servers pkg/rendezvous
// faucet_qualify.go). That is as old as the last registration. A node's isannd
// answers GET /probe/info with the values as they are now (GLink cmd/isannd
// probe_info.go), anonymously and read-only, so settle.go can catch a node that
// changed its model, added a second LLM or swapped its GPU after the slot began:
//
//	{ "services": [ {"name":"llm-api","engine":"llama","model_hash":"sha256:..."} ],
//	  "gpus":     [ {"name":"NVIDIA GeForce RTX 4090","memory_gb":24} ],
//	  "ram_gb":   64,
//	  "tpm_verified": true, "named": true }
//
// model_hash is empty when the engine is stopped or its profile changed since it
// started. Memory is in GB to 0.1. tpm_verified and named are left out when the
// node cannot tell.
//
// liveCheck applies the same rules as the RV (model, one LLM, GPU grade, and the
// budget's TPM and Name-service conditions when it has them). A node with no door
// (an older isannd) or one that does not answer is NOT turned away for it: the check
// the RV made when it built the slot stands. So does a node that leaves tpm_verified
// or named out, for that one condition.

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/isannai/mesh/pkg/faucet"
)

// probeInfoTimeout is how long one /probe/info call waits. settle.go asks the nodes
// one after another, so a node that is down costs this much once and no more.
var probeInfoTimeout = 3 * time.Second

// errNoProbeDoor: the node answered 404 or 405, so its isannd does not have the
// /probe/info door yet.
var errNoProbeDoor = errors.New("the node has no /probe/info door (an older isannd)")

// NodeProbeInfo is the /probe/info body. TPMVerified (the node's TPM is verified)
// and Named (the node has a live Name-service subdomain) are pointers: nil is a node
// that cannot tell or an isannd that does not send them, which is not the same as
// false.
type NodeProbeInfo struct {
	Services    []ProbeInfoService `json:"services"`
	GPUs        []ProbeInfoGPU     `json:"gpus"`
	RAMGB       float64            `json:"ram_gb"`
	TPMVerified *bool              `json:"tpm_verified,omitempty"`
	Named       *bool              `json:"named,omitempty"`
}

// ProbeInfoService is one service of the node. ModelHash is empty when its engine
// is stopped.
type ProbeInfoService struct {
	Name      string `json:"name"`
	Engine    string `json:"engine"`
	ModelHash string `json:"model_hash"`
}

// ProbeInfoGPU is one GPU of the node. MemoryGB is 0 for a GPU that shares memory
// with the CPU.
type ProbeInfoGPU struct {
	Name     string  `json:"name"`
	MemoryGB float64 `json:"memory_gb"`
}

// ProbeInfo asks one node what it runs now. errNoProbeDoor for a node without the
// door; any other error (no connection, no answer in time, a status that is not
// 200, a body that is not the answer) is returned as it is.
func (f *Firer) ProbeInfo(nodeID string) (NodeProbeInfo, error) {
	body, code, err := f.get(f.nodeBase(nodeID)+"/probe/info", probeInfoTimeout)
	if err != nil {
		return NodeProbeInfo{}, err
	}
	switch code {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return NodeProbeInfo{}, errNoProbeDoor
	default:
		return NodeProbeInfo{}, fmt.Errorf("probe info: HTTP %d", code)
	}
	var info NodeProbeInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return NodeProbeInfo{}, fmt.Errorf("parse probe info: %w", err)
	}
	// The door always sends "services" (an empty list when there are none). An
	// answer without it is not the door's answer, and must not read as a node that
	// runs nothing.
	if info.Services == nil {
		return NodeProbeInfo{}, errors.New("probe info: the answer has no services list")
	}
	return info, nil
}

// liveRefusal asks node t what it runs now and says why it no longer fits group g's
// budget, or "" when it fits or cannot be looked at. A system group (no model) is
// not asked. A node with no door, or one that does not answer, is not turned away:
// one log line says the check was skipped, and settle.go decides each node once a
// slot, so the line comes once per node per slot.
func (p *Prober) liveRefusal(t Target, g AssignGroup) string {
	if g.Model == "" || p.firer == nil {
		return ""
	}
	info, err := p.firer.ProbeInfo(t.Node.ID)
	switch {
	case err == nil:
		return liveCheck(info, g.liveWant())
	case errors.Is(err, errNoProbeDoor):
		log.Printf("[probe] %s: no live check, its isannd has no /probe/info; the check made when the slot was built stands",
			short(t.Node.ID))
	default:
		log.Printf("[probe] %s: no live check, /probe/info failed (%v); the check made when the slot was built stands",
			short(t.Node.ID), err)
	}
	return ""
}

// What liveCheck says when a node no longer fits its budget.
const (
	liveModel    = "model"
	liveExtraLLM = "extra LLM"
	liveGPUGrade = "GPU grade"
	liveTPM      = "TPM"
	liveNamed    = "name"
)

// liveWant is what a group asks of a node: the budget's model hash, its lowest GPU
// grade (0 for no limit), and whether it pays only TPM-verified nodes or only nodes
// with a live Name-service subdomain.
type liveWant struct {
	Model        string
	MinGrade     int
	RequireTPM   bool
	RequireNamed bool
}

// liveWant is the group's side of liveCheck.
func (g AssignGroup) liveWant() liveWant {
	return liveWant{Model: g.Model, MinGrade: g.MinGrade, RequireTPM: g.RequireTPM, RequireNamed: g.RequireNamed}
}

// isLLMService tells a text-generation service from the others (image and so on)
// by the engine or the service name, as the RV does.
func isLLMService(s ProbeInfoService) bool {
	switch strings.ToLower(strings.TrimSpace(s.Engine)) {
	case "llama", "vllm":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(s.Name)) {
	case "llm-api", "vllm-api":
		return true
	}
	return false
}

// liveCheck holds what a node runs now against the budget it is paid from (w: its
// model hash, lowest GPU grade, and TPM / Name-service conditions). It returns ""
// when the node fits, otherwise why not:
//
//	model      none of its running LLMs is the budget's model
//	extra LLM  one is, and another LLM runs next to it
//	GPU grade  its GPU memory is below the grade the budget asks for
//	TPM        the budget asks for a TPM-verified node and the node says it is not
//	name       the budget asks for a Name-service subdomain and the node says it has none
//
// Only LLM services count, so an image engine alongside is fine. An LLM with no
// model hash is a stopped engine and is left out. TPM and name are refused only on an
// answer of false: a node that does not report the field is not turned away for it
// (the RV's check stands), like a node with no door. A pure function.
func liveCheck(info NodeProbeInfo, w liveWant) string {
	want := strings.ToLower(strings.TrimSpace(w.Model))
	if m, err := faucet.ModelFromReport(w.Model); err == nil {
		want = m
	}
	matching, others := 0, 0
	for _, s := range info.Services {
		if !isLLMService(s) || strings.TrimSpace(s.ModelHash) == "" {
			continue
		}
		if m, err := faucet.ModelFromReport(s.ModelHash); err == nil && m == want {
			matching++
		} else {
			others++
		}
	}
	if matching == 0 {
		return liveModel
	}
	if others > 0 {
		return liveExtraLLM
	}
	if min := clampMinGrade(w.MinGrade); min > 0 && gpuGrade(info.memoryGB()) < min {
		return liveGPUGrade
	}
	if w.RequireTPM && info.TPMVerified != nil && !*info.TPMVerified {
		return liveTPM
	}
	if w.RequireNamed && info.Named != nil && !*info.Named {
		return liveNamed
	}
	return ""
}
