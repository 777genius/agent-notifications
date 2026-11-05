package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Real Windows native argument E2E: both shell engines invoke a compiled Go
// executable, rather than merely comparing shell-escaped presentation text.
func TestPowerShellLegacyAndModernRetryJSONArgv(t *testing.T) {
	compiler, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "TEST argv space's $literal")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "probe.go")
	probe := filepath.Join(root, "argv-probe.exe")
	if err := os.WriteFile(source, []byte("package main\nimport (\"encoding/json\"; \"os\")\nfunc main() { _ = json.NewEncoder(os.Stdout).Encode(os.Args[1:]) }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	build := exec.Command(compiler, "build", "-o", probe, source)
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile native probe: %s: %v", output, err)
	}
	argv := []string{
		probe, "--local-mcp-names", `["agent-notifications","custom-name"]`,
		"--local-skill-names", `["agent-notifications"]`,
		"--scope-root", `C:\\TEST folder\$literal's`, "--yes",
	}
	script := strings.Join(quotePowerShellArgs(argv), " ")
	for _, shell := range []string{"powershell.exe", "pwsh.exe"} {
		t.Run(shell, func(t *testing.T) {
			binary, err := exec.LookPath(shell)
			if err != nil {
				t.Fatalf("real Windows PowerShell engine not available: %v", err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			run := exec.CommandContext(ctx, binary, "-NoProfile", "-NonInteractive", "-Command", script)
			output, err := run.CombinedOutput()
			if err != nil {
				t.Fatalf("real %s native command failed: %v, output=%q", shell, err, output)
			}
			var got []string
			if err := json.Unmarshal(output, &got); err != nil {
				t.Fatalf("%s corrupted native JSON output: %v, %q", shell, err, output)
			}
			if !reflect.DeepEqual(got, argv[1:]) {
				t.Fatalf("%s native argv changed: got=%#v, want=%#v", shell, got, argv[1:])
			}
		})
	}
}
