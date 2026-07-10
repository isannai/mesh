package pipeline

// topoSort orders node IDs by dependency. Config edges (targetHandle =
// "node" | "options") are excluded from ordering because they inject
// values into already-resolved nodes rather than sequencing execution.
//
// Implementation: Kahn's algorithm. Isolated nodes (no incoming/outgoing
// edges) are included with in-degree 0.
//
// The returned slice length may be less than len(nodes) when a cycle
// exists; callers are expected to have rejected cycles in validate.go
// before calling, but the function itself is cycle-safe.
func topoSort(nodes []Node, edges []Edge) []string {
	adj := make(map[string][]string, len(nodes))
	inDegree := make(map[string]int, len(nodes))
	for _, n := range nodes {
		adj[n.ID] = nil
		inDegree[n.ID] = 0
	}
	for _, e := range edges {
		if isConfigHandle(e.TargetHandle) {
			continue
		}
		if _, ok := adj[e.Source]; !ok {
			// Dangling source — skip to avoid index errors.
			continue
		}
		if _, ok := inDegree[e.Target]; !ok {
			continue
		}
		adj[e.Source] = append(adj[e.Source], e.Target)
		inDegree[e.Target]++
	}

	queue := make([]string, 0, len(nodes))
	// Preserve node declaration order among nodes of in-degree 0.
	for _, n := range nodes {
		if inDegree[n.ID] == 0 {
			queue = append(queue, n.ID)
		}
	}

	sorted := make([]string, 0, len(nodes))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		sorted = append(sorted, id)
		for _, next := range adj[id] {
			inDegree[next]--
			if inDegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	return sorted
}

// hasCycle reports whether the graph (ignoring config edges) contains a cycle.
func hasCycle(nodes []Node, edges []Edge) bool {
	return len(topoSort(nodes, edges)) != len(nodes)
}
