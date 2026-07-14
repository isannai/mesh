package stationwire

import (
	"os"
	"path/filepath"
	"testing"
)

func seed(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appPath(root, engine, rel string) string {
	return filepath.Join(root, "artifacts", "addon", "engines", engine, rel)
}

// TestDeriveServices covers zero-config derivation: engine name from folder,
// addr from compose ports (+ .env var resolve), service_name/queue from the
// manifest station block; non-engine folders and unroutable engines are skipped.
func TestDeriveServices(t *testing.T) {
	root := t.TempDir()

	// llama: plain short port mapping, no station block → derived defaults.
	seed(t, appPath(root, "llama", "manifest.json"), `{"spec_version":"1","name":"llama"}`)
	seed(t, appPath(root, "llama", "docker-compose.yml"),
		"services:\n  llama:\n    image: x\n    ports:\n      - \"7862:8080\"\n")

	// sd: ${VAR:-default} port + a station block (custom name + queue).
	seed(t, appPath(root, "sd", "manifest.json"),
		`{"spec_version":"1","name":"sd","station":{"service_name":"sd-api","queue":{"concurrency":2}}}`)
	seed(t, appPath(root, "sd", "docker-compose.yaml"),
		"services:\n  sd:\n    ports:\n      - \"${SD_PORT:-7860}:7860\"\n")

	// isann: core app (tools.json, no manifest.json) → skipped.
	seed(t, appPath(root, "isann", "tools.json"), `{"name":"isann"}`)

	// bare: manifest but no host port (container-only) → unroutable, skipped.
	seed(t, appPath(root, "bare", "manifest.json"), `{"spec_version":"1","name":"bare"}`)
	seed(t, appPath(root, "bare", "docker-compose.yml"),
		"services:\n  bare:\n    ports:\n      - \"8080\"\n")

	got, err := DeriveServices(root)
	if err != nil {
		t.Fatal(err)
	}
	byEngine := map[string]string{} // engine → "name@addr"
	for _, s := range got {
		byEngine[s.Engine] = s.Name + "@" + s.Addr
	}

	if byEngine["llama"] != "llama-api@127.0.0.1:7862" {
		t.Errorf("llama derived = %q, want llama-api@127.0.0.1:7862 (all: %v)", byEngine["llama"], byEngine)
	}
	if byEngine["sd"] != "sd-api@127.0.0.1:7860" {
		t.Errorf("sd derived = %q, want sd-api@127.0.0.1:7860 (.env default resolved)", byEngine["sd"])
	}
	if _, ok := byEngine["isann"]; ok {
		t.Error("core isann (no manifest.json) must be skipped")
	}
	if _, ok := byEngine["bare"]; ok {
		t.Error("engine with no host port must be skipped (unroutable)")
	}

	// station block queue override carried onto the ServiceEntry.
	for _, s := range got {
		if s.Engine == "sd" {
			if s.Queue == nil || s.Queue.Concurrency != 2 {
				t.Errorf("sd queue override lost: %+v", s.Queue)
			}
		}
	}
}

// TestDeriveServices_ShippedFormat matches the exact compose form the base
// engines ship: "127.0.0.1:${PORT:-7862}:8080" (ip:host:container with a
// defaulted var) + a manifest station block (service_name override).
func TestDeriveServices_ShippedFormat(t *testing.T) {
	root := t.TempDir()
	seed(t, appPath(root, "llama", "manifest.json"),
		`{"spec_version":"1","name":"llama","station":{"service_name":"llm-api","queue":{"max_queue":50,"concurrency":1}}}`)
	seed(t, appPath(root, "llama", "docker-compose.yml"),
		"services:\n  llama:\n    image: x\n    ports:\n      - \"127.0.0.1:${PORT:-7862}:8080\"\n")

	got, err := DeriveServices(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 service, got %+v", got)
	}
	s := got[0]
	if s.Name != "llm-api" || s.Addr != "127.0.0.1:7862" || s.Engine != "llama" {
		t.Errorf("got name=%q addr=%q engine=%q, want llm-api / 127.0.0.1:7862 / llama", s.Name, s.Addr, s.Engine)
	}
	if s.Queue == nil || s.Queue.MaxQueue != 50 || s.Queue.Concurrency != 1 {
		t.Errorf("queue = %+v, want max_queue:50 concurrency:1", s.Queue)
	}
}

// TestDeriveServices_EnvHostPort resolves ${HOST_PORT} from the sibling .env.
func TestDeriveServices_EnvHostPort(t *testing.T) {
	root := t.TempDir()
	seed(t, appPath(root, "vllm", "manifest.json"), `{"spec_version":"1","name":"vllm"}`)
	seed(t, appPath(root, "vllm", "docker-compose.yml"),
		"services:\n  vllm:\n    ports:\n      - \"${HOST_PORT}:8000\"\n")
	seed(t, appPath(root, "vllm", ".env"), "HOST_PORT=7864\n")

	got, err := DeriveServices(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Addr != "127.0.0.1:7864" {
		t.Errorf("got %+v, want addr 127.0.0.1:7864 from .env HOST_PORT", got)
	}
}

// TestDeriveServices_VersionedFolder: the engine identity comes from the
// manifest `name`, NOT the folder — so a versioned/renamed folder still yields
// the right engine (and service).
func TestDeriveServices_VersionedFolder(t *testing.T) {
	root := t.TempDir()
	// folder is "llama@0.1.0" but manifest name is "llama".
	seed(t, appPath(root, "llama@0.1.0", "manifest.json"),
		`{"spec_version":"1","name":"llama","station":{"service_name":"llm-api"}}`)
	seed(t, appPath(root, "llama@0.1.0", "docker-compose.yml"),
		"services:\n  x:\n    ports:\n      - \"7862:8080\"\n")

	got, err := DeriveServices(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1, got %+v", got)
	}
	if got[0].Engine != "llama" {
		t.Errorf("engine = %q, want llama (from manifest name, not folder llama@0.1.0)", got[0].Engine)
	}
	if got[0].Name != "llm-api" {
		t.Errorf("name = %q, want llm-api", got[0].Name)
	}
}

// TestDeriveServices_NoApps: a missing apps/ dir is a no-op, not an error.
func TestDeriveServices_NoApps(t *testing.T) {
	got, err := DeriveServices(t.TempDir())
	if err != nil || got != nil {
		t.Errorf("missing apps/ → (nil,nil), got (%v,%v)", got, err)
	}
}

// TestPortCollision: two engines on the same host port → alphabetically-first
// wins in DeriveServices, and Collisions reports the conflict.
func TestPortCollision(t *testing.T) {
	root := t.TempDir()
	for _, eng := range []string{"aaa", "bbb"} {
		seed(t, appPath(root, eng, "manifest.json"), `{"spec_version":"1","name":"`+eng+`"}`)
		seed(t, appPath(root, eng, "docker-compose.yml"),
			"services:\n  x:\n    ports:\n      - \"9999:8080\"\n")
	}
	got, err := DeriveServices(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Engine != "aaa" {
		t.Errorf("collision: want only aaa (first), got %+v", got)
	}
	cols, err := Collisions(root)
	if err != nil {
		t.Fatal(err)
	}
	if engs := cols["127.0.0.1:9999"]; len(engs) != 2 {
		t.Errorf("Collisions should report aaa+bbb on :9999, got %v", cols)
	}
}
