package pipeline

import (
	"fmt"
	"strings"
)

// validateGraph runs static checks before execution:
//   - unique node IDs
//   - edges reference existing nodes
//   - target handle exists on target entity (when entity declares inputs)
//   - source/target handle types are compatible
//   - no cycles (ignoring config handles)
//
// Missing entities or "any" types silently pass — keeps graph valid while
// development is in progress.
func validateGraph(g *Graph, registry *Registry) []string {
	var errs []string

	// Unique ID check
	seen := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		if n.ID == "" {
			errs = append(errs, "node with empty id")
			continue
		}
		if seen[n.ID] {
			errs = append(errs, fmt.Sprintf("duplicate node id: %s", n.ID))
			continue
		}
		seen[n.ID] = true
	}

	nodeByID := make(map[string]Node, len(g.Nodes))
	for _, n := range g.Nodes {
		nodeByID[n.ID] = n
	}

	for _, e := range g.Edges {
		src, srcOk := nodeByID[e.Source]
		tgt, tgtOk := nodeByID[e.Target]
		if !srcOk {
			errs = append(errs, fmt.Sprintf("edge %s → %s: source not found", e.Source, e.Target))
			continue
		}
		if !tgtOk {
			errs = append(errs, fmt.Sprintf("edge %s → %s: target not found", e.Source, e.Target))
			continue
		}

		srcEntity := registry.Get(src.Type)
		tgtEntity := registry.Get(tgt.Type)
		if srcEntity == nil || tgtEntity == nil {
			// Missing entity — let runner handle it (warn + skip). Not a validation error.
			continue
		}

		srcHandle := edgeSourceHandle(e)
		tgtHandle := edgeTargetHandle(e)

		// target handle must exist when entity declares non-empty input schema
		tgtInputs := tgtEntity.Inputs()
		if len(tgtInputs) > 0 {
			if _, ok := tgtInputs[tgtHandle]; !ok {
				errs = append(errs, fmt.Sprintf(
					"edge %s → %s: target has no input handle %q (node.type=%s)",
					e.Source, e.Target, tgtHandle, tgt.Type,
				))
				continue
			}
		}

		srcType := srcEntity.Outputs()[srcHandle]
		tgtType := tgtEntity.Inputs()[tgtHandle]
		if srcType != "" && tgtType != "" && !isTypeCompatible(srcType, tgtType) {
			errs = append(errs, fmt.Sprintf(
				"edge %s[%s:%s] → %s[%s:%s]: type mismatch",
				e.Source, srcHandle, srcType, e.Target, tgtHandle, tgtType,
			))
		}
	}

	// Cycle detection (ignoring config handles)
	if hasCycle(g.Nodes, g.Edges) {
		errs = append(errs, "graph contains a cycle")
	}

	return errs
}

// isTypeCompatible reports whether a source handle type can feed a target
// handle type. Rules mirror the JS runner:
//   - exact match
//   - "any" on either side
//   - json ↔ object interchangeable
//   - pipe-separated union ("image|json") matches any member
func isTypeCompatible(source, target string) bool {
	if source == target {
		return true
	}
	if source == "any" || target == "any" {
		return true
	}
	if (source == "json" && target == "object") || (source == "object" && target == "json") {
		return true
	}
	if strings.Contains(target, "|") {
		for _, t := range strings.Split(target, "|") {
			if isTypeCompatible(source, strings.TrimSpace(t)) {
				return true
			}
		}
	}
	if strings.Contains(source, "|") {
		for _, s := range strings.Split(source, "|") {
			if isTypeCompatible(strings.TrimSpace(s), target) {
				return true
			}
		}
	}
	return false
}
