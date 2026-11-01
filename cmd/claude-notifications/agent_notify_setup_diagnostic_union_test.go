package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Breakage: setup invents a hybrid diagnostic, admits untyped JSON, or changes
// the current SnapshotDiagnostic wire consumed by existing status failures.
func TestSetupDiagnosticTypedTransportUnion(t *testing.T) {
	snapshot := installruntime.SnapshotDiagnostic{Code: "managed_file_changed", Path: "TEST-path", Action: "TEST-action"}
	admission := installruntime.AdmissionDiagnostic{Phase: "config_lock_admission", Role: "global_config", Classification: "foreign_mutation_ace"}
	for _, tc := range []struct {
		name string
		want agentNotifySetupDiagnostic
		json string
	}{
		{"snapshot", agentNotifySetupDiagnostic{Snapshot: &snapshot}, `{"code":"managed_file_changed","path":"TEST-path","action":"TEST-action"}`},
		{"admission", agentNotifySetupDiagnostic{Admission: &admission}, `{"phase":"config_lock_admission","role":"global_config","classification":"foreign_mutation_ace"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := json.Marshal(tc.want)
			if err != nil || string(data) != tc.json {
				t.Fatalf("wire changed: %s %v", data, err)
			}
			var got agentNotifySetupDiagnostic
			if err := json.Unmarshal(data, &got); err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("roundtrip changed: %+v %v", got, err)
			}
		})
	}
	for _, malformed := range []string{
		`null`, `{}`, `[]`, `{"phase":"x","code":"y","action":"z"}`,
		`{"phase":"x","unknown":true}`, `{"code":"x","action":"y","unknown":true}`,
		`{"phase":"x","phase":"y"}`,
	} {
		var got agentNotifySetupDiagnostic
		if err := json.Unmarshal([]byte(malformed), &got); err == nil {
			t.Fatalf("ambiguous transport admitted: %s -> %+v %v", malformed, got, err)
		}
	}
}
