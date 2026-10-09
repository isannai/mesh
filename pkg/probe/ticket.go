package probe

// ticket.go — writing the receipt when a node passes.
//
// A ticket is the only durable trace of a check. Everything else here is
// measurement the operator can read; this is the part that turns into credits,
// so it is signed and handed to the node that earned it.
//
// # A SECOND ROUND TRIP, AND IT HAS TO BE
//
// The shot itself cannot carry it. Grading happens after the answer comes back,
// by which time that request is finished. So the ticket goes out on its own
// POST, wearing the SAME X-ISANN-Probe header the shot wore — which is what
// tells the node this is one of its assigned probers and not a stranger.
//
// # WHY THE TPM SIGNS IT, NOT A WALLET
//
// A wallet would mean a keystore file and its passphrase sitting in this
// process's config. Copy that pair and you mint tickets in this machine's name
// from anywhere. The TPM key cannot leave the chip, and the rendezvous already
// knows which key belongs to which node — it recorded that when this machine
// answered its challenge — so nothing extra has to travel with the ticket to
// say who signed it.
//
// 🔴 NO TPM MEANS NO TICKETS. Signing fails and the check is still recorded
// locally, but the node gets nothing. That is the intended shape: receipts come
// from machines that can prove they are machines.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/isannai/mesh/pkg/faucet"
	"github.com/isannai/mesh/pkg/setup"
)

// TicketPath is the node door a receipt is posted to. Must match isannd's
// ticketDoorPath.
const TicketPath = "/faucet/ticket"

// ticketEnvelope is the wire shape. Field names must match isannd's struct of
// the same name — a contract between two repositories that do not import each
// other, like the probe bundle above it.
type ticketEnvelope struct {
	Ticket faucet.Ticket `json:"ticket"`
	Proof  ticketProof   `json:"proof"`
}

// ticketProof is the merkle bundle, carried so the rendezvous can check the
// assignment months later. It keeps only the CURRENT slot's membership, so this
// is the only record of which group a past ticket belonged to.
type ticketProof struct {
	N       int      `json:"n"`
	Members []string `json:"members"`
	Probers []string `json:"probers"`
	Path    []string `json:"path"`
}

// issueTicket signs a receipt for one passing check and delivers it.
//
// kind names the track the check belongs to (faucet.KindText / KindImage). It
// is written into the ticket only alongside a model budget (models.go).
//
// 🔴 A passing node gets a ticket while a budget pays for it. A model with a
// live budget adds the model fields and the budget id to the ticket; any other
// node gets a plain ticket paid from the system budget. When no budget is live
// (the system budget has run out) no ticket is written.
//
// Failures are logged and swallowed. A ticket that cannot be written or
// delivered is lost income for the node, not a reason to stop probing — and the
// next slot brings another chance.
func (p *Prober) issueTicket(t Target, at time.Time, kind string) {
	if !p.hasAssign || p.assign.Stale(at) {
		return
	}
	addr := strings.ToLower(strings.TrimSpace(nodeAddressOf(t.Node.ID)))
	gi := p.assign.GroupOf(addr)
	if gi < 0 {
		return
	}
	model, params, budgetID, write := p.ticketBudget(t.Service)
	if !write {
		return
	}

	node, err := faucet.ParseAddr(addr)
	if err != nil {
		log.Printf("[probe] ticket for %s: node address: %v", short(t.Node.ID), err)
		return
	}
	// 🔴 The payee comes from the directory, not from us. We are attesting to
	// work, not choosing who gets paid, and the node refuses a ticket whose
	// owner is not the one in its own config — so guessing would only produce a
	// rejection.
	owner, err := faucet.ParseAddr(strings.TrimSpace(t.Node.OwnerAddress))
	if err != nil {
		log.Printf("[probe] ticket for %s: node advertises no usable owner", short(t.Node.ID))
		return
	}
	self, err := faucet.ParseAddr(p.self)
	if err != nil {
		log.Printf("[probe] ticket for %s: own address: %v", short(t.Node.ID), err)
		return
	}
	chainID, faucetAddr := ticketDeployment(p.assign, p.cfg)

	ticket := faucet.Ticket{
		ChainID:    chainID,
		FaucetAddr: faucetAddr,
		Prober:     self,
		Node:       node,
		Owner:      owner,
		Root:       p.assign.Root32(),
	}
	if err := faucet.SignTicket(&ticket, setup.SignWithTPMKey); err != nil {
		log.Printf("[probe] ticket for %s: %v", short(t.Node.ID), err)
		return
	}
	// The second signature, over the six fields plus the model and its budget
	// id. Sig above is
	// unchanged by it, so a rendezvous that has never heard of these fields
	// still pays the ticket. See pkg/faucet ticket_ext.go.
	if model != "" {
		ticket.Kind, ticket.Params, ticket.Model, ticket.BudgetID = kind, params, model, budgetID
		if err := faucet.SignTicketExt(&ticket, setup.SignWithTPMKey); err != nil {
			// The plain ticket is already signed and still earns the slot, so
			// it goes out without the model fields rather than not at all.
			log.Printf("[probe] ticket for %s: %v (sent without model fields)", short(t.Node.ID), err)
			ticket.Kind, ticket.Params, ticket.Model, ticket.BudgetID, ticket.SigExt = "", "", "", "", ""
		}
	}

	g := p.assign.Groups[gi]
	env := ticketEnvelope{
		Ticket: ticket,
		Proof: ticketProof{
			N:       p.assign.N,
			Members: g.Members,
			Probers: g.Probers,
			Path:    g.Path,
		},
	}
	if err := p.deliverTicket(t, env); err != nil {
		log.Printf("[probe] ticket for %s: %v", short(t.Node.ID), err)
		return
	}
	if ticket.Params != "" {
		log.Printf("[probe] %s issued a ticket (budget %s, %sB)", short(t.Node.ID), ticket.BudgetID, ticket.Params)
	} else {
		log.Printf("[probe] %s issued a ticket", short(t.Node.ID))
	}
}

// ticketDeployment is the chain id and faucet contract a ticket is signed for.
//
// The RV's own pair, from the assignment, whenever it sends one: the RV checks
// a claim against exactly that pair, and the config keys below were left unset
// on every prober that ever ran, so every ticket used to say chain 0.
//
// An RV older than the field sends zeros, and then the config keeps working the
// way it always did. Unset there too is zero for both, which the RV still
// accepts from older probers.
func ticketDeployment(a Assignment, cfg Config) (uint64, faucet.Addr) {
	if a.ChainID != 0 {
		addr, err := faucet.ParseAddr(a.FaucetAddr)
		if err != nil {
			addr = faucet.Addr{}
		}
		return a.ChainID, addr
	}
	addr, err := faucet.ParseAddr(cfg.FaucetAddr)
	if err != nil {
		// Unset until a contract is deployed. The zero address is the honest
		// value for "no deployment yet" and keeps the field in the signature
		// rather than adding it later and breaking every stored ticket.
		addr = faucet.Addr{}
	}
	return cfg.ChainID, addr
}

// deliverTicket posts the envelope through this prober's own isannd, the same
// route every other call to a node takes.
func (p *Prober) deliverTicket(t Target, env ticketEnvelope) error {
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	id := strings.TrimSpace(t.Node.ID)
	if !strings.Contains(id, ":") {
		id = "s:" + id
	}
	endpoint := strings.TrimRight(p.firer.IsanndURL, "/") + "/node/" + url.PathEscape(id) + TicketPath

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if t.Probe != "" {
		req.Header.Set(ProbeHeader, t.Probe)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The body carries the node's reason. Without it a 400 says only "the
		// node did not like it", which covers everything from a wire-shape
		// mismatch to a genuine rejected check.
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("node replied HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	return nil
}

// issueImageTicket writes the receipt for a picture that passed.
//
// 🔴 The image track fires in one round and judges in a later one — a node may
// still be drawing when the round ends. So the ticket must name the slot the
// shot was FIRED under, not the one we happen to be in when the verdict lands.
// That root is on the shot row (AssignRoot), recorded at fire time for exactly
// this reason.
//
// When the slot has since turned over we skip. The proof a ticket carries is
// the group from THAT slot, and once the assignment is replaced we no longer
// hold it — a ticket without its proof cannot be claimed, so writing one would
// only look like an earning that never pays. Judgement normally lands a round
// or two after the shot, well inside a three-hour slot, so this is the rare
// case rather than the common one.
func (p *Prober) issueImageTicket(sh Shot, at time.Time) {
	if sh.AssignRoot == "" {
		// The row was fired before the column was recorded, or read back by a
		// query that left it out — which is exactly what happened once, and the
		// receipt simply never appeared. Logged rather than swallowed: money
		// that quietly does not happen is the hardest kind to find.
		log.Printf("[probe] %s image: shot has no slot recorded — no ticket", short(sh.NodeID))
		return
	}
	if !p.hasAssign {
		log.Printf("[probe] %s image: we hold no assignment — no ticket", short(sh.NodeID))
		return
	}
	if !strings.EqualFold(sh.AssignRoot, p.assign.Root) {
		log.Printf("[probe] %s image: slot turned over before the verdict — no ticket",
			short(sh.NodeID))
		return
	}
	t, ok := p.imageTargetFor(sh.NodeID)
	if !ok {
		// The node left the directory between firing and judging. Nothing to
		// address the ticket to, and its owner is what we would be guessing.
		log.Printf("[probe] %s image: no longer in the directory — no ticket", short(sh.NodeID))
		return
	}
	p.issueTicket(t, at, faucet.KindImage)
}

// imageTargetFor finds the current target for a node, for its owner address and
// its probe header. Both change per slot, so they are read now rather than
// stored on the shot.
func (p *Prober) imageTargetFor(nodeID string) (Target, bool) {
	for _, t := range p.imgTargets {
		if strings.EqualFold(t.Node.ID, nodeID) {
			return t, true
		}
	}
	return Target{}, false
}
