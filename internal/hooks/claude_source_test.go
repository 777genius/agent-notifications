package hooks

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// neverEOFReader yields one complete JSON value and then blocks forever on
// the next Read; a decoder that waits for EOF would hang here.
type neverEOFReader struct {
	data []byte
	pos  int
}

func (r *neverEOFReader) Read(p []byte) (int, error) {
	if r.pos < len(r.data) {
		n := copy(p, r.data[r.pos:])
		r.pos += n
		return n, nil
	}
	select {} // block: EOF never arrives
}

func TestClaudeSourceDecodeAllFields(t *testing.T) {
	payload := `{"session_id":"sess-1","transcript_path":"/t.jsonl","cwd":"/proj","tool_name":"ExitPlanMode","hook_event_name":"PreToolUse","team_name":"alpha","teammate_name":"bob"}`
	ev, err := ClaudeSource{}.Decode(context.Background(), "PreToolUse", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if ev.Product != ProductClaude {
		t.Fatalf("Product = %q", ev.Product)
	}
	if ev.Session.SessionID != "sess-1" || ev.Session.CWD != "/proj" || ev.Session.TranscriptPath != "/t.jsonl" {
		t.Fatalf("Session = %+v", ev.Session)
	}
	if ev.PayloadEventName != "PreToolUse" {
		t.Fatalf("PayloadEventName = %q", ev.PayloadEventName)
	}
	p, ok := ev.Payload.(PreToolUsePayload)
	if !ok || p.ToolName != "ExitPlanMode" {
		t.Fatalf("Payload = %#v", ev.Payload)
	}
	if len(ev.Raw) == 0 || !strings.Contains(string(ev.Raw), "sess-1") {
		t.Fatalf("Raw not retained: %s", ev.Raw)
	}
}

func TestClaudeSourceDecodeTeammateIdle(t *testing.T) {
	payload := `{"session_id":"s","team_name":"alpha","teammate_name":"bob"}`
	ev, err := ClaudeSource{}.Decode(context.Background(), "TeammateIdle", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	p, ok := ev.Payload.(TeammateIdlePayload)
	if !ok || p.TeamName != "alpha" || p.TeammateName != "bob" {
		t.Fatalf("Payload = %#v", ev.Payload)
	}
}

func TestClaudeSourceDecodeBOMAndTrailingData(t *testing.T) {
	payload := "\xEF\xBB\xBF" + `{"session_id":"s"}` + "trailing garbage"
	ev, err := ClaudeSource{}.Decode(context.Background(), "Stop", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("Decode() with BOM+trailing error = %v", err)
	}
	if _, ok := ev.Payload.(StopPayload); !ok {
		t.Fatalf("Payload = %#v", ev.Payload)
	}
}

func TestClaudeSourceDecodeWithoutEOF(t *testing.T) {
	done := make(chan struct{})
	var ev Event
	var err error
	go func() {
		defer close(done)
		ev, err = ClaudeSource{}.Decode(context.Background(), "Stop", &neverEOFReader{data: []byte(`{"session_id":"s"}`)})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Decode() waited for EOF")
	}
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if ev.Session.SessionID != "s" {
		t.Fatalf("SessionID = %q", ev.Session.SessionID)
	}
}

func TestClaudeSourceDefaultsEmptySession(t *testing.T) {
	ev, err := ClaudeSource{}.Decode(context.Background(), "Stop", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if ev.Session.SessionID != "unknown" {
		t.Fatalf("SessionID = %q, want unknown", ev.Session.SessionID)
	}
}

func TestClaudeSourceUnknownEventKeepsNilPayload(t *testing.T) {
	ev, err := ClaudeSource{}.Decode(context.Background(), "Bogus", strings.NewReader(`{"session_id":"s"}`))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if ev.Payload != nil {
		t.Fatalf("Payload = %#v, want nil for unknown event", ev.Payload)
	}
}

func TestClaudeSourceMalformedInput(t *testing.T) {
	_, err := ClaudeSource{}.Decode(context.Background(), "Stop", strings.NewReader(`{oops`))
	if err == nil || !strings.Contains(err.Error(), "failed to parse hook data") {
		t.Fatalf("Decode() error = %v, want parse failure", err)
	}
}

var _ io.Reader = (*neverEOFReader)(nil)
