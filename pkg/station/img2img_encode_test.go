package station

// img2img_encode_test.go — verifies the queue-side img2img path (B안): a JSON
// submit {path:/v1/images/edits, run:{prompt,image,strength}} is mapped through
// the run template and then transcoded to multipart/form-data at the engine
// edge, with the base64 image param decoded into a file part. The engine's
// OpenAI-images response is decoded back to image/png.
// See docs/confirm/20260704/sd-img2img-queue-plan.md.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/daesob/http3proxy/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/station/queue"
	"github.com/daesob/http3proxy/pkg/setup"
)

// mpEngine records the Content-Type + raw body it receives and returns an
// OpenAI-images response so a wait=true submit resolves to a decoded image.
type mpEngine struct {
	mu   sync.Mutex
	ct   string
	body []byte
	seen bool
}

func newMPEngine(pngB64 string) (*mpEngine, *httptest.Server) {
	e := &mpEngine{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		e.mu.Lock()
		e.ct, e.body, e.seen = r.Header.Get("Content-Type"), b, true
		e.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"b64_json":"` + pngB64 + `"}]}`))
	}))
	return e, srv
}

func (e *mpEngine) get() (string, []byte, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ct, e.body, e.seen
}

func newAPIHandler(t *testing.T, engine *httptest.Server, api *manifest.APISpec) *httptest.Server {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := queue.NewManager(ctx, stubFactory(engine))
	services := []setup.ServiceEntry{{Name: "sd-api", Addr: "ignored"}}
	apiFor := func(svc string) *manifest.APISpec {
		if svc == "sd-api" {
			return api
		}
		return nil
	}
	h := NewJobsHandler(mgr, nil, services, apiFor, nil)
	mux := http.NewServeMux()
	h.Register(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// sdEditAPI mirrors sd.json's shape: a default txt2img run plus an img2img run
// variant, with /v1/images/edits declaring multipart encoding.
func sdEditAPI() *manifest.APISpec {
	return &manifest.APISpec{
		ProxyRoutes: []manifest.ProxyRoute{
			{Path: "/v1/images/generations", Method: "POST"},
			{Path: "/v1/images/edits", Method: "POST",
				Encoding: &manifest.EncodingSpec{Type: "multipart", FileFields: []string{"image", "mask"}}},
		},
		Run: &manifest.RunSpec{
			Path:   "/v1/images/generations",
			Result: manifest.ResultSpec{Modality: "image", ContentPath: "data[0].b64_json", Encoding: "base64"},
			Params: []manifest.RunParam{{Name: "prompt", Type: "string", Required: true}},
			Body:   json.RawMessage(`{"prompt":"${prompt}"}`),
		},
		Runs: []manifest.RunSpec{{
			Path:   "/v1/images/edits",
			Result: manifest.ResultSpec{Modality: "image", ContentPath: "data[0].b64_json", Encoding: "base64"},
			Params: []manifest.RunParam{
				{Name: "prompt", Type: "string", Required: true},
				{Name: "image", Type: "file", Required: true},
				{Name: "mask", Type: "file"},
				{Name: "strength", Type: "float", Default: float64(0.75)},
			},
			Body: json.RawMessage(`{"prompt":"${prompt}","image":"${image}","mask":"${mask}","strength":"${strength}"}`),
		}},
	}
}

func TestImg2ImgMultipart_EndToEnd(t *testing.T) {
	srcBytes := []byte("SOURCEIMAGEBYTES")
	srcB64 := base64.StdEncoding.EncodeToString(srcBytes)
	outB64 := base64.StdEncoding.EncodeToString([]byte("RESULTPNG"))

	eng, engine := newMPEngine(outB64)
	defer engine.Close()
	srv := newAPIHandler(t, engine, sdEditAPI())

	reqBody := `{"service":"sd-api","path":"/v1/images/edits","run":{"prompt":"watercolor","image":"` + srcB64 + `","strength":0.6},"wait":true}`
	resp, err := http.Post(srv.URL+"/v1/jobs", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, b)
	}

	// wait=true → the decoded image/png comes back inline.
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("result Content-Type = %q, want image/png", ct)
	}
	if gotOut, _ := io.ReadAll(resp.Body); string(gotOut) != "RESULTPNG" {
		t.Errorf("result body = %q, want decoded RESULTPNG", gotOut)
	}

	// The engine received multipart with the image decoded into a file part.
	ct, body, seen := eng.get()
	if !seen {
		t.Fatal("engine never received the request")
	}
	mediatype, params, err := mime.ParseMediaType(ct)
	if err != nil || mediatype != "multipart/form-data" {
		t.Fatalf("engine Content-Type = %q, want multipart/form-data", ct)
	}
	mr := multipart.NewReader(strings.NewReader(string(body)), params["boundary"])
	fields, files := map[string]string{}, map[string][]byte{}
	for {
		p, perr := mr.NextPart()
		if perr != nil {
			break
		}
		data, _ := io.ReadAll(p)
		if p.FileName() != "" {
			files[p.FormName()] = data
		} else {
			fields[p.FormName()] = string(data)
		}
	}
	if string(files["image"]) != string(srcBytes) {
		t.Errorf("image part = %q, want decoded %q", files["image"], srcBytes)
	}
	if _, ok := files["mask"]; ok {
		t.Errorf("unset mask should produce no part")
	}
	if fields["prompt"] != "watercolor" {
		t.Errorf("prompt field = %q, want watercolor", fields["prompt"])
	}
	if fields["strength"] != "0.6" {
		t.Errorf("strength field = %q, want 0.6", fields["strength"])
	}
}

func TestPathAllowlist_Rejects(t *testing.T) {
	eng, engine := newMPEngine("")
	defer engine.Close()
	_ = eng
	srv := newAPIHandler(t, engine, sdEditAPI())

	reqBody := `{"service":"sd-api","path":"/v1/../secret","run":{"prompt":"x"},"wait":true}`
	resp, err := http.Post(srv.URL+"/v1/jobs", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=%d, want 400 (path not allowed)", resp.StatusCode)
	}
}
