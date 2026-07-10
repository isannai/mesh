package station

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/daesob/http3proxy/pkg/glog"
	"github.com/quic-go/quic-go"
)

// handleLogs serves log file list or tail of a specific file.
// GET /provider/logs              → file list
// GET /provider/logs?file=x&tail=100&q=search → tail of file
func (p *Provider) handleLogs(stream quic.Stream, req *http.Request) {
	stream.SetWriteDeadline(time.Now().Add(10 * time.Second))

	file := req.URL.Query().Get("file")

	if file == "" {
		// Return file list
		p.handleLogFileList(stream)
		return
	}

	// Return tail of specific file
	p.handleLogTail(stream, req, file)
}

func (p *Provider) handleLogFileList(stream quic.Stream) {
	type logFile struct {
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		Category string `json:"category"` // "log" or "event"
	}

	var files []logFile
	logsDir := filepath.Join(p.InstallClient.WorkDir, "logs")

	// Scan logs/*.log
	entries, _ := os.ReadDir(logsDir)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".log") {
			info, err := e.Info()
			if err != nil {
				continue
			}
			// Skip unreadable files so clients don't hit 404 after seeing
			// the name in the listing (file appears, then open fails).
			if f, oerr := os.Open(filepath.Join(logsDir, e.Name())); oerr != nil {
				continue
			} else {
				f.Close()
			}
			files = append(files, logFile{
				Name:     e.Name(),
				Size:     info.Size(),
				Category: "log",
			})
		}
	}

	// Scan logs/events/*.jsonl
	eventsDir := filepath.Join(logsDir, "events")
	eventEntries, _ := os.ReadDir(eventsDir)
	for _, e := range eventEntries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".jsonl") {
			info, err := e.Info()
			if err != nil {
				continue
			}
			files = append(files, logFile{
				Name:     "events/" + e.Name(),
				Size:     info.Size(),
				Category: "event",
			})
		}
	}

	if files == nil {
		files = []logFile{}
	}

	data, _ := json.Marshal(files)
	writeHTTPResponse(stream, 200, "application/json", data)
}

func (p *Provider) handleLogTail(stream quic.Stream, req *http.Request, file string) {
	tail := 100
	if v := req.URL.Query().Get("tail"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 1000 {
				n = 1000
			}
			tail = n
		}
	}
	search := strings.ToLower(req.URL.Query().Get("q"))

	// Sanitize file path to prevent directory traversal
	file = filepath.Clean(file)
	if strings.Contains(file, "..") {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"invalid file path"}`))
		return
	}

	logsDir := filepath.Join(p.InstallClient.WorkDir, "logs")
	fullPath := filepath.Join(logsDir, file)

	// Verify file is under logs directory
	absLogs, _ := filepath.Abs(logsDir)
	absFile, _ := filepath.Abs(fullPath)
	if !strings.HasPrefix(absFile, absLogs) {
		writeHTTPResponse(stream, 400, "application/json", []byte(`{"error":"invalid file path"}`))
		return
	}

	f, err := os.Open(fullPath)
	if err != nil {
		p.Log.Log(glog.Error, "[station] logs tail open FAILED: path=%q err=%v workDir=%q", fullPath, err, p.InstallClient.WorkDir)
		body, _ := json.Marshal(map[string]string{
			"error":    "file not found",
			"path":     fullPath,
			"workDir":  p.InstallClient.WorkDir,
			"cause":    err.Error(),
			"binMark":  "v2-diag-0425",
		})
		writeHTTPResponse(stream, 404, "application/json", body)
		return
	}
	defer f.Close()

	// Read all lines (for tail)
	var allLines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text()+"\n")
	}

	// Tail
	start := len(allLines) - tail
	if start < 0 {
		start = 0
	}
	lines := allLines[start:]

	// Search filter
	if search != "" {
		filtered := make([]string, 0, len(lines))
		for _, line := range lines {
			if strings.Contains(strings.ToLower(line), search) {
				filtered = append(filtered, line)
			}
		}
		lines = filtered
	}

	data, _ := json.Marshal(map[string]any{
		"lines": lines,
		"total": len(lines),
	})
	writeHTTPResponse(stream, 200, "application/json", data)
}
