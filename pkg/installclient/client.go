package installclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/isannai/mesh/pkg/setup"
)

// InstallClient provides a shared interface for spawning the Installer CLI
// and streaming its SSE progress events. Used by both Provider and Broker.
type InstallClient struct {
	FetcherBin string // resolved path to fetcher worker binary (isann-fetcher.exe)
	SSEAddr    string // SSE progress server address (default: 127.0.0.1:47801)
	WorkDir    string // working directory for installer process
}

// InstallRequest describes a software install/uninstall request.
type InstallRequest struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Version     string          `json:"version"`
	PackageJSON json.RawMessage `json:"package_json,omitempty"`
	// Catalog-install fields. The broker frontend sets these when installing
	// from a HuggingFace / Civitai search result so the spawned installer CLI
	// can resolve the upstream package. Without these, model installs fail
	// with "-model requires --repo or --src".
	Repo    string `json:"repo,omitempty"`
	Src     string `json:"src,omitempty"`
	Service string `json:"service,omitempty"`
	// Architecture targets LoRA installs — required by the strict CLI
	// (`-lora` refuses without --architecture). Maps to the architecture
	// subdir under packages/loras/ (e.g. sd15, sdxl, qwen2).
	Architecture string `json:"architecture,omitempty"`
	Path         string `json:"path,omitempty"`
}

// ProgressEvent represents a single SSE progress event from the Installer.
type ProgressEvent struct {
	Event    string `json:"event"`
	FileName string `json:"file_name,omitempty"`
	Index    int    `json:"index,omitempty"`
	Total    int    `json:"total,omitempty"`
	Percent  int    `json:"percent,omitempty"`
	Message  string `json:"message,omitempty"`
}

const defaultSSEAddr = "127.0.0.1:47801"

// TypeDir returns the plural on-disk folder name for a singular package type.
// Type values from Gate are singular ("core", "service", "engine", "model", "mcp"),
// but the on-disk packages/ subfolder is the plural collection form.
func TypeDir(typeName string) string {
	switch typeName {
	case "core":
		return "cores"
	case "service":
		return "services"
	case "engine":
		return "engines"
	case "model":
		return "models"
	case "lora":
		return "loras"
	case "dependency":
		return "deps"
	default:
		if strings.HasSuffix(typeName, "s") {
			return typeName
		}
		return typeName + "s"
	}
}

// New creates an InstallClient by auto-detecting the installer binary location.
// WorkDir is set to the iann root (the directory containing isann.config.json)
// rather than the binary's own directory — the spawned installer expects to
// read "conf/<bin>.json" relative to CWD, and the binary's own bin/ dir has
// no conf/ subtree.
func New() *InstallClient {
	bin := FindFetcherBin()
	workDir := findIannRootFrom(filepath.Dir(bin))
	if workDir == "" {
		workDir = filepath.Dir(bin) // legacy / dev fallback
	}
	return &InstallClient{
		FetcherBin: bin,
		SSEAddr:    defaultSSEAddr,
		WorkDir:    workDir,
	}
}

// findIannRootFrom walks up from startDir looking for the iann anchor
// (isann.config.json). Returns the directory containing the anchor, or
// empty string if none found within 12 hops.
func findIannRootFrom(startDir string) string {
	cur := startDir
	for i := 0; i < 12; i++ {
		if _, err := os.Stat(filepath.Join(cur, "isann.config.json")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return ""
		}
		cur = parent
	}
	return ""
}

// FindFetcherBin locates the fetcher worker binary (isann-fetcher) relative
// to the running executable. Layouts handled:
//
//	bin/isannd.exe   + <root>/<bin>(.exe)   (legacy, fetcher one level up)
//	bin/isannd.exe   + bin/<bin>(.exe)      (flat — ivm layout, all binaries in bin/)
//
// Falls back to bare name (PATH lookup) only if no candidate exists.
// Binary name comes from setup.FetcherBin (lowercase project name).
func FindFetcherBin() string {
	name := setup.FetcherBin
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if exePath, err := os.Executable(); err == nil {
		dir := filepath.Dir(exePath)
		// Cheap direct candidates relative to the executable's directory.
		for _, c := range []string{
			filepath.Join(dir, "..", name), // bin/isannd.exe → ../isann-fetcher
			filepath.Join(dir, name),       // flat side-by-side (ivm bin/)
		} {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		}
	}
	return name
}


// PID-based ListProcesses / KillByPID / findProcessByName / ProcessStatus
// were removed when IANN moved from engine-runner (host PID lifecycle) to
// docker containers. Container lifecycle now lives in isannd's
// /internal/docker/* endpoints, and pkg/station/docker.go drives it.

// ReadVersions reads all installed package descriptors from the packages/ directory.
func (ic *InstallClient) ReadVersions() ([]json.RawMessage, error) {
	packagesDir := filepath.Join(ic.WorkDir, "packages")

	typeDirs, err := os.ReadDir(packagesDir)
	if err != nil {
		return []json.RawMessage{}, nil
	}

	var versions []json.RawMessage
	for _, td := range typeDirs {
		if !td.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(packagesDir, td.Name()))
		if err != nil {
			continue
		}
		for _, e := range entries {
			// New layout: packages/<type>/<name>/package.json (folder per package).
			if e.IsDir() {
				path := filepath.Join(packagesDir, td.Name(), e.Name(), "package.json")
				if data, err := os.ReadFile(path); err == nil && json.Valid(data) {
					versions = append(versions, json.RawMessage(data))
					continue
				}
				// Nested layout used by models: packages/models/<category>/<name>/package.json
				// (where <category> = service like "llm-api"/"sd-api"). When the
				// flat lookup above missed, walk one level deeper for any subdirs
				// that hold their own package.json.
				nestedDir := filepath.Join(packagesDir, td.Name(), e.Name())
				if subEntries, err := os.ReadDir(nestedDir); err == nil {
					for _, se := range subEntries {
						if !se.IsDir() {
							continue
						}
						nestedPath := filepath.Join(nestedDir, se.Name(), "package.json")
						if data, err := os.ReadFile(nestedPath); err == nil && json.Valid(data) {
							versions = append(versions, json.RawMessage(data))
						}
					}
				}
				continue
			}
			// Legacy layout: packages/<type>/<name>.json (top-level file)
			if filepath.Ext(e.Name()) != ".json" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(packagesDir, td.Name(), e.Name()))
			if err != nil {
				continue
			}
			if json.Valid(data) {
				versions = append(versions, json.RawMessage(data))
			}
		}
	}

	if versions == nil {
		versions = []json.RawMessage{}
	}
	return versions, nil
}
