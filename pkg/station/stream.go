package station

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/isannai/mesh/pkg/glog"
	"github.com/isannai/mesh/pkg/setup"
	"github.com/quic-go/quic-go"
)

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
// /provider/* HTTP path (HandleProviderHTTP). All response writes go through
// `stream` — HandleProviderHTTP passes a bufStream adapter that captures the raw
// HTTP/1.1 response and replays it onto the real http.ResponseWriter.
//
// 🔴 READS ONLY (SEC-16). The station is the bridge between isannd and the
// engines. Its management API (auth settings, profiles, install progress, local
// scan, package save, logs, about/emblem edits, forced register) served only the
// control app, and its gate took any owner signature with no expiry and no
// target, so a signature the owner had sent anywhere else opened it. It is gone
// with its gate; operating a node goes through isannd.
func (p *Provider) dispatchOrchestratorRequest(stream quic.Stream, req *http.Request) {
	p.Log.Log(glog.Request, "[station] orchestrator: %s %s", req.Method, req.URL.Path)

	path := req.URL.Path
	method := req.Method

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
	case path == "/provider/file" && method == "GET":
		p.handleServeFile(stream, req)
	case path == "/provider/about" && method == "GET":
		p.handleGetAbout(stream, req)

	default:
		stream.SetWriteDeadline(time.Now().Add(3 * time.Minute))
		writeHTTPResponse(stream, 404, "application/json", []byte(`{"error":"not found"}`))
	}
}

// findService looks up a ServiceEntry by name under read lock. Returns
// (zero, false) when no match.
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
