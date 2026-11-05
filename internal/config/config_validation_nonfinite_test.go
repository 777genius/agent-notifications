package config

import (
	"math"
	"strings"
	"testing"
)

func TestValidateDesktopVolumeNonFinite(t *testing.T) {
	tests := []struct {
		name       string
		volume     float64
		wantReject bool
	}{
		{name: "NaN", volume: math.NaN(), wantReject: true},
		{name: "positive infinity", volume: math.Inf(1), wantReject: true},
		{name: "negative infinity", volume: math.Inf(-1), wantReject: true},
		{name: "lower bound", volume: 0},
		{name: "upper bound", volume: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Notifications.Desktop.Volume = tc.volume
			err := cfg.Validate()
			if tc.wantReject {
				if err == nil || !strings.Contains(err.Error(), "desktop volume") {
					t.Fatalf("Validate() error = %v; want desktop volume rejection", err)
				}
			} else if err != nil {
				t.Fatalf("Validate() error = %v; want nil", err)
			}
		})
	}
}
