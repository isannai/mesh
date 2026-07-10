package station

// m1_run_mapping_test.go — M1 검증: 제출 시 generic run-params 가 manifest
// api.run 템플릿을 통해 엔진 body 로 매핑되는지 실제 핸들러+큐를 관통해 확인.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/daesob/http3proxy/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/station/queue"
	"github.com/daesob/http3proxy/pkg/setup"
)

// captureEngine 는 받은 요청의 path 와 body 를 기록하는 mock 엔진이다.
type captureEngine struct {
	mu   sync.Mutex
	path string
	body string
}

func newCaptureEngine() (*captureEngine, *httptest.Server) {
	ce := &captureEngine{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ce.mu.Lock()
		ce.path = r.URL.Path
		ce.body = string(b)
		ce.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	return ce, srv
}

func (ce *captureEngine) seen() (string, string) {
	ce.mu.Lock()
	defer ce.mu.Unlock()
	return ce.path, ce.body
}

// newMappingHandler 는 "sd-api" 에 대해 주어진 RunSpec 을 돌려주는 resolver 를
// 단 핸들러 + httptest 서버를 띄운다.
func newMappingHandler(t *testing.T, engine *httptest.Server, rs *manifest.RunSpec) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := queue.NewManager(ctx, stubFactory(engine))
	services := []setup.ServiceEntry{{Name: "sd-api", Addr: "ignored"}}
	apiFor := func(svc string) *manifest.APISpec {
		if svc == "sd-api" {
			return &manifest.APISpec{Run: rs}
		}
		return nil
	}
	h := NewJobsHandler(mgr, nil, services, apiFor, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// mappingRunSpec — prompt(필수) + steps(default 20) + negative(옵션) 스칼라 셋.
func mappingRunSpec() *manifest.RunSpec {
	return &manifest.RunSpec{
		Path:   "/sdapi/v1/txt2img",
		Result: manifest.ResultSpec{Modality: "image", ContentPath: "images[0]", Encoding: "base64"},
		Params: []manifest.RunParam{
			{Name: "prompt", Type: "string", Required: true},
			{Name: "negative", Type: "string"},
			{Name: "steps", Type: "int", Default: float64(20)},
		},
		Body: json.RawMessage(`{"prompt":"${prompt}","negative_prompt":"${negative}","steps":"${steps}"}`),
	}
}

// TestSubmitRunMapping_EndToEnd: {"run":{prompt,steps}} → 엔진은 매핑된 body 를
// api.run.path 에서 받는다. negative 미지정 → 드롭, steps 명시값이 default 덮음.
func TestSubmitRunMapping_EndToEnd(t *testing.T) {
	ce, engine := newCaptureEngine()
	defer engine.Close()
	srv := newMappingHandler(t, engine, mappingRunSpec())

	reqBody := `{"service":"sd-api","run":{"prompt":"a fox","steps":30},"wait":true}`
	resp, err := http.Post(srv.URL+"/v1/jobs", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, b)
	}

	// 엔진이 매핑된 요청을 받을 때까지 잠깐 대기 (wait=true 라 이미 끝났지만 방어적).
	deadline := time.Now().Add(2 * time.Second)
	var path, body string
	for time.Now().Before(deadline) {
		path, body = ce.seen()
		if path != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if path != "/sdapi/v1/txt2img" {
		t.Errorf("engine path = %q, want /sdapi/v1/txt2img (api.run.path)", path)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("engine body not JSON: %q", body)
	}
	if got["prompt"] != "a fox" {
		t.Errorf("prompt = %v, want 'a fox'", got["prompt"])
	}
	if v, ok := got["steps"].(float64); !ok || v != 30 { // 숫자 타입 + 명시값
		t.Errorf("steps = %v (%T), want 30 number", got["steps"], got["steps"])
	}
	if _, present := got["negative_prompt"]; present { // 옵션 미지정 → 드롭
		t.Errorf("negative_prompt should be dropped, got body=%s", body)
	}
}

// TestSubmitRunMapping_ExtraArgs: api.run.extra_args(prompt_tag) 가 선언된 서비스에
// run 제출 시, 엔진이 받는 body 의 선언 필드(steps)가 top-level 에서 빠져 prompt
// 태그 안으로 들어가고, 비선언 필드(size)는 top-level 에 남는지 e2e 확인.
func TestSubmitRunMapping_ExtraArgs(t *testing.T) {
	ce, engine := newCaptureEngine()
	defer engine.Close()
	rs := &manifest.RunSpec{
		Path:   "/v1/images/generations",
		Result: manifest.ResultSpec{Modality: "image", ContentPath: "data[0].b64_json", Encoding: "base64"},
		Params: []manifest.RunParam{
			{Name: "prompt", Type: "string", Required: true},
			{Name: "steps", Type: "int", Default: float64(20)},
			{Name: "size", Type: "string", Default: "512x512"},
		},
		Body: json.RawMessage(`{"prompt":"${prompt}","steps":"${steps}","size":"${size}"}`),
		ExtraArgs: &manifest.ExtraArgSpec{
			Inject: "prompt", Tag: "sd_cpp_extra_args", Fields: []string{"steps"},
		},
	}
	srv := newMappingHandler(t, engine, rs)

	reqBody := `{"service":"sd-api","run":{"prompt":"a fox","steps":30},"wait":true}`
	resp, err := http.Post(srv.URL+"/v1/jobs", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}

	deadline := time.Now().Add(2 * time.Second)
	var got map[string]any
	for time.Now().Before(deadline) {
		_, body := ce.seen()
		if body != "" {
			_ = json.Unmarshal([]byte(body), &got)
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("engine never received the request")
	}
	if _, present := got["steps"]; present { // 태그로 이동했어야
		t.Errorf("steps should be moved into prompt tag, body=%v", got)
	}
	if got["size"] != "512x512" { // 비선언 필드 유지
		t.Errorf("size should stay top-level, got %v", got["size"])
	}
	prompt, _ := got["prompt"].(string)
	if !strings.Contains(prompt, "<sd_cpp_extra_args>") || !strings.Contains(prompt, "\"steps\":30") {
		t.Errorf("prompt should carry the extra_args tag with steps=30, got %q", prompt)
	}
}

// TestSubmitRunMapping_NoSchema: api.run 없는 서비스에 run 제출 → 400.
func TestSubmitRunMapping_NoSchema(t *testing.T) {
	ce, engine := newCaptureEngine()
	defer engine.Close()
	_ = ce
	srv := newMappingHandler(t, engine, nil) // resolver 가 "sd-api" 에도 nil 반환

	reqBody := `{"service":"sd-api","run":{"prompt":"x"},"wait":true}`
	resp, _ := http.Post(srv.URL+"/v1/jobs", "application/json", strings.NewReader(reqBody))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (no api.run schema)", resp.StatusCode)
	}
}

// TestSubmitRunMapping_RequiredMissing: 필수 prompt 누락 → 400.
func TestSubmitRunMapping_RequiredMissing(t *testing.T) {
	ce, engine := newCaptureEngine()
	defer engine.Close()
	_ = ce
	srv := newMappingHandler(t, engine, mappingRunSpec())

	reqBody := `{"service":"sd-api","run":{"steps":10},"wait":true}`
	resp, _ := http.Post(srv.URL+"/v1/jobs", "application/json", strings.NewReader(reqBody))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (missing required prompt)", resp.StatusCode)
	}
}
