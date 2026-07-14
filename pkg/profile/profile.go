// Package profile defines the per-service profile schema and loader.
//
// A profile is a named bundle of engine knobs (model_default, ctx_size,
// gpu_layers, …) tuned for a specific deployment scenario — typically a
// hardware tier (12GB GPU, 24GB GPU, …). Each service has a separate
// profile file at `conf/profiles/<service>.json` containing one or more
// profiles plus an `active` selector.
package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/isannai/mesh/pkg/artifactmeta"
)

// Profile is one named configuration bundle.
type Profile struct {
	Name              string `json:"name"`
	Version           string `json:"version"` // artifact version (semver) — see pkg/artifactmeta
	artifactmeta.Meta        // min_isann / author / license (flattened to top-level)
	Label             string `json:"label,omitempty"`

	// PackageRef points to the model package directory under packages/, e.g.
	// "models/llm-api/Qwen2.5-14B-Instruct-Q4_K_M". When set, the engine-runner
	// reads packages/{PackageRef}/package.json to resolve the actual model file
	// path (RefPath for reference-mode imports, or InstallPath+FileName for
	// downloaded packages). Mutually exclusive with values["model"]; PackageRef
	// takes priority when both are set.
	PackageRef string `json:"package_ref,omitempty"`

	// Architecture is the normalized base-model family identifier, e.g.
	// "sd15" / "sdxl" / "flux" / "qwen2" / "llama3". Drives LoRA folder
	// routing (sd.cpp's --lora-model-dir + llama.cpp's --lora) and
	// compatibility filtering in UI ("show only LoRAs that match this
	// architecture"). Auto-populated at install time from package
	// metadata; operators can override via the profile editor.
	Architecture string `json:"architecture,omitempty"`

	Values map[string]any `json:"values"`

	// Loras is the per-profile LoRA policy. Two engines are wired up:
	//   - sd-api  → Defaults []  (multi, per-request prepend on top of caller's loras)
	//   - llm-api → Active *Active (single, boot-time merge into the model)
	// vllm-api leaves Loras nil because IANN doesn't manage vLLM lifecycle.
	// Disabled=true overrides everything: engine spawns without --lora-model-dir
	// (sd-api) / --lora-scaled (llm-api) and request-level loras are ignored.
	// Pointer + omitempty so existing profile files (no loras field) deserialize
	// to nil and run unchanged.
	Loras *LoraSettings `json:"loras,omitempty"`

	// NeedsConfig marks an entry that was auto-seeded (e.g. by a fresh
	// `isann install -model` run) and has not yet been reviewed/saved by
	// an operator. The engine-runner refuses to start when the active
	// profile carries this flag, so the operator is forced to open the
	// profile in the broker UI, look the defaults over, and save once.
	// `handleUpsertProfile` clears the flag on save (a save is treated as
	// the review). External-launcher engines (vllm) bypass the gate.
	NeedsConfig bool `json:"needs_config,omitempty"`
}

// LoraSettings groups per-profile LoRA policy. Defaults vs Active is
// engine-dependent (sd-api uses Defaults, llm-api uses Active) — either
// can be empty/nil. Disabled is a master kill switch.
type LoraSettings struct {
	Disabled bool          `json:"disabled,omitempty"`
	Defaults []LoraDefault `json:"defaults,omitempty"` // sd-api: multi
	Active   *LoraActive   `json:"active,omitempty"`   // llm-api: single
}

// LoraDefault is one entry of sd-api's automatic per-request LoRA prepend list.
// PackageRef points to a packages/loras/{arch}/{name}/ directory; weight is the
// `<lora:NAME:weight>` slider value sd.cpp consumes (typical 0.5 ~ 1.0).
type LoraDefault struct {
	PackageRef string  `json:"package_ref"`
	Weight     float64 `json:"weight"`
}

// LoraActive is llm-api's single boot-time LoRA. Engine-runner resolves the
// PackageRef to a file path and passes `--lora-scaled <path> <scale>` to
// llama-server, which merges it into the model on load.
type LoraActive struct {
	PackageRef string  `json:"package_ref"`
	Scale      float64 `json:"scale"`
}

// Set holds the full profile file contents for a service: the engine name
// (matched against the manifest for safety), the active profile name, and
// the list of available profiles.
type Set struct {
	Engine   string    `json:"engine,omitempty"`
	Active   string    `json:"active,omitempty"`
	Profiles []Profile `json:"profiles"`

	// Path is the absolute path of the source file. Set by Load.
	Path string `json:"-"`
}

// LoadSet reads `conf/profiles/<service>.json` and returns its parsed Set.
// Returns nil + nil error when the file does not exist (caller decides
// whether that is fatal — service may still start with empty values).
func LoadSet(path string) (*Set, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("profile: read %s: %w", path, err)
	}
	var s Set
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("profile: parse %s: %w", path, err)
	}
	abs, _ := filepath.Abs(path)
	s.Path = abs
	return &s, nil
}

// SaveSet rewrites the source file with pretty-printed JSON. Caller must
// supply a Set whose Path is set (Load returns this; manual construction
// must populate it explicitly).
func SaveSet(s *Set) error {
	if s == nil || s.Path == "" {
		return fmt.Errorf("profile: cannot save Set with empty Path")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o755); err != nil {
		return fmt.Errorf("profile: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("profile: marshal: %w", err)
	}
	return os.WriteFile(s.Path, data, 0o644)
}

// SetPathFor returns the conventional `conf/profiles/<service>.json` path
// next to the given service conf file (e.g. conf/llm-api.json).
func SetPathFor(svcConfPath string) string {
	dir := filepath.Dir(svcConfPath)
	if dir == "" {
		dir = "conf"
	}
	base := filepath.Base(svcConfPath)
	name := strings.TrimSuffix(base, filepath.Ext(base))
	return filepath.Join(dir, "profiles", name+".json")
}

// SetPathForName builds the path from a bare service name (e.g. "llm-api")
// rooted at the given conf directory.
func SetPathForName(confDir, service string) string {
	if confDir == "" {
		confDir = "conf"
	}
	return filepath.Join(confDir, "profiles", service+".json")
}

// Active returns the currently-active Profile, falling back to the first
// entry when Active is empty / missing. Returns nil when the Set has no
// profiles at all.
func (s *Set) ActiveProfile() *Profile {
	if s == nil || len(s.Profiles) == 0 {
		return nil
	}
	if s.Active != "" {
		for i := range s.Profiles {
			if s.Profiles[i].Name == s.Active {
				return &s.Profiles[i]
			}
		}
		// Active name set but not found — fall through to first as a
		// graceful fallback.
	}
	return &s.Profiles[0]
}

// FindProfile returns the profile with the given name, or nil.
func (s *Set) FindProfile(name string) *Profile {
	if s == nil {
		return nil
	}
	for i := range s.Profiles {
		if s.Profiles[i].Name == name {
			return &s.Profiles[i]
		}
	}
	return nil
}

// SetActive updates the Active selector after verifying the profile exists.
func (s *Set) SetActive(name string) error {
	if s.FindProfile(name) == nil {
		return fmt.Errorf("profile: %q not found in set", name)
	}
	s.Active = name
	return nil
}

// Upsert adds the profile when its name is new or replaces the existing
// entry in place. Returns true when an existing entry was replaced.
//
// Two fields are sticky across upserts when the incoming value is empty:
//   - PackageRef: set by `isann install -model` at install time; the
//     broker UI doesn't surface it, so a UI save with empty PackageRef
//     must keep the existing model binding intact.
//   - Architecture: auto-populated at install from package metadata. The
//     UI shows it but lets the operator clear it accidentally; preserving
//     the existing value when incoming is empty lets the editor "leave it
//     alone" semantics work for partial edits.
func (s *Set) Upsert(p Profile) bool {
	for i := range s.Profiles {
		if s.Profiles[i].Name == p.Name {
			if p.PackageRef == "" {
				p.PackageRef = s.Profiles[i].PackageRef
			}
			if p.Architecture == "" {
				p.Architecture = s.Profiles[i].Architecture
			}
			// Loras is sticky too — UI partial-edits (e.g. just changing
			// `values`) don't have to round-trip the full LoRA policy back
			// or risk wiping default_loras / disabled. nil-incoming preserves.
			if p.Loras == nil {
				p.Loras = s.Profiles[i].Loras
			}
			s.Profiles[i] = p
			return true
		}
	}
	s.Profiles = append(s.Profiles, p)
	return false
}

// Remove deletes the profile with the given name. When the removed profile
// was active, Active is reassigned to the first remaining entry. Refuses to
// remove the last profile (caller would end up with an empty set the engine
// can't start from).
func (s *Set) Remove(name string) error {
	if len(s.Profiles) <= 1 {
		return fmt.Errorf("profile: cannot remove last profile %q", name)
	}
	idx := -1
	for i := range s.Profiles {
		if s.Profiles[i].Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("profile: %q not found in set", name)
	}
	wasActive := s.Active == name
	s.Profiles = append(s.Profiles[:idx], s.Profiles[idx+1:]...)
	if wasActive {
		s.Active = s.Profiles[0].Name
	}
	return nil
}

// NormalizeArchitecture maps free-form architecture strings (Civitai's
// `baseModel`, HF repo metadata, operator input) onto a fixed enum used
// for LoRA folder routing and compatibility filtering.
//
// Civitai-distinct base models are kept as separate enum values even when
// they share the same UNet shape (Pony / Illustrious / NoobAI all use SDXL
// internals but their feature spaces are different enough that LoRAs are
// not cross-compatible — collapsing them under "sdxl" produced unusable
// results). Folder layout follows the enum, so each base lives at
// packages/loras/{enum}/ and engine-runner / UI naturally filter by base.
//
// Returned enum (ordered by family):
//   sd15 | sd21 | sd3 | sd35
//   sdxl | pony | illustrious | noobai
//   flux | flux-d | flux-s
//   hunyuan-video
//   qwen2 | qwen25 | qwen3
//   llama3 | llama31
//   mistral | mixtral
//   other
//
// Unknown inputs fall through to "other"; empty input returns "".
//
// Order matters in the switch — more specific labels come first so
// "Pony" doesn't get caught by an "sdxl" prefix pattern.
func NormalizeArchitecture(s string) string {
	q := strings.ToLower(strings.TrimSpace(s))
	if q == "" {
		return ""
	}
	switch {
	// SDXL-family finetunes (must precede generic "sdxl" match — they
	// share UNet shape but distinct feature spaces, distinct folders).
	case contains(q, "noobai", "noob ai", "noob-ai"):
		return "noobai"
	case contains(q, "illustrious", "illust"):
		return "illustrious"
	case contains(q, "pony"):
		return "pony"
	// Stable Diffusion family — version-specific labels first
	case contains(q, "sd 3.5", "sd3.5", "sd-3.5", "stable-diffusion-3.5"):
		return "sd35"
	case contains(q, "sd 3", "sd3", "sd-3", "stable-diffusion-3"):
		return "sd3"
	case contains(q, "sd 2.1", "sd2.1", "sd-2.1", "sd_2.1", "stable-diffusion-v2-1", "sd21"):
		return "sd21"
	case contains(q, "sd 1.5", "sd1.5", "sd-1.5", "sd_1.5", "stable-diffusion-v1-5", "stable-diffusion 1.5", "sd15"):
		return "sd15"
	case contains(q, "sdxl", "sd-xl", "sd xl", "stable-diffusion-xl"):
		return "sdxl"
	// Flux variants — specific D / S labels first
	case contains(q, "flux.1 d", "flux1-d", "flux dev", "flux-dev"):
		return "flux-d"
	case contains(q, "flux.1 s", "flux1-s", "flux schnell", "flux-schnell"):
		return "flux-s"
	case contains(q, "flux"):
		return "flux"
	// Video families
	case contains(q, "hunyuan video", "hunyuanvideo", "hunyuan-video", "hunyuan"):
		return "hunyuan-video"
	// LLM family — version-specific labels first
	case contains(q, "qwen2.5", "qwen 2.5", "qwen-2.5"):
		return "qwen25"
	case contains(q, "qwen3", "qwen 3", "qwen-3"):
		return "qwen3"
	case contains(q, "qwen2", "qwen 2", "qwen-2"):
		return "qwen2"
	case contains(q, "llama 3.1", "llama-3.1", "llama3.1"):
		return "llama31"
	case contains(q, "llama 3", "llama-3", "llama3", "meta-llama-3"):
		return "llama3"
	case contains(q, "mixtral"):
		return "mixtral"
	case contains(q, "mistral"):
		return "mistral"
	}
	return "other"
}

// contains reports whether s contains any of the provided substrings.
// Helper kept package-local; case-folding done by caller.
func contains(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// ValidName reports whether the candidate name is a valid profile identifier.
// Profile names mirror model package directory names (e.g.
// "Qwen2.5-14B-Instruct-Q4_K_M") so we accept the punctuation that shows up
// in upstream model file names: dash, dot, underscore. 1–100 chars; first
// character must be a letter or digit (no leading punctuation).
func ValidName(name string) bool {
	if name == "" || len(name) > 100 {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case (r == '-' || r == '.' || r == '_') && i > 0:
		default:
			return false
		}
	}
	return true
}

// FlattenValues returns the active profile's Values flattened to a string
// map suitable for engine-runner token substitution. Nested objects use
// {parent}_{child} key naming, matching the legacy conf flatten behavior so
// `{ctx_size}` and `{model_dir}` tokens keep working.
func (s *Set) FlattenValues() map[string]string {
	out := map[string]string{}
	p := s.ActiveProfile()
	if p == nil {
		return out
	}
	for k, v := range p.Values {
		flattenInto(out, k, v)
	}
	return out
}

// flattenInto mirrors the helper in cmd/engine-runner/main.go so the two
// flatten layouts stay in sync.
func flattenInto(out map[string]string, key string, v any) {
	switch val := v.(type) {
	case string:
		out[key] = val
	case bool:
		if val {
			out[key] = "true"
		} else {
			out[key] = "false"
		}
	case float64:
		if val == float64(int64(val)) {
			out[key] = fmt.Sprintf("%d", int64(val))
		} else {
			out[key] = fmt.Sprintf("%v", val)
		}
	case int:
		out[key] = fmt.Sprintf("%d", val)
	case int64:
		out[key] = fmt.Sprintf("%d", val)
	case map[string]any:
		for k2, v2 := range val {
			flattenInto(out, key+"_"+k2, v2)
		}
	}
}
