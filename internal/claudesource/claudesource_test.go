package claudesource

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/hooks"
	pluginkitai "github.com/777genius/plugin-kit-ai/sdk"
)

type neverEOFReader struct {
	data []byte
	pos  int
}

func (r *neverEOFReader) Read(payload []byte) (int, error) {
	if r.pos < len(r.data) {
		read := copy(payload, r.data[r.pos:])
		r.pos += read
		return read, nil
	}
	select {}
}

func TestDecodeAllFiveEventsThroughTypedCallbacks(t *testing.T) {
	tests := []struct {
		name    string
		event   string
		payload string
		assert  func(*testing.T, hooks.Event)
	}{
		{
			name:    "PreToolUse",
			event:   "PreToolUse",
			payload: `{"session_id":"session-1","cwd":"/work","transcript_path":"/transcript","hook_event_name":"PreToolUse","tool_name":"ExitPlanMode"}`,
			assert: func(t *testing.T, event hooks.Event) {
				payload, ok := event.Payload.(hooks.PreToolUsePayload)
				if !ok || payload.ToolName != "ExitPlanMode" {
					t.Fatalf("payload = %#v", event.Payload)
				}
			},
		},
		{
			name:    "Notification",
			event:   "Notification",
			payload: `{"session_id":"session-2","cwd":"/work","transcript_path":"/transcript","hook_event_name":"Notification"}`,
			assert: func(t *testing.T, event hooks.Event) {
				if _, ok := event.Payload.(hooks.NotificationPayload); !ok {
					t.Fatalf("payload = %#v", event.Payload)
				}
			},
		},
		{
			name:    "Stop",
			event:   "Stop",
			payload: `{"session_id":"session-3","cwd":"/work","transcript_path":"/transcript","hook_event_name":"Stop","last_assistant_message":"finished"}`,
			assert: func(t *testing.T, event hooks.Event) {
				payload, ok := event.Payload.(hooks.StopPayload)
				if !ok || payload.AssistantMessage != "finished" {
					t.Fatalf("payload = %#v", event.Payload)
				}
			},
		},
		{
			name:    "SubagentStop",
			event:   "SubagentStop",
			payload: `{"session_id":"session-4","cwd":"/work","transcript_path":"/transcript","hook_event_name":"SubagentStop","last_assistant_message":"subagent finished"}`,
			assert: func(t *testing.T, event hooks.Event) {
				payload, ok := event.Payload.(hooks.SubagentStopPayload)
				if !ok || payload.Stop.AssistantMessage != "subagent finished" {
					t.Fatalf("payload = %#v", event.Payload)
				}
			},
		},
		{
			name:    "TeammateIdle",
			event:   "TeammateIdle",
			payload: `{"session_id":"session-5","cwd":"/work","transcript_path":"/transcript","hook_event_name":"TeammateIdle","team_name":"alpha","teammate_name":"bob"}`,
			assert: func(t *testing.T, event hooks.Event) {
				payload, ok := event.Payload.(hooks.TeammateIdlePayload)
				if !ok || payload.TeamName != "alpha" || payload.TeammateName != "bob" {
					t.Fatalf("payload = %#v", event.Payload)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event, sdkIO, err := decodeWithIO(context.Background(), test.event, strings.NewReader(test.payload))
			if err != nil {
				t.Fatalf("decodeWithIO() error = %v", err)
			}
			if sdkIO == nil || sdkIO.reads != 1 {
				t.Fatalf("SDK reads = %+v", sdkIO)
			}
			test.assert(t, event)
		})
	}
}

func TestDecodePreservesBOMFirstJSONAndTrailingBytes(t *testing.T) {
	firstJSON := `{"session_id":"s","hook_event_name":"Stop"}`
	payload := "\xEF\xBB\xBF" + firstJSON + `{"session_id":"ignored"}` + "trailing garbage"
	event, _, err := decodeWithIO(context.Background(), "Stop", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("decodeWithIO() error = %v", err)
	}
	if string(event.Raw) != firstJSON {
		t.Fatalf("Raw = %q, want %q", event.Raw, firstJSON)
	}
	if event.Session.SessionID != "s" {
		t.Fatalf("SessionID = %q", event.Session.SessionID)
	}
}

func TestDecodeFirstJSONDoesNotWaitForEOF(t *testing.T) {
	done := make(chan struct{})
	var event hooks.Event
	var decodeErr error
	go func() {
		defer close(done)
		event, _, decodeErr = decodeWithIO(
			context.Background(),
			"Stop",
			&neverEOFReader{data: []byte(`{"session_id":"s","hook_event_name":"Stop"}`)},
		)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("decodeWithIO() waited for EOF")
	}
	if decodeErr != nil {
		t.Fatalf("decodeWithIO() error = %v", decodeErr)
	}
	if event.Session.SessionID != "s" {
		t.Fatalf("SessionID = %q", event.Session.SessionID)
	}
}

func TestDecodeMalformedFirstJSONFails(t *testing.T) {
	_, _, err := decodeWithIO(context.Background(), "Stop", strings.NewReader(`{oops`))
	if err == nil || !strings.Contains(err.Error(), "failed to parse hook data") {
		t.Fatalf("error = %v, want parse failure", err)
	}
}

func TestDecodeEmptyToolNameRestoresLegacyValue(t *testing.T) {
	payload := `{"session_id":"s","hook_event_name":"PreToolUse","tool_name":""}`
	event, sdkIO, err := decodeWithIO(context.Background(), "PreToolUse", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("decodeWithIO() error = %v", err)
	}
	preToolUse, ok := event.Payload.(hooks.PreToolUsePayload)
	if !ok || preToolUse.ToolName != "" {
		t.Fatalf("payload = %#v", event.Payload)
	}

	var projected map[string]any
	if err := json.Unmarshal(sdkIO.payload, &projected); err != nil {
		t.Fatalf("projected payload error = %v", err)
	}
	if projected["tool_name"] != sdkPlaceholder {
		t.Fatalf("projected tool_name = %#v, want placeholder", projected["tool_name"])
	}
}

func TestDecodeUnknownEventStaysCaseSensitiveWithoutSDKDispatch(t *testing.T) {
	payload := `{"session_id":"","hook_event_name":"Stop"}`
	event, sdkIO, err := decodeWithIO(context.Background(), "stop", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("decodeWithIO() error = %v", err)
	}
	if sdkIO != nil {
		t.Fatal("unknown event dispatched through SDK")
	}
	if event.Payload != nil || event.PayloadEventName != "Stop" {
		t.Fatalf("event = %+v, want unknown payload and raw event name", event)
	}
	if event.Session.SessionID != "unknown" {
		t.Fatalf("SessionID = %q, want unknown", event.Session.SessionID)
	}
}

func TestDecodeOversizedStringHasNoProductCap(t *testing.T) {
	message := strings.Repeat("x", pluginkitai.MaxPayloadBytes+1)
	raw, err := json.Marshal(map[string]string{
		"session_id":             "large-session",
		"cwd":                    "/work",
		"transcript_path":        "/transcript",
		"hook_event_name":        "Stop",
		"last_assistant_message": message,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	event, sdkIO, err := decodeWithIO(context.Background(), "Stop", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("decodeWithIO() error = %v", err)
	}
	if string(event.Raw) != string(raw) {
		t.Fatal("Raw was not preserved exactly")
	}
	stop, ok := event.Payload.(hooks.StopPayload)
	if !ok || stop.AssistantMessage != message {
		t.Fatalf("payload = %#v", event.Payload)
	}
	if len(sdkIO.payload) > pluginkitai.MaxPayloadBytes {
		t.Fatalf("SDK payload = %d bytes, want <= %d", len(sdkIO.payload), pluginkitai.MaxPayloadBytes)
	}
}

func TestSubagentStopOverlaysLegacyFinalMessage(t *testing.T) {
	payload := `{"session_id":"s","cwd":"/work","transcript_path":"/subagent","hook_event_name":"SubagentStop","last_assistant_message":"legacy final"}`
	event, _, err := decodeWithIO(context.Background(), "SubagentStop", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("decodeWithIO() error = %v", err)
	}
	subagentStop, ok := event.Payload.(hooks.SubagentStopPayload)
	if !ok || subagentStop.Stop.AssistantMessage != "legacy final" {
		t.Fatalf("payload = %#v", event.Payload)
	}
}

func TestDecodeCapturesSDKOutputWithoutProcessLeakage(t *testing.T) {
	var sdkIO *bufferedIO
	processStdout, processStderr := captureProcessOutput(t, func() {
		var err error
		_, sdkIO, err = decodeWithIO(context.Background(), "Stop", strings.NewReader(`{}`))
		if err != nil {
			t.Fatalf("decodeWithIO() error = %v", err)
		}
	})
	if len(processStdout) != 0 || len(processStderr) != 0 {
		t.Fatalf("process output leaked: stdout=%q stderr=%q", processStdout, processStderr)
	}
	if sdkIO == nil || sdkIO.reads != 1 {
		t.Fatalf("SDK IO = %+v", sdkIO)
	}
}

func captureProcessOutput(t *testing.T, run func()) ([]byte, []byte) {
	t.Helper()
	stdoutPath := filepath.Join(t.TempDir(), "stdout")
	stderrPath := filepath.Join(t.TempDir(), "stderr")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatalf("create stdout file: %v", err)
	}
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		t.Fatalf("create stderr file: %v", err)
	}

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	os.Stdout = stdoutFile
	os.Stderr = stderrFile
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
	}()

	run()
	if err := stdoutFile.Sync(); err != nil {
		t.Fatalf("sync stdout: %v", err)
	}
	if err := stderrFile.Sync(); err != nil {
		t.Fatalf("sync stderr: %v", err)
	}
	stdout, err := os.ReadFile(stdoutPath)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stderr, err := os.ReadFile(stderrPath)
	if err != nil {
		t.Fatalf("read stderr: %v", err)
	}
	return stdout, stderr
}

var (
	_ io.Reader = (*neverEOFReader)(nil)
	_           = hooks.Event{}
)
