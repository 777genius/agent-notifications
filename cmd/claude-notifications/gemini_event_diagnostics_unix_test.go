//go:build linux || darwin

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/geminihooks"
)

func geminiDiagnosticTestSocket(t *testing.T) (string, *net.UnixConn) {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "TEST-gd-")
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: filepath.Join(root, ".gd.sock"), Net: "unixgram"})
	if err != nil {
		_ = os.RemoveAll(directory)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = receiver.Close(); _ = os.RemoveAll(root) })
	return root, receiver
}

// This child enters the real production command, not a replacement observer.
// The ordinary test invocation has no positional separator and performs no IO.
func TestGeminiEventDiagnosticGuardChild(t *testing.T) {
	for i, arg := range os.Args {
		if arg == "--" {
			os.Exit(runGeminiEvent(os.Args[i+1:], os.Stdin, os.Stdout))
		}
	}
}

func TestGeminiEventDiagnosticPinnedQuietWrapper(t *testing.T) {
	for _, tc := range []struct{ name, flag, payload, phase string }{
		{"default-silent", "", `{"hook_event_name":"AfterAgent","session_id":"PRIVATE-session","timestamp":"PRIVATE-invalid-time"}`, ""},
		{"decode", "1", `{"hook_event_name":"AfterAgent","session_id":"PRIVATE-session","timestamp":"PRIVATE-invalid-time"}`, "decode"},
		{"snapshot", "1", `{"hook_event_name":"AfterAgent","session_id":"PRIVATE-session","timestamp":"2026-11-02T15:00:00Z"}`, "snapshot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, receiver := geminiDiagnosticTestSocket(t)
			if tc.phase == "snapshot" {
				if err := os.WriteFile(filepath.Join(root, "ownership.json"), []byte("PRIVATE invalid ledger"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			plan, err := geminihooks.Plan(geminihooks.Request{Shell: geminihooks.Bash, Operation: geminihooks.Install, Hooks: []geminihooks.HookSpec{{Event: "AfterAgent", Name: "TEST-diagnostic", Observer: true, Timeout: 5000, Argv: []string{os.Args[0], "-test.run=TestGeminiEventDiagnosticGuardChild", "--", "--event", "AfterAgent", "--control-root", root, "--binding", "PRIVATE-binding"}}}})
			if err != nil {
				t.Fatal(err)
			}
			var settings struct {
				Hooks map[string][]struct{ Hooks []struct{ Command string } }
			}
			if err := json.Unmarshal(plan.Desired, &settings); err != nil {
				t.Fatal(err)
			}
			command := settings.Hooks["AfterAgent"][0].Hooks[0].Command
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, "/bin/bash", "-c", command)
			child.Env = []string{"PATH=/usr/bin:/bin", "AGENT_NOTIFICATIONS_OBSERVATION_DIAGNOSTICS=" + tc.flag}
			child.Stdin = strings.NewReader(tc.payload)
			var stdout, stderr bytes.Buffer
			child.Stdout, child.Stderr = &stdout, &stderr
			if err := child.Run(); err != nil || stdout.String() != "{}\n" || stderr.Len() != 0 {
				t.Fatalf("pinned wrapper: err=%v stdout=%q stderr=%q", err, stdout.String(), stderr.String())
			}
			_ = receiver.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			data := make([]byte, 513)
			n, _, err := receiver.ReadFromUnix(data)
			if tc.phase == "" {
				if err == nil {
					t.Fatal("default path emitted diagnostics")
				}
				return
			}
			if err != nil || n > 512 {
				t.Fatalf("real wrapper diagnostic missing/bounded: %v n=%d", err, n)
			}
			var frame struct {
				Version   int    `json:"version"`
				Event     string `json:"event"`
				Session   string `json:"session_sha256"`
				Timestamp string `json:"timestamp_sha256"`
				Result    string `json:"result"`
			}
			if err := json.Unmarshal(data[:n], &frame); err != nil {
				t.Fatal(err)
			}
			session := sha256.Sum256([]byte("PRIVATE-session"))
			var input struct {
				Timestamp string `json:"timestamp"`
			}
			if err := json.Unmarshal([]byte(tc.payload), &input); err != nil {
				t.Fatal(err)
			}
			timestamp := sha256.Sum256([]byte(input.Timestamp))
			want := "gemini.event.result phase=" + tc.phase + " class=unavailable status=none reason=none desktop=none webhook=none\n"
			if frame.Version != 1 || frame.Event != "AfterAgent" || frame.Session != hex.EncodeToString(session[:]) || frame.Timestamp != hex.EncodeToString(timestamp[:]) || frame.Result != want || bytes.Contains(data[:n], []byte("PRIVATE")) {
				t.Fatalf("invalid/private diagnostic frame: %q", data[:n])
			}
		})
	}
}

func TestGeminiEventDiagnosticBlockedDatagramRetainsAdmissionDeadline(t *testing.T) {
	root, receiver := geminiDiagnosticTestSocket(t)
	path := filepath.Join(root, ".gd.sock")
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	// A timed-out send can mean sender-buffer exhaustion, or a deadline
	// that elapsed before its first syscall. Fill without timers and require
	// backpressure on the FIRST send of a fresh empty sender. Retain every
	// sender so the verified queue precondition survives until writer.Write.
	total, receiverFull := 0, false
	for sender := 0; sender < 32 && total < 16384 && !receiverFull; sender++ {
		filler, dialErr := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: path, Net: "unixgram"})
		if dialErr != nil {
			t.Fatal(dialErr)
		}
		t.Cleanup(func() { _ = filler.Close() })
		raw, rawErr := filler.SyscallConn()
		if rawErr != nil {
			t.Fatal(rawErr)
		}
		for sent := 0; total < 16384; sent++ {
			var n int
			var sendErr error
			controlErr := raw.Control(func(fd uintptr) {
				for {
					n, sendErr = syscall.SendmsgN(int(fd), []byte{0}, nil, nil, syscall.MSG_DONTWAIT)
					if sendErr != syscall.EINTR {
						break
					}
				}
			})
			if controlErr != nil {
				t.Fatal(controlErr)
			}
			if sendErr == syscall.EAGAIN || sendErr == syscall.EWOULDBLOCK {
				receiverFull = sent == 0 && total > 0
				break
			}
			if sendErr != nil || n != 1 {
				t.Fatalf("TEST queue fill failed: bytes=%d error=%v", n, sendErr)
			}
			total++
		}
	}
	if !receiverFull {
		t.Fatalf("TEST receiver backpressure unproved within bounds: writes=%d", total)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	writer := &geminiDiagnosticOutput{ctx: ctx}
	writer.input(root, "AfterAgent", []byte(`{"hook_event_name":"AfterAgent","session_id":"PRIVATE-session","timestamp":"2026-11-02T15:00:00Z"}`))
	started := time.Now()
	_, err = writer.Write([]byte("TEST diagnostic"))
	if err == nil || time.Since(started) > 500*time.Millisecond {
		t.Fatalf("diagnostic exceeded admission: %v %v", time.Since(started), err)
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("borrowed receiver changed: %v", err)
	}
	if err := receiver.SetReadDeadline(time.Now()); err != nil {
		t.Fatalf("caller receiver closed: %v", err)
	}
	cancel()
	started = time.Now()
	_, err = writer.Write([]byte("TEST diagnostic"))
	if err == nil || time.Since(started) > 100*time.Millisecond {
		t.Fatalf("canceled diagnostic waited: %v", err)
	}
}
