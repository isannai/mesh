package manifest

// runspec_test.go — api.run 치환 엔진(BuildBody) + Validate 검증 (M1).

import (
	"encoding/json"
	"testing"
)

// parseBody 는 BuildBody 결과 JSON 을 비교하기 쉽게 map 으로 푼다.
func parseBody(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal body: %v (%s)", err, raw)
	}
	return m
}

// llamaRun 은 OpenAI chat 형태의 대표 RunSpec — system(옵션) + prompt(필수) +
// 스칼라 파라미터 + 고정 2-원소 messages 배열.
func llamaRun() *RunSpec {
	return &RunSpec{
		Path:   "/v1/chat/completions",
		Result: ResultSpec{Modality: "text", ContentPath: "choices[0].message.content"},
		Params: []RunParam{
			{Name: "system", Type: "string"},
			{Name: "prompt", Type: "string", Required: true},
			{Name: "max-tokens", Type: "int"},
			{Name: "temperature", Type: "float"},
			{Name: "stream", Type: "bool"},
		},
		Body: json.RawMessage(`{
			"messages": [
				{ "role": "system", "content": "${system}" },
				{ "role": "user",   "content": "${prompt}" }
			],
			"max_tokens": "${max-tokens}",
			"temperature": "${temperature}",
			"stream": "${stream}"
		}`),
	}
}

// TestBuildBody_TypeSubstitution: 스칼라가 따옴표 없이 올바른 JSON 타입으로 나간다.
// {"max_tokens":"${max-tokens}"} + max-tokens=128(int) → {"max_tokens":128}.
func TestBuildBody_TypeSubstitution(t *testing.T) {
	rs := llamaRun()
	body, err := rs.BuildBody(map[string]any{
		"prompt":      "hi",
		"max-tokens":  float64(128), // JSON 숫자는 CLI 에서 float64 로 도착
		"temperature": 0.7,
		"stream":      true,
	})
	if err != nil {
		t.Fatalf("BuildBody: %v", err)
	}
	m := parseBody(t, body)
	// 숫자/불리언이 문자열이 아니라 네이티브 타입으로 들어갔는지.
	if v, ok := m["max_tokens"].(float64); !ok || v != 128 {
		t.Errorf("max_tokens = %v (%T), want 128 number", m["max_tokens"], m["max_tokens"])
	}
	if v, ok := m["temperature"].(float64); !ok || v != 0.7 {
		t.Errorf("temperature = %v, want 0.7 number", m["temperature"])
	}
	if v, ok := m["stream"].(bool); !ok || v != true {
		t.Errorf("stream = %v (%T), want true bool", m["stream"], m["stream"])
	}
}

// TestBuildBody_OmitIfUnset_MapKey: 값도 default 도 없는 placeholder 의 map 키는
// 통째로 드롭. temperature/stream/max-tokens 미지정 → 그 키들이 body 에서 사라진다.
func TestBuildBody_OmitIfUnset_MapKey(t *testing.T) {
	rs := llamaRun()
	body, _ := rs.BuildBody(map[string]any{"prompt": "hi"})
	m := parseBody(t, body)
	for _, k := range []string{"max_tokens", "temperature", "stream"} {
		if _, present := m[k]; present {
			t.Errorf("key %q should have been dropped (unset), body=%s", k, body)
		}
	}
}

// TestBuildBody_OmitIfUnset_ArrayElement: system 미지정 → messages 배열에서 system
// 원소가 통째로 빠지고 user 원소만 남는다.
func TestBuildBody_OmitIfUnset_ArrayElement(t *testing.T) {
	rs := llamaRun()
	body, _ := rs.BuildBody(map[string]any{"prompt": "hi"})
	m := parseBody(t, body)
	msgs, ok := m["messages"].([]any)
	if !ok {
		t.Fatalf("messages not an array: %s", body)
	}
	if len(msgs) != 1 {
		t.Fatalf("messages len = %d, want 1 (system dropped): %s", len(msgs), body)
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "user" || first["content"] != "hi" {
		t.Errorf("remaining message = %v, want {role:user, content:hi}", first)
	}
}

// TestBuildBody_SystemKept: system 지정 시 두 원소 모두 유지.
func TestBuildBody_SystemKept(t *testing.T) {
	rs := llamaRun()
	body, _ := rs.BuildBody(map[string]any{"system": "you are a cat", "prompt": "hi"})
	m := parseBody(t, body)
	msgs := m["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages len = %d, want 2: %s", len(msgs), body)
	}
	if msgs[0].(map[string]any)["content"] != "you are a cat" {
		t.Errorf("system content = %v", msgs[0])
	}
}

// TestBuildBody_RequiredMissing: 필수 prompt 누락 → 에러.
func TestBuildBody_RequiredMissing(t *testing.T) {
	rs := llamaRun()
	if _, err := rs.BuildBody(map[string]any{"system": "x"}); err == nil {
		t.Error("expected error for missing required prompt")
	}
}

// TestBuildBody_DefaultApplied: default 가 값처럼 채워지고, 명시값이 default 를 덮는다.
func TestBuildBody_DefaultApplied(t *testing.T) {
	rs := &RunSpec{
		Path:   "/sdapi/v1/txt2img",
		Result: ResultSpec{Modality: "image", ContentPath: "images[0]", Encoding: "base64"},
		Params: []RunParam{
			{Name: "prompt", Type: "string", Required: true},
			{Name: "steps", Type: "int", Default: float64(20)},
			{Name: "cfg", Type: "float", Default: 7.0},
		},
		Body: json.RawMessage(`{"prompt":"${prompt}","steps":"${steps}","cfg_scale":"${cfg}"}`),
	}
	body, err := rs.BuildBody(map[string]any{"prompt": "fox", "steps": float64(30)})
	if err != nil {
		t.Fatalf("BuildBody: %v", err)
	}
	m := parseBody(t, body)
	if m["steps"].(float64) != 30 { // 명시값이 default(20) 를 덮음
		t.Errorf("steps = %v, want 30", m["steps"])
	}
	if m["cfg_scale"].(float64) != 7.0 { // default 적용
		t.Errorf("cfg_scale = %v, want 7.0 (default)", m["cfg_scale"])
	}
}

// TestBuildBody_EmbeddedInterpolation: 문자열 중간 placeholder 는 문자열 보간.
func TestBuildBody_EmbeddedInterpolation(t *testing.T) {
	rs := &RunSpec{
		Path:   "/run",
		Result: ResultSpec{Modality: "text"},
		Params: []RunParam{{Name: "name", Type: "string"}},
		Body:   json.RawMessage(`{"greeting":"hello ${name}!"}`),
	}
	body, _ := rs.BuildBody(map[string]any{"name": "fox"})
	if parseBody(t, body)["greeting"] != "hello fox!" {
		t.Errorf("greeting = %v, want 'hello fox!'", parseBody(t, body)["greeting"])
	}
}

// ----------------------------------------------------------------------------
// Validate
// ----------------------------------------------------------------------------

func TestRunSpec_Validate(t *testing.T) {
	ok := llamaRun()
	if err := ok.Validate(); err != nil {
		t.Errorf("valid spec rejected: %v", err)
	}

	bad := []struct {
		name string
		rs   *RunSpec
	}{
		{"no path", &RunSpec{Result: ResultSpec{Modality: "text"}}},
		{"bad method", &RunSpec{Path: "/x", Method: "PUT", Result: ResultSpec{Modality: "text"}}},
		{"bad modality", &RunSpec{Path: "/x", Result: ResultSpec{Modality: "video"}}},
		{"bad param type", &RunSpec{Path: "/x", Result: ResultSpec{Modality: "text"}, Params: []RunParam{{Name: "p", Type: "blob"}}}},
		{"empty param name", &RunSpec{Path: "/x", Result: ResultSpec{Modality: "text"}, Params: []RunParam{{Type: "string"}}}},
		{"dup param name", &RunSpec{Path: "/x", Result: ResultSpec{Modality: "text"}, Params: []RunParam{{Name: "p", Type: "string"}, {Name: "p", Type: "int"}}}},
		{"bad body json", &RunSpec{Path: "/x", Result: ResultSpec{Modality: "text"}, Body: json.RawMessage(`{nope`)}},
	}
	for _, tc := range bad {
		if err := tc.rs.Validate(); err == nil {
			t.Errorf("%s: expected validation error", tc.name)
		}
	}
}
