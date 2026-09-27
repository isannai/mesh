package queue

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/isannai/mesh/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/setup"
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

	// Pictures marks a service that answers with pictures (manifest result
	// modality image). A success that carries no picture is a failed job: the
	// payment gate prices a picture job by what it asked for, so an empty
	// answer must not come back as done.
	Pictures bool

	// StreamPath is the manifest's api.run.result.stream_path — the JSONPath
	// into an SSE delta chunk (e.g. "choices[0].delta.content"). When set and
	// a job has Stream=true, the processor reads the engine's SSE response,
	// extracts tokens via this path, and accumulates sentence chunks on the
	// job. Empty disables streaming (falls back to buffering). See M3 in
	// docs/TODO/infer-streaming.md. A /v1/completions job reads its tokens from
	// completionText instead: that stream carries text, not chat deltas.
	StreamPath string

	// Timeout bounds a single engine call so a stalled engine cannot wedge the
	// worker slot forever (docs/bugs/2026-07-30-queue-worker-wedge-on-stream-stall.md).
	// Streaming jobs treat it as an *idle* deadline (reset on each chunk);
	// buffered jobs treat it as a *total* deadline. A job may override it via
	// Job.Timeout. Zero = no timeout (legacy behaviour).
	Timeout time.Duration
}

// MakeManagedProcess returns a ProcessFunc for managed engines reached via
// engine-runner (sd.cpp, llama.cpp, …). The processor:
//
//  1. POSTs job.RequestBody to http://{svc.Addr}{job.Path} preserving headers
//  2. Reads the full response body. An OpenAI images answer stays as the
//     engine gave it ({data:[{b64_json}, …]}), every picture in it.
//  3. If opts.Storage is non-nil, persists to disk and stamps job.URL.
//     On successful save returns body=nil so runJob frees the heap.
//  4. Returns (code, contentType, body, err) for the queue's runJob to record.
//
// Network errors and ctx cancellation propagate via the err return — runJob
// flips Status=Failed and surfaces the message. So does an engine that
// answers 4xx/5xx (*EngineError, which keeps the answer) and, for Pictures, a
// success without a picture: none of them is work that can be billed.
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

	return func(ctx context.Context, job *Job) (int, string, []byte, error) {
		// engine-runner is now a sync reverse-proxy (Phase 8) — POSTing here
		// blocks until the child engine (sd-server / llama-server) returns,
		// no special query string needed. Provider's queue is the single
		// source of truth for concurrency / back-pressure.

		// Per-call timeout: request override (job.Timeout) else service default
		// (opts.Timeout). A stalled engine call must not hold the worker slot
		// forever (that froze the whole queue — see docs/bugs). For a streaming
		// job this is an *idle* deadline (reset on each chunk); for a buffered
		// job a *total* deadline. On expiry cancel() closes the engine
		// connection, which frees the slot AND signals the engine to stop.
		timeout := job.Timeout
		if timeout <= 0 {
			timeout = opts.Timeout
		}
		// Streamable engines (llm/vllm — those with a stream_path) are ALWAYS
		// consumed as a stream internally, regardless of what the client asked
		// for. Every call then gets the idle-timeout protection above, and the
		// worker reassembles a full response for buffered clients (/result,
		// wait=true) while streaming clients poll /chunk. Non-streamable engines
		// (sd/image) stay buffered. See docs/bugs …stream-stall.md §3-3.
		internalStream := opts.StreamPath != ""

		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		var watchdog *time.Timer
		if timeout > 0 {
			watchdog = time.AfterFunc(timeout, cancel)
			defer watchdog.Stop()
		}

		reqBody := job.RequestBody
		if internalStream {
			reqBody = forceStreamFlag(job.RequestBody) // make the engine emit SSE
		}
		target := fmt.Sprintf("http://%s%s", addrFn(svc), job.Path)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, target, bytes.NewReader(reqBody))
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
		if internalStream {
			respCT := resp.Header.Get("Content-Type")
			if strings.Contains(respCT, "event-stream") {
				// Idle-bounded: streamSentenceChunks resets the watchdog on each
				// chunk, so a progressing generation never times out — only a
				// stalled stream (no chunk for `timeout`) does.
				streamPath := opts.StreamPath
				if isCompletions(job.Path) {
					streamPath = completionText
				}
				return streamSentenceChunks(reqCtx, resp, job, streamPath, watchdog, timeout)
			}
			body, rerr := io.ReadAll(resp.Body)
			if rerr != nil {
				return 0, "", nil, rerr
			}
			// An engine refusing the request answers JSON instead of a stream.
			// That is a failed job, not a chunk of the answer.
			if !success(resp.StatusCode) {
				return 0, "", nil, &EngineError{Code: resp.StatusCode, ContentType: respCT, Body: body}
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
		if !success(code) {
			return 0, "", nil, &EngineError{Code: code, ContentType: ct, Body: body}
		}
		if opts.Pictures && !hasPicture(ct, body) {
			return 0, "", nil, fmt.Errorf("engine answered %d without a picture", code)
		}

		// Persist to disk when storage is wired.
		if opts.Storage != nil && len(body) > 0 {
			if opts.Storage.Save(job, body, ct) {
				return code, ct, nil, nil // body persisted, drop in-memory copy
			}
			// Storage.Save returned false (write failed or nil-safe path) —
			// fall through and keep the body in memory as a graceful fallback.
		}

		return code, ct, body, nil
	}
}

// EngineError is an engine that answered 4xx or 5xx. The job fails, so the
// payment gate does not bill it, and the answer is kept: the result door gives
// it back with the engine's own status (jobs_handler writeFailed).
type EngineError struct {
	Code        int
	ContentType string
	Body        []byte
}

func (e *EngineError) Error() string {
	return fmt.Sprintf("engine answered %d: %s", e.Code, errorMessage(e.Body))
}

func success(code int) bool { return code >= 200 && code < 300 }

// errorMessage is the message in an engine's error body ({"error": {"message":
// …}}, {"error": "…"}, {"message": …} or {"detail": …}), else the body itself,
// cut short.
func errorMessage(body []byte) string {
	var m struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
		Detail  string          `json:"detail"`
	}
	msg := ""
	if json.Unmarshal(body, &m) == nil {
		var s string
		var inner struct {
			Message string `json:"message"`
		}
		switch {
		case json.Unmarshal(m.Error, &s) == nil && s != "":
			msg = s
		case json.Unmarshal(m.Error, &inner) == nil && inner.Message != "":
			msg = inner.Message
		case m.Message != "":
			msg = m.Message
		case m.Detail != "":
			msg = m.Detail
		}
	}
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300]) + "…"
	}
	return msg
}

// hasPicture reports an answer that carries at least one picture: an image
// body, or OpenAI images JSON with a b64_json or url entry.
func hasPicture(contentType string, body []byte) bool {
	if strings.HasPrefix(contentType, "image/") {
		return len(body) > 0
	}
	var payload struct {
		Data []struct {
			B64JSON string `json:"b64_json"`
			URL     string `json:"url"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	for _, d := range payload.Data {
		if d.B64JSON != "" || d.URL != "" {
			return true
		}
	}
	return false
}

// completionText is where a /v1/completions stream chunk carries its text.
const completionText = "choices[0].text"

// isCompletions reports the OpenAI completions path. Its stream carries text
// (choices[0].text), not the chat delta the manifest's stream_path names, and
// its answer is a text_completion, not a chat.completion.
func isCompletions(path string) bool { return path == "/v1/completions" }

// forceStreamFlag injects "stream": true (+ stream_options.include_usage) into a
// JSON-object engine body so a streamable engine emits SSE even when the client
// requested a buffered response. The worker then reassembles the full result for
// buffered clients. Non-object / unparseable bodies are returned unchanged.
// Mirrors the station handler's ensureStreamFlag (kept local to avoid an import
// cycle). See docs/bugs …stream-stall.md §3-3.
func forceStreamFlag(body []byte) []byte {
	var m map[string]any
	if len(body) > 0 {
		if json.Unmarshal(body, &m) != nil {
			return body // not a JSON object — leave as-is
		}
	}
	if m == nil {
		m = map[string]any{}
	}
	m["stream"] = true
	if _, ok := m["stream_options"]; !ok {
		m["stream_options"] = map[string]any{"include_usage": true}
	}
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

// streamSentenceChunks reads an engine SSE response, extracts the incremental
// token from each `data:` event via streamPath, feeds a sentence Segmenter, and
// appends completed sentences to the job as it goes (so a poller sees
// chunk_count grow). It also captures the stream's metadata (the last chunk's
// id/model/finish_reason + the usage chunk) and reassembles a non-streaming
// OpenAI chat.completion response (a text_completion for /v1/completions):
// returned as the result body (application/
// json) so `infer result` extracts content via content_path and `-json` shows
// usage/timings/etc. The message-excluded metadata is stored on the job
// (job.SetMeta) for `infer chunk --index -1` (the EOF marker). Stops on
// `data: [DONE]`, EOF, or read error. See docs/TODO/infer-streaming.md (M8).
func streamSentenceChunks(ctx context.Context, resp *http.Response, job *Job, streamPath string, watchdog *time.Timer, idle time.Duration) (int, string, []byte, error) {
	seg := NewSegmenter(job.ChunkMode, 0)
	var full strings.Builder
	var lastWithChoices map[string]any  // last chunk carrying choices[] (id/model/finish_reason)
	var usageObj any                    // top-level usage (final chunk, needs stream_options.include_usage)
	toolAcc := map[int]map[string]any{} // tool_calls reassembled from deltas, keyed by index
	var toolOrder []int                 // first-seen order of tool-call indices

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		// Any read activity means the stream is alive — push the idle deadline
		// back. A genuinely stalled stream stops reaching here, so the watchdog
		// fires, cancels ctx, and the next read errors out below.
		if watchdog != nil && idle > 0 {
			watchdog.Reset(idle)
		}
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
					if ch0, ok := chs[0].(map[string]any); ok {
						if delta, ok := ch0["delta"].(map[string]any); ok {
							mergeToolCallDeltas(toolAcc, &toolOrder, delta)
						}
					}
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

	// If the loop ended because the idle watchdog cancelled the request (engine
	// stalled mid-stream), fail the job so the worker slot is released and the
	// caller sees an error rather than a silently-truncated success.
	if ctx.Err() != nil {
		return 0, "", nil, fmt.Errorf("engine stream stalled: no chunk for %s", idle)
	}

	code := resp.StatusCode
	if code == 0 {
		code = http.StatusOK
	}
	if isCompletions(job.Path) {
		job.SetMeta(assembleCompletionResult(lastWithChoices, usageObj, full.String(), false))
		return code, "application/json", assembleCompletionResult(lastWithChoices, usageObj, full.String(), true), nil
	}

	var toolCalls []any
	for _, idx := range toolOrder {
		toolCalls = append(toolCalls, toolAcc[idx])
	}

	job.SetMeta(assembleStreamResult(lastWithChoices, usageObj, full.String(), toolCalls, false))
	fullJSON := assembleStreamResult(lastWithChoices, usageObj, full.String(), toolCalls, true)
	return code, "application/json", fullJSON, nil
}

// assembleCompletionResult is assembleStreamResult for /v1/completions: the
// last choices-bearing chunk as a "text_completion", usage folded in, and
// choices[0].text holding the whole text (withText) or dropped (the EOF
// marker's metadata).
func assembleCompletionResult(last map[string]any, usage any, text string, withText bool) []byte {
	var obj map[string]any
	if last != nil {
		obj = cloneJSONObj(last)
	} else {
		obj = map[string]any{"choices": []any{map[string]any{"index": 0}}}
	}
	obj["object"] = "text_completion"
	if usage != nil {
		obj["usage"] = usage
	}
	if chs, ok := obj["choices"].([]any); ok && len(chs) > 0 {
		if ch0, ok := chs[0].(map[string]any); ok {
			if withText {
				ch0["text"] = text
			} else {
				delete(ch0, "text")
			}
		}
	}
	b, _ := json.Marshal(obj)
	return b
}

// assembleStreamResult reconstructs a non-streaming OpenAI chat.completion JSON
// from a streamed response. It clones the last choices-bearing chunk (carrying
// id/model/created/system_fingerprint/finish_reason), sets object to
// "chat.completion", folds in usage, and replaces choices[0].delta with either
// a message (withMessage=true → full result body) or nothing (withMessage=false
// → message-excluded metadata for the EOF marker). last==nil → minimal shape.
func assembleStreamResult(last map[string]any, usage any, content string, toolCalls []any, withMessage bool) []byte {
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
				msg := map[string]any{"role": "assistant"}
				if len(toolCalls) > 0 {
					msg["tool_calls"] = toolCalls
					// OpenAI convention: a pure tool-call turn has content: null.
					if content == "" {
						msg["content"] = nil
					} else {
						msg["content"] = content
					}
				} else {
					msg["content"] = content
				}
				ch0["message"] = msg
			} else {
				delete(ch0, "message")
			}
		}
	}
	b, _ := json.Marshal(obj)
	return b
}

// mergeToolCallDeltas folds a streamed choices[0].delta.tool_calls fragment into
// an accumulator keyed by tool-call index: id/type/function.name are set once
// (first non-empty wins), function.arguments are concatenated across chunks.
// This reconstructs the full tool_calls array a buffered client would have
// received from a non-streamed response. See docs/bugs …stream-stall.md §3-3.
func mergeToolCallDeltas(acc map[int]map[string]any, order *[]int, delta map[string]any) {
	tcs, ok := delta["tool_calls"].([]any)
	if !ok {
		return
	}
	for _, raw := range tcs {
		tc, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		idx := 0
		if f, ok := tc["index"].(float64); ok {
			idx = int(f)
		}
		cur := acc[idx]
		if cur == nil {
			cur = map[string]any{"index": idx, "type": "function", "function": map[string]any{"name": "", "arguments": ""}}
			acc[idx] = cur
			*order = append(*order, idx)
		}
		if id, ok := tc["id"].(string); ok && id != "" {
			cur["id"] = id
		}
		if typ, ok := tc["type"].(string); ok && typ != "" {
			cur["type"] = typ
		}
		if fn, ok := tc["function"].(map[string]any); ok {
			curFn, _ := cur["function"].(map[string]any)
			if curFn == nil {
				curFn = map[string]any{"name": "", "arguments": ""}
				cur["function"] = curFn
			}
			if name, ok := fn["name"].(string); ok && name != "" {
				curFn["name"] = name
			}
			if args, ok := fn["arguments"].(string); ok {
				prev, _ := curFn["arguments"].(string)
				curFn["arguments"] = prev + args
			}
		}
	}
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
