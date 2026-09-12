package main

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Windows plugin JSON remains an adapter concern. Replace only the exact
// shipped sh handler or our exact exec handler; retain every foreign field.
func prepareRuntimeHooks(path, exe string, remove bool) ([]installruntime.File, error) {
	before, err := installruntime.Fingerprint(path)
	if err != nil || !before.Exists {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root map[string]json.RawMessage
	if json.Unmarshal(data, &root) != nil || root == nil {
		return nil, fmt.Errorf("invalid Windows hooks object")
	}
	events := map[string]json.RawMessage{}
	if raw, ok := root["hooks"]; ok {
		if json.Unmarshal(raw, &events) != nil || events == nil {
			return nil, fmt.Errorf("invalid Windows hook events")
		}
	}
	for event, wanted := range newWindowsHookSettings(exe).Hooks {
		var groups []json.RawMessage
		if raw, ok := events[event]; ok && json.Unmarshal(raw, &groups) != nil {
			return nil, fmt.Errorf("invalid Windows hook groups")
		}
		next := []json.RawMessage{}
		found := false
		for _, raw := range groups {
			var group map[string]json.RawMessage
			if json.Unmarshal(raw, &group) != nil || group == nil {
				next = append(next, raw)
				continue
			}
			var commands []json.RawMessage
			if json.Unmarshal(group["hooks"], &commands) != nil {
				next = append(next, raw)
				continue
			}
			kept := []json.RawMessage{}
			changed := false
			for _, command := range commands {
				managed := sameHookJSON(command, newExecHook(exe, event))
				legacy := newExecHook("sh", event)
				legacy.Args = []string{"${CLAUDE_PLUGIN_ROOT}/bin/hook-wrapper.sh", "handle-hook", event}
				if !managed && (remove || !sameHookJSON(command, legacy)) {
					kept = append(kept, command)
					continue
				}
				changed = true
				if !remove && !found {
					encoded, _ := json.Marshal(newExecHook(exe, event))
					kept = append(kept, encoded)
					found = true
				}
			}
			if !changed {
				next = append(next, raw)
				continue
			}
			// Do not discard foreign annotations on an emptied matcher group.
			if len(kept) == 0 && len(group) <= 2 {
				_, matcher := group["matcher"]
				if len(group) == 1 || matcher {
					continue
				}
			}
			group["hooks"], _ = json.Marshal(kept)
			encoded, _ := json.Marshal(group)
			next = append(next, encoded)
		}
		if !remove && !found {
			encoded, _ := json.Marshal(wanted[0])
			next = append(next, encoded)
		}
		if _, exists := events[event]; exists || len(next) > 0 {
			events[event], _ = json.Marshal(next)
		}
	}
	root["hooks"], _ = json.Marshal(events)
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, err
	}
	return []installruntime.File{{Path: path, Before: before, Data: append(out, '\n'), Mode: 0600}}, nil
}

func sameHookJSON(data []byte, expected hookCommand) bool {
	var actual, want any
	if json.Unmarshal(data, &actual) != nil {
		return false
	}
	encoded, _ := json.Marshal(expected)
	_ = json.Unmarshal(encoded, &want)
	return reflect.DeepEqual(actual, want)
}
