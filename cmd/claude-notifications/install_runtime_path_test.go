package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/installruntime"
)

// Removing the flag, printing the default status, or returning an alias/stage
// instead of the committed Native.Path makes this contract test fail.
func TestInstallRuntimePrintNativePath(t *testing.T) {
	for _, native := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-native", true: "durable-native"}[native], func(t *testing.T) {
			if native && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
				t.Skip("native staging is Unix-only")
			}
			root := t.TempDir()
			stage := filepath.Join(root, "stage")
			target := filepath.Join(root, "runtime", "bin")
			control := filepath.Join(root, "control")
			if err := os.MkdirAll(stage, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stage, "claude-notifications-linux-amd64"), []byte("inert "+installruntime.WriterProtocolMarker), 0700); err != nil {
				t.Fatal(err)
			}
			if native {
				app := filepath.Join(stage, "ClaudeNotifier.app", "Contents", "MacOS")
				if err := os.MkdirAll(app, 0700); err != nil {
					t.Fatal(err)
				}
				// Unknown native packages are copied and hashed, never executed.
				if err := os.WriteFile(filepath.Join(app, "terminal-notifier-modern"), []byte("inert native"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			args := []string{"--stage", stage, "--target", target, "--control-root", control, "--print-native-path"}
			if err := installRuntime(args, &output); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(control, "ownership.json"))
			if err != nil {
				t.Fatal(err)
			}
			var ledger installruntime.Ledger
			if err = json.Unmarshal(raw, &ledger); err != nil {
				t.Fatal(err)
			}
			want := ""
			if ledger.Native != nil {
				want = ledger.Native.Path
			}
			if output.String() != want+"\n" {
				t.Fatalf("stdout=%q want %q", output.String(), want+"\n")
			}
			if native {
				if want == "" || strings.Contains(want, ".candidate-") || strings.HasPrefix(want, stage) {
					t.Fatalf("not published: %s", want)
				}
				if _, err := os.Stat(want); err != nil {
					t.Fatal(err)
				}
			}
			output.Reset()
			if err := installRuntime(args[:len(args)-1], &output); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(output.String(), "managed-runtime committed generation=") {
				t.Fatalf("default output changed: %q", output.String())
			}
			output.Reset()
			if err := installRuntime([]string{"--remove", "--purge-native", "--target", target, "--control-root", control, "--print-native-path"}, &output); err != nil {
				t.Fatal(err)
			}
			if output.String() != "\n" {
				t.Fatalf("purge stdout=%q", output.String())
			}

		})
	}
}
