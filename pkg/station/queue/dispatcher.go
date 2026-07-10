package queue

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/daesob/http3proxy/pkg/engine/manifest"
	"github.com/daesob/http3proxy/pkg/setup"
)

// DispatchOptions tunes a managed-engine dispatcher. All fields are optional.
type DispatchOptions struct {
	// Storage persists successful results to disk and stamps job.URL. Nil =
	// in-memory mode (vLLM-style streaming, small responses, etc.).
	Storage *Storage

	// Client overrides the HTTP client used to forward requests. Nil =
	// http.Client{} with no timeout (image generation can take minutes).
	Client *http.Client

	// AddrFunc maps a ServiceEntry to its addressable host:port. Nil =
	// svc.Addr verbatim. Override for tests pointing at httptest servers.
	AddrFunc func(setup.ServiceEntry) string

	// DecodeOpenAIImage controls whether successful JSON responses with the
	// shape {data:[{b64_json}]} are decoded into raw PNG bytes. Defaults to
	// true (sd-api compatibility — sd-server speaks OpenAI Images API and
	// the broker expects raw image bytes).
	DecodeOpenAIImage *bool

	// StreamPath is the manifest's api.run.result.stream_path — the JSONPath
	// into an SSE delta chunk (e.g. "choices[0].delta.content"). When set and
	// a job has Stream=true, the processor reads the engine's SSE response,
	// extracts tokens via this path, and accumulates sentence chunks on the
	// job. Empty disables streaming (falls back to buffering). See M3 in
	// docs/TODO/infer-streaming.md.
	StreamPath string
}

// MakeManagedProcess returns a ProcessFunc for managed engines reached via
// engine-runner (sd.cpp, llama.cpp, …). The processor:
//
//  1. POSTs job.RequestBody to http://{svc.Addr}{job.Path} preserving headers
//  2. Reads the full response body
//  3. If the response is a JSON success body shaped like an OpenAI Images
//     response, decodes the first b64_json into raw bytes (sd-api compat)
//  4. If opts.Storage is non-nil, persists to disk and stamps job.URL.
//     On successful save returns body=nil so runJob frees the heap.
//  5. Returns (code, contentType, body, err) for the queue's runJob to record.
//
// Network errors and ctx cancellation propagate via the err return — runJob
// flips Status=Failed and surfaces the message.
func MakeManagedProcess(svc setup.ServiceEntry, opts DispatchOptions) ProcessFunc {
	client := opts.Client
	if client == nil {
		// No global timeout. Long-running image generation needs unbounded
		// I/O; the queue's outer ctx (Manager.ctx) handles real shutdown.
		client = &http.Client{}
	}
	addrFn := opts.AddrFunc
	if addrFn == nil {
		addrFn = func(s setup.ServiceEntry) string { return s.Addr }
	}
	decodeImage := true
	if opts.DecodeOpenAIImage != nil {
		decodeImage = *opts.DecodeOpenAIImage
	}

	return func(ctx context.Context, job *Job) (int, string, []byte, error) {
		// engine-runner is now a sync reverse-proxy (Phase 8) — POSTing here
		// blocks until the child engine (sd-server / llama-server) returns,
		// no special query string needed. Provider's queue is the single
		// source of truth for concurrency / back-pressure.
		target := fmt.Sprintf("http://%s%s", addrFn(svc), job.Path)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(job.RequestBody))
		if err != nil {
			return 0, "", nil, err
		}
		for k, vs := range job.RequestHeader {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		// Tell the engine which Provider-side job this request maps to.
		// Set explicitly (not via Add) so any inbound X-Job-Id from the
		// broker is overwritten — it's an internal IPC name, not user-
		// supplied. Progress events used to ride X-Progress-Callback back
		// to a localhost listener; that path was retired with engine-runner.
		req.Header.Set("X-Job-Id", job.ID)

		resp, err := client.Do(req)
		if err != nil {
			return 0, "", nil, err
		}
		defer resp.Body.Close()

		// Streaming (sentence-chunk) path: read the engine's SSE response,
		// extract tokens via StreamPath, accumulate sentence chunks on the job.
		// Falls back to buffering when the engine ignored stream:true (the
		// whole body becomes a single chunk). See M3, docs/TODO/infer-streaming.md.
		if job.Stream && opts.StreamPath != "" {
			respCT := resp.Header.Get("Content-Type")
			if strings.Contains(respCT, "event-stream") {
				return streamSentenceChunks(resp, job, opts.StreamPath)
			}
			body, rerr := io.ReadAll(resp.Body)
			if rerr != nil {
				return 0, "", nil, rerr
			}
			if len(body) > 0 {
				job.AppendChunk(string(body)) // non-SSE fallback: one chunk
			}
			return resp.StatusCode, respCT, body, nil
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return 0, "", nil, err
		}

		code := resp.StatusCode
		ct := resp.Header.Get("Content-Type")

		// OpenAI image decode (sd-api convention): success + JSON body with
		// {data:[{b64_json}]} → unwrap to raw image bytes.
		if decodeImage && code >= 200 && code < 300 && strings.Contains(ct, "json") {
			if pngBytes, ok := decodeOpenAIImageBody(body); ok {
				body = pngBytes
				ct = "image/png"
			}
		}

		// Persist to disk on success when storage is wired.
		if opts.Storage != nil && code >= 200 && code < 300 && len(body) > 0 {
			if opts.Storage.Save(job, body, ct) {
				return code, ct, nil, nil // body persisted, drop in-memory copy
			}
			// Storage.Save returned false (write failed or nil-safe path) —
			// fall through and keep the body in memory as a graceful fallback.
		}

		return code, ct, body, nil
	}
}

// decodeOpenAIImageBody parses an OpenAI images.generations-style payload
// and returns the first b64_json image as raw bytes. Returns false when
// the payload does not match.
//
// Same logic as engine-runner's decodeOpenAIImage; will be the single
// source of truth once Phase 8 deletes the engine-runner copy.
func decodeOpenAIImageBody(body []byte) ([]byte, bool) {
	var payload struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false
	}
	if len(payload.Data) == 0 || payload.Data[0].B64JSON == "" {
		return nil, false
	}
	raw, err := base64.StdEncoding.DecodeString(payload.Data[0].B64JSON)
	if err != nil {
		return nil, false
	}
	return raw, true
}

// streamSentenceChunks reads an engine SSE response, extracts the incremental
// token from each `data:` event via streamPath, feeds a sentence Segmenter, and
// appends completed sentences to the job as it goes (so a poller sees
// chunk_count grow). It also captures the stream's metadata (the last chunk's
// id/model/finish_reason + the usage chunk) and reassembles a non-streaming
// OpenAI chat.completion response: returned as the result body (application/
// json) so `infer result` extracts content via content_path and `-json` shows
// usage/timings/etc. The message-excluded metadata is stored on the job
// (job.SetMeta) for `infer chunk --index -1` (the EOF marker). Stops on
// `data: [DONE]`, EOF, or read error. See docs/TODO/infer-streaming.md (M8).
func streamSentenceChunks(resp *http.Response, job *Job, streamPath string) (int, string, []byte, error) {
	seg := NewSegmenter(job.ChunkMode, 0)
	var full strings.Builder
	var lastWithChoices map[string]any // last chunk carrying choices[] (id/model/finish_reason)
	var usageObj any                   // top-level usage (final chunk, needs stream_options.include_usage)

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if data, ok := parseSSEData(line); ok && data != "" {
			if data == "[DONE]" {
				break
			}
			var obj map[string]any
			if json.Unmarshal([]byte(data), &obj) == nil {
				if tok, ok := manifest.JSONPath(obj, streamPath).(string); ok && tok != "" {
					full.WriteString(tok)
					for _, ch := range seg.Feed(tok) {
						job.AppendChunk(ch)
					}
				}
				if chs, ok := obj["choices"].([]any); ok && len(chs) > 0 {
					lastWithChoices = obj
				}
				if u, ok := obj["usage"]; ok && u != nil {
					usageObj = u
				}
			}
		}
		if err != nil {
			break // EOF or read error — finish with what we have
		}
	}
	if last := seg.Flush(); last != "" {
		job.AppendChunk(last)
	}

	job.SetMeta(assembleStreamResult(lastWithChoices, usageObj, full.String(), false))
	fullJSON := assembleStreamResult(lastWithChoices, usageObj, full.String(), true)

	code := resp.StatusCode
	if code == 0 {
		code = http.StatusOK
	}
	return code, "application/json", fullJSON, nil
}

// assembleStreamResult reconstructs a non-streaming OpenAI chat.completion JSON
// from a streamed response. It clones the last choices-bearing chunk (carrying
// id/model/created/system_fingerprint/finish_reason), sets object to
// "chat.completion", folds in usage, and replaces choices[0].delta with either
// a message (withMessage=true → full result body) or nothing (withMessage=false
// → message-excluded metadata for the EOF marker). last==nil → minimal shape.
func assembleStreamResult(last map[string]any, usage any, content string, withMessage bool) []byte {
	var obj map[string]any
	if last != nil {
		obj = cloneJSONObj(last)
	} else {
		obj = map[string]any{"choices": []any{map[string]any{"index": 0}}}
	}
	obj["object"] = "chat.completion"
	if usage != nil {
		obj["usage"] = usage
	}
	if chs, ok := obj["choices"].([]any); ok && len(chs) > 0 {
		if ch0, ok := chs[0].(map[string]any); ok {
			delete(ch0, "delta")
			if withMessage {
				ch0["message"] = map[string]any{"role": "assistant", "content": content}
			} else {
				delete(ch0, "message")
			}
		}
	}
	b, _ := json.Marshal(obj)
	return b
}

// cloneJSONObj deep-copies a JSON-decoded object via a marshal round-trip, so
// the full and metadata variants can be mutated independently.
func cloneJSONObj(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var c map[string]any
	_ = json.Unmarshal(b, &c)
	return c
}

// parseSSEData returns the payload of an SSE `data:` line (ok=false for
// comments, event:/id: fields, and blank lines). Trailing CR/LF stripped.
func parseSSEData(line string) (string, bool) {
	line = strings.TrimRight(line, "\r\n")
	if !strings.HasPrefix(line, "data:") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "data:")), true
}
