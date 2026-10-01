package stationwire

import (
	"encoding/json"
	"testing"

	"github.com/isannai/mesh/pkg/setup"
)

// BUG-01: `"enable": false` in station.json took an engine out of service and
// had the station ask isannd to stop its container, which isannd refused (no
// operator session), so the engine kept running unserved. There is no such
// switch now: engines are started and stopped by hand with isann. A config
// still carrying the old field keeps the engine in service.
func TestOldEnableFieldDoesNotDropAnEngine(t *testing.T) {
	var override []setup.ServiceEntry
	if err := json.Unmarshal([]byte(`[{"engine":"clip","enable":false}]`), &override); err != nil {
		t.Fatal(err)
	}
	derived := []setup.ServiceEntry{{Name: "clip-api", Addr: "127.0.0.1:7870", Engine: "clip"}}

	got := MergeServices(derived, override)
	if len(got) != 1 || got[0].Name != "clip-api" || got[0].Addr != "127.0.0.1:7870" {
		t.Fatalf("services = %+v, want clip-api still served", got)
	}
}
