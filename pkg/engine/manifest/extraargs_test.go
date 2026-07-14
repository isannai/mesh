package manifest

// extraargs_test.go — extra_args(prompt 태그 주입) Apply + Validate 검증 (M1.5).

import (
	"encoding/json"
	"strings"
	"testing"
)

func sdExtra() *ExtraArgSpec {
	return &ExtraArgSpec{
		Inject: "prompt",
		Tag:    "sd_cpp_extra_args",
		Fields: []string{"steps", "cfg_scale", "sample_method", "seed", "negative_prompt"},
	}
}

// TestExtraArgsApply_MovesFieldsIntoPrompt: 선언된 필드가 top-level 에서 빠져
// prompt 끝 <tag>{json}</tag> 로 이동하고, 비선언 필드(size/n)는 top-level 유지.
func TestExtraArgsApply_MovesFieldsIntoPrompt(t *testing.T) {
	body := map[string]any{
		"prompt":          "a fox",
		"negative_prompt": "blurry",
		"steps":           float64(30),
		"cfg_scale":       float64(8),
		"seed":            float64(-1),
		"size":            "768x512",
		"n":               float64(1),
	}
	sdExtra().Apply(body)

	// 선언 필드는 top-level 에서 사라져야 함.
	for _, k := range []string{"steps", "cfg_scale", "seed", "negative_prompt"} {
		if _, present := body[k]; present {
			t.Errorf("%q should have been moved into the prompt tag", k)
		}
	}
	// 비선언 필드는 유지.
	if body["size"] != "768x512" || body["n"] != float64(1) {
		t.Errorf("size/n should stay top-level: %v", body)
	}
	// prompt 에 태그가 붙고, 그 안 JSON 에 옮긴 값들이 들어있어야 함.
	prompt, _ := body["prompt"].(string)
	if !strings.HasPrefix(prompt, "a fox<sd_cpp_extra_args>") || !strings.HasSuffix(prompt, "</sd_cpp_extra_args>") {
		t.Fatalf("prompt not wrapped: %q", prompt)
	}
	inner := prompt[len("a fox<sd_cpp_extra_args>") : len(prompt)-len("</sd_cpp_extra_args>")]
	var tag map[string]any
	if err := json.Unmarshal([]byte(inner), &tag); err != nil {
		t.Fatalf("tag not JSON: %q", inner)
	}
	if tag["steps"] != float64(30) || tag["cfg_scale"] != float64(8) || tag["negative_prompt"] != "blurry" {
		t.Errorf("tag contents wrong: %v", tag)
	}
}

// TestExtraArgsApply_Empty: 옮길 값이 하나도 없으면 prompt 에 태그를 붙이지 않음.
func TestExtraArgsApply_Empty(t *testing.T) {
	body := map[string]any{"prompt": "a fox", "size": "512x512"} // 선언 필드 전무
	sdExtra().Apply(body)
	if body["prompt"] != "a fox" {
		t.Errorf("prompt should be untouched, got %q", body["prompt"])
	}
}

// TestExtraArgsApply_EmptyStringSkipped: 빈 문자열 필드는 옮기지 않음(엔진 default).
func TestExtraArgsApply_EmptyStringSkipped(t *testing.T) {
	body := map[string]any{"prompt": "a fox", "negative_prompt": "", "steps": float64(20)}
	sdExtra().Apply(body)
	prompt := body["prompt"].(string)
	if strings.Contains(prompt, "negative_prompt") {
		t.Errorf("empty negative_prompt should be skipped: %q", prompt)
	}
	if !strings.Contains(prompt, "\"steps\":20") {
		t.Errorf("steps should be in tag: %q", prompt)
	}
}

// TestExtraArgsApply_AlreadyWrapped: prompt 가 이미 태그를 포함하면 재포장하지 않고
// top-level 선언 필드만 제거(중복 방지).
func TestExtraArgsApply_AlreadyWrapped(t *testing.T) {
	body := map[string]any{
		"prompt": "a fox<sd_cpp_extra_args>{\"steps\":50}</sd_cpp_extra_args>",
		"steps":  float64(20), // top-level 중복 → 제거돼야
	}
	sdExtra().Apply(body)
	if _, present := body["steps"]; present {
		t.Errorf("top-level steps should be stripped when already wrapped")
	}
	if !strings.Contains(body["prompt"].(string), "{\"steps\":50}") {
		t.Errorf("existing tag should be preserved: %q", body["prompt"])
	}
}

// TestExtraArgsApply_Nil: nil / 비-prompt inject 는 no-op (패닉 없이).
func TestExtraArgsApply_Nil(t *testing.T) {
	var e *ExtraArgSpec
	body := map[string]any{"prompt": "x", "steps": float64(1)}
	e.Apply(body) // nil receiver → no-op
	if _, present := body["steps"]; !present {
		t.Errorf("nil ExtraArgs must be a no-op")
	}
}

// TestExtraArgsValidate: inject/tag/fields 검증.
func TestExtraArgsValidate(t *testing.T) {
	if err := sdExtra().Validate(); err != nil {
		t.Errorf("valid spec rejected: %v", err)
	}
	bad := []*ExtraArgSpec{
		{Inject: "query", Tag: "t", Fields: []string{"a"}},   // unsupported inject
		{Inject: "prompt", Tag: "", Fields: []string{"a"}},   // missing tag
		{Inject: "prompt", Tag: "t", Fields: nil},            // empty fields
	}
	for i, e := range bad {
		if err := e.Validate(); err == nil {
			t.Errorf("bad[%d] expected validation error", i)
		}
	}
}
