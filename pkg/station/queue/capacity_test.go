package queue

import (
	"testing"
	"time"

	"github.com/daesob/http3proxy/pkg/engine/manifest"
	"github.com/daesob/http3proxy/pkg/setup"
)

// helper: build a manifest with the given queue defaults.
func mfest(c manifest.QueueSpec) *manifest.Manifest {
	return &manifest.Manifest{Queue: c}
}

// helper: pointer to bool literal.
func boolPtr(b bool) *bool { return &b }

func TestResolveMaxQueue(t *testing.T) {
	cases := []struct {
		name   string
		svc    setup.ServiceEntry
		m      *manifest.Manifest
		expect int
	}{
		{
			name:   "service config wins",
			svc:    setup.ServiceEntry{Queue: &setup.QueueOverride{MaxQueue: 10}},
			m:      mfest(manifest.QueueSpec{MaxPending: 5}),
			expect: 10,
		},
		{
			name:   "manifest fallback when svc unset",
			svc:    setup.ServiceEntry{},
			m:      mfest(manifest.QueueSpec{MaxPending: 5}),
			expect: 5,
		},
		{
			name:   "manifest fallback when svc.Queue.MaxQueue=0",
			svc:    setup.ServiceEntry{Queue: &setup.QueueOverride{Concurrency: 4}},
			m:      mfest(manifest.QueueSpec{MaxPending: 50}),
			expect: 50,
		},
		{
			name:   "code fallback when both unset (0 = unlimited)",
			svc:    setup.ServiceEntry{},
			m:      mfest(manifest.QueueSpec{}),
			expect: 0,
		},
		{
			name:   "no manifest at all",
			svc:    setup.ServiceEntry{Queue: &setup.QueueOverride{MaxQueue: 7}},
			m:      nil,
			expect: 7,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveMaxQueue(tc.svc, tc.m); got != tc.expect {
				t.Errorf("got %d, want %d", got, tc.expect)
			}
		})
	}
}

func TestResolveConcurrency(t *testing.T) {
	cases := []struct {
		name   string
		svc    setup.ServiceEntry
		m      *manifest.Manifest
		expect int
	}{
		{
			name:   "service config wins",
			svc:    setup.ServiceEntry{Queue: &setup.QueueOverride{Concurrency: 4}},
			m:      mfest(manifest.QueueSpec{Concurrency: 1}),
			expect: 4,
		},
		{
			name:   "manifest fallback",
			svc:    setup.ServiceEntry{},
			m:      mfest(manifest.QueueSpec{Concurrency: 50}),
			expect: 50,
		},
		{
			name:   "code fallback = 1",
			svc:    setup.ServiceEntry{},
			m:      nil,
			expect: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveConcurrency(tc.svc, tc.m); got != tc.expect {
				t.Errorf("got %d, want %d", got, tc.expect)
			}
		})
	}
}

func TestResolveMaxDone(t *testing.T) {
	cases := []struct {
		name   string
		svc    setup.ServiceEntry
		m      *manifest.Manifest
		expect int
	}{
		{"svc wins", setup.ServiceEntry{Queue: &setup.QueueOverride{MaxDone: 50}}, mfest(manifest.QueueSpec{MaxDone: 100}), 50},
		{"manifest fallback", setup.ServiceEntry{}, mfest(manifest.QueueSpec{MaxDone: 100}), 100},
		{"code fallback", setup.ServiceEntry{}, nil, 100},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveMaxDone(tc.svc, tc.m); got != tc.expect {
				t.Errorf("got %d, want %d", got, tc.expect)
			}
		})
	}
}

func TestResolveTTL(t *testing.T) {
	cases := []struct {
		name   string
		svc    setup.ServiceEntry
		m      *manifest.Manifest
		expect time.Duration
	}{
		{
			name:   "service config wins",
			svc:    setup.ServiceEntry{Queue: &setup.QueueOverride{TTLSec: 1800}},
			m:      mfest(manifest.QueueSpec{DoneTTLSec: 3600}),
			expect: 30 * time.Minute,
		},
		{
			name:   "manifest with positive TTL",
			svc:    setup.ServiceEntry{},
			m:      mfest(manifest.QueueSpec{DoneTTLSec: 3600}),
			expect: time.Hour,
		},
		{
			name:   "manifest TTL=0 means immediate (vLLM intent)",
			svc:    setup.ServiceEntry{},
			m:      mfest(manifest.QueueSpec{DoneTTLSec: 0}),
			expect: 0,
		},
		{
			name:   "no manifest -> 1hr fallback",
			svc:    setup.ServiceEntry{},
			m:      nil,
			expect: time.Hour,
		},
		{
			name:   "service config TTL=0 falls through to manifest (not override)",
			svc:    setup.ServiceEntry{Queue: &setup.QueueOverride{TTLSec: 0}},
			m:      mfest(manifest.QueueSpec{DoneTTLSec: 3600}),
			expect: time.Hour,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveTTL(tc.svc, tc.m); got != tc.expect {
				t.Errorf("got %v, want %v", got, tc.expect)
			}
		})
	}
}

func TestResolveSaveToDisk(t *testing.T) {
	cases := []struct {
		name   string
		svc    setup.ServiceEntry
		m      *manifest.Manifest
		expect bool
	}{
		{
			name:   "service config explicit true",
			svc:    setup.ServiceEntry{Queue: &setup.QueueOverride{SaveToDisk: boolPtr(true)}},
			m:      mfest(manifest.QueueSpec{SaveToDisk: false}),
			expect: true,
		},
		{
			name:   "service config explicit false overrides manifest true",
			svc:    setup.ServiceEntry{Queue: &setup.QueueOverride{SaveToDisk: boolPtr(false)}},
			m:      mfest(manifest.QueueSpec{SaveToDisk: true}),
			expect: false,
		},
		{
			name:   "service config nil falls through to manifest",
			svc:    setup.ServiceEntry{Queue: &setup.QueueOverride{}},
			m:      mfest(manifest.QueueSpec{SaveToDisk: true}),
			expect: true,
		},
		{
			name:   "no override at all -> false",
			svc:    setup.ServiceEntry{},
			m:      nil,
			expect: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveSaveToDisk(tc.svc, tc.m); got != tc.expect {
				t.Errorf("got %v, want %v", got, tc.expect)
			}
		})
	}
}

func TestBuildConfigSDApi(t *testing.T) {
	// sd-api 시나리오: manifest default 만 사용, service config 빈 상태.
	svc := setup.ServiceEntry{Name: "sd-api"}
	m := mfest(manifest.QueueSpec{
		Concurrency: 1,
		MaxPending:  5,
		MaxDone:     100,
		DoneTTLSec:  3600,
		SaveToDisk:  true,
	})

	cfg := BuildConfig(svc, m)
	if cfg.ServiceName != "sd-api" {
		t.Errorf("ServiceName = %q", cfg.ServiceName)
	}
	if cfg.Concurrency != 1 {
		t.Errorf("Concurrency = %d", cfg.Concurrency)
	}
	if cfg.MaxQueue != 5 {
		t.Errorf("MaxQueue = %d", cfg.MaxQueue)
	}
	if cfg.MaxDone != 100 {
		t.Errorf("MaxDone = %d", cfg.MaxDone)
	}
	if cfg.DoneTTL != time.Hour {
		t.Errorf("DoneTTL = %v", cfg.DoneTTL)
	}
	if !cfg.SaveToDisk {
		t.Error("SaveToDisk = false, want true")
	}
}

func TestBuildConfigVLLM(t *testing.T) {
	// vLLM 시나리오: manifest 의 default_ttl_sec=0 / save_to_disk=false 그대로 적용.
	svc := setup.ServiceEntry{Name: "vllm-api"}
	m := mfest(manifest.QueueSpec{
		Concurrency: 50,
		MaxPending:  100,
		MaxDone:     100,
		DoneTTLSec:  0, // streaming → 즉시 evict
		SaveToDisk:  false,
	})

	cfg := BuildConfig(svc, m)
	if cfg.Concurrency != 50 {
		t.Errorf("Concurrency = %d", cfg.Concurrency)
	}
	if cfg.MaxQueue != 100 {
		t.Errorf("MaxQueue = %d", cfg.MaxQueue)
	}
	if cfg.DoneTTL != 0 {
		t.Errorf("DoneTTL = %v, want 0 (immediate evict)", cfg.DoneTTL)
	}
	if cfg.SaveToDisk {
		t.Error("SaveToDisk = true, want false")
	}
}

func TestBuildConfigOverride(t *testing.T) {
	// Operator 가 conf/provider.json 에서 override: max_queue=10 + save_to_disk=false.
	svc := setup.ServiceEntry{
		Name: "sd-api",
		Queue: &setup.QueueOverride{
			MaxQueue:   10,
			SaveToDisk: boolPtr(false),
		},
	}
	m := mfest(manifest.QueueSpec{
		Concurrency: 1,
		MaxPending:  5,
		MaxDone:     100,
		DoneTTLSec:  3600,
		SaveToDisk:  true,
	})

	cfg := BuildConfig(svc, m)
	if cfg.MaxQueue != 10 {
		t.Errorf("MaxQueue = %d, want 10 (override)", cfg.MaxQueue)
	}
	if cfg.SaveToDisk {
		t.Error("SaveToDisk = true, want false (override)")
	}
	// 나머지는 manifest default 그대로
	if cfg.Concurrency != 1 {
		t.Errorf("Concurrency = %d", cfg.Concurrency)
	}
	if cfg.DoneTTL != time.Hour {
		t.Errorf("DoneTTL = %v", cfg.DoneTTL)
	}
}

func TestBuildConfigNoManifest(t *testing.T) {
	// manifest 없는 서비스 (custom external 등): service config 만 사용.
	svc := setup.ServiceEntry{
		Name: "custom",
		Queue: &setup.QueueOverride{
			MaxQueue:    20,
			Concurrency: 2,
		},
	}

	cfg := BuildConfig(svc, nil)
	if cfg.MaxQueue != 20 {
		t.Errorf("MaxQueue = %d", cfg.MaxQueue)
	}
	if cfg.Concurrency != 2 {
		t.Errorf("Concurrency = %d", cfg.Concurrency)
	}
	// nil manifest 에선 fallback 동작 확인
	if cfg.MaxDone != 100 {
		t.Errorf("MaxDone = %d (expected fallback 100)", cfg.MaxDone)
	}
	if cfg.DoneTTL != time.Hour {
		t.Errorf("DoneTTL = %v (expected fallback 1h)", cfg.DoneTTL)
	}
}
