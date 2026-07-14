package manifest

import (
	"testing"
)

func TestSubstituteUnknownToken(t *testing.T) {
	got := substitute("{a}/{missing}/{b}", map[string]string{"a": "1", "b": "2"})
	if got != "1/{missing}/2" {
		t.Errorf("substitute = %q", got)
	}
}

func TestSubstituteNoTokens(t *testing.T) {
	got := substitute("plain string", map[string]string{"a": "1"})
	if got != "plain string" {
		t.Errorf("substitute should pass through, got %q", got)
	}
}

func TestValidateExternal(t *testing.T) {
	good := &Manifest{
		SpecVersion: "1",
		Name:        "vllm",
		Launcher:    "external",
		ReadyCheck:  ReadyCheckSpec{Type: "http_get", URL: "http://{addr}/v1/models"},
	}
	if err := good.Validate(); err != nil {
		t.Errorf("good external manifest failed validation: %v", err)
	}
}

func TestValidateDocker(t *testing.T) {
	good := &Manifest{
		SpecVersion: "1",
		Name:        "sd",
		Launcher:    "docker",
		Container:   &ContainerSpec{Image: "isannai/sd:latest"},
		ReadyCheck:  ReadyCheckSpec{Type: "http_get", URL: "http://{addr}/sdapi/v1/sd-models"},
	}
	if err := good.Validate(); err != nil {
		t.Errorf("good docker manifest failed validation: %v", err)
	}

	bad := *good
	bad.Container = nil
	if err := bad.Validate(); err == nil {
		t.Errorf("docker without container block should fail")
	}

	bad = *good
	bad.Container = &ContainerSpec{}
	if err := bad.Validate(); err == nil {
		t.Errorf("docker without container.image should fail")
	}
}

func TestValidateMissing(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"missing spec_version", func(m *Manifest) { m.SpecVersion = "" }},
		{"missing name", func(m *Manifest) { m.Name = "" }},
		{"bad launcher", func(m *Manifest) { m.Launcher = "engine-runner" }},
		{"bad ready_check type", func(m *Manifest) { m.ReadyCheck.Type = "tcp" }},
		{"missing ready_check url", func(m *Manifest) { m.ReadyCheck.URL = "" }},
	}
	base := &Manifest{
		SpecVersion: "1",
		Name:        "test",
		ReadyCheck:  ReadyCheckSpec{Type: "http_get", URL: "http://x/y"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := *base
			tc.mutate(&m)
			if err := m.Validate(); err == nil {
				t.Errorf("expected validation error")
			}
		})
	}
}

func TestValidateParsers(t *testing.T) {
	good := &Manifest{
		SpecVersion: "1",
		Name:        "sd",
		ReadyCheck:  ReadyCheckSpec{Type: "http_get", URL: "http://{addr}/health"},
		Parsers: []ParserSpec{
			{Name: "p1", Stream: "stderr", Type: "regex", Pattern: `(\d+)%`, Emit: map[string]string{"event": "progress"}},
		},
	}
	if err := good.Validate(); err != nil {
		t.Errorf("good parsers failed: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Manifest)
	}{
		{"bad regex", func(m *Manifest) { m.Parsers[0].Pattern = "(unclosed" }},
		{"unknown parser type", func(m *Manifest) { m.Parsers[0].Type = "morse" }},
		{"missing parser name", func(m *Manifest) { m.Parsers[0].Name = "" }},
		{"missing emit event", func(m *Manifest) { m.Parsers[0].Emit = map[string]string{} }},
		{"bad stream", func(m *Manifest) { m.Parsers[0].Stream = "stdin" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := *good
			m.Parsers = []ParserSpec{
				{Name: "p1", Stream: "stderr", Type: "regex", Pattern: `(\d+)%`, Emit: map[string]string{"event": "progress"}},
			}
			tc.mutate(&m)
			if err := m.Validate(); err == nil {
				t.Errorf("expected validation error")
			}
		})
	}
}
