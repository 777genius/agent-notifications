package pisource

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func testFrame() map[string]any {
	return map[string]any{"schema_version": 1, "event": "agent_settled", "observer_id": "170ff6e4-aa22-4cad-a5e8-2e5b7415b6bc", "observation_id": "416eb070-d117-4ddb-bd2e-a40fca7821b4", "session_id": "native-session", "cwd": filepath.Clean(tPath()), "mode": "tui", "pi_pid": 123,
		"loaded_revision": map[string]any{"projection_digest": "sha256:" + strings.Repeat("1", 64), "canonical_digest": "sha256:" + strings.Repeat("2", 64), "bootstrap_version": 1, "binding_id": "binding", "runtime_root": filepath.Clean(tPath()), "primary_sha256": strings.Repeat("3", 64)},
		"settled":         map[string]any{"aborted": false, "boundary_outcome": "completed"}}
}
func tPath() string { p, _ := filepath.Abs("."); return p }
func decodeFrame(t *testing.T, frame map[string]any) (Facts, error) {
	t.Helper()
	b, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	return Decode(context.Background(), b)
}
func TestTypedPiFactsRequireFinalOutcome(t *testing.T) {
	for _, tc := range []struct {
		outcome string
		aborted bool
		want    Kind
	}{{"completed", false, Completed}, {"error", false, Failed}, {"completed", true, Aborted}, {"", true, Aborted}, {"", false, Unclassified}, {"aborted", false, Unclassified}} {
		t.Run(string(tc.want)+tc.outcome, func(t *testing.T) {
			frame := testFrame()
			settled := map[string]any{"aborted": tc.aborted}
			if tc.outcome != "" {
				settled["boundary_outcome"] = tc.outcome
			}
			frame["settled"] = settled
			facts, err := decodeFrame(t, frame)
			if err != nil || facts.Kind != tc.want || !facts.Valid() {
				t.Fatalf("facts=%+v err=%v", facts, err)
			}
		})
	}
}
func TestPiPromptIsQuestionWithoutPrivateContent(t *testing.T) {
	frame := testFrame()
	frame["event"] = "ui_prompt_start"
	delete(frame, "settled")
	frame["prompt"] = map[string]any{"kind": "confirm"}
	facts, err := decodeFrame(t, frame)
	if err != nil || facts.Kind != Question {
		t.Fatalf("%+v %v", facts, err)
	}
	for _, key := range []string{"title", "text", "tool_input", "transcript"} {
		frame[key] = "PRIVATE_SENTINEL"
		_, err = decodeFrame(t, frame)
		if err == nil {
			t.Fatalf("admitted private field %s", key)
		}
		delete(frame, key)
	}
}
func TestPiRejectsUnboundedOrAmbiguousFrames(t *testing.T) {
	frame := testFrame()
	valid, _ := json.Marshal(frame)
	for _, raw := range [][]byte{append(append([]byte{}, valid...), []byte(` {}`)...), []byte(`{"schema_version":1,"schema_version":1}`), []byte(strings.Repeat(" ", MaxPayloadBytes+1)), []byte(`null`)} {
		if _, err := Decode(context.Background(), raw); err == nil {
			t.Fatal("invalid frame accepted")
		}
	}
	for _, mutation := range []func(map[string]any){func(f map[string]any) { f["event"] = "agent_end" }, func(f map[string]any) { f["mode"] = "unknown" }, func(f map[string]any) { f["cwd"] = "relative" }, func(f map[string]any) { f["observer_id"] = "invented-native-turn" }, func(f map[string]any) { f["pi_pid"] = 0 }, func(f map[string]any) { f["prompt"] = map[string]any{"kind": "input"} }} {
		f := testFrame()
		mutation(f)
		if _, err := decodeFrame(t, f); err == nil {
			t.Fatalf("invalid shape accepted: %+v", f)
		}
	}
}
