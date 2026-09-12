package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/agentnotify"
	"github.com/777genius/agent-notifications/internal/agentnotify/origin"
	"github.com/777genius/agent-notifications/internal/notification"
)

type backendFunc func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt

func (f backendFunc) Notify(c context.Context, p agentnotify.Payload, o origin.Context, d notification.Deadline) agentnotify.Receipt {
	return f(c, p, o, d)
}

type input struct {
	io.Reader
	closes, reads int
}

func (i *input) Close() error               { i.closes++; return nil }
func (i *input) Read(b []byte) (int, error) { i.reads++; return i.Reader.Read(b) }
func clock() agentnotify.Clock {
	return agentnotify.ClockFunc(func() notification.Deadline { return notification.Deadline{BootID: "boot", NotAfter: 100} })
}

const good = `{"title":"--help $(x)","body":"private-body\n\t","category":"info","request_id":"same-id","navigation":"none"}`

func execute(t *testing.T, raw string, args []string, o Options) (int, agentnotify.Receipt, string) {
	t.Helper()
	in := &input{Reader: strings.NewReader(raw)}
	var out, diag bytes.Buffer
	n := Run(context.Background(), args, in, &out, &diag, o)
	if in.closes != 1 {
		t.Fatalf("closes=%d", in.closes)
	}
	var r agentnotify.Receipt
	if out.Len() > 0 && json.Unmarshal(out.Bytes(), &r) != nil {
		t.Fatalf("invalid receipt %q", out.String())
	}
	if out.Len() > 20<<10 || strings.Contains(out.String(), "private-body") || strings.Contains(diag.String(), "private-body") {
		t.Fatal("output leak/bound")
	}
	return n, r, diag.String()
}
func TestHelpAndFlags(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"--context-file", "missing", "--help"}} {
		in := &input{Reader: strings.NewReader(good)}
		var out bytes.Buffer
		closes := 0
		code := Run(nil, args, in, &out, io.Discard, Options{CloseResources: func() error { closes++; return nil }})
		if code != 0 || in.reads != 0 || in.closes != 1 || closes != 1 || !strings.Contains(out.String(), "Usage:") {
			t.Fatal(code, in, closes)
		}
	}
	for _, args := range [][]string{{"--evil-private-body"}, {"--help", "--help"}, {"--context-file"}, {"--context-file", "x", "--context-file", "y"}, {"literal"}, {"--context-file=x"}, {"--help", "--evil"}} {
		n, _, d := execute(t, good, args, Options{})
		if n != ExitInvalid || d != "invalid_flags\n" {
			t.Fatal(n, d)
		}
	}
}
func TestLiteralAndReceipt(t *testing.T) {
	calls := 0
	closes := 0
	b := backendFunc(func(_ context.Context, p agentnotify.Payload, o origin.Context, d notification.Deadline) agentnotify.Receipt {
		calls++
		if p.Title != "--help $(x)" || p.Body != "private-body\n\t" || *p.RequestID != "same-id" || o.Provenance != origin.CallerAsserted || o.SessionID != "" || o.AnonymousCaller != "local_cli" || d.NotAfter != 115 {
			t.Fatal(p, o, d)
		}
		return agentnotify.Receipt{Status: "submitted"}
	})
	n, r, _ := execute(t, good, nil, Options{Backend: b, Clock: clock(), CloseResources: func() error { closes++; return nil }})
	if n != 0 || calls != 1 || closes != 1 || *r.RequestID != "same-id" {
		t.Fatal(n, r, calls, closes)
	}
}
func TestInvalidJSONAndBounds(t *testing.T) {
	calls := 0
	o := Options{Clock: clock(), Backend: backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		calls++
		return agentnotify.Receipt{}
	})}
	for _, s := range []string{"", `{}`, `null`, good + good, strings.Replace(good, `"title":`, `"title":"a","\u0074itle":`, 1), strings.Replace(good, "--help $(x)", `\ud800`, 1), strings.Replace(good, "--help $(x)", string([]byte{255}), 1), strings.Replace(good, `"none"`, `null`, 1), strings.Replace(good, `"body":`, `"provenance":"client_metadata","body":`, 1), strings.Repeat(" ", RawLimit+1), strings.Replace(good, "private-body", strings.Repeat("a", DecodedLimit), 1)} {
		n, _, _ := execute(t, s, nil, o)
		if n != ExitInvalid {
			t.Fatalf("accepted invalid input, code %d", n)
		}
	}
	if calls != 0 {
		t.Fatal(calls)
	}
}
func TestContextAndMissingSession(t *testing.T) {
	calls := 0
	o := Options{Clock: clock(), Backend: backendFunc(func(_ context.Context, _ agentnotify.Payload, o origin.Context, _ notification.Deadline) agentnotify.Receipt {
		calls++
		if o.Provenance != origin.CallerAsserted || o.Namespace != "cli" {
			t.Fatal(o)
		}
		return agentnotify.Receipt{Status: "suppressed"}
	})}
	n, r, _ := execute(t, strings.Replace(good, `"none"`, `"required"`, 1), nil, o)
	if n != ExitRejected || r.Reason != "session_required" || calls != 0 {
		t.Fatal(n, r)
	}
	for _, s := range []string{`{"provider":"codex","provenance":"client_metadata"}`, `{"provider":"codex","trusted":true}`, `{"provider":"codex","session":"a","session":"b"}`, `{"provider":"codex","session":"\udc00"}`, `{"provider":"codex","locality":"bogus"}`, strings.Repeat(" ", ContextLimit+1)} {
		o.ReadContext = func(context.Context, string) ([]byte, error) { return []byte(s), nil }
		n, r, _ := execute(t, good, []string{"--context-file", "x"}, o)
		if n != ExitRejected || r.Reason != "invalid_context" || calls != 0 {
			t.Fatal(n, r)
		}
	}
	o.ReadContext = func(context.Context, string) ([]byte, error) {
		return []byte(`{"provider":"codex","session":"explicit","locality":"local","interface":"desktop"}`), nil
	}
	n, _, _ = execute(t, strings.Replace(good, `"none"`, `"required"`, 1), []string{"--context-file", "x"}, o)
	if n != 0 || calls != 1 {
		t.Fatal(n, calls)
	}
}
func TestOutcomesNeverRetry(t *testing.T) {
	for _, status := range []string{"submitted", "suppressed", "rejected", "unknown", "private-body"} {
		calls := 0
		n, r, _ := execute(t, good, nil, Options{Clock: clock(), Backend: backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
			calls++
			return agentnotify.Receipt{Status: status, RetrySafe: true}
		})})
		want := ExitUnknown
		if status == "submitted" || status == "suppressed" {
			want = ExitSuccess
		}
		if status == "rejected" {
			want = ExitRejected
		}
		if n != want || calls != 1 || (n == ExitUnknown && r.RetrySafe) || *r.RequestID != "same-id" {
			t.Fatal(n, r, calls)
		}
	}
	n, r, _ := execute(t, good, nil, Options{Clock: clock(), Backend: backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		panic("private-body")
	})})
	if n != ExitUnknown || r.RetrySafe {
		t.Fatal(n, r)
	}
}

type pipeInput struct {
	*io.PipeReader
	closes atomic.Int32
}

func (p *pipeInput) Close() error { p.closes.Add(1); return p.PipeReader.Close() }
func TestCancellationJoinsReader(t *testing.T) {
	for _, partial := range []string{"", `{"body":"unfinished`} {
		r, w := io.Pipe()
		in := &pipeInput{PipeReader: r}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan int, 1)
		go func() {
			done <- Run(ctx, nil, in, io.Discard, io.Discard, Options{Clock: clock(), Backend: backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
				panic("called")
			})})
		}()
		if partial != "" {
			if _, e := io.WriteString(w, partial); e != nil {
				t.Fatal(e)
			}
		}
		cancel()
		select {
		case n := <-done:
			if n != ExitInvalid || in.closes.Load() != 1 {
				t.Fatal(n, in.closes.Load())
			}
		case <-time.After(time.Second):
			t.Fatal("reader did not join")
		}
		w.Close()
	}
}
func TestOriginalDeadlineAndContinuousCancellation(t *testing.T) {
	var now atomic.Int64
	now.Store(100)
	calls := 0
	o := Options{Clock: agentnotify.ClockFunc(func() notification.Deadline {
		return notification.Deadline{BootID: "boot", NotAfter: float64(now.Load())}
	})}
	o.ReadContext = func(context.Context, string) ([]byte, error) {
		now.Store(110)
		return []byte(`{"provider":"codex","session":"s"}`), nil
	}
	o.Backend = backendFunc(func(c context.Context, _ agentnotify.Payload, _ origin.Context, d notification.Deadline) agentnotify.Receipt {
		calls++
		if d.NotAfter != 115 {
			t.Fatal(d)
		}
		now.Store(116)
		select {
		case <-c.Done():
		case <-time.After(time.Second):
			t.Fatal("no continuous cancellation")
		}
		return agentnotify.Receipt{Status: "unknown", RetrySafe: true}
	})
	n, r, _ := execute(t, good, []string{"--context-file", "x"}, o)
	if n != ExitUnknown || r.RetrySafe || calls != 1 {
		t.Fatal(n, r, calls)
	}
	now.Store(100)
	o.ReadContext = func(context.Context, string) ([]byte, error) {
		now.Store(116)
		return []byte(`{"provider":"codex"}`), nil
	}
	n, r, _ = execute(t, good, []string{"--context-file", "x"}, o)
	if n != ExitRejected || r.Reason != "deadline_expired" || calls != 1 {
		t.Fatal(n, r, calls)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("private-body") }
func TestIOErrorsAndCleanup(t *testing.T) {
	in := &input{Reader: errReader{}}
	var out, diag bytes.Buffer
	closed := 0
	n := Run(context.Background(), nil, in, &out, &diag, Options{Clock: clock(), Backend: backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		panic("called")
	}), CloseResources: func() error { closed++; return errors.New("secret") }})
	if n != ExitInvalid || closed != 1 || in.closes != 1 || diag.String() != "input_failed\nresource_close_failed\n" {
		t.Fatal(n, closed, in, diag.String())
	}
}

func TestRawExactBoundAndEscapedLiteral(t *testing.T) {
	calls := 0
	o := Options{Clock: clock(), Backend: backendFunc(func(_ context.Context, p agentnotify.Payload, _ origin.Context, _ notification.Deadline) agentnotify.Receipt {
		calls++
		if p.Title != "--help $(x)" {
			t.Fatal(p.Title)
		}
		return agentnotify.Receipt{Status: "submitted"}
	})}
	raw := strings.Replace(good, "--help", `\u002d\u002dhelp`, 1)
	raw += strings.Repeat(" ", RawLimit-len(raw))
	n, _, _ := execute(t, raw, nil, o)
	if n != ExitSuccess || calls != 1 {
		t.Fatal(n, calls)
	}
	n, _, _ = execute(t, raw+" ", nil, o)
	if n != ExitInvalid || calls != 1 {
		t.Fatal(n, calls)
	}
}

type closeFuncInput struct {
	io.Reader
	close func() error
}

func (i closeFuncInput) Close() error { return i.close() }
func TestCaptureBeforeInputCleanupAndPreserveID(t *testing.T) {
	var now atomic.Int64
	now.Store(100)
	var out bytes.Buffer
	in := closeFuncInput{Reader: strings.NewReader(good), close: func() error { now.Store(110); return nil }}
	n := Run(context.Background(), nil, in, &out, io.Discard, Options{Clock: agentnotify.ClockFunc(func() notification.Deadline {
		return notification.Deadline{BootID: "boot", NotAfter: float64(now.Load())}
	}), Backend: backendFunc(func(_ context.Context, p agentnotify.Payload, _ origin.Context, d notification.Deadline) agentnotify.Receipt {
		if d.NotAfter != 115 {
			t.Fatal(d)
		}
		*p.RequestID = strings.Repeat("x", RawLimit)
		return agentnotify.Receipt{Status: "unknown", Reason: strings.Repeat("x", RawLimit), RetrySafe: true}
	})})
	var r agentnotify.Receipt
	if json.Unmarshal(out.Bytes(), &r) != nil || n != ExitUnknown || r.RetrySafe || *r.RequestID != "same-id" || r.Reason != "invalid_backend_result" {
		t.Fatal(n, r)
	}
}
func TestInvalidClockAndContextCancellation(t *testing.T) {
	for _, sample := range []notification.Deadline{{}, {BootID: "boot", NotAfter: -1}} {
		o := Options{Clock: agentnotify.ClockFunc(func() notification.Deadline { return sample }), Backend: backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
			t.Fatal("backend called")
			return agentnotify.Receipt{}
		})}
		n, r, _ := execute(t, good, nil, o)
		if n != ExitRejected || r.Reason != "deadline_expired" || *r.RequestID != "same-id" {
			t.Fatal(n, r)
		}
	}
	var now atomic.Int64
	now.Store(100)
	o := Options{Clock: agentnotify.ClockFunc(func() notification.Deadline {
		return notification.Deadline{BootID: "boot", NotAfter: float64(now.Load())}
	}), Backend: backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		t.Fatal("backend called")
		return agentnotify.Receipt{}
	}), ReadContext: func(c context.Context, _ string) ([]byte, error) {
		now.Store(116)
		select {
		case <-c.Done():
			return nil, c.Err()
		case <-time.After(time.Second):
			t.Fatal("context not canceled")
			return nil, errors.New("timeout")
		}
	}}
	n, r, _ := execute(t, good, []string{"--context-file", "explicit"}, o)
	if n != ExitRejected || r.Reason != "context_unavailable" {
		t.Fatal(n, r)
	}
}

type badWriter struct{}

func (badWriter) Write([]byte) (int, error) { return 0, errors.New("private-body") }
func TestFailedReceiptWrite(t *testing.T) {
	calls := 0
	var diag bytes.Buffer
	n := Run(context.Background(), nil, io.NopCloser(strings.NewReader(good)), badWriter{}, &diag, Options{Clock: clock(), Backend: backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		calls++
		return agentnotify.Receipt{Status: "submitted"}
	})})
	if n != ExitUnknown || calls != 1 || diag.String() != "output_failed\n" {
		t.Fatal(n, calls, diag.String())
	}
}

func TestContextPathIsLiteral(t *testing.T) {
	calls := 0
	n, _, _ := execute(t, good, []string{"--context-file", "--help"}, Options{Clock: clock(), ReadContext: func(_ context.Context, path string) ([]byte, error) {
		if path != "--help" {
			t.Fatal(path)
		}
		return []byte(`{"provider":"codex"}`), nil
	}, Backend: backendFunc(func(context.Context, agentnotify.Payload, origin.Context, notification.Deadline) agentnotify.Receipt {
		calls++
		return agentnotify.Receipt{Status: "submitted"}
	})})
	if n != ExitSuccess || calls != 1 {
		t.Fatal(n, calls)
	}
}
