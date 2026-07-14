package station

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/isannai/mesh/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/setup"
)

func TestReadEnvInt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := `# comment line
PROFILE_NAME=default

PARALLEL=5
MAX_NUM_SEQS="8"
export THREADS=4
QUOTED='3'
INLINE=6  # six slots
ZERO=0
NEG=-2
EMPTY=
WORDS=abc
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		key  string
		want int
	}{
		{"PARALLEL", 5},      // plain int
		{"MAX_NUM_SEQS", 8},  // double-quoted
		{"THREADS", 4},       // export prefix
		{"QUOTED", 3},        // single-quoted
		{"INLINE", 6},        // inline trailing comment stripped
		{"ZERO", 0},          // 0 → "no opinion"
		{"NEG", 0},           // negative rejected
		{"EMPTY", 0},         // empty value
		{"WORDS", 0},         // non-int
		{"PROFILE_NAME", 0},  // non-int string
		{"MISSING", 0},       // key absent
	}
	for _, tc := range cases {
		if got := readEnvInt(path, tc.key); got != tc.want {
			t.Errorf("readEnvInt(%q) = %d, want %d", tc.key, got, tc.want)
		}
	}
}

func TestReadEnvInt_MissingFile(t *testing.T) {
	if got := readEnvInt(filepath.Join(t.TempDir(), "nope.env"), "PARALLEL"); got != 0 {
		t.Errorf("missing file = %d, want 0", got)
	}
}

func TestReadEnvInt_FirstMatchWins(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("PARALLEL=2\nPARALLEL=9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readEnvInt(path, "PARALLEL"); got != 2 {
		t.Errorf("first match = %d, want 2", got)
	}
}

// writeEngineEnv writes <root>/engines/<engine>/.env with the given body.
func writeEngineEnv(t *testing.T, root, engine, body string) {
	t.Helper()
	dir := filepath.Join(root, "engines", engine)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mfQueue(concurrencyEnv string) *manifest.Manifest {
	return &manifest.Manifest{Queue: manifest.QueueSpec{ConcurrencyEnv: concurrencyEnv}}
}

func TestEngineEnvConcurrency(t *testing.T) {
	root := t.TempDir()
	writeEngineEnv(t, root, "llama", "PARALLEL=5\nCTX_SIZE=8192\n")
	writeEngineEnv(t, root, "vllm", "MAX_NUM_SEQS=12\n")
	writeEngineEnv(t, root, "sd", "STEPS=20\n")

	cases := []struct {
		name string
		root string
		svc  setup.ServiceEntry
		m    *manifest.Manifest
		want int
	}{
		{
			name: "llama PARALLEL",
			root: root,
			svc:  setup.ServiceEntry{Name: "llm-api", Engine: "llama"},
			m:    mfQueue("PARALLEL"),
			want: 5,
		},
		{
			name: "vllm MAX_NUM_SEQS",
			root: root,
			svc:  setup.ServiceEntry{Name: "vllm-api", Engine: "vllm"},
			m:    mfQueue("MAX_NUM_SEQS"),
			want: 12,
		},
		{
			name: "sd has no concurrency_env declared -> 0",
			root: root,
			svc:  setup.ServiceEntry{Name: "sd-api", Engine: "sd"},
			m:    mfQueue(""),
			want: 0,
		},
		{
			name: "nil manifest -> 0",
			root: root,
			svc:  setup.ServiceEntry{Name: "llm-api", Engine: "llama"},
			m:    nil,
			want: 0,
		},
		{
			name: "empty engine name -> 0",
			root: root,
			svc:  setup.ServiceEntry{Name: "llm-api", Engine: ""},
			m:    mfQueue("PARALLEL"),
			want: 0,
		},
		{
			name: "empty root -> 0",
			root: "",
			svc:  setup.ServiceEntry{Name: "llm-api", Engine: "llama"},
			m:    mfQueue("PARALLEL"),
			want: 0,
		},
		{
			name: "declared var absent from .env -> 0",
			root: root,
			svc:  setup.ServiceEntry{Name: "sd-api", Engine: "sd"},
			m:    mfQueue("PARALLEL"), // sd .env has no PARALLEL
			want: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := engineEnvConcurrency(tc.root, tc.svc, tc.m); got != tc.want {
				t.Errorf("engineEnvConcurrency = %d, want %d", got, tc.want)
			}
		})
	}
}
