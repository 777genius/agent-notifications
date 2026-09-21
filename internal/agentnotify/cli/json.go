package cli

import (
	"encoding/json"
	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
	"github.com/777genius/agent-notifications/internal/strictjson"
)

func stringsObject(raw []byte, limit int, allowed map[string]bool) (map[string]string, bool) {
	if strictjson.Validate(raw, strictjson.Budget{Bytes: limit, Depth: 2, Entries: 8}) != nil {
		return nil, false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil, false
	}
	result := make(map[string]string, len(fields))
	total := 0
	for k, v := range fields {
		if !allowed[k] || len(v) == 0 || v[0] != '"' {
			return nil, false
		}
		var s string
		if json.Unmarshal(v, &s) != nil {
			return nil, false
		}
		total += len(k) + len(s)
		if total > DecodedLimit {
			return nil, false
		}
		result[k] = s
	}
	return result, true
}
func payload(raw []byte) (agentnotify.Payload, bool) {
	f, ok := stringsObject(raw, RawLimit, map[string]bool{"title": true, "body": true, "category": true, "request_id": true, "navigation": true})
	p := agentnotify.Payload{Title: f["title"], Body: f["body"], Category: f["category"], Navigation: notification.Required}
	for _, k := range []string{"title", "body", "category"} {
		if _, exists := f[k]; !exists {
			ok = false
		}
	}
	if id, exists := f["request_id"]; exists {
		if !origin.Text(id, 256, true) {
			ok = false
		}
		p.RequestID = &id
	}
	if n, exists := f["navigation"]; exists {
		p.Navigation = notification.Navigation(n)
	}
	if p.Navigation != notification.Required && p.Navigation != notification.None && p.Navigation != notification.BestEffort {
		ok = false
	}
	if p.Category != "info" && p.Category != "attention" && p.Category != "progress" {
		ok = false
	}
	return p, ok
}
func envelope(raw []byte) (origin.Context, bool) {
	f, ok := stringsObject(raw, ContextLimit, map[string]bool{"provider": true, "session": true, "locality": true, "interface": true})
	o := origin.Context{Provider: f["provider"], Namespace: "cli", SessionID: f["session"], AnonymousCaller: "local_cli", Provenance: origin.CallerAsserted, Locality: origin.LocalityUnknown, Interface: origin.InterfaceUnknown}
	if s, exists := f["locality"]; exists {
		o.Locality = origin.Locality(s)
	}
	if s, exists := f["interface"]; exists {
		o.Interface = origin.Interface(s)
	}
	return o, ok && o.Validate(notification.None) == nil
}
