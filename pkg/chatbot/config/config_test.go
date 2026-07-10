package config

import (
	"testing"
	"time"
)

func TestTimeoutDuration(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"30s", 30 * time.Second},
		{"1m30s", 90 * time.Second},
		{"", 30 * time.Second},        // empty -> default
		{"bogus", 30 * time.Second},   // invalid -> default
	}
	for _, tt := range tests {
		if got := (AIConfig{Timeout: tt.in}).TimeoutDuration(); got != tt.want {
			t.Errorf("TimeoutDuration(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}
