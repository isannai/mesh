// Package stationwire derives station inference services from the engines
// installed under artifacts/addon/engines/ — so a node serves its engines with no
// hand-written station.json (zero-config auto-wire).
//
// For each engine folder (manifest.json + docker-compose marker) it builds a
// setup.ServiceEntry: the engine name from the folder, the host addr from the
// compose ports mapping (with .env variable resolution), and queue/enable/name
// from the engine's optional manifest `station` block. The result is the base
// service set; station.json (if present) overrides it (see tunnel.LoadConfig).
//
// Shared by isannd (register-frame advertising) and the mesh station backend
// (inference serving) via the go module replace directive, so both agree on
// which engines a node exposes.
package stationwire

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/isannai/mesh/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/setup"
)

// EnginesDir is the engine recipe tree an install root exposes.
func EnginesDir(installRoot string) string {
	return filepath.Join(installRoot, "artifacts", "addon", "engines")
}

// RootFromConfigPath recovers the install root from a station config path that
// lives under the meshes/ tree (…/artifacts/addon/meshes/station/conf/station.json →
// the dir before "artifacts"). Returns "" when the path isn't under artifacts/,
// so a non-station config (broker etc.) triggers no derivation.
func RootFromConfigPath(configPath string) string {
	abs, err := filepath.Abs(configPath)
	if err != nil {
		abs = configPath
	}
	parts := strings.Split(filepath.ToSlash(abs), "/")
	for i, p := range parts {
		if p == "artifacts" {
			return filepath.FromSlash(strings.Join(parts[:i], "/"))
		}
	}
	return ""
}

// ResolveServices returns the effective service set: DeriveServices(installRoot)
// as the base, with `override` (e.g. station.json's services) applied on top —
// see MergeServices. A missing apps/ dir means the override is returned as-is.
func ResolveServices(installRoot string, override []setup.ServiceEntry) ([]setup.ServiceEntry, error) {
	derived, err := DeriveServices(installRoot)
	if err != nil {
		return override, err
	}
	return MergeServices(derived, override), nil
}

// MergeServices overlays operator `override` entries onto the auto-`derived`
// base. Match is by Engine (then Name); a matching override overlays only its
// SET fields (Name/Addr non-empty, Enable/Queue non-nil) so `{engine, enable:
// false}` disables an engine without restating its addr. An override with no
// match is appended (e.g. an external, non-apps service the operator added).
func MergeServices(derived, override []setup.ServiceEntry) []setup.ServiceEntry {
	out := append([]setup.ServiceEntry(nil), derived...)
	idx := func(o setup.ServiceEntry) int {
		for i, d := range out {
			if (o.Engine != "" && d.Engine == o.Engine) || (o.Engine == "" && d.Name == o.Name) {
				return i
			}
		}
		return -1
	}
	for _, o := range override {
		i := idx(o)
		if i < 0 {
			out = append(out, o) // operator-added, not from apps/
			continue
		}
		if o.Name != "" {
			out[i].Name = o.Name
		}
		if o.Addr != "" {
			out[i].Addr = o.Addr
		}
		if o.Type != "" {
			out[i].Type = o.Type
		}
		if o.Enable != nil {
			out[i].Enable = o.Enable
		}
		if o.Queue != nil {
			out[i].Queue = o.Queue
		}
		if o.Options != nil {
			out[i].Options = o.Options
		}
	}
	return out
}

// DeriveServices scans <root>/artifacts/addon/engines/ and returns one ServiceEntry
// per engine (a folder with manifest.json + a docker-compose file). An engine
// whose host addr can't be determined (no compose ports, no manifest host_addr)
// is skipped — it can't be routed to. A missing apps/ dir yields nil, no error.
func DeriveServices(installRoot string) ([]setup.ServiceEntry, error) {
	base := EnginesDir(installRoot)
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	// os.ReadDir returns entries sorted by name, so derivation is deterministic —
	// on a host-addr collision the alphabetically-first engine wins.
	var out []setup.ServiceEntry
	seenAddr := map[string]string{} // addr → engine that claimed it
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		engine := e.Name()
		dir := filepath.Join(base, engine)
		// engine marker: manifest.json (+ a compose file below).
		m, mErr := manifest.Load(filepath.Join(dir, "manifest.json"))
		if mErr != nil {
			continue // no engine manifest → not an engine (core/app/tool folder)
		}
		composePath, ok := findCompose(dir)
		if !ok {
			continue // marker also requires a compose file
		}
		se, ok := serviceFor(engine, dir, composePath, m)
		if !ok {
			continue // unroutable (no host addr) — skip
		}
		if _, dup := seenAddr[se.Addr]; dup {
			continue // host-addr collision — first engine keeps it, skip this one
		}
		seenAddr[se.Addr] = engine
		out = append(out, se)
	}
	return out, nil
}

// Collisions reports host-addr conflicts among the derived services — engines
// that map to the same "host:port" (a misconfiguration: only one can bind it).
// Returns addr → the engines contending for it (len ≥ 2). Empty when clean.
func Collisions(installRoot string) (map[string][]string, error) {
	base := EnginesDir(installRoot)
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	byAddr := map[string][]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, e.Name())
		m, mErr := manifest.Load(filepath.Join(dir, "manifest.json"))
		if mErr != nil {
			continue
		}
		composePath, ok := findCompose(dir)
		if !ok {
			continue
		}
		if se, ok := serviceFor(e.Name(), dir, composePath, m); ok {
			byAddr[se.Addr] = append(byAddr[se.Addr], e.Name())
		}
	}
	out := map[string][]string{}
	for addr, engines := range byAddr {
		if len(engines) >= 2 {
			out[addr] = engines
		}
	}
	return out, nil
}

// serviceFor builds one ServiceEntry, applying the manifest `station` overrides
// on top of the derived defaults. Returns ok=false when no host addr resolves.
// The ENGINE identity is the manifest's `name` field (NOT the folder name), so a
// versioned/renamed folder (apps/llama@0.1.0/) still yields engine "llama".
func serviceFor(folder, dir, composePath string, m *manifest.Manifest) (setup.ServiceEntry, bool) {
	st := m.Station // may be nil

	engine := m.Name
	if engine == "" {
		engine = folder // fallback: malformed manifest with no name
	}

	addr := ""
	if st != nil && st.HostAddr != "" {
		addr = st.HostAddr
	} else if hp := deriveHostAddr(composePath, dir); hp != "" {
		addr = hp
	}
	if addr == "" {
		return setup.ServiceEntry{}, false
	}

	name := engine + "-api"
	if st != nil && st.ServiceName != "" {
		name = st.ServiceName
	}

	se := setup.ServiceEntry{Name: name, Addr: addr, Engine: engine}
	if st != nil {
		if st.Enable != nil {
			se.Enable = st.Enable
		}
		if st.Queue != nil {
			se.Queue = &setup.QueueOverride{
				MaxQueue:    st.Queue.MaxQueue,
				Concurrency: st.Queue.Concurrency,
				SaveToDisk:  st.Queue.SaveToDisk,
			}
		}
	}
	return se, true
}

// findCompose returns the engine's docker-compose path (yml or yaml).
func findCompose(dir string) (string, bool) {
	for _, f := range []string{"docker-compose.yml", "docker-compose.yaml"} {
		p := filepath.Join(dir, f)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
	}
	return "", false
}

// deriveHostAddr reads the compose file, resolves ${VAR} from the sibling .env,
// and returns "127.0.0.1:<hostPort>" for the FIRST published host port found —
// the address station uses to reach the engine on this host. "" when none.
func deriveHostAddr(composePath, dir string) string {
	raw, err := os.ReadFile(composePath)
	if err != nil {
		return ""
	}
	resolved := resolveComposeVars(raw, dir)
	if port := firstHostPort(resolved); port != "" {
		return "127.0.0.1:" + port
	}
	return ""
}

// portLineRe matches a compose ports LIST item in short string form, capturing
// the host-side port from "HOST:CONTAINER" or "IP:HOST:CONTAINER" (protocol
// suffix like "/tcp" and quotes tolerated). Long-form (published:) handled below.
var (
	portsBlockRe = regexp.MustCompile(`(?m)^\s*ports:\s*$`)
	shortPortRe  = regexp.MustCompile(`^\s*-\s*["']?([0-9.]+(?::[0-9]+){1,2})(?:/\w+)?["']?\s*$`)
	publishedRe  = regexp.MustCompile(`(?m)published:\s*["']?(\d+)`)
)

// firstHostPort extracts the first host-published port from compose YAML text.
// Handles short list form ("7862:8080", "127.0.0.1:7862:8080") and long form
// (published: 7862). A bare container-only port ("8080") yields no host port.
func firstHostPort(compose []byte) string {
	lines := strings.Split(string(compose), "\n")
	inPorts := false
	for _, ln := range lines {
		if portsBlockRe.MatchString(ln) {
			inPorts = true
			continue
		}
		if inPorts {
			if m := shortPortRe.FindStringSubmatch(ln); m != nil {
				return hostPortOf(m[1])
			}
			// leaving the ports: block (a non-list, less-indented key)
			trimmed := strings.TrimSpace(ln)
			if trimmed != "" && !strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "#") {
				inPorts = false
			}
		}
	}
	// long form fallback (published: N anywhere)
	if m := publishedRe.FindSubmatch(compose); m != nil {
		return string(m[1])
	}
	return ""
}

// hostPortOf returns the host port from "HOST:CONTAINER" (2 parts) or
// "IP:HOST:CONTAINER" (3 parts).
func hostPortOf(mapping string) string {
	parts := strings.Split(mapping, ":")
	switch len(parts) {
	case 2:
		return parts[0]
	case 3:
		return parts[1]
	}
	return ""
}

// --- .env variable resolution (mirrors pkg/addon lint's resolver) ------------

var composeVarRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(:-|-)?([^}]*)\}`)

// resolveComposeVars substitutes ${VAR}, ${VAR:-default}, ${VAR-default} using
// the app folder's .env, so a `${HOST_PORT:-7862}:8080` ports entry resolves to
// a concrete host port before parsing.
func resolveComposeVars(raw []byte, appDir string) []byte {
	env := parseDotEnv(filepath.Join(appDir, ".env"))
	return composeVarRe.ReplaceAllFunc(raw, func(m []byte) []byte {
		sub := composeVarRe.FindSubmatch(m)
		name, def := string(sub[1]), string(sub[3])
		if v, ok := env[name]; ok && v != "" {
			return []byte(v)
		}
		return []byte(def) // "" when no default given
	})
}

// parseDotEnv reads KEY=VALUE lines (comments/blanks skipped, quotes stripped).
func parseDotEnv(path string) map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return out
	}
	for _, ln := range strings.Split(string(data), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || strings.HasPrefix(ln, "#") {
			continue
		}
		k, v, ok := strings.Cut(ln, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		out[k] = v
	}
	return out
}
