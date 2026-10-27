package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func bootstrapProgressFixture(t *testing.T) (string, string) {
	t.Helper()
	stage, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(stage, "parallel")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "progress.events")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	privateBootstrapProgressFixture(t, stage, dir, path)
	return filepath.Join(stage, "selector-intent.json"), path
}

func TestBootstrapProgressOnlyAppendsFiniteActivityToOwnedJournal(t *testing.T) {
	intent, path := bootstrapProgressFixture(t)
	write, closeProgress, err := openBootstrapProgress(intent, path)
	if err != nil {
		t.Fatal(err)
	}
	defer closeProgress()
	write("claude", "verification", "running")
	write("codex", "verification", "done")
	write("claude", "MCP", "activation_timeout")
	// Raw provider diagnostics cannot manufacture client completion or mutate
	// readiness through this observational channel.
	write("claude", "complete", "completed")
	write("claude\nSECRET", "verification", "running")
	closeProgress()
	write("codex", "verification", "running")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "transition\tclaude\tverification\trunning\ntransition\tcodex\tverification\tdone\ntransition\tclaude\tMCP\tactivation_timeout\n"
	if string(got) != want {
		t.Fatalf("untrusted or post-close activity accepted: %q", got)
	}
	for _, invalid := range []string{intent, filepath.Join(filepath.Dir(intent), "other.events"), path + ".other"} {
		if _, close, err := openBootstrapProgress(intent, invalid); err == nil {
			close()
			t.Fatalf("accepted non-coordinator output: %s", invalid)
		}
	}
}

func TestBootstrapProgressFlagRequiresOneAbsoluteDestination(t *testing.T) {
	for _, args := range [][]string{
		{"--bootstrap-progress-file"}, {"--bootstrap-progress-file="},
		{"--bootstrap-progress-file=relative"},
		{"--bootstrap-progress-file=/TEST/a", "--bootstrap-progress-file=/TEST/b"},
	} {
		if _, _, err := stripBootstrapProgressFile(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	intent, path := bootstrapProgressFixture(t)
	args, got, err := stripBootstrapProgressFile([]string{"--bootstrap-progress-file", path, "--bootstrap-intent-file", intent})
	if err != nil || got != path || strings.Join(args, "|") != "--bootstrap-intent-file|"+intent {
		t.Fatalf("parse: %v %q %v", args, got, err)
	}
}

func TestBootstrapProgressRefusesJournalReplacementBetweenAppends(t *testing.T) {
	for _, kind := range []string{"replacement", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			intent, path := bootstrapProgressFixture(t)
			write, closeProgress, err := openBootstrapProgress(intent, path)
			if err != nil {
				t.Fatal(err)
			}
			defer closeProgress()
			write("claude", "verification", "running")
			retained := path + ".retained"
			if err := os.Rename(path, retained); err != nil {
				t.Fatal(err)
			}
			wantReplacement := "untouched"
			if kind == "hardlink" {
				if err := os.Link(retained, path); err != nil {
					t.Fatal(err)
				}
				wantReplacement = "transition\tclaude\tverification\trunning\n"
			} else {
				if err := os.WriteFile(path, []byte(wantReplacement), 0600); err != nil {
					t.Fatal(err)
				}
			}
			privateBootstrapProgressFixture(t, filepath.Dir(intent), filepath.Dir(path), path)
			write("codex", "verification", "running")
			for file, want := range map[string]string{path: wantReplacement, retained: "transition\tclaude\tverification\trunning\n"} {
				got, err := os.ReadFile(file)
				if err != nil || string(got) != want {
					t.Fatalf("changed journal identity accepted: %s: %q %v", file, got, err)
				}
			}
		})
	}
}
