package probe

// models.go: the designated model list, which model files a ticket names and
// the size each one is paid by.
//
// # WHAT THE LIST DOES AND DOES NOT DO
//
// It never decides WHETHER a node gets a ticket. Every node that passes the
// check gets one, as before. The list only decides what the ticket carries:
//
//	model on the list      the ticket also carries the model hash and the
//	                       listed size, under a second signature
//	anything else          a plain ticket, the one every node got before
//
// What either kind is worth is the rendezvous's decision. Today it pays both
// the same; the plan is a small base amount for a plain ticket and a price by
// size for one that names a designated model.
//
// # WHERE IT COMES FROM
//
// The FaucetModelRegistry contract holds it. Anyone requests a model file
// there; the operators approve it (M-of-N) with a period, or reject or stop
// it. One registry line is one file for one period:
//
//	hash     the model file's sha256
//	params   the parameter count the faucet pays that file by
//	startAt  the period, startAt <= t < endAt
//	endAt
//
// The chain query API (eventLogger) collects the registry and serves it at
// /v1/faucet-models. The prober asks for the lines in their period at the
// start of the current slot (activeAt), so every prober of a slot reads the
// same list. The chain keeps approved periods of one hash from overlapping,
// so a hash appears at most once.
//
// The prober asks its own isannd which API it uses (/internal/api/info `api`)
// and reads the list from that API directly, so a node switched between local
// and dev reads the matching deployment without a setting of its own.
//
// 🔴 Not from a config file. The size decides a price later, and a file on the
// prober would let whoever runs the prober raise it. On chain it changes only
// by the operators' M-of-N vote, and anyone can read what it says.
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

// modelPagesMax bounds one fetch. A hundred pages is ten thousand files, far
// past any list an operator would vote through; a cursor that never ends is a
// broken API, not a long list.
const modelPagesMax = 100

// modelRow is one /v1/faucet-models row, only the fields read here.
type modelRow struct {
	ID     int64  `json:"id"`
	Hash   string `json:"hash"`
	Params string `json:"params"`
}

// FetchDesignated reads the designated model list in its period at unix time
// at: ticket model → ticket params. An empty map means the API answered and
// lists nothing.
func FetchDesignated(isanndURL string, client *http.Client, at int64) (map[string]string, error) {
	api, err := chainAPIOf(isanndURL, client)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	cursor := ""
	for page := 0; page < modelPagesMax; page++ {
		q := url.Values{}
		q.Set("activeAt", strconv.FormatInt(at, 10))
		q.Set("limit", strconv.Itoa(modelPageLimit))
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		var body struct {
			Data []modelRow `json:"data"`
			Next *string    `json:"next"`
		}
		if err := getJSON(client, api+"/v1/faucet-models?"+q.Encode(), &body); err != nil {
			return nil, fmt.Errorf("designated models: %w", err)
		}
		for _, r := range body.Data {
			model, params, err := parseModelRow(r)
			if err != nil {
				// One bad line costs that one file its model fields, not the
				// whole list. Logged because the line is on chain and the
				// operators have to stop it there.
				log.Printf("[probe] designated models: id %d: %v (skipped)", r.ID, err)
				continue
			}
			out[model] = params
		}
		if body.Next == nil || *body.Next == "" {
			return out, nil
		}
		cursor = *body.Next
	}
	return nil, fmt.Errorf("designated models: more than %d pages", modelPagesMax)
}

// parseModelRow reads one registry line into the ticket spelling.
func parseModelRow(r modelRow) (model, params string, err error) {
	if model, err = faucet.ModelFromReport(r.Hash); err != nil {
		return "", "", err
	}
	// params arrives as a decimal string (the API keeps uint256 out of JSON
	// numbers). The registry refuses 0.
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
		return "", fmt.Errorf("designated models: isannd info: %w", err)
	}
	api := strings.TrimRight(strings.TrimSpace(info.API), "/")
	if api == "" {
		// An isannd older than the field, or one with no receipt block.
		return "", fmt.Errorf("designated models: isannd names no chain API (conf receipt.api)")
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

// refreshModels re-reads the list for the current slot. A failed read keeps
// the previous one: the list changes by vote, rarely, and one flaky poll
// should not strip the model fields from every ticket until the next.
func (p *Prober) refreshModels() {
	m, err := FetchDesignated(p.cfg.NodeBridgeAddr, p.http, p.modelsAt(time.Now()))
	p.modelsMu.Lock()
	defer p.modelsMu.Unlock()
	if err != nil {
		if p.models == nil {
			log.Printf("[probe] %v; tickets carry no model fields until it can be read", err)
		} else {
			log.Printf("[probe] %v; keeping the previous list of %d", err, len(p.models))
		}
		return
	}
	p.models = m
}

// modelsAt is the time the list is asked for: the start of the assigned slot,
// so a line approved or stopped in the middle of a slot changes the tickets
// from the next slot on, for every prober alike. Without an assignment (only
// an appointed prober writes tickets, so only a test gets here) it is now.
//
// Called from the directory poll, the goroutine that sets p.assign.
func (p *Prober) modelsAt(now time.Time) int64 {
	if !p.hasAssign {
		return now.Unix()
	}
	return faucet.SlotStartAt(p.assign.Epoch, p.assign.SlotSec)
}

// ticketModel returns the model fields a ticket for a shot at svc carries,
// or "" for a plain ticket. It never refuses one.
func (p *Prober) ticketModel(svc rvnodes.Service) (model, params string) {
	p.modelsMu.Lock()
	defer p.modelsMu.Unlock()
	if len(p.models) == 0 {
		return "", ""
	}
	model, err := faucet.ModelFromReport(svc.ModelHash)
	if err != nil {
		return "", ""
	}
	params, ok := p.models[model]
	if !ok {
		return "", ""
	}
	return model, params
}

// designatedSummary is the directory log's note on the list: how many files
// it holds and how many targets run one of them. "" before it was ever read.
func (p *Prober) designatedSummary(targets []Target) string {
	p.modelsMu.Lock()
	listed, known := len(p.models), p.models != nil
	p.modelsMu.Unlock()
	if !known {
		return ""
	}
	n := 0
	for _, t := range targets {
		if m, _ := p.ticketModel(t.Service); m != "" {
			n++
		}
	}
	return fmt.Sprintf(", %d on the designated model list (%d listed)", n, listed)
}
