package setup

import "sort"

// InspectItem is one configured option of a service as a page shows it: the
// key, the name to show and the value. ServiceInfo.Inspects is the list of them
// in display order.
//
// It says what ServiceInfo.Inspect (key to value), InspectLabels (key to name)
// and InspectOrder (display order) say together, so a reader takes one list
// instead of matching three. Those three stay what a node fills: the RV copies
// them without knowing a key, and older nodes and readers use them. Inspects is
// derived from them (DeriveInspects).
//
// This file is copied as-is into GLink, servers and mesh (pkg/setup).
type InspectItem struct {
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
	Value string `json:"value"`
}

// DeriveInspects sets Inspects from Inspect, InspectLabels and InspectOrder: the
// keys of InspectOrder first, in that order, then any other key by name. A key
// with no value is left out. When Inspect is empty it does nothing, so a service
// that carries only the list keeps it.
func (s *ServiceInfo) DeriveInspects() {
	if len(s.Inspect) == 0 {
		return
	}
	items := make([]InspectItem, 0, len(s.Inspect))
	seen := make(map[string]bool, len(s.Inspect))
	add := func(k string) {
		v, ok := s.Inspect[k]
		if !ok || seen[k] {
			return
		}
		seen[k] = true
		items = append(items, InspectItem{Key: k, Label: s.InspectLabels[k], Value: v})
	}
	for _, k := range s.InspectOrder {
		add(k)
	}
	rest := make([]string, 0, len(s.Inspect))
	for k := range s.Inspect {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	for _, k := range rest {
		add(k)
	}
	s.Inspects = items
}

// InspectValue is the value of one option: from the list when it has the key,
// else from the Inspect map an older node sends. "" when the service has none.
func (s ServiceInfo) InspectValue(key string) string {
	for _, it := range s.Inspects {
		if it.Key == key {
			return it.Value
		}
	}
	return s.Inspect[key]
}
