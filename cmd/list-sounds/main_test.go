package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"github.com/777genius/agent-notifications/internal/sounds"
)

func buildBinary(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	binary := tmpDir + "/list-sounds"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}

	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build binary: %v\n%s", err, out)
	}

	return binary
}

func TestMainDefaultOutput(t *testing.T) {
	binary := buildBinary(t)

	cmd := exec.Command(binary)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("binary exited with error: %v\n%s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "task-complete") {
		t.Errorf("expected output to contain 'task-complete', got:\n%s", output)
	}
	if !strings.Contains(output, "Built-in sounds:") {
		t.Errorf("expected output to contain 'Built-in sounds:', got:\n%s", output)
	}
}

func TestMainJSONOutput(t *testing.T) {
	binary := buildBinary(t)

	cmd := exec.Command(binary, "--json")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("binary exited with error: %v\n%s", err, out)
	}

	var result []sounds.SoundInfo
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}

	if len(result) < 4 {
		t.Errorf("expected at least 4 sounds in JSON output, got %d", len(result))
	}
}

func TestMainPlayFlag(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("skipping audio playback test in CI")
	}

	binary := buildBinary(t)

	cmd := exec.Command(binary, "--play", "task-complete", "--volume", "0.1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("binary exited with error: %v\n%s", err, out)
	}

	output := string(out)
	if !strings.Contains(output, "Playback completed") {
		t.Errorf("expected 'Playback completed' in output, got:\n%s", output)
	}
}

func TestMainPlayNotFound(t *testing.T) {
	binary := buildBinary(t)

	cmd := exec.Command(binary, "--play", "nonexistent-sound-xyz")
	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatal("expected non-zero exit code for nonexistent sound")
	}

	output := string(out)
	if !strings.Contains(output, "not found") {
		t.Errorf("expected 'not found' in error output, got:\n%s", output)
	}
}
