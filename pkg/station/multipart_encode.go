package station

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"sort"
	"strings"

	"github.com/isannai/mesh/pkg/engine/manifest"
)

// encodeEngineBody serializes a JSON engine body into the wire format declared
// by a route's EncodingSpec, returning the encoded bytes and the Content-Type
// to stamp on the forwarded request. Today only "multipart" is supported.
//
// This is the single point where params leave their JSON canonical form — it
// runs at the engine edge (queue submit), AFTER preset injection / run-mapping
// / extra_args have all done their work on the JSON. See
// docs/confirm/20260704/sd-img2img-queue-plan.md.
func encodeEngineBody(jsonBody []byte, enc *manifest.EncodingSpec) ([]byte, string, error) {
	if enc == nil || enc.Type != "multipart" {
		return nil, "", fmt.Errorf("unsupported encoding %q", encType(enc))
	}
	return encodeMultipart(jsonBody, enc.FileFields)
}

func encType(enc *manifest.EncodingSpec) string {
	if enc == nil {
		return ""
	}
	return enc.Type
}

// encodeMultipart transcodes a JSON object body into multipart/form-data.
// fileFields name keys whose (base64 or data: URL) string value becomes a file
// part, decoded to raw bytes; every other key becomes a text form field.
// Numbers/bools are stringified to their JSON literal. Keys are emitted in
// sorted order so the output is deterministic (testable). Returns the encoded
// body and the Content-Type header (carrying the boundary).
func encodeMultipart(jsonBody []byte, fileFields []string) ([]byte, string, error) {
	var m map[string]any
	if err := json.Unmarshal(jsonBody, &m); err != nil {
		return nil, "", fmt.Errorf("body is not a JSON object: %w", err)
	}
	isFile := make(map[string]bool, len(fileFields))
	for _, f := range fileFields {
		isFile[f] = true
	}

	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, k := range keys {
		v := m[k]
		if isFile[k] {
			s, ok := v.(string)
			if !ok || s == "" {
				continue // absent / empty file field → skip (optional mask, etc.)
			}
			raw, err := decodeDataField(s)
			if err != nil {
				return nil, "", fmt.Errorf("file field %q: %w", k, err)
			}
			fw, err := mw.CreateFormFile(k, k+".png")
			if err != nil {
				return nil, "", err
			}
			if _, err := fw.Write(raw); err != nil {
				return nil, "", err
			}
			continue
		}
		if err := mw.WriteField(k, scalarString(v)); err != nil {
			return nil, "", err
		}
	}
	if err := mw.Close(); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), mw.FormDataContentType(), nil
}

// decodeDataField base64-decodes a file-field value, tolerating a `data:...;
// base64,` URL prefix.
func decodeDataField(s string) ([]byte, error) {
	if i := strings.Index(s, "base64,"); i >= 0 {
		s = s[i+len("base64,"):]
	}
	s = strings.TrimSpace(s)
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil {
		return raw, nil
	}
	// Fall back to URL-safe / unpadded encodings some clients emit.
	return base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
}

// scalarString renders a non-file value as a form-field string: strings pass
// through; everything else (number/bool/null/object) uses its JSON literal.
func scalarString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
