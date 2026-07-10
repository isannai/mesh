package entities

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/isannai/mesh/pkg/pipeline"
)

// transformParams holds the per-kind parameters; only fields relevant to
// the chosen Transform kind are read.
type transformParams struct {
	Path     string `json:"path,omitempty"`     // extract
	Template string `json:"template,omitempty"` // template
	Pattern  string `json:"pattern,omitempty"`  // regex
	Replace  string `json:"replace,omitempty"`  // regex replace (optional)
	// json_merge: rest of params merged with input via raw map below
}

type transformData struct {
	Transform string          `json:"transform"`        // extract|template|regex|json_merge
	Params    transformParams `json:"params,omitempty"` // typed view
	RawParams json.RawMessage `json:"-"`                // for json_merge raw fallback
}

// UnmarshalJSON captures the raw params blob so json_merge can use the
// original map (not the typed transformParams).
func (d *transformData) UnmarshalJSON(b []byte) error {
	type alias struct {
		Transform string          `json:"transform"`
		Params    json.RawMessage `json:"params"`
	}
	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}
	d.Transform = a.Transform
	d.RawParams = a.Params
	if len(a.Params) > 0 {
		_ = json.Unmarshal(a.Params, &d.Params)
	}
	return nil
}

// Transform performs local JSON/text transforms with no network calls.
// Supported kinds: extract, template, regex, json_merge.
type Transform struct{}

func (Transform) Type() string { return "transformNode" }

// Inputs returns an empty map so the validator does not enforce a specific
// handle name. transformNode reads upstream values from ec.StepResults via
// {{stepId}} / {{stepId.path}} template references, so the targetHandle
// name on incoming edges is meaningful only as a label — any handle is
// accepted.
func (Transform) Inputs() map[string]string  { return map[string]string{} }
func (Transform) Outputs() map[string]string { return map[string]string{"default": "any"} }

func (Transform) Execute(_ context.Context, node pipeline.Node, ec *pipeline.ExecCtx) (any, error) {
	if len(node.Data) == 0 {
		return ec.InputData, nil
	}
	var d transformData
	_ = json.Unmarshal(node.Data, &d)

	switch d.Transform {
	case "extract":
		return dotPath(ec.InputData, d.Params.Path), nil

	case "template":
		return renderTemplate(d.Params.Template, ec.StepResults, ec.InputData), nil

	case "regex":
		return regexOp(ec.InputData, d.Params.Pattern, d.Params.Replace), nil

	case "json_merge":
		return jsonMerge(ec.InputData, d.RawParams), nil

	default:
		return ec.InputData, nil
	}
}

// regexOp finds or replaces a pattern in `input`. When `replace` is empty
// the first match is returned; otherwise replaceAll is performed.
func regexOp(input any, pattern, replace string) any {
	if pattern == "" {
		return input
	}
	str := ""
	if s, ok := input.(string); ok {
		str = s
	} else {
		str = jsonStringify(input)
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return input
	}
	if replace != "" {
		return re.ReplaceAllString(str, replace)
	}
	if loc := re.FindString(str); loc != "" {
		return loc
	}
	return str
}

// jsonMerge shallow-merges params into input when both are objects.
func jsonMerge(input any, rawParams json.RawMessage) any {
	if len(rawParams) == 0 {
		return input
	}
	inMap, ok := input.(map[string]any)
	if !ok {
		return input
	}
	var paramsMap map[string]any
	if err := json.Unmarshal(rawParams, &paramsMap); err != nil {
		return input
	}
	out := make(map[string]any, len(inMap)+len(paramsMap))
	for k, v := range inMap {
		out[k] = v
	}
	for k, v := range paramsMap {
		out[k] = v
	}
	return out
}
