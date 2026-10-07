package installruntime

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// os.Exit bypasses every defer, including lock release and staging cleanup.
// The parent must recover only from kernel lock release and durable evidence.
func TestCrashProcessRecovery(t *testing.T) {
	skipUnsupportedNative(t)
	if root := os.Getenv("PR2_CRASH_ROOT"); root != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sender := filepath.Join(root, "bin", "sender")
		config := filepath.Join(root, "config", "hooks.json")
		source := filepath.Join(root, "source", "ClaudeNotifier.app")
		if err := os.MkdirAll(source, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "old-decoder"), []byte("inert legacy fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		r := Request{ControlRoot: filepath.Join(root, "control"), RuntimeRoot: filepath.Join(root, "bin"), Owner: "existing-installer", ConsumerID: "codex", ConfigPaths: []string{config}}
		var err error
		r.Native, err = StageNative(ctx, r.ControlRoot, source)
		if err != nil {
			t.Fatal(err)
		}
		r.Files = []File{{Path: sender, Data: []byte("sender"), Mode: 0755}, {Path: config, Data: []byte(`{"owned":true}`), Mode: 0600}}
		r.Fault = func(phase string) error {
			boundary := os.Getenv("PR2_CRASH_BOUNDARY")
			if phase == boundary || boundary == "sender" && phase == "promotion:"+sender || boundary == "config" && phase == "promotion:"+config {
				os.Exit(73)
			}
			return nil
		}
		if _, err := Commit(ctx, r); err != nil {
			t.Fatal(err)
		}
		t.Fatal("crash boundary not reached")
	}
	for _, boundary := range []string{"transaction", "native", "sender", "config", "ledger"} {
		t.Run(boundary, func(t *testing.T) {
			root := t.TempDir()
			child := exec.Command(os.Args[0], "-test.run=^TestCrashProcessRecovery$")
			child.Env = append(os.Environ(), "PR2_CRASH_ROOT="+root, "PR2_CRASH_BOUNDARY="+boundary)
			output, err := child.CombinedOutput()
			status, ok := err.(*exec.ExitError)
			if !ok || status.ExitCode() != 73 {
				t.Fatalf("child did not crash as requested: %v %s", err, output)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r := Request{ControlRoot: filepath.Join(root, "control"), RuntimeRoot: filepath.Join(root, "bin"), Owner: "existing-installer", ConsumerID: "codex"}
			l, err := Commit(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			if l.Native == nil {
				t.Fatal("lost callback owner")
			}
			got, err := treeFingerprint(l.Native.Path)
			if err != nil || got != l.Native.SHA256 {
				t.Fatal("callback unavailable after recovery")
			}
			for path, want := range map[string]string{filepath.Join(root, "bin", "sender"): "sender", filepath.Join(root, "config", "hooks.json"): `{"owned":true}`} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != want {
					t.Fatalf("recovery %s: %v", path, err)
				}
			}
			if _, err := Commit(ctx, r); err != nil {
				t.Fatalf("repeat recovery: %v", err)
			}
		})
	}
}
