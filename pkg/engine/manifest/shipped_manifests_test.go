package manifest

// shipped_manifests_test.go — package-stage 에 실제로 배포되는 engine manifest 들
// (artifacts/addon/apps/<engine>/manifest.json) 이 파싱·Validate 를 통과하는지 +
// api.run 스키마가 온전한지 회귀 검증. JSON 오타나 잘못된 type/modality 를 CI 에서 즉시 잡는다.

import (
	"path/filepath"
	"testing"
)

func TestShippedManifestsValid(t *testing.T) {
	// pkg/engine/manifest → repo root 는 3단계 위. 엔진 manifest 는 apps/ 트리.
	base := filepath.Join("..", "..", "..", "package-stage", "isann", "artifacts", "addon", "engines")

	cases := []struct {
		engine    string
		wantRun   bool   // api.run 이 있어야 하는가
		wantModal string // 기대 result.modality (wantRun 일 때)
		wantPath  string // 기대 api.run.path
	}{
		{"llama", true, "text", "/v1/chat/completions"},
		{"vllm", true, "text", "/v1/chat/completions"},
		{"sd", true, "image", "/v1/images/generations"},
	}
	for _, tc := range cases {
		t.Run(tc.engine, func(t *testing.T) {
			m, err := Load(filepath.Join(base, tc.engine, "manifest.json"))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if err := m.Validate(); err != nil {
				t.Fatalf("validate: %v", err)
			}
			if tc.wantRun {
				if m.API.Run == nil {
					t.Fatalf("api.run missing")
				}
				if m.API.Run.Result.Modality != tc.wantModal {
					t.Errorf("modality = %q, want %q", m.API.Run.Result.Modality, tc.wantModal)
				}
				if m.API.Run.Path != tc.wantPath {
					t.Errorf("path = %q, want %q", m.API.Run.Path, tc.wantPath)
				}
				// prompt 는 어느 엔진이든 필수 파라미터여야 한다.
				var hasPrompt bool
				for _, p := range m.API.Run.Params {
					if p.Name == "prompt" && p.Required {
						hasPrompt = true
					}
				}
				if !hasPrompt {
					t.Errorf("expected a required 'prompt' param")
				}
				// 템플릿이 실제 파라미터로 치환되는지 (prompt 만으로 빌드 성공).
				if _, err := m.API.Run.BuildBody(map[string]any{"prompt": "smoke"}); err != nil {
					t.Errorf("BuildBody(prompt only) failed: %v", err)
				}
			}
		})
	}
}
