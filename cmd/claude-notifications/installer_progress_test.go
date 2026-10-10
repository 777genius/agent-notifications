package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallerProgressCapabilityHasNoTerminalEffects(t *testing.T) {
	errout, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer errout.Close()
	var stdout bytes.Buffer
	if code := installerProgressMain([]string{"--capabilities"}, &stdout, errout); code != 0 {
		t.Fatalf("capability exit %d", code)
	}
	if stdout.String() != "installer-progress-v1\n" {
		t.Fatalf("capability stdout %q", stdout.String())
	}
	info, err := errout.Stat()
	if err != nil || info.Size() != 0 {
		t.Fatal("capability touched terminal")
	}
}

func TestInstallerProgressNonTerminalFallbackKeepsStdoutClean(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events")
	if err := os.WriteFile(path, []byte("codex\tconfig\trunning\ncodex\tcomplete\tcompleted\nstop\t\t\n"), 0600); err != nil {
		t.Fatal(err)
	}
	errpath := filepath.Join(t.TempDir(), "stderr")
	errout, err := os.Create(errpath)
	if err != nil {
		t.Fatal(err)
	}
	defer errout.Close()
	var stdout bytes.Buffer
	if code := installerProgressMain([]string{"--events", path, "--ui", "rich"}, &stdout, errout); code != 0 {
		t.Fatalf("observer exit %d", code)
	}
	out, err := os.ReadFile(errpath)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.Len() != 0 || strings.Contains(string(out), "\x1b") || string(out) != "Codex CLI: configuration - running\nCodex CLI: complete - done\n" {
		t.Fatalf("fallback stdout=%q stderr=%q", stdout.String(), out)
	}
}
