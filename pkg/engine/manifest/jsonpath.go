package manifest

import (
	"strconv"
	"strings"
)

// JSONPath walks a dotted path with optional [N] array indexing against a
// generic JSON-decoded object. Returns nil when any segment fails to
// resolve. Used to extract values from engine API responses per
// manifest fields (ready_check.model_path, inspect.fields[].path).
//
// Examples:
//
//	JSONPath(obj, "defaults.ctx_size")
//	JSONPath(obj, "data[0].max_model_len")
//	JSONPath(obj, "data[0].permission[0].id")
//	JSONPath(obj, "[0].model_name")
func JSONPath(obj any, path string) any {
	if path == "" || obj == nil {
		return nil
	}
	cur := obj
	for _, seg := range strings.Split(path, ".") {
		key, indices := parseJSONSegment(seg)
		if key != "" {
			m, ok := cur.(map[string]any)
			if !ok {
				return nil
			}
			cur = m[key]
		}
		for _, idx := range indices {
			arr, ok := cur.([]any)
			if !ok || idx < 0 || idx >= len(arr) {
				return nil
			}
			cur = arr[idx]
		}
		if cur == nil {
			return nil
		}
	}
	return cur
}

// parseJSONSegment splits "data[0][1]" into ("data", []int{0, 1}).
// A leading "[0]" (no key) yields ("", []int{0}) — used for top-level arrays.
func parseJSONSegment(seg string) (string, []int) {
	openIdx := strings.Index(seg, "[")
	if openIdx == -1 {
		return seg, nil
	}
	key := seg[:openIdx]
	rest := seg[openIdx:]
	var indices []int
	for len(rest) > 0 {
		if rest[0] != '[' {
			break
		}
		end := strings.Index(rest, "]")
		if end == -1 {
			break
		}
		n, err := strconv.Atoi(rest[1:end])
		if err != nil {
			break
		}
		indices = append(indices, n)
		rest = rest[end+1:]
	}
	return key, indices
}
