package manifest

// shipped_manifests_test.go — package-stage 에 실제로 배포되는 engine manifest 들
// (artifacts/addon/apps/<engine>/manifest.json) 이 파싱·Validate 를 통과하는지 +
// api.run 스키마가 온전한지 회귀 검증. JSON 오타나 잘못된 type/modality 를 CI 에서 즉시 잡는다.

import (
	"os"
	"path/filepath"
	"testing"
)

// stagedEnginesDir finds the engine manifests the GLink package ships. They live in the
// GLink repo (package-stage/), not in this one, so the lookup is: $ISANN_PACKAGE_STAGE,
// then a package-stage next to this repo's root (the old shared layout), then a GLink
// checkout beside the workspace (<workspace>/GLink and <workspace>/isann/mesh).
func stagedEnginesDir() (string, bool) {
	tail := []string{"isann", "artifacts", "addon", "engines"}
	var roots []string
	if v := os.Getenv("ISANN_PACKAGE_STAGE"); v != "" {
		roots = append(roots, v)
	}
	roots = append(roots,
		filepath.Join("..", "..", "..", "package-stage"),
		filepath.Join("..", "..", "..", "..", "..", "GLink", "package-stage"),
		filepath.Join("..", "..", "..", "..", "GLink", "package-stage"))
	for _, r := range roots {
		dir := filepath.Join(append([]string{r}, tail...)...)
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return dir, true
		}
	}
	return "", false
}

func TestShippedManifestsValid(t *testing.T) {
	// 엔진 manifest 는 GLink 저장소의 package-stage 에 있다 (이 저장소에는 없다).
	base, ok := stagedEnginesDir()
	if !ok {
		t.Skip("package-stage (GLink) not found: check out GLink beside this repo or set ISANN_PACKAGE_STAGE")
	}

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
