package probe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isannai/mesh/pkg/faucet"
	"github.com/isannai/mesh/pkg/rvnodes"
)

var (
	qwen14Hex = strings.Repeat("e0", 32)
	qwen14    = "sha256:" + qwen14Hex
)

// qwenSvc is a service running the 14.768B model with the size a node reports. A
// node that reports no size is not paid as a model (see paramsFit).
func qwenSvc() rvnodes.Service {
	return rvnodes.Service{ModelHash: qwen14, ModelClass: &rvnodes.ModelClass{ParamsCore: 14768307200}}
}

// fakeChain stands in for both the local isannd (/internal/api/info) and the
// chain query API (/v1/faucet-budgets), on one server. Rows are served two per
// page so the cursor is exercised.
type fakeChain struct {
	srv   *httptest.Server
	rows  []map[string]any
	down  atomic.Bool
	noAPI bool
	at    atomic.Value // the last activeAt asked for
}

func newFakeChain(t *testing.T, rows ...map[string]any) *fakeChain {
	t.Helper()
	f := &fakeChain{rows: rows}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/api/info":
			api := f.srv.URL
			if f.noAPI {
				api = ""
			}
			json.NewEncoder(w).Encode(map[string]string{"version": "test", "api": api})
		case "/v1/faucet-budgets":
			if f.down.Load() {
				http.Error(w, "down", http.StatusBadGateway)
				return
			}
			q := r.URL.Query()
			if q.Get("activeAt") == "" || q.Get("state") != "active" || q.Get("exhausted") != "false" {
				http.Error(w, "unexpected filter "+r.URL.RawQuery, http.StatusBadRequest)
				return
			}
			f.at.Store(q.Get("activeAt"))
			start := 0
			if c := q.Get("cursor"); c != "" {
				start = int(c[0] - '0')
			}
			end := min(start+2, len(f.rows))
			var next any
			if end < len(f.rows) {
				next = string(rune('0' + end))
			}
			json.NewEncoder(w).Encode(map[string]any{"data": f.rows[start:end], "next": next})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// rowFor is one /v1/faucet-budgets line as the API spells it: model as 0x + 64
// hex, params as a decimal string.
func rowFor(id int, model, params string) map[string]any {
	return map[string]any{"id": id, "model": model, "name": "model", "params": params,
		"value": "10000000000000000", "state": "active", "active": true}
}

const zeroHash = "0x0000000000000000000000000000000000000000000000000000000000000000"

func TestFetchBudgets(t *testing.T) {
	ab := strings.Repeat("ab", 32)
	f := newFakeChain(t,
		rowFor(1, zeroHash, "0"), // the system budget
		rowFor(2, "0x"+qwen14Hex, "14768307200"),
		rowFor(3, "0x"+strings.ToUpper(ab), "7615616512"),  // the hash is read in any case
		rowFor(4, "not-a-hash", "14768307200"),             // a bad line is skipped, not fatal
		rowFor(5, "0x"+strings.Repeat("ef", 32), "14"),     // a count in billions by mistake
		rowFor(6, "0x"+strings.Repeat("cd", 32), "14.768"), // not a count at all
		rowFor(9, "0x"+qwen14Hex, "14768307200"),           // a second budget for one model: the lowest id wins
	)
	got, err := FetchBudgets(f.srv.URL, f.srv.Client(), 1791504000)
	if err != nil {
		t.Fatal(err)
	}
	if at, _ := f.at.Load().(string); at != "1791504000" {
		t.Errorf("activeAt = %q, want the time asked for", at)
	}
	if got.System != "1" {
		t.Errorf("system budget = %q, want 1", got.System)
	}
	want := map[string]budgetRef{
		"0x" + qwen14Hex: {"2", "14.768", 14768307200},
		"0x" + ab:        {"3", "7.615", 7615616512},
	}
	if len(got.Models) != len(want) {
		t.Fatalf("got %v, want %v", got.Models, want)
	}
	for k, v := range want {
		if got.Models[k] != v {
			t.Errorf("%s: got %v, want %v", k, got.Models[k], v)
		}
	}
}

func TestFetchBudgetsNeedsAnAPI(t *testing.T) {
	f := newFakeChain(t, rowFor(2, "0x"+qwen14Hex, "14768307200"))
	f.noAPI = true
	if _, err := FetchBudgets(f.srv.URL, f.srv.Client(), 1791504000); err == nil {
		t.Error("an isannd naming no API produced a list")
	}
}

// The list is asked for at the start of the assigned slot, not at the moment
// of the poll, so every prober of a slot reads the same one.
func TestModelsAtSlotStart(t *testing.T) {
	p := &Prober{}
	now := time.Unix(1791514800, 0) // 03:00 into a 3h slot that starts at 1791504000
	if got := p.modelsAt(now); got != now.Unix() {
		t.Errorf("no assignment: %d, want now", got)
	}
	p.assign, p.hasAssign = Assignment{SlotSec: 10800, Epoch: 1791504000 / 10800}, true
	if got := p.modelsAt(now); got != 1791504000 {
		t.Errorf("assigned: %d, want the slot start", got)
	}
}

// A model with a live budget gets the model fields and the budget id; any other
// node a plain ticket while the system budget lives; nobody once it is gone.
// While no list is known every node gets a plain ticket.
func TestTicketBudget(t *testing.T) {
	f := newFakeChain(t, rowFor(1, zeroHash, "0"), rowFor(2, "0x"+qwen14Hex, "14768307200"))
	p := &Prober{cfg: Config{NodeBridgeAddr: f.srv.URL}, http: f.srv.Client()}

	if m, params, id, write := p.ticketBudget(rvnodes.Service{ModelHash: qwen14}); m != "" || params != "" || id != "" || !write {
		t.Errorf("before any read: %q %q %q %v, want a plain ticket", m, params, id, write)
	}
	if s := p.budgetSummary(nil); s != "" {
		t.Errorf("before any read the log says %q", s)
	}

	p.refreshModels()
	m, params, id, write := p.ticketBudget(qwenSvc())
	if m != "0x"+qwen14Hex || params != "14.768" || id != "2" || !write {
		t.Errorf("listed: %q %q %q %v", m, params, id, write)
	}
	for _, h := range []string{"", "sha256:" + strings.Repeat("ab", 32), "hf-index"} {
		m, _, id, write := p.ticketBudget(rvnodes.Service{ModelHash: h})
		if m != "" || id != "" || !write {
			t.Errorf("%q: %q %q %v, want a plain ticket from the system budget", h, m, id, write)
		}
	}
	targets := []Target{{Service: qwenSvc()}, {Service: rvnodes.Service{}}}
	if s := p.budgetSummary(targets); s != ", 1 on a live model budget (1 live, system budget true)" {
		t.Errorf("summary = %q", s)
	}

	// One failed read keeps the list it had.
	f.down.Store(true)
	p.refreshModels()
	if m, _, _, _ := p.ticketBudget(qwenSvc()); m == "" {
		t.Error("a failed read dropped the list")
	}
}

// 🔴 A node is paid for ONE model however many it runs: the one with the lowest
// budget id, whichever order the services are listed in and whichever service the
// prober happened to check. The node's other services write no ticket at all.
func TestTicketBudgetOneModelPerNode(t *testing.T) {
	other := "sha256:" + strings.Repeat("c3", 32)
	f := newFakeChain(t,
		rowFor(1, zeroHash, "0"),
		rowFor(2, "0x"+qwen14Hex, "14768307200"),
		rowFor(3, "0x"+strings.Repeat("c3", 32), "7090000000"))
	p := &Prober{cfg: Config{NodeBridgeAddr: f.srv.URL}, http: f.srv.Client()}
	p.refreshModels()

	a := rvnodes.Service{Name: "llm-a", ModelHash: qwen14, ModelClass: &rvnodes.ModelClass{ParamsCore: 14768307200}} // budget 2
	b := rvnodes.Service{Name: "llm-b", ModelHash: other, ModelClass: &rvnodes.ModelClass{ParamsCore: 7090000000}}   // budget 3
	sd := rvnodes.Service{Name: "sd-api"}                                                                            // no model
	for _, order := range [][]rvnodes.Service{{a, b, sd}, {sd, b, a}} {
		node := rvnodes.Node{Services: order}
		if _, _, id, write := p.nodeTicketBudget(Target{Node: node, Service: a}); id != "2" || !write {
			t.Errorf("the lowest-id model must carry the ticket: id %q write %v", id, write)
		}
		for _, svc := range []rvnodes.Service{b, sd} {
			if _, _, _, write := p.nodeTicketBudget(Target{Node: node, Service: svc}); write {
				t.Errorf("service %s of a two-model node wrote a ticket", svc.Name)
			}
		}
	}

	// One model on a live budget and nothing else: unchanged, and a service with
	// no budget of its own still earns the plain ticket on a node without one.
	solo := rvnodes.Node{Services: []rvnodes.Service{b, sd}}
	if _, _, id, write := p.nodeTicketBudget(Target{Node: solo, Service: b}); id != "3" || !write {
		t.Errorf("a one-model node: id %q write %v", id, write)
	}
	plain := rvnodes.Node{Services: []rvnodes.Service{sd, {Name: "clip-api"}}}
	if _, _, id, write := p.nodeTicketBudget(Target{Node: plain, Service: sd}); id != "" || !write {
		t.Errorf("a node with no model budget: id %q write %v, want a plain ticket", id, write)
	}
}

// 🔴 THE NODE'S REPORTED SIZE MUST FIT THE BUDGET. A hash alone is the node's word
// for what it runs; the size the node reads off its own weights has to agree with
// the budget's parameter count, or the service gets no ticket at all (not even a
// plain one: it claims a budgeted model it does not run, or will not say).
func TestTicketBudgetNeedsTheSizeToFit(t *testing.T) {
	f := newFakeChain(t, rowFor(1, zeroHash, "0"), rowFor(2, "0x"+qwen14Hex, "14768307200"))
	p := &Prober{cfg: Config{NodeBridgeAddr: f.srv.URL}, http: f.srv.Client()}
	p.refreshModels()

	cases := []struct {
		name  string
		class *rvnodes.ModelClass
		model bool
	}{
		{"exact", &rvnodes.ModelClass{ParamsCore: 14768307200}, true},
		{"5 percent under", &rvnodes.ModelClass{ParamsCore: 14030000000}, true},
		{"core missing, total fits", &rvnodes.ModelClass{ParamsTotal: 14768307200}, true},
		{"no class reported", nil, false},
		{"class without a size", &rvnodes.ModelClass{Quant: "Q4_K_M"}, false},
		{"a small model under the big hash", &rvnodes.ModelClass{ParamsCore: 1777088000}, false},
		{"a bigger model under the hash", &rvnodes.ModelClass{ParamsCore: 32000000000}, false},
	}
	for _, c := range cases {
		svc := rvnodes.Service{Name: "llm", ModelHash: qwen14, ModelClass: c.class}
		m, _, id, write := p.ticketBudget(svc)
		if c.model && (id != "2" || m == "" || !write) {
			t.Errorf("%s: id %q model %q write %v, want the model ticket", c.name, id, m, write)
		}
		if !c.model && (id != "" || m != "" || write) {
			t.Errorf("%s: id %q model %q write %v, want no ticket", c.name, id, m, write)
		}
	}
	for _, c := range []*rvnodes.ModelClass{{ParamsCore: 1777088000}, nil} {
		if note := p.modelMismatch(rvnodes.Service{ModelHash: qwen14, ModelClass: c}); note == "" {
			t.Errorf("a size that does not fit left no note for the log (class %v)", c)
		}
	}
}

// A service whose size does not fit is not a model service of its node, so it
// cannot be the one the node is paid for.
func TestNodeModelSkipsAServiceThatDoesNotFit(t *testing.T) {
	other := "sha256:" + strings.Repeat("c3", 32)
	f := newFakeChain(t,
		rowFor(1, zeroHash, "0"),
		rowFor(2, "0x"+qwen14Hex, "14768307200"),
		rowFor(3, "0x"+strings.Repeat("c3", 32), "7090000000"))
	p := &Prober{cfg: Config{NodeBridgeAddr: f.srv.URL}, http: f.srv.Client()}
	p.refreshModels()

	liar := rvnodes.Service{Name: "llm-a", ModelHash: qwen14, ModelClass: &rvnodes.ModelClass{ParamsCore: 1777088000}}  // budget 2, wrong size
	honest := rvnodes.Service{Name: "llm-b", ModelHash: other, ModelClass: &rvnodes.ModelClass{ParamsCore: 7090000000}} // budget 3, fits
	node := rvnodes.Node{Services: []rvnodes.Service{liar, honest}}

	if _, _, id, write := p.nodeTicketBudget(Target{Node: node, Service: honest}); id != "3" || !write {
		t.Errorf("the service that fits must carry the ticket: id %q write %v", id, write)
	}
	if _, _, _, write := p.nodeTicketBudget(Target{Node: node, Service: liar}); write {
		t.Error("the service that does not fit wrote a ticket next to the one that does")
	}
}

// 🔴 With the system budget spent out no ticket is written for a plain node,
// and a model whose own budget ran out falls back to that plain ticket (here:
// no ticket either). A model that still has its budget keeps earning.
func TestTicketBudgetStopsWhenSpentOut(t *testing.T) {
	f := newFakeChain(t, rowFor(2, "0x"+qwen14Hex, "14768307200")) // no live system budget
	p := &Prober{cfg: Config{NodeBridgeAddr: f.srv.URL}, http: f.srv.Client()}
	p.refreshModels()
	if _, _, id, write := p.ticketBudget(qwenSvc()); id != "2" || !write {
		t.Errorf("a model with a live budget stopped: %q %v", id, write)
	}
	if _, _, _, write := p.ticketBudget(rvnodes.Service{}); write {
		t.Error("a plain ticket was written with no live system budget")
	}
	if _, _, _, write := p.ticketBudget(rvnodes.Service{ModelHash: "sha256:" + strings.Repeat("ab", 32)}); write {
		t.Error("a model with no budget got a ticket with no system budget")
	}
}

// The ticket names the deployment the RV sent in the assignment, and only an
// RV that sent none leaves the config in charge.
func TestTicketDeployment(t *testing.T) {
	cfg := Config{ChainID: 7, FaucetAddr: "0x" + strings.Repeat("0a", 20)}
	a := Assignment{ChainID: 1339, FaucetAddr: "0x610178dA211FEF7D417bC0e6FeD39F05609AD788"}
	chainID, addr := ticketDeployment(a, cfg)
	if chainID != 1339 || addr.Hex() != "0x610178da211fef7d417bc0e6fed39f05609ad788" {
		t.Errorf("from the assignment: %d %s", chainID, addr.Hex())
	}
	chainID, addr = ticketDeployment(Assignment{}, cfg)
	if chainID != 7 || addr.Hex() != cfg.FaucetAddr {
		t.Errorf("older RV, from the config: %d %s", chainID, addr.Hex())
	}
	if chainID, addr = ticketDeployment(Assignment{}, Config{}); chainID != 0 || addr != (faucet.Addr{}) {
		t.Errorf("nothing anywhere: %d %s, want zeros", chainID, addr.Hex())
	}
}
