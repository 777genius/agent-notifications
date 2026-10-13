package main

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/geminisource"
)

// Red if the SDK is bypassed, a nonapplicable frame becomes eligible, or Facts
// leak through the public diagnostic protocol. No consumer/effect is executed.
func TestSDKClassificationAndPrivateProjection(t *testing.T) {
	raw := `{"hook_event_name":"AfterAgent","session_id":"TEST-private-session","timestamp":"2026-10-02T06:45:01Z","stop_hook_active":false,"prompt":"TEST-private-prompt","cwd":"TEST-private-cwd"}`
	r := classify(context.Background(), geminisource.AfterAgent, strings.NewReader(raw))
	if r.Classification != "decoded" || !r.Eligible || !r.TimestampPresent || len(r.SessionSHA256) != 64 || len(r.TimestampSHA256) != 64 {
		t.Fatalf("unsupported protocol: %+v", r)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"TEST-private-session", "TEST-private-prompt", "TEST-private-cwd", "2026-10-02T06:45:01Z"} {
		if strings.Contains(string(b), private) {
			t.Fatal("private frame leaked")
		}
	}
	stopped := classify(context.Background(), geminisource.AfterAgent, strings.NewReader(strings.Replace(raw, `"stop_hook_active":false`, `"stop_hook_active":true`, 1)))
	if stopped.Classification != "decoded" || stopped.Eligible {
		t.Fatal("nested Stop was eligible")
	}
	invalid := classify(context.Background(), geminisource.Notification, strings.NewReader(raw))
	if invalid.Classification != "invalid" || invalid.Eligible || invalid.SessionSHA256 != "" || invalid.TimestampSHA256 != "" {
		t.Fatal("mismatched selector bypassed SDK or leaked facts")
	}
	permission := `{"hook_event_name":"Notification","session_id":"TEST-private-session","timestamp":"2026-10-02T06:45:01Z","notification_type":"ToolPermission","message":"TEST-private-message"}`
	for _, value := range []struct {
		frame    string
		eligible bool
	}{{permission, true}, {strings.Replace(permission, "ToolPermission", "TEST-private-subtype", 1), false}} {
		got := classify(context.Background(), geminisource.Notification, strings.NewReader(value.frame))
		if got.Classification != "decoded" || got.Eligible != value.eligible {
			t.Fatal("permission applicability differs", got)
		}
		projected, _ := json.Marshal(got)
		if strings.Contains(string(projected), "TEST-private") {
			t.Fatal("permission frame leaked")
		}
	}

}

type countedReader struct {
	reader io.Reader
	bytes  int
}

func (r *countedReader) Read(p []byte) (int, error) {
	n, e := r.reader.Read(p)
	r.bytes += n
	return n, e
}

// Red if untrusted stdin can force reads beyond the payload cap, or a fresh
// decoder context silently replaces the orchestrator's canceled context.
func TestBoundedReadsAndCanceledDecode(t *testing.T) {
	input := &countedReader{reader: strings.NewReader(strings.Repeat("x", geminisource.MaxPayloadBytes+1024))}
	r := classify(context.Background(), geminisource.AfterAgent, input)
	if r.Classification != "invalid" || input.bytes != geminisource.MaxPayloadBytes+1 {
		t.Fatal("unbounded payload read", input.bytes)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	raw := `{"hook_event_name":"AfterAgent","session_id":"TEST-session","timestamp":"2026-10-02T06:45:01Z","stop_hook_active":false}`
	r = classify(ctx, geminisource.AfterAgent, strings.NewReader(raw))
	if r.Classification != "invalid" || r.Eligible || r.SessionSHA256 != "" {
		t.Fatal("canceled frame accepted or leaked")
	}
}
