package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

func TestInstallAdapterSelectsSuppliedNativeOverOldExecutable(t *testing.T) {
	for _, candidate := range []string{"missing", "unattested", "wrong-attestation"} {
		t.Run(candidate, func(t *testing.T) {
			if candidate != "missing" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
				t.Skip("native bundle promotion requires a supported native platform")
			}
			root := t.TempDir()
			stage, target, control := filepath.Join(root, "stage"), filepath.Join(root, "bin"), filepath.Join(root, "control")
			write := func(path, content string) {
				t.Helper()
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(content), 0700); err != nil {
					t.Fatal(err)
				}
			}
			sentinel := filepath.Join(root, "executed")
			t.Setenv("ADAPTER_NATIVE_SPY", sentinel)
			old := filepath.Join(target, "terminal-notifier.app", "Contents", "MacOS", "terminal-notifier")
			spy := "#!/bin/sh\nprintf executed >> \"$ADAPTER_NATIVE_SPY\"\n"
			write(old, spy)
			entry := "claude-notifications-darwin-amd64"
			write(filepath.Join(stage, entry), "inert current sender "+installruntime.WriterProtocolMarker)
			if candidate != "missing" {
				modern := filepath.Join(stage, "ClaudeNotifier.app")
				write(filepath.Join(modern, "Contents", "MacOS", "terminal-notifier-modern"), spy+"# supplied newer package\n")
				write(filepath.Join(modern, "Contents", "Resources", "managed-runtime.json"), `{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1}`)
				if candidate == "wrong-attestation" {
					write(modern+".managed-runtime.json", `{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1,"ExecutableSHA256":"bad"}`)
				}
			}
			err := installRuntime([]string{"--stage", stage, "--target", target, "--entry", entry, "--control-root", control}, io.Discard)
			want := "required compatible native release"
			if candidate == "wrong-attestation" {
				want = "native offline executable fingerprint mismatch"
			}
			if err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("selected package error = %v, want %q", err, want)
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatalf("unknown/unverified native executed: %v", err)
			}
			got, err := os.ReadFile(old)
			if err != nil || string(got) != spy {
				t.Fatal("old concrete helper changed")
			}
			if _, err := os.Stat(filepath.Join(target, entry)); !os.IsNotExist(err) {
				t.Fatal("sender promoted before required native verification")
			}
			if _, err := os.Stat(filepath.Join(control, "ownership.json")); !os.IsNotExist(err) {
				t.Fatal("ownership mutated before required verification")
			}
		})
	}
}

func TestInstallAdapterOldHelperSelectsQualifiedSuppliedRelease(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native bundle promotion requires a supported native platform")
	}
	root := t.TempDir()
	stage, target, control := filepath.Join(root, "stage"), filepath.Join(root, "bin"), filepath.Join(root, "control")
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0700); err != nil {
			t.Fatal(err)
		}
	}
	sentinel := filepath.Join(root, "executed")
	t.Setenv("ADAPTER_NATIVE_SPY", sentinel)
	old := filepath.Join(target, "terminal-notifier.app", "Contents", "MacOS", "terminal-notifier")
	oldBytes := []byte("#!/bin/sh\nprintf old >> \"$ADAPTER_NATIVE_SPY\"\n")
	write(old, oldBytes)
	modern := filepath.Join(stage, "ClaudeNotifier.app")
	payload := []byte("#!/bin/sh\nprintf newer >> \"$ADAPTER_NATIVE_SPY\"\n")
	write(filepath.Join(modern, "Contents", "MacOS", "terminal-notifier-modern"), payload)
	sealed := []byte(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1}`)
	write(filepath.Join(modern, "Contents", "Resources", "managed-runtime.json"), sealed)
	evidence := []byte(fmt.Sprintf(`{"SchemaVersion":1,"ProtocolVersion":1,"DecoderFloor":1,"ExecutableSHA256":"%x"}`, sha256.Sum256(payload)))
	write(modern+".managed-runtime.json", evidence)
	entry := "claude-notifications-darwin-amd64"
	write(filepath.Join(stage, entry), []byte("inert new sender "+installruntime.WriterProtocolMarker))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// Model a qualification completed by another managed consumer, as the kernel
	// fixtures do. Only this prior OS qualification is synthetic: selection,
	// identity matching, staging, ownership, aliases and Commit below are real.
	qualified, err := installruntime.StageRetainedNative(ctx, control, modern)
	if err != nil {
		t.Fatal(err)
	}
	qualified.After.DecoderFloor = 1
	qualified.After.Attestation = evidence
	binding := sha256.Sum256(append([]byte(fmt.Sprintf("managed-native-v1:%s:%d:", qualified.After.SHA256, len(evidence))), evidence...))
	qualified.After.InstalledTreeSHA256 = fmt.Sprintf("%x", binding)
	_, err = installruntime.Commit(ctx, installruntime.Request{ControlRoot: control, Owner: "existing-installer", RuntimeRoot: filepath.Dir(target), ConsumerID: "qualified-fixture", Native: qualified})
	if err != nil {
		t.Fatal(err)
	}
	// An executable old helper cannot suppress selection of the supplied current
	// release. Exact previously qualified bytes are retained without any probe.
	if err := installRuntime([]string{"--stage", stage, "--target", target, "--entry", entry, "--control-root", control}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("old or cached native was executed")
	}
	got, err := os.ReadFile(old)
	if err != nil || string(got) != string(oldBytes) {
		t.Fatal("old concrete callback overwritten")
	}
	snap, err := installruntime.ReadInstalledSnapshot(control)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Ledger.Native == nil || snap.Ledger.Native.DecoderFloor != 1 || string(snap.Ledger.Native.Attestation) != string(evidence) {
		t.Fatal("qualified native evidence not retained")
	}
	if _, ok := snap.Ledger.Consumers["claude-hooks"]; !ok {
		t.Fatal("actual adapter did not commit consumer")
	}
	if err := os.RemoveAll(stage); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(target, "ClaudeNotifier.app", "Contents", "MacOS", "terminal-notifier-modern")
	got, err = os.ReadFile(installed)
	if err != nil || string(got) != string(payload) {
		t.Fatalf("selected newer helper not persistent after source removal: %q %v", got, err)
	}
}
