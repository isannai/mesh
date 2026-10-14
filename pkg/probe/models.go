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
//	                           size of the model the node runs and the budget
//	                           id, under a second signature
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
// package.json. That is the node's word; whether the node runs that model is
// checked by its output (compare.go), not by its reported size.
//
// The budget lists no model name or size (2026-10-13): the model hash comes
// from the market recipe, so a size typed into the budget has nothing left to
// catch. The ticket's parameter count is the size the node reports.

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
	ID    int64  `json:"id"`
	Model string `json:"model"`
}

// budgetRef is what a ticket needs of a model budget.
type budgetRef struct {
	ID string // budget id, decimal
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
			model, err := faucet.ModelFromReport(r.Model)
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
			out.Models[model] = budgetRef{ID: strconv.FormatInt(r.ID, 10)}
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
//	model has a live budget    its model, the node's model size and the budget
//	                           id; write
//	otherwise                  plain ticket; write only while a system budget is
//	                           live
//	no list read yet           plain ticket; write (the API may just be down)
func (p *Prober) ticketBudget(svc rvnodes.Service) (model, params, budgetID string, write bool) {
	p.modelsMu.Lock()
	defer p.modelsMu.Unlock()
	if !p.budgetsRead {
		return "", "", "", true
	}
	if m, ref, ok := p.modelRefLocked(svc); ok {
		return m, ticketParams(svc.ModelClass), ref.ID, true
	}
	return "", "", "", p.systemBudget != ""
}

// ticketParams is the parameter count a model ticket carries: the size of the
// model the node runs, as the node reads it off its own weights (ParamsCore, or
// ParamsTotal when it reports no core), in the ticket spelling. "0.000" when the
// node reports no size, which still passes the rendezvous' check of the field.
//
// It is not held against the budget (2026-10-13): the budget lists no size any
// more. Nothing pays by this number today. A price by model size would have to
// check it first, since it is the node's own word.
func ticketParams(c *rvnodes.ModelClass) string {
	var n uint64
	if c != nil {
		n = c.ParamsCore
		if n == 0 {
			n = c.ParamsTotal
		}
	}
	return faucet.FormatParams(n)
}

// modelRefLocked finds the live model budget a service sits on: ok when the
// service's model hash has one.
//
// The caller holds modelsMu.
func (p *Prober) modelRefLocked(svc rvnodes.Service) (model string, ref budgetRef, ok bool) {
	m, err := faucet.ModelFromReport(svc.ModelHash)
	if err != nil {
		return "", budgetRef{}, false
	}
	r, found := p.models[m]
	if !found {
		return "", budgetRef{}, false
	}
	return m, r, true
}

// nodeTicketBudget is ticketBudget for a whole node: a node is paid for ONE
// model, however many it runs.
//
// 🔴 A node that serves two models with live budgets would otherwise get a ticket
// for whichever service passed first, and a different one from each prober. The
// node keeps one ticket per prober and slot, so which budget paid was luck, and a
// client that picks among the tickets could take the dearest. Every prober reads
// the same directory and the same budget list, so they all settle on the same
// model: the one with the LOWEST budget id. Its service carries the ticket; the
// node's other services write none, or a plain ticket from another of them could
// reach the node first and hide it.
//
// A node with no model on a live budget is unchanged: any passing service earns
// the plain ticket.
func (p *Prober) nodeTicketBudget(t Target) (model, params, budgetID string, write bool) {
	model, params, budgetID, write = p.ticketBudget(t.Service)
	if len(t.Node.Services) < 2 {
		return
	}
	p.modelsMu.Lock()
	chosen := p.nodeModelLocked(t.Node.Services)
	p.modelsMu.Unlock()
	if chosen == "" || chosen == model {
		return
	}
	return "", "", "", false
}

// nodeModelLocked is the model a node is paid for: of its services that sit on a
// live model budget, the one with the lowest budget id. "" when there is none.
// The caller holds modelsMu.
func (p *Prober) nodeModelLocked(svcs []rvnodes.Service) string {
	var (
		best   string
		bestID uint64
	)
	for _, svc := range svcs {
		m, ref, ok := p.modelRefLocked(svc)
		if !ok {
			continue
		}
		id, err := strconv.ParseUint(ref.ID, 10, 64)
		if err != nil {
			continue
		}
		if best == "" || id < bestID {
			best, bestID = m, id
		}
	}
	return best
}

// ticketFor is what a ticket for t carries, whether to write one, and why not.
//
// From an RV that groups by budget registration (Assignment.Planned), the node's
// group decides, not the node's model hash: the group's budget is the one its
// owner registered in, or the system budget. A model budget's ticket names that
// budget and its model, and the node must be running it. A system group's ticket
// is plain (the RV pays it from the system budget). From an older RV, the choice
// by model hash below (nodeTicketBudget).
func (p *Prober) ticketFor(t Target) (model, params, budgetID string, write bool, why string) {
	if !p.hasAssign || !p.assign.Planned() {
		model, params, budgetID, write = p.nodeTicketBudget(t)
		return model, params, budgetID, write, ""
	}
	g, mode := p.modeOf(nodeAddressOf(t.Node.ID))
	switch {
	case mode == ModeUnlisted:
		return "", "", "", false, "its GPU is not in the budget's check info"
	case g.BudgetID == 0:
		return "", "", "", false, "no system budget is active"
	case g.Model == "":
		return "", "", "", true, ""
	}
	want := strings.ToLower(strings.TrimSpace(g.Model))
	if m, err := faucet.ModelFromReport(t.Service.ModelHash); err != nil || m != want {
		return "", "", "", false, fmt.Sprintf("it does not run the model of budget %d", g.BudgetID)
	}
	return want, ticketParams(t.Service.ModelClass), strconv.FormatUint(g.BudgetID, 10), true, ""
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
		if m, _, _, _ := p.nodeTicketBudget(t); m != "" {
			n++
		}
	}
	return fmt.Sprintf(", %d on a live model budget (%d live, system budget %t)", n, listed, sys)
}
