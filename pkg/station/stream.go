package station

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/daesob/http3proxy/pkg/auth"
	"github.com/daesob/http3proxy/pkg/glog"
	"github.com/daesob/http3proxy/pkg/installclient"
	"github.com/daesob/http3proxy/pkg/profile"
	"github.com/daesob/http3proxy/pkg/setup"
	"github.com/daesob/http3proxy/pkg/tunnel"
	"github.com/quic-go/quic-go"
)

var modelExtensions = map[string]bool{
	".gguf": true, ".safetensors": true, ".bin": true,
	".pt": true, ".ckpt": true, ".pth": true,
	".yaml": true, ".yml": true, ".json": true, ".txt": true,
}

func writeHTTPResponse(stream quic.Stream, statusCode int, contentType string, body []byte) {
	fmt.Fprintf(stream, "HTTP/1.1 %d %s\r\n", statusCode, http.StatusText(statusCode))
	fmt.Fprintf(stream, "Content-Type: %s\r\n", contentType)
	fmt.Fprintf(stream, "Content-Length: %d\r\n", len(body))
	fmt.Fprintf(stream, "\r\n")
	stream.Write(body)
}

func writeHTTPResponseWithETag(stream quic.Stream, req *http.Request, statusCode int, contentType string, body []byte) {
	h := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(h[:8]) + `"`
	if match := req.Header.Get("If-None-Match"); match == etag {
		fmt.Fprintf(stream, "HTTP/1.1 304 Not Modified\r\n")
		fmt.Fprintf(stream, "ETag: %s\r\n", etag)
		fmt.Fprintf(stream, "Cache-Control: private, max-age=0, must-revalidate\r\n")
		fmt.Fprintf(stream, "\r\n")
		return
	}
	fmt.Fprintf(stream, "HTTP/1.1 %d %s\r\n", statusCode, http.StatusText(statusCode))
	fmt.Fprintf(stream, "Content-Type: %s\r\n", contentType)
	fmt.Fprintf(stream, "Content-Length: %d\r\n", len(body))
	fmt.Fprintf(stream, "ETag: %s\r\n", etag)
	fmt.Fprintf(stream, "Cache-Control: private, max-age=0, must-revalidate\r\n")
	fmt.Fprintf(stream, "\r\n")
	stream.Write(body)
}

// dispatchOrchestratorRequest is the path/method routing for the provider's
// control-plane HTTP path (HandleProviderHTTP). All response writes go through
// `stream` — HandleProviderHTTP passes a bufStream adapter that captures the raw
// HTTP/1.1 response and replays it onto the real http.ResponseWriter.
func (p *Provider) dispatchOrchestratorRequest(stream quic.Stream, req *http.Request) {
	p.Log.Log(glog.Request, "[station] orchestrator: %s %s", req.Method, req.URL.Path)

	path := req.URL.Path
	method := req.Method

	// Inline auth guard:
	// - Public paths → pass through
	// - Bearer-token paths → handler validates its own pre-shared token
	// - Management paths → always require owner/admin IANN wallet auth
	// - Other paths + protected mode → require auth (owner/admin/user)
	// - Other paths + open mode → pass through
	if !isPublicOrchestratorPath(path, method) && !isBearerAuthPath(path, method) {
		if isManagementPath(path, method) {
			if !p.verifyOrchestratorAuth(stream, req) {
				return
			}
		} else if !p.Auth.IsPublic() {
			// Inference paths (jobs/queue/outputs) take the 4-role gate so
			// user/issuer wallets can run inference in protected mode (M0);
			// everything else stays owner/admin-only (operation).
			if isInferencePath(path, method) {
				if !p.verifyInferenceAuth(stream, req) {
					return
				}
			} else if !p.verifyOrchestratorAuth(stream, req) {
				return
			}
		}
	}

	switch {
	// === Provider 직접 처리 (/provider/*) ===
	// /provider/packages         — all installed packages except services
	// /provider/packages?type=X  — filter to a single type (model | engine | lora | core | dep)
	// Service list lives on node.services (rendezvous payload) — single
	// source of truth, not duplicated here.
	case path == "/provider/packages" && method == "GET":
		t := req.URL.Query().Get("type")
		body, code := p.handlePackagesByType(t)
		stream.SetWriteDeadline(time.Now().Add(3 * time.Minute))
		writeHTTPResponseWithETag(stream, req, code, "application/json", body)
	case path == "/provider/partials" && method == "GET":
		p.handlePartials(stream, req)
	case path == "/provider/scan-local" && method == "POST":
		p.handleScanLocal(stream, req)
	case path == "/provider/save-package" && method == "POST":
		p.handleSavePackage(stream, req)
	case path == "/provider/file" && method == "GET":
		p.handleServeFile(stream, req)
	case path == "/provider/logs" && method == "GET":
		p.handleLogs(stream, req)
	case path == "/provider/about" && method == "GET":
		p.handleGetAbout(stream, req)
	case path == "/provider/about" && method == "POST":
		p.handleSetAbout(stream, req)
	case path == "/provider/emblem" && method == "POST":
		p.handleUploadEmblem(stream, req)
	case path == "/provider/emblem" && method == "DELETE":
		p.handleDeleteEmblem(stream, req)
	case path == "/provider/auth-verify" && method == "POST":
		p.handleAuthVerify(stream, req)
	case path == "/provider/auth" && method == "GET":
		p.handleGetAuth(stream, req)
	case path == "/provider/auth" && method == "POST":
		p.handleSetAuth(stream, req)
	case path == "/provider/register" && method == "POST":
		p.handleProviderRegister(stream, req)
	case path == "/provider/profiles" && method == "GET":
		p.handleGetProfiles(stream, req)
	case path == "/provider/active-profile" && method == "POST":
		p.handleSetActiveProfile(stream, req)
	case path == "/provider/profile" && method == "POST":
		p.handleUpsertProfile(stream, req)
	case path == "/provider/profile" && method == "DELETE":
		p.handleDeleteProfile(stream, req)

	// === Queue (Phase 7 wiring — broker job submission) ===
	// /provider/v1/jobs is POST-only (Submit Job). There is no list endpoint —
	// callers must track their own job IDs and read single jobs via
	// /provider/v1/jobs/{id}. GET on the bare path falls through to the default
	// 404 below (was 405 — corrected so the response stays JSON-shaped).
	case path == "/provider/v1/jobs" && method == "POST":
		p.serveJobsHandler(stream, req, "/v1/jobs")
	case strings.HasPrefix(path, "/provider/v1/jobs/") && method == "GET":
		p.serveJobsHandler(stream, req, strings.TrimPrefix(path, "/provider"))
	case strings.HasPrefix(path, "/provider/outputs/") && method == "GET":
		p.serveJobsHandler(stream, req, strings.TrimPrefix(path, "/provider"))
	case path == "/provider/v1/queue/stats" && method == "GET":
		p.serveJobsHandler(stream, req, "/v1/queue/stats")

	// === Sync ===
	case path == "/provider/sync/create" && method == "POST":
		p.handleSyncCreate(stream, req)
	case path == "/provider/sync/status" && method == "GET":
		p.handleSyncStatus(stream, req)
	case path == "/provider/sync/snapshot" && method == "GET":
		p.handleSyncSnapshot(stream, req)
	case path == "/provider/sync/file" && method == "GET":
		p.handleSyncFile(stream, req)

	default:
		stream.SetWriteDeadline(time.Now().Add(3 * time.Minute))
		writeHTTPResponse(stream, 404, "application/json", []byte(`{"error":"not found"}`))
	}
}

// handleAuthVerify validates an EOA-signed message against this Provider's auth.json.
// Returns role (owner/admin/user) if the signer is in Owner/Admins/Users, else 403.
func (p *Provider) handleAuthVerify(stream quic.Stream, req *http.Request) {
	stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	sig := strings.TrimPrefix(req.Header.Get("Authorization"), "ISANN ")
	message := req.Header.Get("X-ISANN-Message")
	if sig == "" || message == "" {
		writeHTTPResponse(stream, 401, "application/json", []byte(`{"error":"missing auth headers"}`))
		return
	}
	address, err := auth.RecoverAddress(message, sig)
	if err != nil {
		writeHTTPResponse(stream, 401, "application/json", []byte(`{"error":"invalid signature"}`))
		return
	}
	role := ""
	if p.Auth.Owner != "" && strings.EqualFold(address, p.Auth.Owner) {
		role = "owner"
	} else {
		for _, a := range p.Auth.Admins {
			if strings.EqualFold(address, a) {
				role = "admin"
				break
			}
		}
	}
	if role == "" {
		writeHTTPResponse(stream, 403, "application/json", []byte(`{"error":"not authorized"}`))
		return
	}
	body, _ := json.Marshal(map[string]string{"ok": "true", "role": role, "address": address})
	writeHTTPResponse(stream, 200, "application/json", body)
}

// verifyOrchestratorAuth checks the IANN signature headers on the incoming
// orchestrator request. Returns true if the caller is authorized (owner or admin),
// false if it wrote an error response and the caller should return.
func (p *Provider) verifyOrchestratorAuth(stream quic.Stream, req *http.Request) bool {
	sig := strings.TrimPrefix(req.Header.Get("Authorization"), "ISANN ")
	message := req.Header.Get("X-ISANN-Message")
	if sig == "" || message == "" {
		writeHTTPResponse(stream, 401, "application/json", []byte(`{"error":"missing auth headers"}`))
		return false
	}

	// Validate expiration from message: {role}:{target}:{service}:{nonce}:{expiresAt}:{nodes}
	parts := strings.SplitN(message, ":", 6)
	if len(parts) >= 5 {
		if exp, err := strconv.ParseInt(parts[4], 10, 64); err == nil && time.Now().Unix() > exp {
			writeHTTPResponse(stream, 401, "application/json", []byte(`{"error":"signature expired"}`))
			return false
		}
	}

	address, err := auth.RecoverAddress(message, sig)
	if err != nil {
		writeHTTPResponse(stream, 401, "application/json", []byte(`{"error":"invalid signature"}`))
		return false
	}

	if p.Auth.Owner != "" && strings.EqualFold(address, p.Auth.Owner) {
		return true
	}
	for _, a := range p.Auth.Admins {
		if strings.EqualFold(address, a) {
			return true
		}
	}

	writeHTTPResponse(stream, 403, "application/json", []byte(`{"error":"not authorized"}`))
	return false
}

// providerRole classifies a recovered EOA against this provider's auth.json,
// mirroring broker's classifyAddress. Returns "owner"/"admin"/"user"/"issuer",
// or "" when the address holds no role. Drives the 4-role inference gate.
func (p *Provider) providerRole(address string) string {
	if p.Auth.Owner != "" && strings.EqualFold(address, p.Auth.Owner) {
		return "owner"
	}
	for _, a := range p.Auth.Admins {
		if strings.EqualFold(address, a) {
			return "admin"
		}
	}
	for _, u := range p.Auth.Users {
		if strings.EqualFold(address, u) {
			return "user"
		}
	}
	if p.Auth.Issuer != "" && strings.EqualFold(address, p.Auth.Issuer) {
		return "issuer"
	}
	return ""
}

// verifyInferenceAuth gates inference requests (job submit/status/result,
// queue stats, outputs) in protected mode. Unlike verifyOrchestratorAuth
// (owner/admin only — operation), inference allows all four roles
// (owner/admin/user/issuer): running inference is not an operator action.
//
// The caller proves identity with their own IANN signature (Authorization:
// ISANN <sig> + X-ISANN-Message), recovered and role-checked here against
// this provider's auth.json. No forwarded header is trusted — X-Caller-Address
// is never consulted (M0 0-c) — so the gate can't be passed by a spoofed
// header.
//
// Returns false (and writes the error response) when no valid 4-role
// signature is present.
func (p *Provider) verifyInferenceAuth(stream quic.Stream, req *http.Request) bool {
	sig := strings.TrimPrefix(req.Header.Get("Authorization"), "ISANN ")
	message := req.Header.Get("X-ISANN-Message")
	if sig == "" || message == "" {
		writeHTTPResponse(stream, 401, "application/json", []byte(`{"error":"missing auth headers"}`))
		return false
	}
	// Expiry from message: {role}:{target}:{service}:{nonce}:{expiresAt}:{nodes}
	parts := strings.SplitN(message, ":", 6)
	if len(parts) >= 5 {
		if exp, err := strconv.ParseInt(parts[4], 10, 64); err == nil && time.Now().Unix() > exp {
			writeHTTPResponse(stream, 401, "application/json", []byte(`{"error":"signature expired"}`))
			return false
		}
	}
	address, err := auth.RecoverAddress(message, sig)
	if err != nil {
		writeHTTPResponse(stream, 401, "application/json", []byte(`{"error":"invalid signature"}`))
		return false
	}
	if p.providerRole(address) != "" {
		return true
	}
	writeHTTPResponse(stream, 403, "application/json", []byte(`{"error":"not authorized"}`))
	return false
}

// handleGetAuth returns the current AuthConfig. Owner-only.
func (p *Provider) handleGetAuth(stream quic.Stream, req *http.Request) {
	stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if !p.verifyOwnerOnly(stream, req) {
		return
	}
	data, err := json.Marshal(p.Auth)
	if err != nil {
		writeHTTPResponse(stream, 500, "application/json", []byte(`{"error":"marshal failed"}`))
		return
	}
	writeHTTPResponse(stream, 200, "application/json", data)
}

// handleSetAuth updates mode/issuer/admins/users/revoked_grants. Owner is
// never modified from UI. Owner-only.
func (p *Provider) handleSetAuth(stream quic.Stream, req *http.Request) {
	stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if !p.verifyOwnerOnly(stream, req) {
		return
	}
	body, _ := io.ReadAll(req.Body)
	var incoming tunnel.AuthConfig
	if json.Unmarshal(body, &incoming) != nil {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"invalid json"}`))
		return
	}
	// Preserve immutable fields
	incoming.Owner = p.Auth.Owner
	incoming.ConfigFile = p.Auth.ConfigFile
	if incoming.Mode != "public" && incoming.Mode != "open" && incoming.Mode != "protected" {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"mode must be public or protected"}`))
		return
	}
	if err := tunnel.SaveAuthConfig(incoming); err != nil {
		msg, _ := json.Marshal(map[string]string{"error": err.Error()})
		writeHTTPResponse(stream, 500, "application/json", msg)
		return
	}
	p.Auth = incoming
	p.Log.Log(glog.Lifecycle, "[station] auth updated: mode=%s admins=%d users=%d", incoming.Mode, len(incoming.Admins), len(incoming.Users))
	writeHTTPResponse(stream, 200, "application/json", []byte(`{"status":"ok"}`))
}

// verifyOwnerOnly checks that the signer matches Provider.Auth.Owner exactly.
// Used for auth-management endpoints that must not be callable by admins.
func (p *Provider) verifyOwnerOnly(stream quic.Stream, req *http.Request) bool {
	sig := strings.TrimPrefix(req.Header.Get("Authorization"), "ISANN ")
	message := req.Header.Get("X-ISANN-Message")
	if sig == "" || message == "" {
		writeHTTPResponse(stream, 401, "application/json", []byte(`{"error":"missing auth headers"}`))
		return false
	}
	address, err := auth.RecoverAddress(message, sig)
	if err != nil {
		writeHTTPResponse(stream, 401, "application/json", []byte(`{"error":"invalid signature"}`))
		return false
	}
	if p.Auth.Owner == "" || !strings.EqualFold(address, p.Auth.Owner) {
		writeHTTPResponse(stream, 403, "application/json", []byte(`{"error":"owner only"}`))
		return false
	}
	return true
}

// isManagementPath returns true for orchestrator paths that always require
// owner/admin auth, even in open mode (service start/stop, install, config changes).
func isManagementPath(path, method string) bool {
	// All installer actions
	if strings.HasPrefix(path, "/installer/") {
		return true
	}
	// Provider management (config changes, kill, package management, sync)
	switch {
	// Config read exposes internal addresses, paths, and toggles — owner-only.
	case path == "/provider/config" && method == http.MethodGet:
		return true
	case path == "/provider/config" && method == http.MethodPost:
		return true
	// Service lifecycle: starting / stopping services is an operator
	// action, never anonymous — even on open nodes. The /provider/start
	// and /provider/stop variants are retired (replaced by
	// /service/{name}/start|stop), as is the PID-based /provider/kill.
	case strings.HasPrefix(path, "/service/") && method == http.MethodPost:
		return true
	case path == "/provider/save-package":
		return true
	case path == "/provider/scan-local":
		return true
	// Profile management: changes how services boot and behave.
	case path == "/provider/active-profile" && method == http.MethodPost:
		return true
	case path == "/provider/profile" && (method == http.MethodPost || method == http.MethodDelete):
		return true
	// Partial download progress reveals what the operator is installing —
	// admin info, not catalog data. Owner-only even on open nodes.
	case path == "/provider/partials" && method == http.MethodGet:
		return true
	// Filesystem inspection: model directory browse + ongoing-download
	// progress. Both reveal what the operator is running / installing.
	// Owner-only even on open nodes — model lineup and admin work are not
	// catalog data (catalog data is exposed via /v1/nodes services field).
	case path == "/provider/browse" && method == http.MethodGet:
		return true
	case path == "/provider/partials" && method == http.MethodGet:
		return true
	// Force re-register with RV: signal-level action — only owner should be
	// able to demand a FullSync.
	case path == "/provider/register" && method == http.MethodPost:
		return true
	// Sync management: token creation / status (owner-only)
	// /provider/sync/snapshot and /provider/sync/file authenticate via
	// pre-shared bearer token inside the handler (see isBearerAuthPath).
	case path == "/provider/sync/create" && method == http.MethodPost:
		return true
	case path == "/provider/sync/status" && method == http.MethodGet:
		return true
	case path == "/provider/about" && method == http.MethodPost:
		return true
	case path == "/provider/emblem" && (method == http.MethodPost || method == http.MethodDelete):
		return true
	case path == "/provider/auth":
		return true
	// Logs reveal sensitive info (paths, user actions, errors) — owner only,
	// even in open mode.
	case path == "/provider/logs" && method == http.MethodGet:
		return true
	}
	return false
}

// isPublicOrchestratorPath returns true for paths that don't require auth:
//   - /provider/auth-verify (the auth mechanism itself)
//   - GET /provider/emblem, /provider/about, /provider/file (public profile reads)
func isPublicOrchestratorPath(path, method string) bool {
	if path == "/provider/auth-verify" {
		return true
	}
	if method == http.MethodGet {
		switch path {
		case "/provider/emblem", "/provider/about", "/provider/file":
			return true
		}
	}
	return false
}

// isBearerAuthPath returns true for paths that authenticate via a pre-shared
// bearer token validated inside the handler itself. The orchestrator-level
// IANN wallet auth gate must be skipped for them so the request can reach
// the handler, which then enforces the token.
func isBearerAuthPath(path, method string) bool {
	if method != http.MethodGet {
		return false
	}
	switch path {
	case "/provider/sync/snapshot", "/provider/sync/file":
		return true
	}
	return false
}

// isInferencePath reports whether the orchestrator path is an inference
// request (job submit/status/result, queue stats, outputs). Inference paths
// take the 4-role gate (verifyInferenceAuth) instead of the owner/admin
// operator gate, so user/issuer wallets can run inference in protected mode.
func isInferencePath(path, method string) bool {
	switch {
	case method == http.MethodPost && path == "/provider/v1/jobs":
		return true
	case method == http.MethodGet && strings.HasPrefix(path, "/provider/v1/jobs/"):
		return true
	case method == http.MethodGet && strings.HasPrefix(path, "/provider/outputs/"):
		return true
	case method == http.MethodGet && path == "/provider/v1/queue/stats":
		return true
	}
	return false
}

// findService looks up a ServiceEntry by name under read lock. Returns
// (zero, false) when no match. Used by /service/* handlers and the
// profile-restart paths.
func (p *Provider) findService(name string) (setup.ServiceEntry, bool) {
	p.CfgMu.RLock()
	defer p.CfgMu.RUnlock()
	for _, svc := range p.Cfg.Services {
		if svc.Name == name {
			return svc, true
		}
	}
	return setup.ServiceEntry{}, false
}

// handleGetProfiles returns the profile set + manifest schema for a service —
//
//	GET /provider/profiles?service=<name>
//
//	Body: {
//		  engine, active, profiles[{name, label, values}],
//		  schema: [{ key, label, type, from, path }]   // manifest.inspect.fields
//		}
//
// schema is included so the broker UI can render the Add/Edit modal form
// without a separate manifest fetch (engine-specific fields differ).
func (p *Provider) handleGetProfiles(stream quic.Stream, req *http.Request) {
	service := req.URL.Query().Get("service")
	if service == "" {
		writeHTTPResponse(stream, http.StatusBadRequest, "application/json",
			[]byte(`{"error":"service query param required"}`))
		return
	}
	p.CfgMu.RLock()
	cfgPath := p.Cfg.ConfigFile
	svcs := append([]setup.ServiceEntry(nil), p.Cfg.Services...)
	p.CfgMu.RUnlock()
	confDir := filepath.Dir(cfgPath)
	if confDir == "" {
		confDir = "conf"
	}

	// External services (vllm etc.) skip the profile concept entirely —
	// IANN can't change docker run args, so a profile selector would be
	// pure noise. UI relies on inspect's from="api" path for live values.
	managed := false
	var managedSvc *setup.ServiceEntry
	for i, svc := range svcs {
		if svc.Name != service {
			continue
		}
		if svc.IsManagedLocally() {
			managed = true
			managedSvc = &svcs[i]
		}
		break
	}
	if !managed {
		writeHTTPResponse(stream, http.StatusOK, "application/json",
			[]byte(`{"profiles":[],"schema":[],"editable":false}`))
		return
	}

	// Load schema from the engine manifest. Empty array if the manifest is
	// missing — UI can still show profiles, just without typed inputs.
	var schema []manifestField
	editable := true
	if mf := loadInspectManifest(*managedSvc, cfgPath, p.PackagesDir); mf != nil && mf.Inspect != nil {
		for _, f := range mf.Inspect.Fields {
			schema = append(schema, manifestField{
				Key: f.Key, Label: f.Label, Type: f.Type, From: f.From, Path: f.Path, Options: f.Options,
			})
		}
	}
	if schema == nil {
		schema = []manifestField{}
	}

	path := profile.SetPathForName(confDir, service)
	set, err := profile.LoadSet(path)
	if err != nil {
		writeHTTPResponse(stream, http.StatusInternalServerError, "application/json",
			[]byte(`{"error":"profile load failed"}`))
		return
	}
	if set == nil {
		// No profile file — empty profiles but schema still useful for "Add first profile" form.
		empty := struct {
			Profiles []profile.Profile `json:"profiles"`
			Schema   []manifestField   `json:"schema"`
			Editable bool              `json:"editable"`
		}{Profiles: []profile.Profile{}, Schema: schema, Editable: editable}
		body, _ := json.Marshal(empty)
		writeHTTPResponse(stream, http.StatusOK, "application/json", body)
		return
	}

	resp := struct {
		Engine   string            `json:"engine,omitempty"`
		Active   string            `json:"active,omitempty"`
		Editable bool              `json:"editable"`
		Profiles []profile.Profile `json:"profiles"`
		Schema   []manifestField   `json:"schema"`
	}{
		Engine:   set.Engine,
		Active:   set.Active,
		Editable: editable,
		Profiles: set.Profiles,
		Schema:   schema,
	}
	body, _ := json.Marshal(resp)
	writeHTTPResponse(stream, http.StatusOK, "application/json", body)
}

// manifestField mirrors manifest.InspectField for the wire format. Defined
// here so the response shape stays decoupled from internal type names.
type manifestField struct {
	Key     string   `json:"key"`
	Label   string   `json:"label,omitempty"`
	Type    string   `json:"type,omitempty"`
	From    string   `json:"from,omitempty"`
	Path    string   `json:"path,omitempty"`
	Options []string `json:"options,omitempty"`
}

// handleSetActiveProfile changes which profile is active for a service and
// then restarts the service so the new values take effect —
//
//	POST /provider/active-profile  body={"service":"llm-api","name":"qwen14b-12gb"}
//
// External services skip the restart (provider doesn't own their lifecycle).
func (p *Provider) handleSetActiveProfile(stream quic.Stream, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		writeHTTPResponse(stream, http.StatusBadRequest, "application/json",
			[]byte(`{"error":"read body"}`))
		return
	}
	var input struct {
		Service string `json:"service"`
		Name    string `json:"name"`
	}
	if err := json.Unmarshal(body, &input); err != nil || input.Service == "" || input.Name == "" {
		writeHTTPResponse(stream, http.StatusBadRequest, "application/json",
			[]byte(`{"error":"service+name required"}`))
		return
	}

	p.CfgMu.RLock()
	cfgPath := p.Cfg.ConfigFile
	svcs := append([]setup.ServiceEntry(nil), p.Cfg.Services...)
	p.CfgMu.RUnlock()
	confDir := filepath.Dir(cfgPath)
	if confDir == "" {
		confDir = "conf"
	}

	path := profile.SetPathForName(confDir, input.Service)
	set, err := profile.LoadSet(path)
	if err != nil || set == nil {
		writeHTTPResponse(stream, http.StatusNotFound, "application/json",
			[]byte(`{"error":"profile set not found"}`))
		return
	}
	if err := set.SetActive(input.Name); err != nil {
		writeHTTPResponse(stream, http.StatusBadRequest, "application/json",
			[]byte(`{"error":"profile not found in set"}`))
		return
	}
	if err := profile.SaveSet(set); err != nil {
		writeHTTPResponse(stream, http.StatusInternalServerError, "application/json",
			[]byte(`{"error":"save failed"}`))
		return
	}

	// Restart the service so the new profile values become live. Skip for
	// external services (vllm) — IANN doesn't manage their lifecycle.
	managed := false
	for _, svc := range svcs {
		if svc.Name == input.Service && svc.IsManagedLocally() {
			managed = true
			break
		}
	}
	if managed {
		go func(name string) {
			svc, ok := p.findService(name)
			if !ok {
				return
			}
			_ = p.dockerStop(svc)
			time.Sleep(500 * time.Millisecond)
			_ = p.dockerStart(svc)
		}(input.Service)
	}

	writeHTTPResponse(stream, http.StatusOK, "application/json",
		[]byte(`{"status":"active_profile_updated","restarted":`+boolStr(managed)+`}`))
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// isManagedService returns true when the service is locally managed by
// IANN (i.e. engine-runner spawned it) — only those allow profile editing
// since IANN actually controls the engine launch args. External engines
// like vllm are treated as display-only.
func isManagedService(name string, svcs []setup.ServiceEntry) bool {
	for _, svc := range svcs {
		if svc.Name == name {
			return svc.IsManagedLocally()
		}
	}
	return false
}

// handleUpsertProfile creates or updates a profile in a service's set —
//
//	POST /provider/profile
//	body={"service":"llm-api","name":"qwen14b-32k","label":"...","values":{...},"set_active":true}
//
// When set_active=true the upserted profile becomes the active one and the
// service is restarted (managed services only). When the profile already
// existed, Upsert replaces it in place.
func (p *Provider) handleUpsertProfile(stream quic.Stream, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		writeHTTPResponse(stream, http.StatusBadRequest, "application/json",
			[]byte(`{"error":"read body"}`))
		return
	}
	var input struct {
		Service      string                `json:"service"`
		Name         string                `json:"name"`
		Label        string                `json:"label"`
		Architecture string                `json:"architecture"`
		Values       map[string]any        `json:"values"`
		Loras        *profile.LoraSettings `json:"loras,omitempty"`
		SetActive    bool                  `json:"set_active"`
	}
	if err := json.Unmarshal(body, &input); err != nil || input.Service == "" || input.Name == "" {
		writeHTTPResponse(stream, http.StatusBadRequest, "application/json",
			[]byte(`{"error":"service+name required"}`))
		return
	}
	if !profile.ValidName(input.Name) {
		writeHTTPResponse(stream, http.StatusBadRequest, "application/json",
			[]byte(`{"error":"name must be lowercase slug (a-z, 0-9, -)"}`))
		return
	}

	p.CfgMu.RLock()
	cfgPath := p.Cfg.ConfigFile
	svcs := append([]setup.ServiceEntry(nil), p.Cfg.Services...)
	p.CfgMu.RUnlock()
	confDir := filepath.Dir(cfgPath)
	if confDir == "" {
		confDir = "conf"
	}

	// Reject edits for external services (vllm etc.) — IANN doesn't own
	// their lifecycle, so changing profile values has no real effect.
	if !isManagedService(input.Service, svcs) {
		writeHTTPResponse(stream, http.StatusForbidden, "application/json",
			[]byte(`{"error":"profile editing is read-only for external services"}`))
		return
	}

	path := profile.SetPathForName(confDir, input.Service)
	set, err := profile.LoadSet(path)
	if err != nil {
		writeHTTPResponse(stream, http.StatusInternalServerError, "application/json",
			[]byte(`{"error":"profile load failed"}`))
		return
	}
	if set == nil {
		// First profile for this service — start fresh. Path must be set
		// for SaveSet to know where to write.
		set = &profile.Set{Path: path, Profiles: []profile.Profile{}}
	}

	// NeedsConfig is intentionally NOT set here — Upsert replaces the
	// existing entry wholesale, so the zero-value (false) clears any
	// auto-seed flag from `isann install -model`. A save through this
	// endpoint counts as the operator's review, which is exactly what
	// the engine-runner's NeedsConfig gate is waiting for.
	// Architecture: normalize operator input to the canonical enum so the
	// rest of the system (LoRA folder routing, UI badges) doesn't have to
	// deal with variants like "SD 1.5" / "stable-diffusion-v1-5".
	set.Upsert(profile.Profile{
		Name:         input.Name,
		Label:        input.Label,
		Architecture: profile.NormalizeArchitecture(input.Architecture),
		Values:       input.Values,
		Loras:        input.Loras, // nil → sticky preserve in Upsert
	})
	if input.SetActive || set.Active == "" {
		set.Active = input.Name
	}
	if err := profile.SaveSet(set); err != nil {
		writeHTTPResponse(stream, http.StatusInternalServerError, "application/json",
			[]byte(`{"error":"save failed"}`))
		return
	}

	// Restart only when the just-upserted profile is active and the service
	// is managed. External (vllm) restart is the user's responsibility.
	restarted := false
	if set.Active == input.Name {
		for _, svc := range svcs {
			if svc.Name == input.Service && svc.IsManagedLocally() {
				restarted = true
				svcCopy := svc
				go func(svc setup.ServiceEntry) {
					_ = p.dockerStop(svc)
					time.Sleep(500 * time.Millisecond)
					_ = p.dockerStart(svc)
				}(svcCopy)
				break
			}
		}
	}

	resp := fmt.Sprintf(`{"status":"upserted","name":%q,"active":%q,"restarted":%s}`,
		input.Name, set.Active, boolStr(restarted))
	writeHTTPResponse(stream, http.StatusOK, "application/json", []byte(resp))
}

// handleDeleteProfile removes a profile from a service's set —
//
//	DELETE /provider/profile?service=llm-api&name=qwen14b-12gb
//
// Refuses to remove the last remaining profile. When the removed profile
// was active, Active is reassigned to the first remaining entry and a
// service restart is triggered (managed only).
func (p *Provider) handleDeleteProfile(stream quic.Stream, req *http.Request) {
	q := req.URL.Query()
	service := q.Get("service")
	name := q.Get("name")
	if service == "" || name == "" {
		writeHTTPResponse(stream, http.StatusBadRequest, "application/json",
			[]byte(`{"error":"service+name query params required"}`))
		return
	}

	p.CfgMu.RLock()
	cfgPath := p.Cfg.ConfigFile
	svcs := append([]setup.ServiceEntry(nil), p.Cfg.Services...)
	p.CfgMu.RUnlock()
	confDir := filepath.Dir(cfgPath)
	if confDir == "" {
		confDir = "conf"
	}

	if !isManagedService(service, svcs) {
		writeHTTPResponse(stream, http.StatusForbidden, "application/json",
			[]byte(`{"error":"profile editing is read-only for external services"}`))
		return
	}

	path := profile.SetPathForName(confDir, service)
	set, err := profile.LoadSet(path)
	if err != nil || set == nil {
		writeHTTPResponse(stream, http.StatusNotFound, "application/json",
			[]byte(`{"error":"profile set not found"}`))
		return
	}
	wasActive := set.Active == name
	if err := set.Remove(name); err != nil {
		writeHTTPResponse(stream, http.StatusBadRequest, "application/json",
			[]byte(fmt.Sprintf(`{"error":%q}`, err.Error())))
		return
	}
	if err := profile.SaveSet(set); err != nil {
		writeHTTPResponse(stream, http.StatusInternalServerError, "application/json",
			[]byte(`{"error":"save failed"}`))
		return
	}

	restarted := false
	if wasActive {
		for _, svc := range svcs {
			if svc.Name == service && svc.IsManagedLocally() {
				restarted = true
				svcCopy := svc
				go func(svc setup.ServiceEntry) {
					_ = p.dockerStop(svc)
					time.Sleep(500 * time.Millisecond)
					_ = p.dockerStart(svc)
				}(svcCopy)
				break
			}
		}
	}

	resp := fmt.Sprintf(`{"status":"deleted","active":%q,"restarted":%s}`,
		set.Active, boolStr(restarted))
	writeHTTPResponse(stream, http.StatusOK, "application/json", []byte(resp))
}

// handleProviderRegister forces an immediate FullSync register to RV.
// Triggered manually via the broker UI's Register button so owners don't have
// to wait up to 24h for the next session-renewal cycle to push refreshed
// services / inspect data.
func (p *Provider) handleProviderRegister(stream quic.Stream, req *http.Request) {
	// Reset regSent so the next register payload is a signed FullSync.
	p.regMu.Lock()
	p.regSent = false
	p.regMu.Unlock()

	// Wake the FullSync loop. resyncCh is buffered=1; non-blocking send so
	// repeat clicks coalesce instead of stacking.
	select {
	case p.resyncCh <- struct{}{}:
	default:
	}

	writeHTTPResponse(stream, 200, "application/json", []byte(`{"status":"register_queued"}`))
}

// handlePackagesByType reads packages/{type}/.../package.json descriptors
// and returns the slice — optionally filtered to a single type. Empty
// wantType returns every installed package except services (services
// are derived from provider.json via the rendezvous payload, so they're
// kept out of this endpoint to avoid duplicating the source of truth).
func (p *Provider) handlePackagesByType(wantType string) ([]byte, int) {
	ic := p.InstallClient
	raw, err := ic.ReadVersions()
	if err != nil {
		raw = nil
	}
	filtered := make([]json.RawMessage, 0, len(raw))
	for _, v := range raw {
		var t struct {
			Type string `json:"type"`
		}
		if jerr := json.Unmarshal(v, &t); jerr != nil {
			continue
		}
		if wantType == "" {
			if t.Type == "service" {
				continue
			}
		} else if t.Type != wantType {
			continue
		}
		filtered = append(filtered, v)
	}
	data, _ := json.Marshal(filtered)
	return data, 200
}

// modelsRoot returns the absolute, cleaned models directory for this node.
// Resolved from isann.config.json (default `<root>/packages/models`).
// Browse/scan-local are restricted to this subtree.
func (p *Provider) modelsRoot() (string, error) {
	abs, err := filepath.Abs(p.ModelsDir)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// resolveUnderModels resolves reqPath (absolute or relative to WorkDir) to an
// absolute path and verifies that it stays inside ai/models. Empty reqPath
// resolves to the models root itself.
func (p *Provider) resolveUnderModels(reqPath string) (string, error) {
	root, err := p.modelsRoot()
	if err != nil {
		return "", err
	}
	if reqPath == "" || reqPath == "." {
		return root, nil
	}
	var candidate string
	if filepath.IsAbs(reqPath) {
		candidate = filepath.Clean(reqPath)
	} else {
		// Relative reqPath resolves against the iann root so legacy callers
		// that pass `packages/models/<...>` keep working.
		candidate = filepath.Clean(filepath.Join(p.Root, reqPath))
	}
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	rootWithSep := root + string(os.PathSeparator)
	if abs != root && !strings.HasPrefix(abs, rootWithSep) {
		return "", fmt.Errorf("path outside models root")
	}
	return abs, nil
}

// handlePartials lists partial downloads sitting in .temp/download/{name}/.
// Each entry corresponds to a file inside a per-package work directory whose
// on-disk size is greater than zero (and less than or equal to the expected
// total). Used by Broker UI to show "47% saved" + Resume button.
func (p *Provider) handlePartials(stream quic.Stream, req *http.Request) {
	stream.SetWriteDeadline(time.Now().Add(3 * time.Minute))

	type partialFile struct {
		PackageName string `json:"package_name"`
		Type        string `json:"type"`
		Category    string `json:"category,omitempty"`
		FileName    string `json:"file_name"`
		Downloaded  int64  `json:"downloaded"`
		Total       int64  `json:"total"`
		Percent     int    `json:"percent"`
	}
	results := []partialFile{}

	downloadDir := filepath.Join(p.InstallClient.WorkDir, ".temp", "download")
	entries, err := os.ReadDir(downloadDir)
	if err != nil {
		// no .temp/download/ yet → empty list
		writeHTTPResponse(stream, 200, "application/json", []byte("[]"))
		return
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkgDir := filepath.Join(downloadDir, e.Name())
		pkgJSONPath := filepath.Join(pkgDir, "package.json")
		data, rerr := os.ReadFile(pkgJSONPath)
		if rerr != nil {
			continue
		}
		var pkg struct {
			Name      string `json:"name"`
			Type      string `json:"type"`
			Category  string `json:"category,omitempty"`
			Downloads []struct {
				FileName  string `json:"file_name"`
				SizeBytes int64  `json:"size_bytes"`
			} `json:"downloads"`
		}
		if json.Unmarshal(data, &pkg) != nil {
			continue
		}
		for _, f := range pkg.Downloads {
			partialPath := filepath.Join(pkgDir, f.FileName)
			info, statErr := os.Stat(partialPath)
			if statErr != nil || info.Size() <= 0 {
				continue
			}
			pct := 0
			if f.SizeBytes > 0 {
				pct = int(info.Size() * 100 / f.SizeBytes)
				if pct > 100 {
					pct = 100
				}
			}
			results = append(results, partialFile{
				PackageName: pkg.Name,
				Type:        pkg.Type,
				Category:    pkg.Category,
				FileName:    f.FileName,
				Downloaded:  info.Size(),
				Total:       f.SizeBytes,
				Percent:     pct,
			})
		}
	}

	body, _ := json.Marshal(results)
	writeHTTPResponseWithETag(stream, req, 200, "application/json", body)
}

var imageExtensions = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".svg": "image/svg+xml",
	".ico": "image/x-icon", ".bmp": "image/bmp",
}

func (p *Provider) handleServeFile(stream quic.Stream, req *http.Request) {
	stream.SetWriteDeadline(time.Now().Add(1 * time.Minute))

	filePath := req.URL.Query().Get("path")
	if filePath == "" {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"path required"}`))
		return
	}

	p.CfgMu.RLock()
	homeDir := p.Cfg.HomeDir
	p.CfgMu.RUnlock()

	if homeDir == "" {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"home_dir not configured"}`))
		return
	}

	// URL이면 그냥 에러 (외부 URL은 프론트에서 직접 로드)
	if strings.HasPrefix(filePath, "http://") || strings.HasPrefix(filePath, "https://") {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"use URL directly"}`))
		return
	}

	// 절대경로가 아니면 homeDir 기준 상대경로로 조합
	var absPath string
	if filepath.IsAbs(filePath) {
		absPath = filepath.Clean(filePath)
	} else {
		absPath = filepath.Clean(filepath.Join(homeDir, filePath))
	}

	// path traversal 방지: homeDir 밖 접근 차단
	if !strings.HasPrefix(absPath, filepath.Clean(homeDir)) {
		writeHTTPResponse(stream, 403, "application/json", []byte(`{"error":"access denied"}`))
		return
	}

	// 이미지 확장자 체크
	ext := strings.ToLower(filepath.Ext(absPath))
	contentType, ok := imageExtensions[ext]
	if !ok {
		writeHTTPResponse(stream, 403, "application/json", []byte(`{"error":"only image files allowed"}`))
		return
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		msg, _ := json.Marshal(map[string]string{"error": err.Error()})
		writeHTTPResponse(stream, 404, "application/json", msg)
		return
	}

	writeHTTPResponse(stream, 200, contentType, data)
}

func (p *Provider) handleScanLocal(stream quic.Stream, req *http.Request) {
	var body struct {
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil || len(body.Paths) == 0 {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"paths required"}`))
		return
	}

	// Collect all files to scan. Every requested path must resolve inside ai/models.
	type scanFile struct {
		Path        string
		FileName    string
		InstallPath string
	}
	var toScan []scanFile

	for _, reqPath := range body.Paths {
		resolved, err := p.resolveUnderModels(reqPath)
		if err != nil {
			msg, _ := json.Marshal(map[string]string{"error": err.Error()})
			writeHTTPResponse(stream, 403, "application/json", msg)
			return
		}
		info, statErr := os.Stat(resolved)
		if statErr != nil {
			continue
		}
		if !info.IsDir() {
			toScan = append(toScan, scanFile{Path: resolved, FileName: filepath.Base(resolved), InstallPath: filepath.Dir(resolved)})
			continue
		}
		filepath.Walk(resolved, func(fp string, fi os.FileInfo, err error) error {
			if err != nil || fi.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(fi.Name()))
			if modelExtensions[ext] {
				toScan = append(toScan, scanFile{Path: fp, FileName: fi.Name(), InstallPath: filepath.Dir(fp)})
			}
			return nil
		})
	}

	stream.SetWriteDeadline(time.Now().Add(10 * time.Minute))

	type fileResult struct {
		FileName    string `json:"file_name"`
		InstallPath string `json:"install_path"`
		SizeBytes   int64  `json:"size_bytes"`
	}
	var results []fileResult

	for _, sf := range toScan {
		info, _ := os.Stat(sf.Path)
		var size int64
		if info != nil {
			size = info.Size()
		}
		// Store install_path relative to WorkDir when possible so manifests are portable.
		installPath := sf.InstallPath
		if rel, err := filepath.Rel(p.InstallClient.WorkDir, sf.InstallPath); err == nil {
			installPath = filepath.ToSlash(rel)
		}
		results = append(results, fileResult{FileName: sf.FileName, InstallPath: installPath, SizeBytes: size})
	}

	if results == nil {
		results = []fileResult{}
	}
	data, _ := json.Marshal(map[string]interface{}{"files": results})
	writeHTTPResponse(stream, 200, "application/json", data)
}

func (p *Provider) handleSavePackage(stream quic.Stream, req *http.Request) {
	stream.SetWriteDeadline(time.Now().Add(3 * time.Minute))

	var pkg struct {
		Type         string          `json:"type"`
		Name         string          `json:"name"`
		Version      string          `json:"version"`
		Platform     string          `json:"platform"`
		Service      string          `json:"service,omitempty"`
		Architecture string          `json:"architecture,omitempty"`
		InstallRoot  string          `json:"install_root,omitempty"`
		IsPublic     bool            `json:"is_public,omitempty"`
		Files        json.RawMessage `json:"files"`
	}
	if err := json.NewDecoder(req.Body).Decode(&pkg); err != nil {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"invalid json"}`))
		return
	}
	if pkg.Name == "" || pkg.Type == "" {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"name and type required"}`))
		return
	}

	iv := map[string]interface{}{
		"type":         pkg.Type,
		"name":         pkg.Name,
		"version":      pkg.Version,
		"platform":     pkg.Platform,
		"installed_at": time.Now().Format(time.RFC3339),
		"version_id":   0,
		"files":        json.RawMessage(pkg.Files),
	}
	if pkg.Service != "" {
		iv["service"] = pkg.Service
	}
	if pkg.Architecture != "" {
		iv["architecture"] = pkg.Architecture
	}

	data, _ := json.MarshalIndent(iv, "", "  ")
	typeDir := filepath.Join(p.InstallClient.WorkDir, "packages", installclient.TypeDir(pkg.Type))
	os.MkdirAll(typeDir, 0755)
	pkgPath := filepath.Join(typeDir, pkg.Name+".json")
	if err := os.WriteFile(pkgPath, data, 0644); err != nil {
		msg, _ := json.Marshal(map[string]string{"error": err.Error()})
		writeHTTPResponse(stream, 500, "application/json", msg)
		return
	}
	writeHTTPResponse(stream, 200, "application/json", []byte(`{"status":"ok"}`))
}

// === Sync Handlers ===

// handleSyncCreate starts async snapshot creation.
func (p *Provider) handleSyncCreate(stream quic.Stream, req *http.Request) {
	stream.SetWriteDeadline(time.Now().Add(10 * time.Second))

	// Parse TTL from request body
	var body struct {
		TTLHours int `json:"ttl_hours"`
	}
	if b, err := io.ReadAll(req.Body); err == nil {
		json.Unmarshal(b, &body)
	}
	ttl := time.Duration(body.TTLHours) * time.Hour
	if ttl <= 0 {
		ttl = 1 * time.Hour
	}
	if ttl > 24*time.Hour {
		ttl = 24 * time.Hour
	}

	// Kick off snapshot creation fully async. Hashing happens inside the
	// syncMgr goroutine — the handler must return fast so the broker's
	// 30s stream read deadline isn't hit on large WorkDirs. Process list
	// used to come from PID-file scans (engine-runner era); with docker
	// containers IANN no longer tracks PIDs, so an empty slice is fed in.
	listProcs := func() ([]ProcessInfo, error) {
		return nil, nil
	}
	if err := p.syncMgr.StartCreateSnapshot(p.InstallClient.WorkDir, p.NodeIdentity.Address, listProcs, ttl); err != nil {
		msg, _ := json.Marshal(map[string]string{"error": err.Error()})
		writeHTTPResponse(stream, 409, "application/json", msg)
		return
	}

	resp, _ := json.Marshal(map[string]string{"status": "creating"})
	writeHTTPResponse(stream, 202, "application/json", resp)
}

// handleSyncStatus returns current snapshot creation status.
func (p *Provider) handleSyncStatus(stream quic.Stream, req *http.Request) {
	stream.SetWriteDeadline(time.Now().Add(10 * time.Second))
	status := p.syncMgr.Status(p.InstallClient.WorkDir)
	// Annotate with this node's peer identity + rendezvous coordinate for the
	// sync-status UI. The peer ID includes a "S:" prefix for providers (see
	// rendezvous.go:225).
	status["node_id"] = "S:" + p.NodeIdentity.Address
	p.CfgMu.RLock()
	status["rendezvous_addr"] = p.Cfg.OutboundGateway.RendezvousHostPort()
	p.CfgMu.RUnlock()
	data, _ := json.Marshal(status)
	writeHTTPResponse(stream, 200, "application/json", data)
}

// handleSyncSnapshot returns the cached snapshot for a valid token.
func (p *Provider) handleSyncSnapshot(stream quic.Stream, req *http.Request) {
	stream.SetWriteDeadline(time.Now().Add(1 * time.Minute))

	token := extractBearerToken(req)
	if token == "" {
		writeHTTPResponse(stream, 401, "application/json", []byte(`{"error":"authorization required"}`))
		return
	}

	snap, err := p.syncMgr.GetSnapshot(p.InstallClient.WorkDir, token)
	if err != nil {
		msg, _ := json.Marshal(map[string]string{"error": err.Error()})
		writeHTTPResponse(stream, 401, "application/json", msg)
		return
	}

	data, _ := json.Marshal(snap)
	writeHTTPResponse(stream, 200, "application/json", data)
}

// handleSyncFile serves a single file from WorkDir for sync download.
func (p *Provider) handleSyncFile(stream quic.Stream, req *http.Request) {
	token := extractBearerToken(req)
	if token == "" {
		writeHTTPResponse(stream, 401, "application/json", []byte(`{"error":"authorization required"}`))
		return
	}

	_, err := p.syncMgr.GetSnapshot(p.InstallClient.WorkDir, token)
	if err != nil {
		msg, _ := json.Marshal(map[string]string{"error": err.Error()})
		writeHTTPResponse(stream, 401, "application/json", msg)
		return
	}

	relPath := req.URL.Query().Get("path")
	if relPath == "" {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"path required"}`))
		return
	}

	workDir := p.InstallClient.WorkDir
	absPath := filepath.Clean(filepath.Join(workDir, relPath))

	// Path traversal protection
	if !strings.HasPrefix(absPath, filepath.Clean(workDir)) {
		writeHTTPResponse(stream, 403, "application/json", []byte(`{"error":"access denied"}`))
		return
	}

	f, err := os.Open(absPath)
	if err != nil {
		msg, _ := json.Marshal(map[string]string{"error": "file not found"})
		writeHTTPResponse(stream, 404, "application/json", msg)
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		writeHTTPResponse(stream, 500, "application/json", []byte(`{"error":"stat failed"}`))
		return
	}

	// Stream the file with chunked-style writing
	stream.SetWriteDeadline(time.Now().Add(30 * time.Minute)) // large files
	fmt.Fprintf(stream, "HTTP/1.1 200 OK\r\n")
	fmt.Fprintf(stream, "Content-Type: application/octet-stream\r\n")
	fmt.Fprintf(stream, "Content-Length: %d\r\n", info.Size())
	fmt.Fprintf(stream, "\r\n")
	io.Copy(stream, f)
}

// extractBearerToken extracts the token from "Authorization: Bearer <token>" header.
func extractBearerToken(req *http.Request) string {
	auth := req.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}

// removeFileQuiet removes a file silently.
func removeFileQuiet(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}
