package station

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/isannai/mesh/pkg/engine/manifest"
	"github.com/isannai/mesh/pkg/setup"
	"github.com/isannai/mesh/pkg/station/queue"
	"github.com/isannai/mesh/pkg/tunnel"
)

// TestCheckPicture: the gate's table, the architecture's narrower list, and n.
func TestCheckPicture(t *testing.T) {
	cases := []struct {
		body, arch string
		ok         bool
	}{
		{`{"size":"0x0"}`, "", false}, // killed sd-server (SP-09)
		{`{"size":"-512x512"}`, "", false},
		{`{"size":"300x300"}`, "", false},
		{`{"size":"abc"}`, "", false},
		{`{"size":"512x512"}`, "", true},
		{`{"size":"768x512"}`, "sd15", true}, // same entry as 512x768
		{`{"size":"1024x1024"}`, "sd15", false},
		{`{"size":"1024x1024"}`, "sdxl", true},
		{`{"size":"512x512"}`, "sdxl", false},
		{`{"size":"1024x1024"}`, "somearch", true}, // unknown arch: the table only
		{`{"width":0,"height":512}`, "", false},
		{`{"width":512,"height":512}`, "sd15", true},
		{`{"n":0}`, "", false},
		{`{"n":17}`, "", false},
		{`{"n":"2","size":"512x512"}`, "sd15", true},
		{`{"prompt":"a cat"}`, "sd15", true}, // no size: the default applies
		{`not json`, "", true},               // left to the engine
	}
	for _, c := range cases {
		err := checkPictureRequest("application/json", []byte(c.body), c.arch)
		if (err == nil) != c.ok {
			t.Errorf("%s arch=%q: err=%v, want ok=%v", c.body, c.arch, err, c.ok)
		}
		if err != nil && !errors.Is(err, errPictureSize) {
			t.Errorf("%s: %v is not errPictureSize", c.body, err)
		}
	}
}

// TestCheckPictureSteps: steps 1..maxSteps, as a body field or wrapped into the
// prompt the way sd.cpp reads it.
func TestCheckPictureSteps(t *testing.T) {
	cases := []struct {
		body string
		ok   bool
	}{
		{`{"prompt":"x","steps":20}`, true},
		{`{"prompt":"x","steps":40}`, true},
		{`{"prompt":"x","steps":"40"}`, true}, // a ${steps} template arrives as a string
		{`{"prompt":"x","steps":41}`, false},
		{`{"prompt":"x","steps":0}`, false},
		{`{"prompt":"x","steps":-5}`, false},
		{`{"prompt":"x","steps":20.5}`, false},
		{`{"prompt":"x","steps":""}`, true}, // empty: the engine's default
		{`{"prompt":"x","sample_steps":500}`, false},
		{`{"prompt":"x<sd_cpp_extra_args>{\"steps\":30,\"seed\":1}</sd_cpp_extra_args>"}`, true},
		{`{"prompt":"x<sd_cpp_extra_args>{\"steps\":500}</sd_cpp_extra_args>"}`, false},
		{`{"prompt":"x<sd_cpp_extra_args>{\"sample_steps\":500}</sd_cpp_extra_args>"}`, false},
		{`{"prompt":"a<sd_cpp_extra_args>{\"seed\":1}</sd_cpp_extra_args>b<sd_cpp_extra_args>{\"steps\":99}</sd_cpp_extra_args>"}`, false},
		{`{"prompt":"x<sd_cpp_extra_args>steps=500</sd_cpp_extra_args>"}`, false}, // not JSON: cannot be checked
		{`{"prompt":"x<sd_cpp_extra_args>{\"steps\":500}"}`, false},               // no closing tag
		{`{"prompt":"x"}`, true}, // no steps: the default applies
	}
	for _, c := range cases {
		err := checkPictureRequest("application/json", []byte(c.body), "sd15")
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v, want ok=%v", c.body, err, c.ok)
		}
		if err != nil && !errors.Is(err, errPictureSteps) {
			t.Errorf("%s: %v is not errPictureSteps", c.body, err)
		}
	}
}

// TestCheckPictureMultipart: an img2img edit carries its size as a form field.
func TestCheckPictureMultipart(t *testing.T) {
	form := func(size string) (string, []byte) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		mw.WriteField("prompt", "a cat")
		mw.WriteField("size", size)
		fw, _ := mw.CreateFormFile("image", "in.png")
		fw.Write([]byte("\x89PNG not really"))
		mw.Close()
		return mw.FormDataContentType(), buf.Bytes()
	}
	ct, body := form("0x0")
	if err := checkPictureRequest(ct, body, "sd15"); err == nil {
		t.Error("multipart 0x0 passed")
	}
	ct, body = form("512x512")
	if err := checkPictureRequest(ct, body, "sd15"); err != nil {
		t.Errorf("multipart 512x512: %v", err)
	}

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("prompt", `a cat<sd_cpp_extra_args>{"steps":500}</sd_cpp_extra_args>`)
	mw.WriteField("size", "512x512")
	mw.Close()
	if err := checkPictureRequest(mw.FormDataContentType(), buf.Bytes(), "sd15"); !errors.Is(err, errPictureSteps) {
		t.Errorf("multipart wrapped steps 500: %v, want errPictureSteps", err)
	}
	buf.Reset()
	mw = multipart.NewWriter(&buf)
	mw.WriteField("prompt", "a cat")
	mw.WriteField("steps", "41")
	mw.Close()
	if err := checkPictureRequest(mw.FormDataContentType(), buf.Bytes(), "sd15"); !errors.Is(err, errPictureSteps) {
		t.Errorf("multipart steps 41: %v, want errPictureSteps", err)
	}
}

// countingEngine answers every request 200 and counts them.
func countingEngine(t *testing.T) (*httptest.Server, *atomic.Int32) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"b64_json":"iVBORw0KGgo="}]}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestSubmitRefusesPictureSize: a job submit for a size the engine cannot make
// is 400 and never reaches the engine; a good size queues as before.
func TestSubmitRefusesPictureSize(t *testing.T) {
	engine, hits := countingEngine(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	mgr := queue.NewManager(ctx, stubFactory(engine))
	api := &manifest.APISpec{Run: &manifest.RunSpec{
		Path:   "/v1/images/generations",
		Result: manifest.ResultSpec{Modality: "image"},
		Params: []manifest.RunParam{
			{Name: "prompt", Type: "string", Required: true},
			{Name: "size", Type: "string", Default: "512x512"},
		},
		Body: json.RawMessage(`{"prompt":"${prompt}","size":"${size}"}`),
	}}
	h := NewJobsHandler(mgr, nil, []setup.ServiceEntry{{Name: "sd-api", Addr: "ignored"}},
		func(string) *manifest.APISpec { return api }, nil)
	h.archOf = func(setup.ServiceEntry) string { return "sd15" }
	srv := serveJobs(t, h)

	submit := func(body string) (int, errorResponse) {
		resp, err := http.Post(srv.URL+"/svc/sd-api/v1/jobs", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		defer resp.Body.Close()
		var er errorResponse
		json.NewDecoder(resp.Body).Decode(&er)
		return resp.StatusCode, er
	}

	for _, body := range []string{
		`{"service":"sd-api","params":{"prompt":"x","size":"0x0"}}`,
		`{"service":"sd-api","params":{"prompt":"x","size":"1024x1024"}}`, // sd15
		`{"service":"sd-api","run":{"prompt":"x","size":"0x0"}}`,
		`{"service":"sd-api","params":{"prompt":"x","size":"512x512","n":17}}`,
	} {
		code, er := submit(body)
		if code != http.StatusBadRequest || er.Reason != reasonImageSize || !strings.Contains(er.Error, "image size not accepted") {
			t.Errorf("%s: %d %+v, want 400 %s", body, code, er, reasonImageSize)
		}
	}
	for _, body := range []string{
		`{"service":"sd-api","params":{"prompt":"x","steps":41}}`,
		`{"service":"sd-api","run":{"prompt":"x<sd_cpp_extra_args>{\"steps\":500}</sd_cpp_extra_args>"}}`,
	} {
		code, er := submit(body)
		if code != http.StatusBadRequest || er.Reason != reasonImageSteps || !strings.Contains(er.Error, "image steps not accepted") {
			t.Errorf("%s: %d %+v, want 400 %s", body, code, er, reasonImageSteps)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("engine was called %d times for refused pictures", n)
	}

	for _, body := range []string{
		`{"service":"sd-api","params":{"prompt":"x","size":"512x512"},"wait":true}`,
		`{"service":"sd-api","run":{"prompt":"x"},"wait":true}`, // manifest default 512x512
	} {
		if code, er := submit(body); code != http.StatusOK {
			t.Errorf("%s: %d %+v, want it to run", body, code, er)
		}
	}
	if n := hits.Load(); n != 2 {
		t.Errorf("engine calls = %d, want 2", n)
	}
}

// TestServiceProxyRefusesPictureSize: a call handed straight to the engine (a
// free node's /svc/<svc>/v1/images/generations) gets the same check.
func TestServiceProxyRefusesPictureSize(t *testing.T) {
	engine, hits := countingEngine(t)
	p := &Provider{Base: &tunnel.Base{}}
	p.Cfg.Services = []setup.ServiceEntry{{Name: "sd-api", Addr: strings.TrimPrefix(engine.URL, "http://")}}
	p.pictureRuleFor = func(string) (string, bool) { return "sd15", true }
	srv := httptest.NewServer(http.HandlerFunc(p.HandleServiceProxy))
	t.Cleanup(srv.Close)

	post := func(body string) int {
		resp, err := http.Post(srv.URL+"/svc/sd-api/v1/images/generations", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post(`{"prompt":"x","size":"0x0"}`); code != http.StatusBadRequest {
		t.Errorf("0x0 = %d, want 400", code)
	}
	if code := post(`{"prompt":"x","size":"1024x1024"}`); code != http.StatusBadRequest {
		t.Errorf("1024x1024 on sd15 = %d, want 400", code)
	}
	if code := post(`{"prompt":"x<sd_cpp_extra_args>{\"steps\":500}</sd_cpp_extra_args>","size":"512x512"}`); code != http.StatusBadRequest {
		t.Errorf("wrapped steps 500 = %d, want 400", code)
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("engine was called %d times for refused pictures", n)
	}
	if code := post(`{"prompt":"x","size":"512x512"}`); code != http.StatusOK {
		t.Errorf("512x512 = %d, want the engine's 200", code)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("engine calls = %d, want 1", n)
	}
}
