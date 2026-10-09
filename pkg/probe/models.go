package probe

// models.go: the live faucet budgets, which budget a ticket is paid from and
// whether a ticket is written at all.
//
// # WHAT A BUDGET DOES AND DOES NOT DO
//
// The faucet pays out of budgets. A budget is one line of the FaucetBudgets
// contract: a period, a total, a value per ticket and, for a model budget, the
// sha256 of the model file it pays for. A budget with no model (the zero hash)
// is the system budget that pays any node.
//
//	model has a live budget    the ticket also carries the model hash, the
//	                           parameter count and the budget id, under a
//	                           second signature
//	anything else              a plain ticket; the rendezvous pays it from the
//	                           system budget
//
// A budget that has run out (spent >= total) or ended is not live. Its tickets
// are not written: a model whose budget ran out falls back to the plain ticket,
// and when the system budget has run out too, no ticket is written at all.
// Vouchers already issued are still accepted by the chain; only new tickets stop.
//
// # WHERE IT COMES FROM
//
// The chain query API (eventLogger) collects the FaucetBudgets contract and
// serves it at /v1/faucet-budgets. The prober asks for the lines in their
// period at the start of the current slot (activeAt) that are active and not
// spent out, so every prober of a slot reads the same list.
//
// The prober asks its own isannd which API it uses (/internal/api/info `api`)
// and reads the list from that API directly, so a node switched between local
// and dev reads the matching deployment without a setting of its own.
//
// 🔴 Not from a config file. The budget decides a price, and a file on the
// prober would let whoever runs the prober raise it. On chain it changes only
// by the operators' M-of-N vote or a funder's deposit, and anyone can read it.
//
// The model hash in the ticket comes from the directory: the node's own report
// of the file it loaded, the sha256 `isann model pull` wrote into its
// package.json. That is the node's word; checking the node really holds that
// file is a later step.

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/isannai/mesh/pkg/faucet"
	"github.com/isannai/mesh/pkg/rvnodes"
)

// modelPageLimit is the API's largest page.
const modelPageLimit = 100

// modelPagesMax bounds one fetch. A hundred pages is ten thousand budgets, far
// past any list an operator would vote through; a cursor that never ends is a
// broken API, not a long list.
const modelPagesMax = 100

// zeroModel is the model of the system budget.
const zeroModel = "0x0000000000000000000000000000000000000000000000000000000000000000"

// budgetRow is one /v1/faucet-budgets row, only the fields read here.
type budgetRow struct {
	ID     int64  `json:"id"`
	Model  string `json:"model"`
	Params string `json:"params"`
}

// budgetRef is what a ticket needs of a model budget.
type budgetRef struct {
	ID     string // budget id, decimal
	Params string // ticket params, billions with three decimals
}

// Budgets is the live budget list: model to its budget, and the system budget.
type Budgets struct {
	Models map[string]budgetRef
	// System is the id of the system budget, "" when none is live.
	System string
}

// FetchBudgets reads the budgets that are live in the slot starting at unix
// time at: in their period, active, and not spent out. An empty result means
// the API answered and lists nothing.
//
// When several live budgets share a model, the lowest id wins, so every
// prober of the slot picks the same one.
func FetchBudgets(isanndURL string, client *http.Client, at int64) (Budgets, error) {
	api, err := chainAPIOf(isanndURL, client)
	if err != nil {
		return Budgets{}, err
	}
	out := Budgets{Models: map[string]budgetRef{}}
	var systemID int64
	ids := map[string]int64{}
	cursor := ""
	for page := 0; page < modelPagesMax; page++ {
		q := url.Values{}
		q.Set("activeAt", strconv.FormatInt(at, 10))
		q.Set("state", "active")
		q.Set("exhausted", "false")
		q.Set("limit", strconv.Itoa(modelPageLimit))
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var body struct {
			Data []budgetRow `json:"data"`
			Next *string     `json:"next"`
		}
		if err := getJSON(client, api+"/v1/faucet-budgets?"+q.Encode(), &body); err != nil {
			return Budgets{}, fmt.Errorf("budgets: %w", err)
		}
		for _, r := range body.Data {
			if r.ID <= 0 {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(r.Model), zeroModel) {
				if systemID == 0 || r.ID < systemID {
					systemID = r.ID
				}
				continue
			}
			model, params, err := parseBudgetRow(r)
			if err != nil {
				// One bad line costs that one model its model fields, not the
				// whole list. Logged because the line is on chain and the
				// operators have to fix it there.
				log.Printf("[probe] budgets: id %d: %v (skipped)", r.ID, err)
				continue
			}
			if prev, ok := ids[model]; ok && prev < r.ID {
				continue
			}
			ids[model] = r.ID
			out.Models[model] = budgetRef{ID: strconv.FormatInt(r.ID, 10), Params: params}
		}
		if body.Next == nil || *body.Next == "" {
			if systemID != 0 {
				out.System = strconv.FormatInt(systemID, 10)
			}
			return out, nil
		}
		cursor = *body.Next
	}
	return Budgets{}, fmt.Errorf("budgets: more than %d pages", modelPagesMax)
}

// parseBudgetRow reads one model budget into the ticket spelling.
func parseBudgetRow(r budgetRow) (model, params string, err error) {
	if model, err = faucet.ModelFromReport(r.Model); err != nil {
		return "", "", err
	}
	// params arrives as a decimal string (the API keeps uint256 out of JSON
	// numbers).
	count, err := strconv.ParseUint(strings.TrimSpace(r.Params), 10, 64)
	if err != nil || count == 0 {
		return "", "", fmt.Errorf("params %q is not a parameter count", r.Params)
	}
	if count < 1_000_000 {
		// Below a million the ticket would read "0.000", almost certainly a
		// count requested in billions by mistake and approved anyway.
		return "", "", fmt.Errorf("params %d is not a parameter count", count)
	}
	return model, faucet.FormatParams(count), nil
}

// chainAPIOf asks the local isannd which chain query API it reads.
func chainAPIOf(isanndURL string, client *http.Client) (string, error) {
	var info struct {
		API string `json:"api"`
	}
	if err := getJSON(client, strings.TrimRight(isanndURL, "/")+"/internal/api/info", &info); err != nil {
		return "", fmt.Errorf("budgets: isannd info: %w", err)
	}
	api := strings.TrimRight(strings.TrimSpace(info.API), "/")
	if api == "" {
		// An isannd older than the field, or one with no receipt block.
		return "", fmt.Errorf("budgets: isannd names no chain API (conf receipt.api)")
	}
	return api, nil
}

func getJSON(client *http.Client, u string, out any) error {
	resp, err := client.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", resp.Status, snippetOf(body))
	}
	return json.Unmarshal(body, out)
}

func snippetOf(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// refreshModels re-reads the budgets for the current slot. A failed read keeps
// the previous ones: budgets change by vote or deposit, rarely, and one flaky
// poll should not strip the model fields from every ticket until the next.
func (p *Prober) refreshModels() {
	b, err := FetchBudgets(p.cfg.NodeBridgeAddr, p.http, p.modelsAt(time.Now()))
	p.modelsMu.Lock()
	defer p.modelsMu.Unlock()
	if err != nil {
		if !p.budgetsRead {
			log.Printf("[probe] %v; tickets are written as plain ones until it can be read", err)
		} else {
			log.Printf("[probe] %v; keeping the previous list of %d", err, len(p.models))
		}
		return
	}
	if b.System == "" && p.systemBudget != "" {
		log.Printf("[probe] no live system budget; plain tickets stop")
	}
	p.models, p.systemBudget, p.budgetsRead = b.Models, b.System, true
}

// modelsAt is the time the list is asked for: the start of the assigned slot,
// so a budget approved, stopped or spent out in the middle of a slot changes
// the tickets from the next slot on, for every prober alike. Without an
// assignment (only an appointed prober writes tickets, so only a test gets
// here) it is now.
//
// Called from the directory poll, the goroutine that sets p.assign.
func (p *Prober) modelsAt(now time.Time) int64 {
	if !p.hasAssign {
		return now.Unix()
	}
	return faucet.SlotStartAt(p.assign.Epoch, p.assign.SlotSec)
}

// ticketBudget returns the fields a ticket for a shot at svc carries and
// whether to write one.
//
//	model has a live budget    its model, params and budget id; write
//	otherwise                  plain ticket; write only while a system budget is
//	                           live
//	no list read yet           plain ticket; write (the API may just be down)
func (p *Prober) ticketBudget(svc rvnodes.Service) (model, params, budgetID string, write bool) {
	p.modelsMu.Lock()
	defer p.modelsMu.Unlock()
	if !p.budgetsRead {
		return "", "", "", true
	}
	if m, err := faucet.ModelFromReport(svc.ModelHash); err == nil {
		if ref, ok := p.models[m]; ok {
			return m, ref.Params, ref.ID, true
		}
	}
	return "", "", "", p.systemBudget != ""
}

// budgetSummary is the directory log's note on the budgets: how many model
// budgets are live and how many targets run one of them. "" before it was ever
// read.
func (p *Prober) budgetSummary(targets []Target) string {
	p.modelsMu.Lock()
	listed, known, sys := len(p.models), p.budgetsRead, p.systemBudget != ""
	p.modelsMu.Unlock()
	if !known {
		return ""
	}
	n := 0
	for _, t := range targets {
		if m, _, _, _ := p.ticketBudget(t.Service); m != "" {
			n++
		}
	}
	return fmt.Sprintf(", %d on a live model budget (%d live, system budget %t)", n, listed, sys)
}
