package station

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/isannai/mesh/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/profile"
	"github.com/isannai/mesh/pkg/setup"
)

// extractInspect builds the (values, labels, order) triple advertised by a
// service per the manifest's inspect.fields declaration.
//
// Resolution order per field:
//   - f.From == "api"  → walk apiBody (engine HTTP probe), nil-safe
//   - f.From == "conf" → walk confJSON (parsed conf/<service>.json), nil-safe
//   - default ("" or anything else) → look up profileValues by f.Key
//
// A field is silently dropped when its source is unavailable or its path
// resolves to an empty/missing value, so unreachable engines just produce
// fewer rows rather than blocking the register.
func extractInspect(
	m *manifest.Manifest,
	confJSON map[string]any,
	apiBody []byte,
	profileValues map[string]any,
	svc setup.ServiceEntry,
) (values, labels map[string]string, order []string) {
	if m == nil || m.Inspect == nil || len(m.Inspect.Fields) == 0 {
		return nil, nil, nil
	}

	var apiObj any
	if len(apiBody) > 0 {
		_ = json.Unmarshal(apiBody, &apiObj)
	}

	values = map[string]string{}
	labels = map[string]string{}
	for _, f := range m.Inspect.Fields {
		var v any
		switch f.From {
		case "api":
			if apiObj != nil {
				v = manifest.JSONPath(apiObj, f.Path)
			}
		case "conf":
			if confJSON != nil {
				v = manifest.JSONPath(confJSON, f.Path)
			}
		default:
			// Lookup in active profile values by the field's flat key,
			// supporting nested objects (e.g. model.default → model_default
			// is already flattened by profile.FlattenValues callers).
			if profileValues != nil {
				if pv, ok := profileValues[f.Key]; ok {
					v = pv
				}
			}
		}
		s, ok := stringifyInspectValue(v)
		if !ok {
			continue
		}
		values[f.Key] = s
		labels[f.Key] = f.Label
		order = append(order, f.Key)
	}
	if len(values) == 0 {
		return nil, nil, nil
	}
	return values, labels, order
}

// stringifyInspectValue collapses a JSON-decoded value into the string the
// register payload carries. Returns ok=false when the value is empty/missing
// and the field should be dropped.
func stringifyInspectValue(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", false
	case string:
		if t == "" {
			return "", false
		}
		return t, true
	case bool:
		return strconv.FormatBool(t), true
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10), true
		}
		return strconv.FormatFloat(t, 'f', -1, 64), true
	case json.Number:
		return t.String(), true
	}
	// Anything else (object, array): emit JSON form. Avoids surprising
	// "map[a:1]" style stringification.
	if b, err := json.Marshal(v); err == nil {
		s := string(b)
		if s == "" || s == `""` || s == "null" {
			return "", false
		}
		return s, true
	}
	return fmt.Sprintf("%v", v), true
}

// loadServiceConfJSON reads the conf JSON adjacent to a service entry.
// For services whose conf path isn't explicitly declared, this falls back
// to conf/<svc.Name>.json relative to provider's conf dir.
//
// Returns nil if no conf file exists (e.g. external services like vllm-api).
func loadServiceConfJSON(svc setup.ServiceEntry, providerCfgPath string) map[string]any {
	if svc.IsManagedLocally() == false {
		return nil
	}
	dir := filepath.Dir(providerCfgPath)
	if dir == "" {
		dir = "conf"
	}
	candidate := filepath.Join(dir, svc.Name+".json")
	data, err := os.ReadFile(candidate)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil
	}
	return out
}

// loadActiveProfileValues reads `<conf-dir>/profiles/<svc>.json` and returns
// the active profile's Values map. Returns nil when the file is missing or
// has no profiles — extractInspect treats nil as "no profile values".
func loadActiveProfileValues(svc setup.ServiceEntry, providerCfgPath string) map[string]any {
	confDir := filepath.Dir(providerCfgPath)
	if confDir == "" {
		confDir = "conf"
	}
	path := profile.SetPathForName(confDir, svc.Name)
	set, err := profile.LoadSet(path)
	if err != nil || set == nil {
		return nil
	}
	active := set.ActiveProfile()
	if active == nil {
		return nil
	}
	return active.Values
}

// loadInspectManifest resolves the engine manifest for a service for the
// inspect path. Path comes from svc.Manifest (declared in
// conf/provider.json's services[]). Docker-launched services normally
// declare it there; returns nil when the field is empty or the file
// can't be loaded — callers degrade to "no inspect detail" rather than
// erroring the request.
func loadInspectManifest(svc setup.ServiceEntry, providerCfgPath, packagesDir string) *manifest.Manifest {
	return loadServiceManifest(svc, packagesDir)
}

