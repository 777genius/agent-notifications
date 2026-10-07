package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDoNotDisturbMode(t *testing.T) {
	tests := []struct {
		name  string
		value *string
		want  string
	}{
		{name: "unset defaults to off", value: nil, want: DNDModeOff},
		{name: "off", value: strPtr("off"), want: DNDModeOff},
		{name: "silent", value: strPtr("silent"), want: DNDModeSilent},
		{name: "suppress", value: strPtr("suppress"), want: DNDModeSuppress},
		{name: "case insensitive", value: strPtr("SILENT"), want: DNDModeSilent},
		{name: "surrounding whitespace", value: strPtr("  suppress \n"), want: DNDModeSuppress},
		{name: "empty string falls back to off", value: strPtr(""), want: DNDModeOff},
		{name: "boolean-looking value falls back to off", value: strPtr("true"), want: DNDModeOff},
		{name: "typo falls back to off", value: strPtr("silnet"), want: DNDModeOff},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Notifications.RespectDoNotDisturb = tt.value
			assert.Equal(t, tt.want, cfg.GetDoNotDisturbMode())
		})
	}
}

// TestValidate_AcceptsAnyDoNotDisturbValue pins the deliberate asymmetry with
// teamMode: an unrecognised respectDoNotDisturb must not fail config loading,
// because that would silence every notification over a typo in one optional
// field. GetDoNotDisturbMode warns and falls back instead.
func TestValidate_AcceptsAnyDoNotDisturbValue(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Notifications.RespectDoNotDisturb = strPtr("nonsense")

	require.NoError(t, cfg.Validate())
	assert.Equal(t, DNDModeOff, cfg.GetDoNotDisturbMode())
}

func TestRespectDoNotDisturb_JSONRoundTrip(t *testing.T) {
	t.Run("absent field stays nil and omits on marshal", func(t *testing.T) {
		var cfg Config
		require.NoError(t, json.Unmarshal([]byte(`{"notifications":{}}`), &cfg))
		assert.Nil(t, cfg.Notifications.RespectDoNotDisturb)
		assert.Equal(t, DNDModeOff, cfg.GetDoNotDisturbMode())

		encoded, err := json.Marshal(cfg.Notifications)
		require.NoError(t, err)
		assert.NotContains(t, string(encoded), "respectDoNotDisturb")
	})

	t.Run("explicit value is parsed", func(t *testing.T) {
		var cfg Config
		require.NoError(t, json.Unmarshal([]byte(`{"notifications":{"respectDoNotDisturb":"silent"}}`), &cfg))
		require.NotNil(t, cfg.Notifications.RespectDoNotDisturb)
		assert.Equal(t, DNDModeSilent, cfg.GetDoNotDisturbMode())
	})
}

func strPtr(s string) *string { return &s }
