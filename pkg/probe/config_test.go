package probe

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "probe.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// 🔴 The distinction the pointer fields exist for. Absent and empty are two
// different statements, and encoding/json gives a plain slice len 0 for both.
func TestGeneratorsAbsentVsEmpty(t *testing.T) {
	t.Run("absent means this node", func(t *testing.T) {
		cfg, err := LoadConfig(writeConfig(t, `{"db":"x.db"}`))
		if err != nil {
			t.Fatal(err)
		}
		got := cfg.generatorEntries()
		if len(got) != 1 || !got[0].IsSelf() || got[0].Service != "llm-api" {
			t.Fatalf("got %+v, want one self entry on llm-api", got)
		}
	})

	t.Run("empty means no generation", func(t *testing.T) {
		cfg, err := LoadConfig(writeConfig(t, `{"generators":[]}`))
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.generatorEntries(); len(got) != 0 {
			t.Fatalf("got %+v, want nothing — an explicit [] is arithmetic only", got)
		}
	})

	t.Run("named allies", func(t *testing.T) {
		cfg, err := LoadConfig(writeConfig(t,
			`{"generators":["this","0xabc","0xdef/llm-api-2"],"generator_service":"llm-api"}`))
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"this/llm-api", "0xabc/llm-api", "0xdef/llm-api-2"}
		got := cfg.GeneratorNames()
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("[%d] got %q, want %q", i, got[i], want[i])
			}
		}
	})
}

// Clips default to OFF, not to this node. There is no code-generated fallback
// for judging, and firing at an image node with no judge waiting only burns
// someone else's GPU.
func TestClipsDefaultOff(t *testing.T) {
	for _, body := range []string{`{}`, `{"clips":[]}`} {
		cfg, err := LoadConfig(writeConfig(t, body))
		if err != nil {
			t.Fatal(err)
		}
		if got := cfg.clipEntries(); len(got) != 0 {
			t.Errorf("%s → %+v, want no validators", body, got)
		}
	}

	cfg, err := LoadConfig(writeConfig(t, `{"clips":["0xabc"]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.ClipNames()
	if len(got) != 1 || got[0] != "0xabc/clip-api" {
		t.Fatalf("got %v, want [0xabc/clip-api]", got)
	}
}

// Configs are already deployed carrying the old key. Ignoring it would quietly
// move generation to whatever the default happened to be.
func TestLegacyGeneratorServiceKey(t *testing.T) {
	cfg, err := LoadConfig(writeConfig(t, `{"question_generator_service":"my-llm"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GeneratorService != "my-llm" {
		t.Errorf("GeneratorService = %q, want the legacy value", cfg.GeneratorService)
	}

	// An explicit new key always wins — otherwise a config mid-migration would
	// be steered by the field the operator was trying to replace.
	cfg, err = LoadConfig(writeConfig(t,
		`{"question_generator_service":"old","generator_service":"new"}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GeneratorService != "new" {
		t.Errorf("GeneratorService = %q, want the explicit new key", cfg.GeneratorService)
	}
}

// The two deadlines were named inconsistently — one said what it waited FOR
// (response), its sibling said what it was ABOUT (image) — so they read as
// unrelated settings when they are the same knob per track. Renaming a key in a
// config that is already deployed is only safe if the old spelling keeps
// working: an operator who raised it for a 70B model and silently got 30s back
// would see every shot time out.
func TestLegacyResponseDeadlineKey(t *testing.T) {
	t.Run("old key is carried over", func(t *testing.T) {
		cfg, err := LoadConfig(writeConfig(t, `{"response_deadline_sec":90}`))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.TextDeadline != 90 {
			t.Errorf("TextDeadline = %d, want the legacy 90", cfg.TextDeadline)
		}
	})

	t.Run("explicit new key wins", func(t *testing.T) {
		cfg, err := LoadConfig(writeConfig(t,
			`{"response_deadline_sec":90,"text_deadline_sec":45}`))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.TextDeadline != 45 {
			t.Errorf("TextDeadline = %d, want the explicit new key", cfg.TextDeadline)
		}
	})

	t.Run("old env var is carried over too", func(t *testing.T) {
		// The absorb runs AFTER the environment for exactly this: a node whose
		// mesh config still holds the old name would otherwise be reset.
		t.Setenv("PROBE_RESPONSE_DEADLINE_SEC", "120")
		cfg, err := LoadConfig(writeConfig(t, `{}`))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.TextDeadline != 120 {
			t.Errorf("TextDeadline = %d, want the legacy env value", cfg.TextDeadline)
		}
	})

	t.Run("neither set leaves the default", func(t *testing.T) {
		cfg, err := LoadConfig(writeConfig(t, `{}`))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.TextDeadline != DefaultConfig().TextDeadline {
			t.Errorf("TextDeadline = %d, want the default", cfg.TextDeadline)
		}
	})
}

// The dead zones are configured in SECONDS, for the reason the ladder they
// replaced was: production wants half an hour and a smoke test wants ten
// seconds, and only one of those is writable in both units.
func TestFireWindowConfig(t *testing.T) {
	t.Run("defaults are half an hour at each end", func(t *testing.T) {
		cfg, err := LoadConfig(writeConfig(t, `{}`))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.FireLead() != DefaultFireLead || cfg.FireTail() != DefaultFireTail {
			t.Errorf("window = +%s..-%s, want +%s..-%s",
				cfg.FireLead(), cfg.FireTail(), DefaultFireLead, DefaultFireTail)
		}
	})

	t.Run("seconds are seconds", func(t *testing.T) {
		cfg, err := LoadConfig(writeConfig(t, `{"fire_lead_sec":10,"fire_tail_sec":20}`))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.FireLead() != 10*time.Second || cfg.FireTail() != 20*time.Second {
			t.Errorf("window = +%s..-%s, want +10s..-20s", cfg.FireLead(), cfg.FireTail())
		}
	})

	// Zero is an operator saying "use the whole slot", which is a real setting.
	// A negative is a typo, and honouring it would place the window outside the
	// slot entirely.
	t.Run("zero is honoured, negative falls back", func(t *testing.T) {
		cfg, err := LoadConfig(writeConfig(t, `{"fire_lead_sec":0,"fire_tail_sec":-5}`))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.FireLead() != 0 {
			t.Errorf("lead = %s, want 0", cfg.FireLead())
		}
		if cfg.FireTail() != DefaultFireTail {
			t.Errorf("tail = %s, want the default %s", cfg.FireTail(), DefaultFireTail)
		}
	})

	// 🔴 A CONCURRENCY LIMIT IS NOT A BATCH LIMIT. Thirty-two groups used to
	// go out in one round, sixteen at a time, and the round just took twice as
	// long — the whole directory answering inside one minute.
	t.Run("batch sizes default and clamp", func(t *testing.T) {
		cfg, err := LoadConfig(writeConfig(t, `{}`))
		if err != nil {
			t.Fatal(err)
		}
		if g, i := cfg.Batch(); g != DefaultSubnetsPerRound || i != DefaultImagesPerRound {
			t.Errorf("batch = %d/%d, want %d/%d", g, i, DefaultSubnetsPerRound, DefaultImagesPerRound)
		}

		cfg, err = LoadConfig(writeConfig(t, `{"subnets_per_round":3,"images_per_round":1}`))
		if err != nil {
			t.Fatal(err)
		}
		if g, i := cfg.Batch(); g != 3 || i != 1 {
			t.Errorf("batch = %d/%d, want 3/1", g, i)
		}

		// Zero would stop the prober silently, which reads as a broken
		// schedule rather than as a setting.
		cfg, err = LoadConfig(writeConfig(t, `{"subnets_per_round":0,"images_per_round":-2}`))
		if err != nil {
			t.Fatal(err)
		}
		if g, i := cfg.Batch(); g != DefaultSubnetsPerRound || i != DefaultImagesPerRound {
			t.Errorf("batch = %d/%d, want the defaults", g, i)
		}
	})

	// The directory is read to learn slot membership, which does not change
	// inside a slot. Polling it by the minute asked the RV one question a
	// hundred and eighty times.
	t.Run("directory refresh is half an hour", func(t *testing.T) {
		cfg, err := LoadConfig(writeConfig(t, `{}`))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.RefreshSec != 1800 {
			t.Errorf("RefreshSec = %d, want 1800", cfg.RefreshSec)
		}
	})
}

func TestPoolEnvOverrides(t *testing.T) {
	t.Setenv("PROBE_GENERATORS", " 0xa , 0xb/llm-api-2 ")
	cfg, err := LoadConfig(writeConfig(t, `{"generators":["this"]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := cfg.GeneratorNames()
	if len(got) != 2 || got[0] != "0xa/llm-api" || got[1] != "0xb/llm-api-2" {
		t.Fatalf("got %v", got)
	}

	// An empty env var is indistinguishable from an unset one, so "none" is how
	// a list is emptied through the environment.
	t.Setenv("PROBE_GENERATORS", "none")
	cfg, err = LoadConfig(writeConfig(t, `{"generators":["this"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.generatorEntries(); len(got) != 0 {
		t.Fatalf("got %+v, want nothing for PROBE_GENERATORS=none", got)
	}
}
