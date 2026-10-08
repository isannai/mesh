package probe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/isannai/mesh/pkg/faucet"
	"github.com/isannai/mesh/pkg/rvnodes"
)

var (
	qwen14Hex = strings.Repeat("e0", 32)
	qwen14    = "sha256:" + qwen14Hex
)

// fakeChain stands in for both the local isannd (/internal/api/info) and the
// chain query API (/v1/settings), on one server. Rows are served two per page
// so the cursor is exercised.
type fakeChain struct {
	srv   *httptest.Server
	rows  []map[string]any
	down  atomic.Bool
	noAPI bool
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
		case "/v1/settings":
			if f.down.Load() {
				http.Error(w, "down", http.StatusBadGateway)
				return
			}
			q := r.URL.Query()
			if q.Get("contract") != "OPERATOR_CONFIG" || q.Get("namePrefix") != ModelSettingPrefix {
				http.Error(w, "unexpected filter "+r.URL.RawQuery, http.StatusBadRequest)
				return
			}
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

func settingFor(hash string, value any) map[string]any {
	return map[string]any{"contract": "0x9fe4", "contractName": "OPERATOR_CONFIG",
		"name": ModelSettingPrefix + hash, "value": value}
}

func TestFetchDesignated(t *testing.T) {
	ab := strings.Repeat("ab", 32)
	cd := strings.Repeat("cd", 32)
	f := newFakeChain(t,
		settingFor(qwen14Hex, "14768307200"),       // a uint arrives as a decimal string
		settingFor(ab, 7615616512),                 // a plain number is read too
		settingFor(cd, "0"),                        // 0 takes a file off the list
		settingFor("not-a-hash", "14768307200"),    // a bad entry is skipped, not fatal
		settingFor(strings.Repeat("ef", 32), "14"), // a count in billions by mistake
	)
	got, err := FetchDesignated(f.srv.URL, f.srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"0x" + qwen14Hex: "14.768", "0x" + ab: "7.615"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
}

func TestFetchDesignatedNeedsAnAPI(t *testing.T) {
	f := newFakeChain(t, settingFor(qwen14Hex, "14768307200"))
	f.noAPI = true
	if _, err := FetchDesignated(f.srv.URL, f.srv.Client()); err == nil {
		t.Error("an isannd naming no API produced a list")
	}
}

// 🔴 The list never takes a ticket away. A listed model gets the model fields,
// anything else a plain ticket, and so does every node while no list is known.
func TestTicketModelNeverRefuses(t *testing.T) {
	f := newFakeChain(t, settingFor(qwen14Hex, "14768307200"))
	p := &Prober{cfg: Config{NodeBridgeAddr: f.srv.URL}, http: f.srv.Client()}

	if m, params := p.ticketModel(rvnodes.Service{ModelHash: qwen14}); m != "" || params != "" {
		t.Errorf("before any read: %q %q, want a plain ticket", m, params)
	}
	if s := p.designatedSummary(nil); s != "" {
		t.Errorf("before any read the log says %q", s)
	}

	p.refreshModels()
	m, params := p.ticketModel(rvnodes.Service{ModelHash: qwen14})
	if m != "0x"+qwen14Hex || params != "14.768" {
		t.Errorf("listed: %q %q", m, params)
	}
	for _, h := range []string{"", "sha256:" + strings.Repeat("ab", 32), "hf-index"} {
		if m, _ := p.ticketModel(rvnodes.Service{ModelHash: h}); m != "" {
			t.Errorf("%q got model fields", h)
		}
	}
	targets := []Target{{Service: rvnodes.Service{ModelHash: qwen14}}, {Service: rvnodes.Service{}}}
	if s := p.designatedSummary(targets); s != ", 1 on the designated model list (1 listed)" {
		t.Errorf("summary = %q", s)
	}

	// One failed read keeps the list it had.
	f.down.Store(true)
	p.refreshModels()
	if m, _ := p.ticketModel(rvnodes.Service{ModelHash: qwen14}); m == "" {
		t.Error("a failed read dropped the list")
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
