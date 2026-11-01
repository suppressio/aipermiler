package pacing_test

import (
	"testing"

	"github.com/suppressio/aipermiler/gocore/internal/pacing"
)

func TestNormalizedUtilization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		used      float64
		total     float64
		want      float64
		wantValid bool
	}{
		{"half", 50, 100, 50, true},
		{"over total is capped", 150, 100, 100, true},
		{"negative used is floored", -5, 100, 0, true},
		{"zero total", 10, 0, 0, false},
		{"negative total", 10, -1, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := pacing.NormalizedUtilization(tt.used, tt.total)
			if ok != tt.wantValid || got != tt.want {
				t.Fatalf("NormalizedUtilization(%v, %v) = %v, %v; want %v, %v",
					tt.used, tt.total, got, ok, tt.want, tt.wantValid)
			}
		})
	}
}
