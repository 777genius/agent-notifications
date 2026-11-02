package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/777genius/agent-notifications/internal/geminievent"
)

func TestGeminiEventDiagnosticNeutralStages(t *testing.T) {
	invalidControl := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(invalidControl, []byte("TEST"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, flag, payload, phase string }{
		{"default-silent", "", "PRIVATE invalid input", ""},
		{"decode", "1", "PRIVATE invalid input", "decode"},
		{"snapshot", "1", `{"hook_event_name":"AfterAgent","session_id":"PRIVATE-session"}`, "snapshot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AGENT_NOTIFICATIONS_OBSERVATION_DIAGNOSTICS", tc.flag)
			reader, file, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			defer reader.Close()
			captured := make(chan []byte, 1)
			go func() { data, _ := io.ReadAll(reader); captured <- data }()
			before := os.Stderr
			os.Stderr = file
			var output bytes.Buffer
			code := func() int {
				defer func() { os.Stderr = before }()
				return runGeminiEvent([]string{"--event", "AfterAgent", "--control-root", invalidControl, "--binding", "PRIVATE-binding"}, io.NopCloser(strings.NewReader(tc.payload)), &output)
			}()
			_ = file.Close()
			data := <-captured
			if code != 0 || output.String() != "{}\n" {
				t.Fatalf("nonneutral code=%d stdout=%q", code, output.String())
			}
			want := ""
			if tc.phase != "" && runtime.GOOS != "windows" {
				want = "gemini.event.result phase=" + tc.phase + " class=unavailable status=none reason=none desktop=none webhook=none\n"
			}
			if string(data) != want {
				t.Fatalf("diagnostic=%q want=%q", data, want)
			}
		})
	}
}

func TestGeminiEventDiagnosticPanicAndResultPrivacy(t *testing.T) {
	var diagnostics, stdout bytes.Buffer
	d := &geminiEventDiagnostic{enabled: true, output: &diagnostics, phase: "decode", class: "unavailable"}
	code := runGeminiEventWith(context.Background(), geminiTestArgs(t), io.NopCloser(strings.NewReader(`{}`)), &stdout,
		func(context.Context, geminiEventArgs, []byte) {
			defer d.recoverPanic()
			panic("PRIVATE-path-token-error")
		})
	d.emit()
	if code != 0 || stdout.String() != "{}\n" || diagnostics.String() != "gemini.event.result phase=decode class=panic status=none reason=none desktop=none webhook=none\n" {
		t.Fatalf("panic output=%q diagnostic=%q code=%d", stdout.String(), diagnostics.String(), code)
	}
	diagnostics.Reset()
	d.enter("consume")
	d.consumed(geminievent.Receipt{Status: "suppressed", Reason: "cache_unavailable", Webhook: "cache_unavailable", Desktop: "PRIVATE-path"})
	d.emit()
	if diagnostics.String() != "gemini.event.result phase=consume class=completed status=suppressed reason=cache_unavailable desktop=none webhook=cache_unavailable\n" {
		t.Fatal(diagnostics.String())
	}
}

func TestGeminiEventDiagnosticDeadlineSnapshot(t *testing.T) {
	var output bytes.Buffer
	d := &geminiEventDiagnostic{enabled: true, output: &output, phase: "snapshot", class: "unavailable"}
	d.expired(context.DeadlineExceeded)
	d.emit()
	if output.String() != "gemini.event.result phase=snapshot class=deadline status=none reason=none desktop=none webhook=none\n" {
		t.Fatal(output.String())
	}
	// A timed-out transport can snapshot while its callback finishes. Only the
	// outer owner emits, and a later callback cannot race its captured labels.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			d.enter("consume")
			d.consumed(geminievent.Receipt{Status: "submitted", Webhook: "submitted"})
		}
	}()
	for i := 0; i < 100; i++ {
		d.emit()
	}
	wg.Wait()
}
