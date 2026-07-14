package manifest

import "testing"

func testAPI() *APISpec {
	return &APISpec{
		ProxyRoutes: []ProxyRoute{
			{Path: "/v1/images/generations", Method: "POST"},
			{Path: "/v1/images/edits", Method: "POST",
				Encoding: &EncodingSpec{Type: "multipart", FileFields: []string{"image", "mask"}}},
		},
		Run:  &RunSpec{Path: "/v1/images/generations"},
		Runs: []RunSpec{{Path: "/v1/images/edits"}},
	}
}

func TestRunFor(t *testing.T) {
	a := testAPI()
	if got := a.RunFor("/v1/images/edits"); got == nil || got.Path != "/v1/images/edits" {
		t.Errorf("RunFor(edits) = %v, want the edits variant", got)
	}
	if got := a.RunFor("/v1/images/generations"); got != a.Run {
		t.Errorf("RunFor(generations) should be the default Run")
	}
	if got := a.RunFor(""); got != a.Run {
		t.Errorf("RunFor(\"\") should be the default Run")
	}
	if got := a.RunFor("/unknown"); got != a.Run {
		t.Errorf("RunFor(unknown) should fall back to the default Run")
	}
}

func TestEncodingFor(t *testing.T) {
	a := testAPI()
	if enc := a.EncodingFor("/v1/images/edits"); enc == nil || enc.Type != "multipart" {
		t.Errorf("EncodingFor(edits) = %v, want multipart", enc)
	}
	if enc := a.EncodingFor("/v1/images/generations"); enc != nil {
		t.Errorf("EncodingFor(generations) = %v, want nil", enc)
	}
	if enc := a.EncodingFor("/nope"); enc != nil {
		t.Errorf("EncodingFor(unknown) = %v, want nil", enc)
	}
}

func TestAllowsPath(t *testing.T) {
	a := testAPI()
	for _, p := range []string{"/v1/images/generations", "/v1/images/edits"} {
		if !a.AllowsPath(p) {
			t.Errorf("AllowsPath(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"/v1/../secret", "/etc/passwd", ""} {
		if a.AllowsPath(p) {
			t.Errorf("AllowsPath(%q) = true, want false", p)
		}
	}
}

func TestEncodingSpecValidate(t *testing.T) {
	if err := (&EncodingSpec{Type: "multipart", FileFields: []string{"image"}}).Validate(); err != nil {
		t.Errorf("valid multipart encoding rejected: %v", err)
	}
	if err := (&EncodingSpec{Type: "form", FileFields: []string{"image"}}).Validate(); err == nil {
		t.Error("unsupported type should be rejected")
	}
	if err := (&EncodingSpec{Type: "multipart"}).Validate(); err == nil {
		t.Error("empty file_fields should be rejected")
	}
}
