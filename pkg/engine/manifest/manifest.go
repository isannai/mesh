// Package manifest defines the engine addon manifest schema and loader.
//
// A manifest describes how a service is reachable and how to detect
// readiness. The wrapped engine-runner pattern (host binary + child
// reverse-proxy) was retired — services now run either as Docker
// containers (launcher: "docker") or as fully external endpoints
// (launcher: "external"). Both flavours share the same probe contract:
// the manifest declares a ready_check URL and provider polls it.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// Manifest describes a single engine addon.
//
// Launcher discriminates the lifecycle model:
//   - "docker" — isannd manages the container lifecycle on the host on
//     behalf of provider (start/stop/probe via /internal/docker/*).
//     Requires the container block.
//   - "external" (default) — user runs the engine separately (vLLM,
//     Ollama, etc). IANN only does health probes and (optionally)
//     metrics scraping.
type Manifest struct {
	SpecVersion string         `json:"spec_version"`
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name"`
	Version     string         `json:"version"`
	ServiceType string         `json:"service_type"`
	Launcher    string         `json:"launcher,omitempty"`
	Description string         `json:"description,omitempty"`
	Container   *ContainerSpec `json:"container,omitempty"`
	ReadyCheck  ReadyCheckSpec `json:"ready_check"`
	Parsers     []ParserSpec   `json:"parsers,omitempty"`
	API         APISpec        `json:"api"`
	Queue       QueueSpec      `json:"queue,omitempty"`
	Metrics     *MetricsSpec   `json:"metrics,omitempty"`

	// Inspect declares the service's configured options for the broker UI
	// (engine knobs + dynamically-resolved fields from API responses).
	Inspect *InspectSpec `json:"inspect,omitempty"`

	// Station is the auto-wire block: how this engine registers itself as an
	// inference service so station serves it without a hand-written station.json.
	// All fields optional — absent values are derived (service_name from the
	// folder, host_addr from the compose ports mapping, queue = defaults). See
	// pkg/stationwire.DeriveServices.
	Station *StationSpec `json:"station,omitempty"`

	// Path is the absolute path of the manifest file. Set by Load.
	Path string `json:"-"`
}

// StationSpec is the engine's self-declared station wiring (Manifest.Station).
// It overrides the auto-derived defaults; every field is optional.
type StationSpec struct {
	ServiceName string        `json:"service_name,omitempty"` // default: "<engine>-api"
	HostAddr    string        `json:"host_addr,omitempty"`    // default: derived from compose ports
	Enable      *bool         `json:"enable,omitempty"`       // nil = enabled
	Queue       *StationQueue `json:"queue,omitempty"`        // default: sensible fallbacks
}

// StationQueue mirrors the queue knobs stationwire maps onto a ServiceEntry.
type StationQueue struct {
	MaxQueue    int   `json:"max_queue,omitempty"`
	Concurrency int   `json:"concurrency,omitempty"`
	SaveToDisk  *bool `json:"save_to_disk,omitempty"` // nil = engine default
}

// ContainerSpec describes how isannd should run the engine container.
// Only meaningful when Launcher == "docker".
//
// The shape is intentionally a thin wrapper over `docker run` flags —
// isannd serialises this into POST /internal/docker/start. Volume / port
// mappings are passed verbatim; templating happens on the isannd side
// against the runtime ServiceEntry.
type ContainerSpec struct {
	Image      string            `json:"image"`          // "isannai/sd:latest"
	Name       string            `json:"name,omitempty"` // default = svc.Name
	Port       int               `json:"port,omitempty"` // port the image listens on inside the container (informational)
	Ports      []PortMapping     `json:"ports,omitempty"`
	Volumes    []VolumeMapping   `json:"volumes,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Entrypoint []string          `json:"entrypoint,omitempty"`
	Command    []string          `json:"command,omitempty"`
	GPU        string            `json:"gpu,omitempty"`        // "all" | "0" | ""
	Restart    string            `json:"restart,omitempty"`    // unless-stopped | always | no
	LogMaxMB   int               `json:"log_max_mb,omitempty"` // rotation threshold for the host log capture file
}

// PortMapping mirrors `docker run -p host:container`. Host accepts the
// `{addr_port}` template token so provider can keep one canonical port
// per service in conf/provider.json.
type PortMapping struct {
	Host      string `json:"host"`
	Container int    `json:"container"`
}

// VolumeMapping mirrors `docker run -v host:container[:ro]`. Host accepts
// template tokens (`{models_dir}`, etc.) resolved against the manifest
// Env / runtime service options.
type VolumeMapping struct {
	Host      string `json:"host"`
	Container string `json:"container"`
	ReadOnly  bool   `json:"ro,omitempty"`
}

// InspectSpec is the manifest-declared list of fields to surface to UI.
type InspectSpec struct {
	Fields []InspectField `json:"fields"`
}

// InspectField describes one row in the Configured Options list.
//
// Two forms:
//   - Static: set `value`. Broker UI displays it verbatim.
//   - Dynamic: set `from` + `path` and resolve at register time:
//     from="api"     → engine HTTP probe response (vLLM /v1/models)
//     from="conf"    → service's own conf JSON
//     from="service" → conf/provider.json services[].options.<path>
type InspectField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type,omitempty"` // "string" (default) | "int" | "bool" | "float"

	// Static manifest value (mutually exclusive with From/Path).
	Value any `json:"value,omitempty"`

	// Dynamic resolution.
	From string `json:"from,omitempty"`
	Path string `json:"path,omitempty"`

	// Options: enum choices for the UI dropdown. When set, frontend renders
	// a dropdown instead of a free-text input.
	Options []string `json:"options,omitempty"`
}

// IsExternal reports whether the manifest declares an externally-managed
// engine (user runs it; IANN only probes).
func (m *Manifest) IsExternal() bool {
	return m.Launcher == "" || m.Launcher == "external"
}

// IsDocker reports whether isannd should manage the container lifecycle.
func (m *Manifest) IsDocker() bool {
	return m.Launcher == "docker"
}

// MetricsSpec describes how to scrape runtime metrics from an external
// engine. Currently Prometheus text format is supported. Mapping translates
// IANN's standard metric keys (queue_depth, running_count, total_jobs_done,
// latency_sum, latency_count) to engine-specific Prometheus metric names.
type MetricsSpec struct {
	Type             string            `json:"type"`
	URL              string            `json:"url"`
	Mapping          map[string]string `json:"mapping"`
	AvgJobSecFormula string            `json:"avg_job_sec_formula,omitempty"`
}

// QueueSpec configures the in-memory job queue used to wrap proxy_routes.
// All fields are engine-level defaults; service config can override (except
// Enabled which is manifest-only — see below).
type QueueSpec struct {
	Concurrency int  `json:"default_concurrency,omitempty"`  // simultaneous workers (default 1)
	MaxPending  int  `json:"default_max_queue,omitempty"`    // max pending+running jobs; 0 = unlimited
	MaxDone     int  `json:"default_max_done,omitempty"`     // LRU cap for done/failed jobs (default 100)
	DoneTTLSec  int  `json:"default_ttl_sec,omitempty"`      // done/failed retention seconds (default 3600)
	SaveToDisk  bool `json:"default_save_to_disk,omitempty"` // persist results to output_dir (default false)

	// ConcurrencyEnv names the engine .env variable that carries the engine's
	// own concurrent-slot count (llama → "PARALLEL", vLLM → "MAX_NUM_SEQS").
	// When set, the provider reads <root>/engines/<engine>/.env and uses that
	// value as the queue's worker count — so the operator tunes concurrency in
	// ONE place (the engine .env) and the queue follows, never forwarding more
	// requests than the engine can serve in parallel. Omit it for engines with
	// no parallel concept (e.g. sd): the queue then stays at the manifest /
	// fallback default. Resolution priority lives in the provider's queue
	// factory (pkg/station/queue_init.go), above the static default_concurrency.
	ConcurrencyEnv string `json:"concurrency_env,omitempty"`

	// Enable controls whether requests for this engine flow through the
	// queue subsystem. Pointer semantics: nil = unspecified → default true.
	// Set explicitly to false to bypass the queue and reverse-proxy
	// requests directly — used for streaming/long-lived services (webdav,
	// terminal) where job-unit framing makes no sense.
	Enable *bool `json:"enable,omitempty"`
}

// IsEnabled reports whether this engine's queue subsystem is active.
func (q QueueSpec) IsEnabled() bool {
	return q.Enable == nil || *q.Enable
}

// ReadyCheckSpec describes how to detect when the engine is ready.
//
// Two distinct timeout concepts:
//
//	TimeoutMS      = total budget for boot-time WaitReady (large LLM model
//	                 loads can take 10+ minutes). NOT a per-request timeout.
//	PollTimeoutMS  = per-HTTP-request timeout used by provider's steady-state
//	                 polling. Must be short — the watcher loop blocks on
//	                 this every IntervalMS. Default 1s.
type ReadyCheckSpec struct {
	Type          string `json:"type"`            // "http_get"
	URL           string `json:"url"`             // may contain {addr}
	IntervalMS    int    `json:"interval_ms"`     // poll interval, default 1000
	TimeoutMS     int    `json:"timeout_ms"`      // WaitReady total budget, 0 = no timeout
	PollTimeoutMS int    `json:"poll_timeout_ms"` // per-request probe timeout, default 1000
	// ModelPath is the JSON path in the ready_check response body where the
	// active model name lives. Used by provider's pollServiceExternal to
	// populate ServiceInfo.Model. Engine-specific because each server
	// returns a different shape:
	//   vllm / llama.cpp / TGI (OpenAI):   "data[0].id"
	//   sd-server (A1111):                 "[0].model_name"
	//   ollama:                            "models[0].name"
	// Leave empty to skip — Model field will stay blank.
	ModelPath string `json:"model_path,omitempty"`
}

// ParserSpec defines a single stdout/stderr parser. Used by isannd's log
// capture pipeline (Docker logs → host file → fsnotify → parser → metrics).
type ParserSpec struct {
	Name    string            `json:"name"`
	Stream  string            `json:"stream"`  // "stdout" | "stderr" | "both"
	Type    string            `json:"type"`    // "regex" | "regex_cr" | "jsonline" | "keyvalue"
	Pattern string            `json:"pattern"` // for regex/regex_cr
	Emit    map[string]string `json:"emit"`    // {"event": "progress", "current": "$1", "total": "$2"}
}

// APISpec describes the HTTP routes to proxy and the conventional health/queue paths.
type APISpec struct {
	ProxyRoutes    []ProxyRoute `json:"proxy_routes"`
	HealthPath     string       `json:"health_path"`
	QueueStatsPath string       `json:"queue_stats_path"`

	// Run declares manifest-driven inference (Phase 3 M1) — params, request
	// body template, result spec, and (optionally) extra_args injection for
	// engines that don't read top-level body fields. Nil for services that
	// don't expose generic inference (webdav/terminal). See runspec.go.
	Run *RunSpec `json:"run,omitempty"`

	// Runs holds ADDITIONAL run variants beyond the default Run, matched by
	// engine path (e.g. sd's /v1/images/edits for img2img alongside the default
	// /v1/images/generations txt2img). A submit selects the variant via its
	// `path` field; RunFor falls back to Run. See runspec.go.
	Runs []RunSpec `json:"runs,omitempty"`
}

// RunFor selects the run spec for a submit's target path: an exact match in
// Runs, else the default Run when path matches it (or is empty). Nil only when
// there is no default Run at all.
func (a *APISpec) RunFor(path string) *RunSpec {
	if path != "" {
		for i := range a.Runs {
			if a.Runs[i].Path == path {
				return &a.Runs[i]
			}
		}
	}
	return a.Run
}

// EncodingFor returns the wire-encoding declared for an engine path (nil = the
// default: forward the JSON body verbatim). Used by the queue submit to
// transcode a JSON body into the engine's native format (e.g. multipart) at
// the engine edge.
func (a *APISpec) EncodingFor(path string) *EncodingSpec {
	for i := range a.ProxyRoutes {
		if a.ProxyRoutes[i].Path == path {
			return a.ProxyRoutes[i].Encoding
		}
	}
	return nil
}

// AllowsPath reports whether a caller-supplied submit `path` is a declared
// engine path — any proxy_route, the default run, or a run variant. The queue
// submit gates client paths against this so a submission can't forward to an
// arbitrary engine URL. Empty ProxyRoutes+Run means "no manifest gate" and
// callers should skip enforcement.
func (a *APISpec) AllowsPath(p string) bool {
	for i := range a.ProxyRoutes {
		if a.ProxyRoutes[i].Path == p {
			return true
		}
	}
	if a.Run != nil && a.Run.Path == p {
		return true
	}
	for i := range a.Runs {
		if a.Runs[i].Path == p {
			return true
		}
	}
	return false
}

// ProxyRoute is a single HTTP path to forward to the engine endpoint.
type ProxyRoute struct {
	Path   string `json:"path"`
	Method string `json:"method"`

	// Encoding, when set, tells the queue submit to transcode the JSON engine
	// body into a non-JSON wire format before forwarding to this path (e.g.
	// multipart/form-data for sd.cpp's /v1/images/edits). Nil = forward JSON
	// verbatim (the default for every existing route).
	Encoding *EncodingSpec `json:"encoding,omitempty"`
}

// EncodingSpec declares how to serialize the JSON engine body into the engine's
// native wire format at the engine edge (station queue submit). Today only
// "multipart" is supported: FileFields name the body keys whose base64 / data:
// URL string value becomes a file part (decoded to raw bytes); every other key
// becomes a text form field.
type EncodingSpec struct {
	Type       string   `json:"type"`        // "multipart"
	FileFields []string `json:"file_fields"` // body keys that are file uploads
}

// Validate checks the encoding block. Only "multipart" is supported today.
func (e *EncodingSpec) Validate() error {
	if e.Type != "multipart" {
		return fmt.Errorf("type %q unsupported (only \"multipart\")", e.Type)
	}
	if len(e.FileFields) == 0 {
		return fmt.Errorf("file_fields must be non-empty")
	}
	return nil
}

// Load reads and parses a manifest JSON file.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("manifest: read %s: %w", path, err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: parse %s: %w", path, err)
	}
	abs, _ := filepath.Abs(path)
	m.Path = abs
	return &m, nil
}

// Validate checks required fields, regex compilation, and obvious errors.
//
// Validation rules differ by Launcher:
//   - "external" (default): only spec_version/name/ready_check are required.
//   - "docker": additionally requires container.image.
//
// metrics, if present, must declare type and url for either flavour.
func (m *Manifest) Validate() error {
	if m.SpecVersion == "" {
		return fmt.Errorf("manifest: spec_version is required")
	}
	if m.Name == "" {
		return fmt.Errorf("manifest: name is required")
	}
	if m.Launcher != "" && m.Launcher != "docker" && m.Launcher != "external" {
		return fmt.Errorf("manifest: unsupported launcher %q (expected \"docker\" or \"external\")", m.Launcher)
	}

	if m.ReadyCheck.Type == "" {
		return fmt.Errorf("manifest: ready_check.type is required")
	}
	if m.ReadyCheck.Type != "http_get" {
		return fmt.Errorf("manifest: unsupported ready_check.type %q", m.ReadyCheck.Type)
	}
	if m.ReadyCheck.URL == "" {
		return fmt.Errorf("manifest: ready_check.url is required")
	}

	if m.Metrics != nil {
		if m.Metrics.Type == "" {
			return fmt.Errorf("manifest: metrics.type is required when metrics is present")
		}
		if m.Metrics.Type != "prometheus" && m.Metrics.Type != "iann-json" {
			return fmt.Errorf("manifest: unsupported metrics.type %q (expected \"prometheus\" or \"iann-json\")", m.Metrics.Type)
		}
		if m.Metrics.URL == "" {
			return fmt.Errorf("manifest: metrics.url is required when metrics is present")
		}
		if m.Metrics.Type == "prometheus" && len(m.Metrics.Mapping) == 0 {
			return fmt.Errorf("manifest: metrics.mapping is required for prometheus type")
		}
	}

	if m.IsDocker() {
		if m.Container == nil {
			return fmt.Errorf("manifest: launcher=docker requires container block")
		}
		if m.Container.Image == "" {
			return fmt.Errorf("manifest: container.image is required when launcher=docker")
		}
	}

	for i, p := range m.Parsers {
		if p.Name == "" {
			return fmt.Errorf("manifest: parsers[%d].name is required", i)
		}
		if p.Stream != "stdout" && p.Stream != "stderr" && p.Stream != "both" && p.Stream != "" {
			return fmt.Errorf("manifest: parsers[%d].stream must be stdout, stderr, or both", i)
		}
		switch p.Type {
		case "regex", "regex_cr":
			if p.Pattern == "" {
				return fmt.Errorf("manifest: parsers[%d].pattern is required for %s", i, p.Type)
			}
			if _, err := regexp.Compile(p.Pattern); err != nil {
				return fmt.Errorf("manifest: parsers[%d].pattern compile: %w", i, err)
			}
		case "jsonline", "keyvalue":
			// allowed but not yet implemented
		default:
			return fmt.Errorf("manifest: parsers[%d].type %q unsupported", i, p.Type)
		}
		if len(p.Emit) == 0 || p.Emit["event"] == "" {
			return fmt.Errorf("manifest: parsers[%d].emit.event is required", i)
		}
	}

	if m.API.Run != nil {
		if err := m.API.Run.Validate(); err != nil {
			return fmt.Errorf("manifest: %w", err)
		}
	}
	for i := range m.API.Runs {
		if err := m.API.Runs[i].Validate(); err != nil {
			return fmt.Errorf("manifest: api.runs[%d]: %w", i, err)
		}
	}
	for i, pr := range m.API.ProxyRoutes {
		if pr.Encoding != nil {
			if err := pr.Encoding.Validate(); err != nil {
				return fmt.Errorf("manifest: api.proxy_routes[%d].encoding: %w", i, err)
			}
		}
	}
	return nil
}
