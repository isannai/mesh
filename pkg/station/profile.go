package station

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/quic-go/quic-go"
)

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
