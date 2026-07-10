package station

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/daesob/http3proxy/pkg/tunnel"
	"github.com/quic-go/quic-go"
)

// handleUploadEmblem saves a base64 image as home_dir/emblem.{ext} and updates config.
func (p *Provider) handleUploadEmblem(stream quic.Stream, req *http.Request) {
	stream.SetWriteDeadline(time.Now().Add(3 * time.Minute))

	p.CfgMu.RLock()
	homeDir := p.Cfg.HomeDir
	p.CfgMu.RUnlock()

	if homeDir == "" {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"home_dir not configured"}`))
		return
	}

	body, _ := io.ReadAll(req.Body)
	var r struct {
		Image string `json:"image"`
	}
	if json.Unmarshal(body, &r) != nil || r.Image == "" {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"image is required"}`))
		return
	}

	if !strings.HasPrefix(r.Image, "data:image/") {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"invalid image format"}`))
		return
	}
	parts := strings.SplitN(r.Image, ",", 2)
	if len(parts) != 2 {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"invalid base64 image"}`))
		return
	}

	ext := ".png"
	if strings.Contains(parts[0], "jpeg") || strings.Contains(parts[0], "jpg") {
		ext = ".jpg"
	} else if strings.Contains(parts[0], "webp") {
		ext = ".webp"
	}

	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"base64 decode failed"}`))
		return
	}

	profileDir := filepath.Join(homeDir, "profile")
	os.MkdirAll(profileDir, 0755)

	for _, e := range []string{".png", ".jpg", ".webp"} {
		os.Remove(filepath.Join(profileDir, "emblem"+e))
	}

	filename := "emblem" + ext
	dst := filepath.Join(profileDir, filename)
	if err := os.WriteFile(dst, decoded, 0644); err != nil {
		msg, _ := json.Marshal(map[string]string{"error": err.Error()})
		writeHTTPResponse(stream, 500, "application/json", msg)
		return
	}

	p.CfgMu.Lock()
	p.Cfg.Emblem = "profile/" + filename
	cfg := p.Cfg
	p.CfgMu.Unlock()
	tunnel.SaveConfig(cfg)

	resp, _ := json.Marshal(map[string]string{"emblem": "profile/" + filename})
	writeHTTPResponse(stream, 200, "application/json", resp)
}

// handleDeleteEmblem removes the emblem file and clears config.
func (p *Provider) handleDeleteEmblem(stream quic.Stream, req *http.Request) {
	stream.SetWriteDeadline(time.Now().Add(3 * time.Minute))

	p.CfgMu.RLock()
	homeDir := p.Cfg.HomeDir
	p.CfgMu.RUnlock()

	if homeDir == "" {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"home_dir not configured"}`))
		return
	}

	profileDir := filepath.Join(homeDir, "profile")
	for _, e := range []string{".png", ".jpg", ".webp"} {
		os.Remove(filepath.Join(profileDir, "emblem"+e))
	}

	p.CfgMu.Lock()
	p.Cfg.Emblem = ""
	cfg := p.Cfg
	p.CfgMu.Unlock()
	tunnel.SaveConfig(cfg)

	writeHTTPResponse(stream, 200, "application/json", []byte(`{"status":"ok"}`))
}

// handleGetAbout returns the node's about text from home_dir/about.md.
func (p *Provider) handleGetAbout(stream quic.Stream, req *http.Request) {
	stream.SetWriteDeadline(time.Now().Add(3 * time.Minute))

	p.CfgMu.RLock()
	homeDir := p.Cfg.HomeDir
	p.CfgMu.RUnlock()

	about := ""
	if homeDir != "" {
		profileDir := filepath.Join(homeDir, "profile")
		if raw, err := os.ReadFile(filepath.Join(profileDir, "about.md")); err == nil {
			about = string(raw)
		}
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(map[string]string{"about": about})
	data := bytes.TrimSpace(buf.Bytes())
	writeHTTPResponseWithETag(stream, req, 200, "application/json", data)
}

// handleSetAbout saves about text to home_dir/about.md.
func (p *Provider) handleSetAbout(stream quic.Stream, req *http.Request) {
	stream.SetWriteDeadline(time.Now().Add(3 * time.Minute))

	p.CfgMu.RLock()
	homeDir := p.Cfg.HomeDir
	p.CfgMu.RUnlock()

	if homeDir == "" {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"home_dir not configured"}`))
		return
	}

	body, _ := io.ReadAll(req.Body)
	var r struct {
		About string `json:"about"`
	}
	if json.Unmarshal(body, &r) != nil {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"invalid request"}`))
		return
	}

	profileDir := filepath.Join(homeDir, "profile")
	os.MkdirAll(profileDir, 0755)
	if err := os.WriteFile(filepath.Join(profileDir, "about.md"), []byte(r.About), 0644); err != nil {
		msg, _ := json.Marshal(map[string]string{"error": err.Error()})
		writeHTTPResponse(stream, 500, "application/json", msg)
		return
	}
	writeHTTPResponse(stream, 200, "application/json", []byte(`{"status":"ok"}`))
}
