package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func geminiTestArgs(t *testing.T) []string {
	return []string{"--event", "AfterAgent", "--control-root", filepath.Join(t.TempDir(), "control"), "--binding", "test-binding"}
}

func TestGeminiEventTransportNeutralResponse(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		panic         bool
		wantCall      bool
	}{
		{"bounded", `{}`, false, true},
		{"empty", "", false, false},
		{"limit", strings.Repeat("x", geminiPayloadLimit), false, true},
		{"oversized", strings.Repeat("x", geminiPayloadLimit+1), false, false},
		{"panic", `{}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			called := false
			code := runGeminiEventWith(context.Background(), geminiTestArgs(t), io.NopCloser(strings.NewReader(tc.payload)), &output,
				func(ctx context.Context, args geminiEventArgs, data []byte) {
					called = true
					if args.Event != "AfterAgent" || args.Binding != "test-binding" || string(data) != tc.payload {
						t.Error("composition received changed selector, binding or payload")
					}
					if tc.panic {
						panic("private SDK diagnostic must not escape")
					}
				})
			if code != 0 || output.String() != "{}\n" || called != tc.wantCall {
				t.Fatalf("native response code=%d output=%q called=%v", code, output.String(), called)
			}
		})
	}
}

func TestGeminiEventTransportRejectsArgumentsBeforeReading(t *testing.T) {
	base := geminiTestArgs(t)
	for _, args := range [][]string{
		nil,
		{"--event", "Stop", "--control-root", base[3], "--binding", "test-binding"},
		{"--event", "AfterAgent", "--control-root", "relative", "--binding", "test-binding"},
		{"--event", "AfterAgent", "--control-root", base[3]},
		append(append([]string(nil), base...), "--event", "Notification"),
		append(append([]string(nil), base...), "--unknown", "value"),
		append(append([]string(nil), base...), "extra"),
	} {
		var output bytes.Buffer
		if code := runGeminiEventWith(context.Background(), args, panicGeminiInput{}, &output,
			func(context.Context, geminiEventArgs, []byte) { t.Error("invalid argv reached consumer") }); code != 0 || output.String() != "{}\n" {
			t.Fatalf("invalid argv response: %v code=%d output=%q", args, code, output.String())
		}
	}
}

type panicGeminiInput struct{}

func (panicGeminiInput) Read([]byte) (int, error) { panic("input should not be read") }
func (panicGeminiInput) Close() error             { return nil }

// A never-closed parent pipe used to hold hook processes indefinitely. This is
// an actual child process with its own stdin and no agent/provider execution.
func TestGeminiEventTransportBlockedStdinChild(t *testing.T) {
	if os.Getenv("AN_GEMINI_STDIN_CHILD") == "1" {
		args := []string{"--event", "Notification", "--control-root", os.Getenv("AN_GEMINI_TEST_ROOT"), "--binding", "test-binding"}
		os.Exit(runGeminiEventWith(context.Background(), args, os.Stdin, os.Stdout,
			func(context.Context, geminiEventArgs, []byte) { panic("blocked input reached composition") }))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGeminiEventTransportBlockedStdinChild$")
	cmd.Env = append(os.Environ(), "AN_GEMINI_STDIN_CHILD=1", "AN_GEMINI_TEST_ROOT="+t.TempDir())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close() }()
	var output, diagnostics bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &diagnostics
	started := time.Now()
	if err := cmd.Run(); err != nil {
		t.Fatalf("blocked stdin child failed: %v output=%q diagnostics=%q", err, output.String(), diagnostics.String())
	}
	if elapsed := time.Since(started); elapsed < 900*time.Millisecond || elapsed >= 2500*time.Millisecond {
		t.Fatalf("one-second stdin deadline was not respected: %s", elapsed)
	}
	if output.String() != "{}\n" || diagnostics.Len() != 0 {
		t.Fatalf("non-neutral child response: output=%q diagnostics=%q", output.String(), diagnostics.String())
	}
}

func TestGeminiEventTransportTotalBudget(t *testing.T) {
	var output bytes.Buffer
	release := make(chan struct{})
	defer close(release)
	started := time.Now()
	code := runGeminiEventWith(context.Background(), geminiTestArgs(t), io.NopCloser(strings.NewReader(`{}`)), &output,
		func(context.Context, geminiEventArgs, []byte) { <-release })
	if elapsed := time.Since(started); elapsed < 3900*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("total budget was not four seconds: %s", elapsed)
	}
	if code != 0 || output.String() != "{}\n" {
		t.Fatalf("deadline response: code=%d output=%q", code, output.String())
	}
}
