package entities

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Template helpers shared between transform and aiService entities.

// dotPath walks `path` ("a.b[2].c" → ["a","b","2","c"]) into `input` and
// returns the leaf value. Returns nil when any segment is missing.
func dotPath(input any, path string) any {
	if path == "" {
		return input
	}
	// Convert "[N]" to ".N"
	normalized := bracketIdx.ReplaceAllString(path, ".$1")
	parts := strings.Split(normalized, ".")
	val := input
	for _, p := range parts {
		if val == nil {
			return nil
		}
		switch m := val.(type) {
		case map[string]any:
			val = m[p]
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(m) {
				return nil
			}
			val = m[i]
		default:
			return nil
		}
	}
	return val
}

var (
	bracketIdx     = regexp.MustCompile(`\[(\d+)\]`)
	tmplPrev       = regexp.MustCompile(`\{\{prev\}\}`)
	tmplStepDotted = regexp.MustCompile(`\{\{(\w+)\.([^}]+)\}\}`)
	tmplStep       = regexp.MustCompile(`\{\{(\w+)\}\}`)
)

// renderTemplate substitutes {{prev}}, {{stepId.path}}, and {{stepId}}
// references in `tmpl` using the supplied step results and prev value.
//
//   - {{prev}}            → prev (string as-is, others JSON-encoded)
//   - {{stepId.path}}     → dotPath(stepResults[stepId], path)
//   - {{stepId}}          → whole stepResults[stepId]
//
// Missing values render as empty string for {{prev}}/{{stepId.path}} and
// "{{stepId}}" passthrough for the bare-id form (legacy JS behavior).
func renderTemplate(tmpl string, stepResults map[string]any, prev any) string {
	out := tmplPrev.ReplaceAllStringFunc(tmpl, func(string) string {
		if prev == nil {
			return ""
		}
		return jsonStringify(prev)
	})

	out = tmplStepDotted.ReplaceAllStringFunc(out, func(m string) string {
		sub := tmplStepDotted.FindStringSubmatch(m)
		if len(sub) < 3 {
			return ""
		}
		id, path := sub[1], sub[2]
		val := dotPath(stepResults[id], path)
		if val == nil {
			return ""
		}
		return jsonStringify(val)
	})

	out = tmplStep.ReplaceAllStringFunc(out, func(m string) string {
		sub := tmplStep.FindStringSubmatch(m)
		if len(sub) < 2 {
			return m
		}
		id := sub[1]
		val, ok := stepResults[id]
		if !ok || val == nil {
			return m // passthrough literal "{{id}}" when missing
		}
		return jsonStringify(val)
	})

	return out
}

// jsonStringify returns strings as-is; everything else as compact JSON.
func jsonStringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// substituteParams walks an arbitrary JSON-like value, applying
// renderTemplate to every string leaf. Maps and slices recurse.
func substituteParams(v any, stepResults map[string]any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return renderTemplate(x, stepResults, nil)
	case []any:
		out := make([]any, len(x))
		for i, el := range x {
			out[i] = substituteParams(el, stepResults)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = substituteParams(val, stepResults)
		}
		return out
	}
	return v
}
