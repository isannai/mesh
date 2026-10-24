package probe

// probeinfo_test.go: the look at a node's own /probe/info before its ticket
// (probeinfo.go, gpugrade.go), and the settle that uses it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var otherModel = "sha256:" + strings.Repeat("ab", 32)

func llm(hash string) ProbeInfoService {
	return ProbeInfoService{Name: "llm-api", Engine: "llama", ModelHash: hash}
}

func gpu(name string, gb float64) ProbeInfoGPU { return ProbeInfoGPU{Name: name, MemoryGB: gb} }

func TestLiveCheck(t *testing.T) {
	budget := "0x" + qwen14Hex
	mine := "sha256:" + qwen14Hex
	rtx := func(gb float64) []ProbeInfoGPU { return []ProbeInfoGPU{gpu("NVIDIA GeForce RTX 4090", gb)} }
	cases := []struct {
		name     string
		info     NodeProbeInfo
		minGrade int
		want     string
	}{
		{"fits", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}, GPUs: rtx(24)}, 7, ""},
		{"the hash in the 0x spelling", NodeProbeInfo{Services: []ProbeInfoService{llm("0x" + qwen14Hex)}, GPUs: rtx(24)}, 0, ""},
		{"another model", NodeProbeInfo{Services: []ProbeInfoService{llm(otherModel)}, GPUs: rtx(24)}, 0, liveModel},
		{"no service at all", NodeProbeInfo{Services: []ProbeInfoService{}, GPUs: rtx(24)}, 0, liveModel},
		{"a second LLM next to the budget's", NodeProbeInfo{Services: []ProbeInfoService{llm(mine),
			{Name: "vllm-api", Engine: "vllm", ModelHash: otherModel}}}, 0, liveExtraLLM},
		{"a second LLM of the same model is not another one", NodeProbeInfo{Services: []ProbeInfoService{llm(mine),
			{Name: "vllm-api", Engine: "vllm", ModelHash: mine}}}, 0, ""},
		{"an LLM known by its name only", NodeProbeInfo{Services: []ProbeInfoService{llm(mine),
			{Name: " VLLM-API ", ModelHash: otherModel}}}, 0, liveExtraLLM},
		{"an LLM known by its engine only, spelled loosely", NodeProbeInfo{Services: []ProbeInfoService{llm(mine),
			{Name: "chat", Engine: " Llama ", ModelHash: otherModel}}}, 0, liveExtraLLM},
		{"an image engine next to it is not counted", NodeProbeInfo{Services: []ProbeInfoService{llm(mine),
			{Name: "sd-api", Engine: "sd", ModelHash: otherModel}}, GPUs: rtx(24)}, 7, ""},
		{"an LLM with no hash is a stopped engine and is left out", NodeProbeInfo{Services: []ProbeInfoService{llm(mine),
			{Name: "vllm-api", Engine: "vllm", ModelHash: ""}}}, 0, ""},
		{"a whitespace hash is empty too", NodeProbeInfo{Services: []ProbeInfoService{llm(mine), llm("  ")}}, 0, ""},
		{"the budget's model is the stopped one", NodeProbeInfo{Services: []ProbeInfoService{llm(""),
			{Name: "vllm-api", Engine: "vllm", ModelHash: otherModel}}}, 0, liveModel},
		{"a hash that cannot be read counts as another model", NodeProbeInfo{Services: []ProbeInfoService{llm(mine), llm("sha256:xyz")}}, 0, liveExtraLLM},

		{"the GPU is below the grade", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}, GPUs: rtx(24)}, 8, liveGPUGrade},
		{"the GPU is at the grade", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}, GPUs: rtx(24)}, 7, ""},
		{"no GPU when a grade is asked", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}}, 1, liveGPUGrade},
		{"under 2 GB has no grade", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}, GPUs: rtx(1.9)}, 1, liveGPUGrade},
		{"GPUs add up", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}, GPUs: append(rtx(24), gpu("NVIDIA GeForce RTX 4090", 24))}, 9, ""},
		{"GPUs add up, still short", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}, GPUs: append(rtx(24), gpu("NVIDIA GeForce RTX 4090", 24))}, 10, liveGPUGrade},
		{"a GPU with no figure shares the RAM", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)},
			GPUs: []ProbeInfoGPU{gpu("NVIDIA GB10", 0)}, RAMGB: 128}, 10, ""},
		{"a GPU with no figure shares the RAM, short", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)},
			GPUs: []ProbeInfoGPU{gpu("NVIDIA GB10", 0)}, RAMGB: 128}, 11, liveGPUGrade},
		{"the RAM is added once for two such GPUs", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)},
			GPUs: []ProbeInfoGPU{gpu("NVIDIA GB10", 0), gpu("NVIDIA GB10", 0)}, RAMGB: 64}, 10, liveGPUGrade},
		{"the RAM stands for a GPU with a figure only when another has none", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)},
			GPUs: rtx(8), RAMGB: 128}, 4, liveGPUGrade},
		{"RAM with no GPU is nothing", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}, RAMGB: 256}, 1, liveGPUGrade},
		{"a nameless GPU with no figure is not a GPU", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)},
			GPUs: []ProbeInfoGPU{gpu("  ", 0)}, RAMGB: 128}, 1, liveGPUGrade},
		{"rounded to 0.1 GB before the grade (4.04 is 4.0)", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}, GPUs: rtx(4.04)}, 2, liveGPUGrade},
		{"rounded to 0.1 GB before the grade (4.06 is 4.1)", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}, GPUs: rtx(4.06)}, 2, ""},
		{"no grade asked, the GPU is not looked at", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}}, 0, ""},
		{"a grade above the highest is read as the highest", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}, GPUs: rtx(300)}, 99, ""},
		{"a grade above the highest, a node short of it", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}, GPUs: rtx(200)}, 99, liveGPUGrade},
		{"a negative grade is no limit", NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}}, -3, ""},
	}
	for _, c := range cases {
		if got := liveCheck(c.info, liveWant{Model: budget, MinGrade: c.minGrade}); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	// A budget model that cannot be read matches nothing.
	if got := liveCheck(NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}}, liveWant{Model: "not a hash"}); got != liveModel {
		t.Errorf("an unreadable budget model: %q, want %q", got, liveModel)
	}
	// The budget's model in the report spelling matches too.
	if got := liveCheck(NodeProbeInfo{Services: []ProbeInfoService{llm(mine)}}, liveWant{Model: mine}); got != "" {
		t.Errorf("the budget model as sha256:...: %q, want a fit", got)
	}
}

func boolPtr(b bool) *bool { return &b }

// The budget's TPM and Name-service conditions: refused only when the group asks AND
// the node says false. A node that does not report the field is left to the RV's
// check, like a node with no door.
func TestLiveCheckTPMAndName(t *testing.T) {
	budget := "0x" + qwen14Hex
	mine := "sha256:" + qwen14Hex
	fit := func(tpm, named *bool) NodeProbeInfo {
		return NodeProbeInfo{Services: []ProbeInfoService{llm(mine)},
			GPUs: []ProbeInfoGPU{gpu("NVIDIA GeForce RTX 4090", 24)}, TPMVerified: tpm, Named: named}
	}
	yes, no := boolPtr(true), boolPtr(false)
	cases := []struct {
		name string
		info NodeProbeInfo
		w    liveWant
		want string
	}{
		{"TPM asked, reported false", fit(no, nil), liveWant{RequireTPM: true}, liveTPM},
		{"TPM asked, reported true", fit(yes, nil), liveWant{RequireTPM: true}, ""},
		{"TPM asked, not reported", fit(nil, nil), liveWant{RequireTPM: true}, ""},
		{"TPM not asked, reported false", fit(no, nil), liveWant{}, ""},
		{"TPM not asked, not reported", fit(nil, nil), liveWant{}, ""},

		{"name asked, reported false", fit(nil, no), liveWant{RequireNamed: true}, liveNamed},
		{"name asked, reported true", fit(nil, yes), liveWant{RequireNamed: true}, ""},
		{"name asked, not reported", fit(nil, nil), liveWant{RequireNamed: true}, ""},
		{"name not asked, reported false", fit(nil, no), liveWant{}, ""},

		{"both asked, both true", fit(yes, yes), liveWant{RequireTPM: true, RequireNamed: true}, ""},
		{"both asked, both false says TPM first", fit(no, no), liveWant{RequireTPM: true, RequireNamed: true}, liveTPM},
		{"both asked, TPM true, name false", fit(yes, no), liveWant{RequireTPM: true, RequireNamed: true}, liveNamed},
		{"both asked, TPM false, name true", fit(no, yes), liveWant{RequireTPM: true, RequireNamed: true}, liveTPM},
		{"both asked, TPM not reported, name false", fit(nil, no), liveWant{RequireTPM: true, RequireNamed: true}, liveNamed},
		{"only the asked one counts", fit(no, yes), liveWant{RequireNamed: true}, ""},

		// They come after the model, LLM and GPU rules.
		{"another model is said before TPM", NodeProbeInfo{Services: []ProbeInfoService{llm(otherModel)}, TPMVerified: no},
			liveWant{RequireTPM: true}, liveModel},
		{"a GPU below the grade is said before TPM", fit(no, no), liveWant{MinGrade: 8, RequireTPM: true}, liveGPUGrade},
		{"a GPU below the grade is said before name", fit(nil, no), liveWant{MinGrade: 8, RequireNamed: true}, liveGPUGrade},
	}
	for _, c := range cases {
		w := c.w
		w.Model = budget
		if got := liveCheck(c.info, w); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// tpm_verified and named are optional in the /probe/info body: absent is nil, not
// false.
func TestProbeInfoReadsTPMAndNamedAsOptional(t *testing.T) {
	door := &doorServer{reply: func(node string) (int, any) {
		switch node {
		case "S:true":
			return http.StatusOK, `{"services":[],"gpus":[],"ram_gb":0,"tpm_verified":true,"named":true}`
		case "S:false":
			return http.StatusOK, `{"services":[],"gpus":[],"ram_gb":0,"tpm_verified":false,"named":false}`
		case "S:old":
			return http.StatusOK, `{"services":[],"gpus":[],"ram_gb":0}`
		}
		return http.StatusTeapot, nil
	}}
	srv := httptest.NewServer(door)
	t.Cleanup(srv.Close)
	f := NewFirer(srv.URL)
	show := func(b *bool) string {
		if b == nil {
			return "absent"
		}
		return fmt.Sprint(*b)
	}
	for _, c := range []struct {
		node       string
		tpm, named string
	}{
		{"S:true", "true", "true"},
		{"S:false", "false", "false"},
		{"S:old", "absent", "absent"},
	} {
		info, err := f.ProbeInfo(c.node)
		if err != nil {
			t.Fatalf("%s: %v", c.node, err)
		}
		if got := show(info.TPMVerified); got != c.tpm {
			t.Errorf("%s: tpm_verified read as %s, want %s", c.node, got, c.tpm)
		}
		if got := show(info.Named); got != c.named {
			t.Errorf("%s: named read as %s, want %s", c.node, got, c.named)
		}
	}
}

func TestGPUGrade(t *testing.T) {
	cases := []struct {
		gb   float64
		want int
	}{
		{0, 0}, {1.9, 0}, {2, 1}, {4, 1}, {4.1, 2}, {6, 2}, {6.1, 3}, {8, 3}, {10, 4}, {12, 5}, {16, 6}, {24, 7},
		{32, 8}, {64, 9}, {128, 10}, {256, 11}, {256.1, 12}, {1000, 12},
	}
	for _, c := range cases {
		if got := gpuGrade(c.gb); got != c.want {
			t.Errorf("gpuGrade(%v) = %d, want %d", c.gb, got, c.want)
		}
	}
}

// doorServer is a node bridge whose /probe/info answers as the test says. It keeps
// how often each node was asked.
type doorServer struct {
	mu    sync.Mutex
	reply func(node string) (status int, body any)
	asked map[string]int
	hold  chan struct{} // when set, an answer waits until it is closed
}

func (d *doorServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if !strings.HasSuffix(path, "/probe/info") {
		http.NotFound(w, r)
		return
	}
	node := strings.TrimSuffix(strings.TrimPrefix(path, "/node/"), "/probe/info")
	d.mu.Lock()
	if d.asked == nil {
		d.asked = map[string]int{}
	}
	d.asked[node]++
	hold := d.hold
	d.mu.Unlock()
	if hold != nil {
		<-hold
	}
	status, body := d.reply(node)
	w.WriteHeader(status)
	switch b := body.(type) {
	case string:
		_, _ = w.Write([]byte(b))
	case nil:
	default:
		_ = json.NewEncoder(w).Encode(b)
	}
}

func (d *doorServer) count(node string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.asked[node]
}

func (d *doorServer) total() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, c := range d.asked {
		n += c
	}
	return n
}

func TestProbeInfoTellsTheAnswersApart(t *testing.T) {
	want := NodeProbeInfo{
		Services: []ProbeInfoService{{Name: "llm-api", Engine: "llama", ModelHash: "sha256:" + qwen14Hex}},
		GPUs:     []ProbeInfoGPU{{Name: "NVIDIA GeForce RTX 4090", MemoryGB: 24}},
		RAMGB:    64,
	}
	door := &doorServer{reply: func(node string) (int, any) {
		switch node {
		case "S:ok":
			return http.StatusOK, want
		case "S:nodoor":
			return http.StatusNotFound, "not found"
		case "S:nomethod":
			return http.StatusMethodNotAllowed, "GET only"
		case "S:broken":
			return http.StatusInternalServerError, "boom"
		case "S:gateway":
			return http.StatusBadGateway, nil
		case "S:garbage":
			return http.StatusOK, "<html>not json</html>"
		case "S:nolist":
			return http.StatusOK, "{}"
		case "S:nullist":
			return http.StatusOK, `{"services":null,"gpus":[],"ram_gb":0}`
		case "S:empty":
			return http.StatusOK, `{"services":[],"gpus":[],"ram_gb":0}`
		}
		return http.StatusTeapot, nil
	}}
	srv := httptest.NewServer(door)
	t.Cleanup(srv.Close)
	f := NewFirer(srv.URL)

	got, err := f.ProbeInfo("S:ok")
	if err != nil {
		t.Fatalf("a 200: %v", err)
	}
	if len(got.Services) != 1 || got.Services[0] != want.Services[0] || len(got.GPUs) != 1 || got.GPUs[0] != want.GPUs[0] || got.RAMGB != 64 {
		t.Errorf("a 200: %+v, want %+v", got, want)
	}
	if info, err := f.ProbeInfo("S:empty"); err != nil || len(info.Services) != 0 {
		t.Errorf("a node that runs nothing: %+v, %v, want an answer with no services", info, err)
	}
	for _, id := range []string{"S:nodoor", "S:nomethod"} {
		if _, err := f.ProbeInfo(id); !errors.Is(err, errNoProbeDoor) {
			t.Errorf("%s: %v, want errNoProbeDoor", id, err)
		}
	}
	for _, id := range []string{"S:broken", "S:gateway", "S:garbage", "S:nolist", "S:nullist", "S:other"} {
		_, err := f.ProbeInfo(id)
		if err == nil || errors.Is(err, errNoProbeDoor) {
			t.Errorf("%s: %v, want an error that is not errNoProbeDoor", id, err)
		}
	}
	// A server that is not there at all is a transport error.
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	if _, err := NewFirer(url).ProbeInfo("S:ok"); err == nil || errors.Is(err, errNoProbeDoor) {
		t.Errorf("a refused connection: %v, want a transport error", err)
	}
}

// A node that never answers costs the timeout and no more.
func TestProbeInfoGivesUpAfterItsTimeout(t *testing.T) {
	old := probeInfoTimeout
	probeInfoTimeout = 100 * time.Millisecond
	t.Cleanup(func() { probeInfoTimeout = old })

	door := &doorServer{hold: make(chan struct{}), reply: func(string) (int, any) { return http.StatusOK, nil }}
	srv := httptest.NewServer(door)
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(door.hold) }) // runs before srv.Close, which waits for the held request

	start := time.Now()
	_, err := NewFirer(srv.URL).ProbeInfo("S:slow")
	if err == nil || errors.Is(err, errNoProbeDoor) {
		t.Fatalf("a node that does not answer: %v, want a transport error", err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("waited %v for a 100ms timeout", took)
	}
}

// --- settle ---------------------------------------------------------------

// livePlanned is compareProber with the slot planned by budget: its one group of n
// nodes is on budget 2 with the 14.768B model (so every node's directory model
// matches), asked about by door.
func livePlanned(t *testing.T, n, minGrade int, door http.Handler) *Prober {
	t.Helper()
	srv := httptest.NewServer(door)
	t.Cleanup(srv.Close)
	p := compareProber(t, n)
	p.firer = NewFirer(srv.URL)
	g := &p.assign.Groups[0]
	g.Mode, g.BudgetID, g.Model, g.MinGrade = ModeQuiz, 2, "0x"+qwen14Hex, minGrade
	return p
}

func okInfo(gb float64) NodeProbeInfo {
	return NodeProbeInfo{
		Services: []ProbeInfoService{llm("sha256:" + qwen14Hex)},
		GPUs:     []ProbeInfoGPU{{Name: "NVIDIA GeForce RTX 4090", MemoryGB: gb}},
	}
}

func decisionOf(t *testing.T, p *Prober, i int) string {
	t.Helper()
	var d string
	err := p.store.db.QueryRow(`SELECT decision FROM compare WHERE epoch = ? AND node_id = ? AND round = 0`,
		compareEpoch, p.targets[i].Node.ID).Scan(&d)
	if err != nil {
		return "none"
	}
	return d
}

// 🔴 A node of a model budget is asked what it runs now. One that no longer fits
// gets no ticket (and is on record as refused); one with no door, or one that gives
// a server error, keeps the check the RV made.
func TestSettleLooksAtWhatTheNodeRunsNow(t *testing.T) {
	var p *Prober
	door := &doorServer{}
	door.reply = func(node string) (int, any) {
		idx := -1
		for i, tg := range p.targets {
			if node == tg.Node.ID {
				idx = i
			}
		}
		switch idx {
		case 0:
			return http.StatusOK, okInfo(24)
		case 1:
			info := okInfo(24)
			info.Services[0].ModelHash = otherModel // changed its model
			return http.StatusOK, info
		case 2:
			info := okInfo(24)
			info.Services = append(info.Services, ProbeInfoService{Name: "vllm-api", Engine: "vllm", ModelHash: otherModel})
			return http.StatusOK, info
		case 3:
			return http.StatusOK, okInfo(12) // below grade 7
		case 4:
			return http.StatusNotFound, "not found" // an older isannd
		case 5:
			return http.StatusInternalServerError, "boom"
		case 6:
			info := okInfo(24)
			info.Services = append(info.Services, ProbeInfoService{Name: "sd-api", Engine: "sd", ModelHash: otherModel})
			return http.StatusOK, info // an image engine alongside is fine
		}
		return http.StatusTeapot, nil
	}
	p = livePlanned(t, 8, 7, door)
	var issued []string
	issuedBy(p, &issued)
	quizBoth(t, p, 0, 1, 2, 3, 4, 5, 6)
	// Node 7 failed its quiz: no need to look at it at all.
	for r := 1; r <= SampleRounds; r++ {
		quizShot(t, p, 7, r, VerdictFail)
	}

	p.settleTickets(settleAt())
	if got, want := sortedIDs(issued), nodeIDs(p, 0, 4, 5, 6); got != want {
		t.Errorf("issued %v, want nodes 0, 4, 5 and 6 (fits, no door, server error, image engine alongside)", issued)
	}
	for i, want := range []string{DecisionWin, DecisionLose, DecisionLose, DecisionLose, DecisionWin, DecisionWin, DecisionWin, DecisionLose} {
		if got := decisionOf(t, p, i); got != want {
			t.Errorf("node %d's record = %q, want %q", i, got, want)
		}
	}
	if n := door.count(p.targets[7].Node.ID); n != 0 {
		t.Errorf("the node that failed its quiz was asked %d times, want 0", n)
	}

	// A later tick decides nobody again, so nobody is asked again.
	before := door.total()
	p.settleTickets(settleAt().Add(time.Minute))
	if door.total() != before || len(issued) != 4 {
		t.Errorf("a second tick asked %d more times and issued %d in all, want no more of either", door.total()-before, len(issued))
	}
	if before != 7 {
		t.Errorf("%d nodes were asked, want one call each for the 7 that passed their quiz", before)
	}
}

// 🔴 A budget that asks for TPM and a Name-service subdomain: a node that says it has
// neither gets no ticket; one that says yes, one that does not report the field and
// one with no door keep the check the RV made. The same nodes under a group that asks
// for nothing are all paid.
func TestSettleHonoursTPMAndNamedConditions(t *testing.T) {
	yes, no := boolPtr(true), boolPtr(false)
	build := func(requireTPM, requireNamed bool) (*Prober, *[]string) {
		var p *Prober
		door := &doorServer{}
		door.reply = func(node string) (int, any) {
			idx := -1
			for i, tg := range p.targets {
				if node == tg.Node.ID {
					idx = i
				}
			}
			info := okInfo(24)
			switch idx {
			case 0: // both verified
				info.TPMVerified, info.Named = yes, yes
			case 1: // no TPM
				info.TPMVerified, info.Named = no, yes
			case 2: // no subdomain
				info.TPMVerified, info.Named = yes, no
			case 3: // an isannd that sends neither field
			case 4:
				return http.StatusNotFound, "not found" // an older isannd
			default:
				return http.StatusTeapot, nil
			}
			return http.StatusOK, info
		}
		p = livePlanned(t, 5, 7, door)
		g := &p.assign.Groups[0]
		g.RequireTPM, g.RequireNamed = requireTPM, requireNamed
		var issued []string
		issuedBy(p, &issued)
		quizBoth(t, p, 0, 1, 2, 3, 4)
		p.settleTickets(settleAt())
		return p, &issued
	}

	p, issued := build(true, true)
	if got, want := sortedIDs(*issued), nodeIDs(p, 0, 3, 4); got != want {
		t.Errorf("issued %v, want nodes 0, 3 and 4 (verified and named, nothing reported, no door)", *issued)
	}
	for i, want := range []string{DecisionWin, DecisionLose, DecisionLose, DecisionWin, DecisionWin} {
		if got := decisionOf(t, p, i); got != want {
			t.Errorf("both asked: node %d's record = %q, want %q", i, got, want)
		}
	}

	p, issued = build(true, false)
	if got, want := sortedIDs(*issued), nodeIDs(p, 0, 2, 3, 4); got != want {
		t.Errorf("TPM only: issued %v, want nodes 0, 2, 3 and 4", *issued)
	}

	p, issued = build(false, true)
	if got, want := sortedIDs(*issued), nodeIDs(p, 0, 1, 3, 4); got != want {
		t.Errorf("name only: issued %v, want nodes 0, 1, 3 and 4", *issued)
	}

	p, issued = build(false, false)
	if got, want := sortedIDs(*issued), nodeIDs(p, 0, 1, 2, 3, 4); got != want {
		t.Errorf("nothing asked: issued %v, want all five", *issued)
	}
}

// A system budget (no model) and a slot from an older RV are not asked: nothing
// registered them for a model.
func TestSettleDoesNotLookAtASystemGroup(t *testing.T) {
	for _, c := range []struct {
		name  string
		shape func(g *AssignGroup)
	}{
		{"system budget", func(g *AssignGroup) { g.Mode, g.BudgetID, g.Model = ModeQuiz, 1, "" }},
		{"system budget with the conditions set", func(g *AssignGroup) {
			g.Mode, g.BudgetID, g.Model = ModeQuiz, 1, ""
			g.RequireTPM, g.RequireNamed = true, true
		}},
		{"an older RV, no mode", func(g *AssignGroup) { *g = AssignGroup{Members: g.Members} }},
	} {
		door := &doorServer{reply: func(string) (int, any) { return http.StatusOK, NodeProbeInfo{Services: []ProbeInfoService{}} }}
		p := livePlanned(t, 2, 7, door)
		c.shape(&p.assign.Groups[0])
		var issued []string
		issuedBy(p, &issued)
		quizBoth(t, p, 0, 1)

		p.settleTickets(settleAt())
		if len(issued) != 2 {
			t.Errorf("%s: issued %v, want both", c.name, issued)
		}
		if door.total() != 0 {
			t.Errorf("%s: %d nodes were asked, want none", c.name, door.total())
		}
	}
}

// A door that does not answer in time does not hold the loop: each node costs its
// timeout once, and its ticket is still issued.
func TestSettleDoesNotWaitLongOnADeadDoor(t *testing.T) {
	old := probeInfoTimeout
	probeInfoTimeout = 100 * time.Millisecond
	t.Cleanup(func() { probeInfoTimeout = old })

	door := &doorServer{hold: make(chan struct{}), reply: func(string) (int, any) { return http.StatusOK, nil }}
	p := livePlanned(t, 3, 0, door)
	t.Cleanup(func() { close(door.hold) }) // after the server is registered for close, so it runs first
	var issued []string
	issuedBy(p, &issued)
	quizBoth(t, p, 0, 1, 2)

	start := time.Now()
	p.settleTickets(settleAt())
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("settling 3 dead doors took %v, want about 3 timeouts of 100ms", took)
	}
	if len(issued) != 3 {
		t.Errorf("issued %v, want all three: a door that does not answer does not turn a node away", issued)
	}
	for i := 0; i < 3; i++ {
		if n := door.count(p.targets[i].Node.ID); n != 1 {
			t.Errorf("node %d was asked %d times, want once", i, n)
		}
	}
}

// What a ticket carries from a planned quiz group: the group's budget and model, and
// nothing for a node on another model or a slot with no system budget.
func TestTicketForAPlannedQuizGroup(t *testing.T) {
	p := compareProber(t, 2)
	g := &p.assign.Groups[0]
	g.Mode, g.BudgetID, g.Model = ModeQuiz, 2, "0x"+qwen14Hex

	model, _, budget, write, _ := p.ticketFor(p.targets[0])
	if !write || model != "0x"+qwen14Hex || budget != "2" {
		t.Errorf("ticket = %q %q %v, want budget 2 and its model", model, budget, write)
	}
	other := p.targets[0]
	other.Service.ModelHash = otherModel
	if _, _, _, write, why := p.ticketFor(other); write || why == "" {
		t.Errorf("a node on another model: write %v (%q), want no ticket and a reason", write, why)
	}
	g.Model = ""
	if model, _, budget, write, _ := p.ticketFor(p.targets[0]); !write || model != "" || budget != "" {
		t.Errorf("system group ticket = %q %q %v, want a plain ticket", model, budget, write)
	}
	g.BudgetID = 0
	if _, _, _, write, why := p.ticketFor(p.targets[0]); write || why == "" {
		t.Errorf("no system budget: write %v (%q), want no ticket and a reason", write, why)
	}
}

// The group as the RV sends it now, and as it sent it before: fields that are gone
// are ignored, a missing min_grade is no limit.
func TestAssignGroupReadsTheNewShapeAndIgnoresTheOld(t *testing.T) {
	var g AssignGroup
	body := `{"members":["0xa"],"probers":["0xb"],"path":[],"budget_id":2,"model":"0x` + qwen14Hex +
		`","min_grade":7,"mode":"quiz","gpu":"RTX","refs":["0xa"],"engine":"llama","profile":"p","meta_hash":"0x11"}`
	if err := json.Unmarshal([]byte(body), &g); err != nil {
		t.Fatal(err)
	}
	if g.BudgetID != 2 || g.Model != "0x"+qwen14Hex || g.MinGrade != 7 || g.Mode != ModeQuiz || g.MetaHash != "0x11" {
		t.Errorf("group = %+v", g)
	}
	if g.CheckMode() != ModeQuiz {
		t.Errorf("mode = %q, want quiz", g.CheckMode())
	}
	var bare AssignGroup
	if err := json.Unmarshal([]byte(`{"members":["0xa"]}`), &bare); err != nil {
		t.Fatal(err)
	}
	if bare.MinGrade != 0 || bare.CheckMode() != ModeQuiz {
		t.Errorf("a group with no extras = %+v, mode %q, want no limit and quiz", bare, bare.CheckMode())
	}
	if (Assignment{Groups: []AssignGroup{bare}}).Planned() {
		t.Error("a group with no mode reads as planned")
	}
	if !(Assignment{Groups: []AssignGroup{g}}).Planned() {
		t.Error("a group with mode quiz does not read as planned")
	}
	// Neither of the two optional conditions is in this group, as from an older RV.
	if g.RequireTPM || g.RequireNamed || bare.RequireTPM || bare.RequireNamed {
		t.Errorf("a group without require_tpm / require_named asks for them: %+v, %+v", g, bare)
	}
}

// require_tpm and require_named are read when the RV sends them, are false when it
// does not, and are left out of the JSON when false.
func TestAssignGroupReadsTPMAndNamedConditions(t *testing.T) {
	var both, tpm, named, none AssignGroup
	for body, g := range map[string]*AssignGroup{
		`{"members":["0xa"],"budget_id":2,"model":"0x11","mode":"quiz","require_tpm":true,"require_named":true}`: &both,
		`{"members":["0xa"],"budget_id":2,"model":"0x11","mode":"quiz","require_tpm":true}`:                      &tpm,
		`{"members":["0xa"],"budget_id":2,"model":"0x11","mode":"quiz","require_named":true}`:                    &named,
		`{"members":["0xa"],"budget_id":2,"model":"0x11","mode":"quiz","min_grade":7}`:                           &none,
	} {
		if err := json.Unmarshal([]byte(body), g); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name       string
		g          AssignGroup
		tpm, named bool
	}{
		{"both", both, true, true}, {"TPM only", tpm, true, false},
		{"name only", named, false, true}, {"an old group", none, false, false},
	} {
		if c.g.RequireTPM != c.tpm || c.g.RequireNamed != c.named {
			t.Errorf("%s: require_tpm %v, require_named %v, want %v, %v", c.name, c.g.RequireTPM, c.g.RequireNamed, c.tpm, c.named)
		}
		if w := c.g.liveWant(); w.RequireTPM != c.tpm || w.RequireNamed != c.named || w.Model != "0x11" {
			t.Errorf("%s: liveWant = %+v", c.name, w)
		}
	}
	// Written back, the names are the RV's and a false value is not written.
	out, err := json.Marshal(both)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"require_tpm":true`) || !strings.Contains(string(out), `"require_named":true`) {
		t.Errorf("group written as %s, want require_tpm and require_named", out)
	}
	out, err = json.Marshal(none)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "require_") {
		t.Errorf("an old group written as %s, want no require_ fields", out)
	}
}
