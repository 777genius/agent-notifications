package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/journal"
	"github.com/777genius/agent-notifications/internal/config"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/strictjson"
)

var errConfig = errors.New("configuration_invalid")

const configLimit = 64 * 1024

func readGlobal(path string) ([]byte, error) {
	info, e := os.Lstat(path)
	if e != nil {
		return nil, e
	}
	if !info.Mode().IsRegular() || info.Size() > configLimit {
		return nil, errConfig
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	actual, e := f.Stat()
	if e != nil || !os.SameFile(info, actual) {
		return nil, errConfig
	}
	b, e := io.ReadAll(io.LimitReader(f, configLimit+1))
	if e != nil {
		return nil, e
	}
	if len(b) > configLimit {
		return nil, errConfig
	}
	return b, nil
}
func object(b []byte) (map[string]json.RawMessage, error) {
	if strictjson.Validate(b, strictjson.Budget{Bytes: configLimit, Depth: 16, Entries: 1024}) != nil {
		return nil, errConfig
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil || m == nil {
		return nil, errConfig
	}
	return m, nil
}
func field(m map[string]json.RawMessage, k string, v any, required bool) error {
	b, ok := m[k]
	if !ok {
		if required {
			return errConfig
		}
		return nil
	}
	if string(bytes.TrimSpace(b)) == "null" || json.Unmarshal(b, v) != nil {
		return errConfig
	}
	if n, ok := v.(*int); ok && *n <= 0 {
		return errConfig
	}
	return nil
}
func (b *Backend) policy(s installruntime.PolicySnapshot) (agentnotify.Policy, error) {
	var p agentnotify.Policy
	p.Rates = journal.RatePolicy{SessionPerMinute: 6, RuntimePerMinute: 30, Burst: 3}
	p.Delivery.Valid = true
	if len(s.Fields) == 0 {
		return p, nil
	} // authoritative missing policy means disabled
	var version int
	var enabled bool
	if field(s.Fields, "schemaVersion", &version, true) != nil || version != 1 || field(s.Fields, "enabled", &enabled, true) != nil {
		return p, errConfig
	}
	// Intent and installed eligibility are separate; both must allow delivery.
	p.Delivery.ExplicitEnabled = enabled && s.Installation.Enabled && !s.Installation.Recovery
	if raw, ok := s.Fields["route"]; ok {
		m, e := object(raw)
		if e != nil {
			return p, e
		}
		for k, v := range map[string]any{"localRouting": &p.Route.LocalRouting, "allowUnknownCaller": &p.Route.AllowUnknownCaller, "allowCallerAsserted": &p.Route.AllowCallerAsserted, "applicationPath": &p.Route.ApplicationPath, "teamID": &p.Route.TeamID} {
			if field(m, k, v, false) != nil {
				return p, errConfig
			}
		}
	}
	if raw, ok := s.Fields["rates"]; ok {
		m, e := object(raw)
		if e != nil {
			return p, e
		}
		for k, v := range map[string]any{"sessionPerMinute": &p.Rates.SessionPerMinute, "runtimePerMinute": &p.Rates.RuntimePerMinute, "burst": &p.Rates.Burst} {
			if field(m, k, v, false) != nil {
				return p, errConfig
			}
		}
	}
	var e error
	p.Rates, e = p.Rates.Normalize()
	if e != nil {
		return p, errConfig
	}
	if !enabled {
		return p, nil
	}
	data, e := b.opts.ReadGlobal(b.opts.GlobalConfig)
	if os.IsNotExist(e) {
		return p, agentnotify.ErrConfigurationRequired
	}
	if e != nil {
		return p, errConfig
	}
	p.Delivery.DesktopEnabled, p.Delivery.SoundEnabled, p.Delivery.ClickToFocus, e = config.ValidateGlobalDesktop(data)
	if e != nil {
		return p, errConfig
	}
	return p, nil
}

// Status separates operator configuration from offline installed eligibility.
// It never opens the journal/spool or invokes native capability/permission probes.
type Status struct {
	Configuration     string `json:"configuration"`
	ExplicitIntent    bool   `json:"explicitIntent"`
	DesktopEnabled    bool   `json:"desktopEnabled"`
	OfflineCapability string `json:"offlineCapability"`
	Permission        string `json:"permission"`
}

func (b *Backend) Status(ctx context.Context) Status {
	out := Status{Configuration: "configuration_invalid", OfflineCapability: "unavailable", Permission: "not_checked"}
	if ctx == nil {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	s, e := b.opts.ReadSnapshot(ctx, b.opts.ControlRoot)
	if e != nil {
		return out
	}
	var enabled bool
	_ = field(s.Fields, "enabled", &enabled, false)
	out.ExplicitIntent = enabled
	p, e := b.policy(s)
	if e != nil {
		if errors.Is(e, agentnotify.ErrConfigurationRequired) {
			out.Configuration = "configuration_required"
		}
		return out
	}
	out.Configuration = "configured"
	if !enabled {
		out.Configuration = "disabled"
	}
	out.DesktopEnabled = p.Delivery.DesktopEnabled
	if b.clock.Now().BootID == "" {
		out.OfflineCapability = "unsupported_platform"
	} else if s.Installation.Enabled && !s.Installation.Recovery && s.Installation.Ledger.Native != nil && s.Installation.Ledger.Native.DecoderFloor >= 1 {
		out.OfflineCapability = "eligible"
	}
	return out
}
