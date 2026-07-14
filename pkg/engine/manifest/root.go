package manifest

import (
	"os"
	"path/filepath"
	"sync"
)

// InstallRoot resolves the IANN install root directory. Resolution order:
//
//  1. ISANN_HOME environment variable (operator override)
//  2. Walk upward from the executable's directory until a sibling
//     `isann.config.json` file is found. Matches the same anchor that
//     pkg/installclient uses, so isannd / provider / installer all agree
//     on the root for any deployment layout, e.g.
//       <root>/bin/isannd
//       <root>/bin/proxy
//  3. Fallback: <exe>/../..  (deterministic two-level rule, used when
//     isann.config.json isn't deployed yet — e.g. very first install,
//     dev runs from `go run`)
//
// Layout this targets:
//
//	<root>/isann.config.json   ← anchor
//	<root>/manifests/<name>.json
//	<root>/engines/<name>/docker-compose.yml
//	<root>/conf/, packages/, db/, ...
//
// Cached on first call — exe location doesn't change during process life.
var (
	installRootOnce sync.Once
	installRootDir  string
)

// maxWalkDepth caps the upward walk so a runaway loop (e.g. exe on a network
// path with no anchor anywhere) can't pathologically stat all the way up.
// Twelve levels matches pkg/installclient's findIannRootFrom.
const maxWalkDepth = 12

// rootAnchor is the marker file that designates an install root. Matches
// pkg/installclient/client.go's anchor constant so both packages share the
// same definition of "root".
const rootAnchor = "isann.config.json"

func InstallRoot() string {
	installRootOnce.Do(func() {
		if env := os.Getenv("ISANN_HOME"); env != "" {
			installRootDir = env
			return
		}
		exe, err := os.Executable()
		if err != nil {
			if cwd, cerr := os.Getwd(); cerr == nil {
				installRootDir = cwd
			}
			return
		}
		if real, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		for i := 0; i < maxWalkDepth; i++ {
			if _, err := os.Stat(filepath.Join(dir, rootAnchor)); err == nil {
				installRootDir = dir
				return
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
		// Nothing found — fall back to the original two-up rule so the
		// path is still deterministic for pre-deploy / dev scenarios.
		installRootDir = filepath.Dir(filepath.Dir(exe))
	})
	return installRootDir
}

// ManifestPath returns the legacy flat path <root>/manifests/<name>.json.
// Convention: the file stem matches the container name (e.g. container "sd"
// → manifests/sd.json), so callers can look up by service name without an
// extra indirection.
func ManifestPath(name string) string {
	return filepath.Join(InstallRoot(), "manifests", name+".json")
}

// AppManifestPath returns <root>/artifacts/addon/engines/<name>/manifest.json —
// the engine manifest location in the engines/ tree (a folder with manifest.json
// is an engine).
func AppManifestPath(name string) string {
	return filepath.Join(InstallRoot(), "artifacts", "addon", "engines", name, "manifest.json")
}

// ManifestCandidates returns the engine-manifest path(s) for name: the engines/
// tree (artifacts/addon/engines/<name>/manifest.json) is the sole location.
func ManifestCandidates(name string) []string {
	return []string{AppManifestPath(name)}
}

// ResolveManifestPath returns the engine-manifest path for name when it exists,
// or "" otherwise. Shared by isannd (docker probe) and station (ready-check/
// start-body) so both agree on where a manifest lives (engines/<name>/manifest.json).
func ResolveManifestPath(name string) string {
	for _, p := range ManifestCandidates(name) {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}
