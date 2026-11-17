package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/777genius/agent-notifications/internal/geminievent"
)

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
