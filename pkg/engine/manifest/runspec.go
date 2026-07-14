package manifest

// runspec.go — `api.run`: manifest-driven inference (Phase 3 M1,
// docs/TODO/isann-cli-phase3.md).
//
// A RunSpec declares how a service runs inference: the engine endpoint, the
// user-facing parameters, a request-body template, and how to read the
// result. isann reads it and drives any JSON-bodied inference engine
// generically — a new engine ships a manifest, no isann patch. The CLI builds
// its --flags from Params (via the run-schema endpoint), submits generic
// params, and the provider maps them onto the engine body through Body.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// RunSpec is the `api.run` block.
type RunSpec struct {
	Path   string          `json:"path"`             // engine endpoint, e.g. "/v1/chat/completions"
	Method string          `json:"method,omitempty"` // default "POST"
	Result ResultSpec      `json:"result"`
	Params []RunParam      `json:"params"`
	Body   json.RawMessage `json:"body"` // request-body template; "${name}" placeholders

	// ExtraArgs (optional) relocates some body fields out of the top-level
	// request and into the prompt, for engines that ignore top-level params
	// (sd.cpp). Nil for engines that read top-level fields (OpenAI text).
	ExtraArgs *ExtraArgSpec `json:"extra_args,omitempty"`
}

// ExtraArgSpec injects body fields somewhere the engine actually reads them,
// for engines whose HTTP handler ignores top-level request fields.
//
// sd.cpp's OpenAI-compat handler drops top-level steps/cfg_scale/seed/… and
// uses its own defaults; the only way to apply them is to embed a JSON blob
// inside the prompt as `<tag>{...}</tag>`. ExtraArgSpec declares that:
//
//	{ "inject": "prompt", "tag": "sd_cpp_extra_args",
//	  "fields": ["steps","cfg_scale","seed","negative_prompt"] }
//
//	→ moves those keys out of the body and appends
//	  <sd_cpp_extra_args>{...}</sd_cpp_extra_args> to the prompt.
//
// Mirrors the control console's sdcpp.js wrapSdCppExtraArgs (mesh repo),
// generalized server-side.
type ExtraArgSpec struct {
	Inject string   `json:"inject"`        // where to inject: "prompt" (only mode today)
	Tag    string   `json:"tag,omitempty"` // wrapper element name (required for inject=prompt)
	Fields []string `json:"fields"`        // body keys to relocate (also the names used in the tag)
}

// RunParam is one inference parameter, surfaced to the CLI as --<name>.
type RunParam struct {
	Name     string   `json:"name"`               // CLI flag name → --<name>
	Type     string   `json:"type"`               // string | int | float | bool | file
	Required bool     `json:"required,omitempty"`
	Default  any      `json:"default,omitempty"`  // omitted only when nil (0/false/"" are kept)
	Options  []string `json:"options,omitempty"`  // enum choices (UI dropdown / CLI validation)
	Desc     string   `json:"desc,omitempty"`
}

// ResultSpec tells the CLI how to render the engine's response.
type ResultSpec struct {
	Modality    string `json:"modality"`               // text | image | audio | json
	ContentPath string `json:"content_path,omitempty"` // JSONPath to the result value
	StreamPath  string `json:"stream_path,omitempty"`  // JSONPath inside an SSE delta chunk
	Encoding    string `json:"encoding,omitempty"`     // "base64" → decode to a file
	Ext         string `json:"ext,omitempty"`          // file extension for image/audio
}

var (
	runParamTypes = map[string]bool{"string": true, "int": true, "float": true, "bool": true, "file": true}
	runModalities = map[string]bool{"text": true, "image": true, "audio": true, "json": true}

	// wholePlaceholderRe matches a string that is *exactly* "${name}".
	wholePlaceholderRe = regexp.MustCompile(`^\$\{([A-Za-z0-9_-]+)\}$`)
	// embeddedPlaceholderRe matches "${name}" anywhere inside a string.
	embeddedPlaceholderRe = regexp.MustCompile(`\$\{([A-Za-z0-9_-]+)\}`)
)

// MethodOr returns the configured HTTP method, defaulting to POST.
func (rs *RunSpec) MethodOr() string {
	if rs.Method == "" {
		return "POST"
	}
	return rs.Method
}

// Validate checks the run spec's invariants. Called from Manifest.Validate.
func (rs *RunSpec) Validate() error {
	if rs.Path == "" {
		return fmt.Errorf("api.run.path is required")
	}
	if rs.Method != "" && rs.Method != "POST" && rs.Method != "GET" {
		return fmt.Errorf("api.run.method %q unsupported (POST|GET)", rs.Method)
	}
	if !runModalities[rs.Result.Modality] {
		return fmt.Errorf("api.run.result.modality %q unsupported (text|image|audio|json)", rs.Result.Modality)
	}
	seen := map[string]bool{}
	for i, p := range rs.Params {
		if p.Name == "" {
			return fmt.Errorf("api.run.params[%d].name is required", i)
		}
		if seen[p.Name] {
			return fmt.Errorf("api.run.params[%d]: duplicate name %q", i, p.Name)
		}
		seen[p.Name] = true
		if !runParamTypes[p.Type] {
			return fmt.Errorf("api.run.params[%d] (%s): type %q unsupported (string|int|float|bool|file)", i, p.Name, p.Type)
		}
	}
	if len(rs.Body) > 0 {
		var tmp any
		if err := json.Unmarshal(rs.Body, &tmp); err != nil {
			return fmt.Errorf("api.run.body invalid JSON: %w", err)
		}
	}
	if rs.ExtraArgs != nil {
		if err := rs.ExtraArgs.Validate(); err != nil {
			return fmt.Errorf("api.run.extra_args: %w", err)
		}
	}
	return nil
}

// Validate checks the extra_args block. Only inject="prompt" is supported today.
func (e *ExtraArgSpec) Validate() error {
	if e.Inject != "prompt" {
		return fmt.Errorf("inject %q unsupported (only \"prompt\")", e.Inject)
	}
	if e.Tag == "" {
		return fmt.Errorf("tag is required for inject=prompt")
	}
	if len(e.Fields) == 0 {
		return fmt.Errorf("fields must be non-empty")
	}
	return nil
}

// Apply relocates the declared fields out of the top-level body and into the
// prompt as `<tag>{json}</tag>` (inject="prompt"). For engines (sd.cpp) whose
// HTTP handler ignores top-level gen params — the only way to apply them is
// to embed them in the prompt. No-op when nil, not prompt-inject, the prompt
// is already wrapped, or there is nothing to move. Mirrors sdcpp.js.
func (e *ExtraArgSpec) Apply(body map[string]any) {
	if e == nil || e.Inject != "prompt" || e.Tag == "" || len(e.Fields) == 0 {
		return
	}
	prompt, _ := body["prompt"].(string)
	if strings.Contains(prompt, "<"+e.Tag+">") {
		// Already wrapped (a client pre-wrapped the prompt) — just drop the
		// listed top-level keys so the engine doesn't see duplicates.
		for _, f := range e.Fields {
			delete(body, f)
		}
		return
	}
	extra := map[string]any{}
	for _, f := range e.Fields {
		v, ok := body[f]
		if !ok || v == nil {
			continue
		}
		if s, isStr := v.(string); isStr && s == "" {
			continue // empty string → let the engine use its default
		}
		extra[f] = v
		delete(body, f)
	}
	if len(extra) == 0 {
		return // nothing to inject → leave the prompt untouched
	}
	blob, _ := json.Marshal(extra) // encoding/json emits map keys sorted → deterministic
	body["prompt"] = prompt + "<" + e.Tag + ">" + string(blob) + "</" + e.Tag + ">"
}

// resolved is a parameter's effective value for substitution.
type resolved struct {
	val any
	set bool
}

// BuildBody substitutes user params (keyed by CLI name) into the body template
// and returns the engine-ready request JSON. Two rules:
//
//   - type substitution: a whole-string placeholder "${name}" is replaced by
//     the parameter's actual JSON value (number/bool/string), NOT a quoted
//     string — so {"steps":"${steps}"} with steps=20 emits {"steps":20}.
//   - omit-if-unset: a placeholder with neither a supplied value nor a default
//     drops its enclosing map key; an array element (object) carrying such a
//     placeholder drops entirely (e.g. the optional system message).
//
// A required param that is unset (no value and no default) is an error.
// When Body is empty the params object is passed straight through.
func (rs *RunSpec) BuildBody(params map[string]any) (json.RawMessage, error) {
	res := map[string]resolved{}
	for _, p := range rs.Params {
		switch {
		case params[p.Name] != nil:
			res[p.Name] = resolved{val: params[p.Name], set: true}
		case p.Default != nil:
			res[p.Name] = resolved{val: p.Default, set: true}
		default:
			if p.Required {
				return nil, fmt.Errorf("missing required parameter %q", p.Name)
			}
			res[p.Name] = resolved{set: false}
		}
	}

	if len(rs.Body) == 0 {
		return json.Marshal(params)
	}
	var tmpl any
	if err := json.Unmarshal(rs.Body, &tmpl); err != nil {
		return nil, fmt.Errorf("api.run.body invalid JSON: %w", err)
	}
	out, _ := substituteRunTemplate(tmpl, res)
	return json.Marshal(out)
}

// substitute walks the template, replacing placeholders. Returns the
// transformed node and whether it is "present" — false means "drop me" (an
// unset whole-string placeholder).
func substituteRunTemplate(node any, res map[string]resolved) (any, bool) {
	switch v := node.(type) {
	case string:
		if m := wholePlaceholderRe.FindStringSubmatch(v); m != nil {
			r, ok := res[m[1]]
			if !ok || !r.set {
				return nil, false // unset → drop
			}
			return r.val, true // typed value (number/bool/string) emitted as-is
		}
		if embeddedPlaceholderRe.MatchString(v) {
			// Embedded placeholders → string interpolation (unset → "").
			out := embeddedPlaceholderRe.ReplaceAllStringFunc(v, func(tok string) string {
				name := embeddedPlaceholderRe.FindStringSubmatch(tok)[1]
				if r, ok := res[name]; ok && r.set {
					return fmt.Sprintf("%v", r.val)
				}
				return ""
			})
			return out, true
		}
		return v, true // constant string
	case map[string]any:
		out := map[string]any{}
		for k, child := range v {
			if cv, present := substituteRunTemplate(child, res); present {
				out[k] = cv
			}
		}
		return out, true
	case []any:
		out := []any{}
		for _, elem := range v {
			// An object element carrying an unset placeholder drops entirely.
			if obj, ok := elem.(map[string]any); ok && objectHasUnset(obj, res) {
				continue
			}
			if cv, present := substituteRunTemplate(elem, res); present {
				out = append(out, cv)
			}
		}
		return out, true
	default:
		return node, true // number, bool, null
	}
}

// objectHasUnset reports whether any direct value of obj is a whole-string
// placeholder that resolves to unset — the signal to drop the whole element
// from an array.
func objectHasUnset(obj map[string]any, res map[string]resolved) bool {
	for _, child := range obj {
		s, ok := child.(string)
		if !ok {
			continue
		}
		if m := wholePlaceholderRe.FindStringSubmatch(s); m != nil {
			if r, ok := res[m[1]]; !ok || !r.set {
				return true
			}
		}
	}
	return false
}
