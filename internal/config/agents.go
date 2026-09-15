package config

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// AgentID is selected by the composition root, never inferred from paths or env.
type AgentID string

const (
	AgentClaude                AgentID = "claude"
	AgentCodex                 AgentID = "codex"
	AssetRootPlaceholder               = "AGENT_NOTIFICATIONS_ROOT"
	LegacyAssetRootPlaceholder         = "CLAUDE_PLUGIN_ROOT"
)

func assetRoot(a AssetContext) string {
	if a.PluginRoot != "" {
		return a.PluginRoot
	}
	if a.LookupEnv != nil {
		for _, k := range []string{AssetRootPlaceholder, LegacyAssetRootPlaceholder} {
			if v, ok := a.LookupEnv(k); ok && !isUnresolvedPluginRoot(v) {
				return v
			}
		}
	}
	return "."
}

var agentIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func validateAgents(raw map[string]json.RawMessage, schema int) error {
	fail := func() error { return &Error{Code: ConfigInvalid} }
	count := 0
	for key, value := range raw {
		if !strings.EqualFold(key, "agents") {
			continue
		}
		count++
		if schema != 2 || count > 1 {
			return fail()
		}
		var agents map[string]json.RawMessage
		if json.Unmarshal(value, &agents) != nil || agents == nil {
			return fail()
		}
		for id, profile := range agents {
			if !agentIDPattern.MatchString(id) || containsNull(profile) {
				return fail()
			}
			var fields map[string]json.RawMessage
			if json.Unmarshal(profile, &fields) != nil || fields == nil {
				return fail()
			}
			for field := range fields {
				switch strings.ToLower(field) {
				case "notifications", "statuses", "debug":
				default:
					return fail()
				}
			}
			var c Config
			if decodeTyped(profile, &c) != nil || ambiguousFields(profile, reflect.TypeOf(c), "") != "" {
				return fail()
			}
		}
	}
	return nil
}

func containsNull(data []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if tok == nil {
			return true
		}
	}
}

// canonicalObject matches encoding/json's known-field casing before merging.
// Dynamic dictionary and status names remain case-sensitive and untouched.
func canonicalObject(data []byte, typ reflect.Type) map[string]json.RawMessage {
	var raw map[string]json.RawMessage
	_ = json.Unmarshal(data, &raw)
	return canonicalFields(raw, typ)
}

func canonicalFields(raw map[string]json.RawMessage, typ reflect.Type) map[string]json.RawMessage {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct && typ.Kind() != reflect.Map {
		return raw
	}
	out := make(map[string]json.RawMessage, len(raw))
	for key, value := range raw {
		name := key
		var child reflect.Type
		if typ.Kind() == reflect.Map {
			child = typ.Elem()
		} else {
			for i := 0; i < typ.NumField(); i++ {
				f := typ.Field(i)
				n := strings.Split(f.Tag.Get("json"), ",")[0]
				if strings.EqualFold(key, n) {
					name, child = n, f.Type
					break
				}
			}
		}
		if typ.Kind() == reflect.Struct && child == nil {
			continue
		}
		if child != nil && len(bytes.TrimSpace(value)) > 0 && bytes.TrimSpace(value)[0] == '{' {
			value, _ = json.Marshal(canonicalObject(value, child))
		}
		out[name] = value
	}
	return out
}

func mergeObjects(base, override map[string]json.RawMessage, path string) {
	for key, value := range override {
		pointer := path + "/" + key
		replace := pointer == "/notifications/webhook/headers" || pointer == "/notifications/webhook/payloadFields"
		var right, left map[string]json.RawMessage
		if !replace && json.Unmarshal(value, &right) == nil && right != nil {
			_ = json.Unmarshal(base[key], &left)
			if left == nil {
				left = map[string]json.RawMessage{}
			}
			mergeObjects(left, right, pointer)
			value, _ = json.Marshal(left)
		}
		base[key] = value
	}
}

// preparedProfiles owns only recognized runtime fields. Raw document metadata
// remains solely in Document for storage; it is never cloned for each profile.
type preparedProfiles struct {
	global map[string]json.RawMessage
	agents map[AgentID]map[string]json.RawMessage
}

func (d Document) prepareProfiles() preparedProfiles {
	raw := d.Raw() // Parse the document once, independent of the number of agents.
	typ := reflect.TypeOf(Config{})
	defaults, _ := json.Marshal(buildDefaultConfig("${AGENT_NOTIFICATIONS_ROOT}"))
	p := preparedProfiles{global: canonicalObject(defaults, typ), agents: map[AgentID]map[string]json.RawMessage{}}
	mergeObjects(p.global, canonicalFields(raw, typ), "")
	for key, value := range raw {
		if !strings.EqualFold(key, "agents") {
			continue
		}
		var agents map[string]json.RawMessage
		_ = json.Unmarshal(value, &agents)
		for id, body := range agents {
			p.agents[AgentID(id)] = canonicalObject(body, typ)
		}
	}
	return p
}

func (p preparedProfiles) mergedProfile(agent AgentID) ([]byte, error) {
	// RawMessage values are immutable. mergeObjects decodes objects into fresh
	// maps and replaces values, so copying this small outer map isolates a view.
	base := make(map[string]json.RawMessage, len(p.global))
	for key, value := range p.global {
		base[key] = value
	}
	mergeObjects(base, p.agents[agent], "")
	return json.Marshal(base)
}

func (p preparedProfiles) validationAgents() []AgentID {
	ids := make([]AgentID, 0, len(p.agents)+1)
	ids = append(ids, "")
	for id := range p.agents {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
