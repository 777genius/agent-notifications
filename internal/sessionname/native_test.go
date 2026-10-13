package sessionname

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const nativeTestID = "73b5e210-ec1a-4294-96e4-c2aecb2e1063"

func writeNativeMetadata(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func indexEntry(t *testing.T, id string, title any) string {
	t.Helper()
	line, err := json.Marshal(map[string]any{"id": id, "thread_name": title, "updated_at": "2026-10-03T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	return string(line) + "\n"
}

// Regression: a cached or forward-scanned index can keep an old name after
// /rename, resurrect a cleared name, or attach another thread's name.
func TestCodexNativeTitleUsesLatestExactRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	index := filepath.Join(home, "session_index.jsonl")
	writeNativeMetadata(t, index, indexEntry(t, nativeTestID, "Old name"))
	if got := NativeTitle("codex", nativeTestID, ""); got != "Old name" {
		t.Fatalf("initial title = %q", got)
	}
	for _, title := range []any{"Релиз [SDK] | install", "", 42} {
		writeNativeMetadata(t, index, indexEntry(t, nativeTestID, "Old name")+
			indexEntry(t, nativeTestID, title)+indexEntry(t, "other-id", "Wrong thread"))
		want, _ := title.(string)
		if got := NativeTitle("codex", nativeTestID, ""); got != want {
			t.Fatalf("latest title = %q, want %q", got, want)
		}
	}
}

// Regression: a root id on a child hook or a remote rollout must not acquire
// the local root's title. Valid local rollouts still resolve through CODEX_HOME.
func TestCodexNativeTitleRejectsForeignRollout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeNativeMetadata(t, filepath.Join(home, "session_index.jsonl"), indexEntry(t, nativeTestID, "Native name"))
	for _, path := range []string{
		filepath.Join(t.TempDir(), "rollout-"+nativeTestID+".jsonl"),
		filepath.Join(home, "sessions", "rollout-child-id.jsonl"),
		"relative/rollout-" + nativeTestID + ".jsonl",
	} {
		if got := NativeTitle("codex", nativeTestID, path); got != "" {
			t.Fatalf("foreign rollout %q resolved as %q", path, got)
		}
	}
	path := filepath.Join(home, "sessions", "2026", "10", "rollout-2026-10-03-"+nativeTestID+".jsonl")
	if got := NativeTitle("codex", nativeTestID, path); got != "Native name" {
		t.Fatalf("local rollout title = %q", got)
	}
}

// Regression: large/corrupt metadata must not block hooks or cause a partial
// line to masquerade as a title. Only complete records within the IO budget count.
func TestCodexNativeTitleBoundedFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	path := filepath.Join(home, "session_index.jsonl")
	for _, content := range []string{
		"{broken",
		indexEntry(t, nativeTestID, strings.Repeat("x", maxNativeTitleBytes+1)),
		indexEntry(t, nativeTestID, "Broken\x00title"),
		indexEntry(t, nativeTestID, "Too old") + strings.Repeat("x", indexReadBytes+1),
	} {
		writeNativeMetadata(t, path, content)
		if got := NativeTitle("codex", nativeTestID, ""); got != "" {
			t.Fatalf("invalid metadata resolved as %q", got)
		}
	}
	writeNativeMetadata(t, path, strings.Repeat("x", indexReadBytes+1)+"\n"+indexEntry(t, nativeTestID, "Tail rename"))
	if got := NativeTitle("codex", nativeTestID, ""); got != "Tail rename" {
		t.Fatalf("tail title = %q", got)
	}
	if got := NativeTitle("codex", "unknown", ""); got != "" {
		t.Fatalf("unknown id resolved as %q", got)
	}
}

// Regression: the default Codex home must work without accidentally selecting
// a global index when a custom home is configured or missing.
func TestCodexNativeTitleDefaultAndMissingHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CODEX_HOME", "")
	writeNativeMetadata(t, filepath.Join(home, ".codex", "session_index.jsonl"), indexEntry(t, nativeTestID, "Default home"))
	if got := NativeTitle("codex", nativeTestID, ""); got != "Default home" {
		t.Fatalf("default title = %q", got)
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	if got := NativeTitle("codex", nativeTestID, ""); got != "" {
		t.Fatalf("missing custom index fell back to another home: %q", got)
	}
}

func claudeTitleEntry(t *testing.T, kind, id, title string) string {
	t.Helper()
	field := "customTitle"
	if kind == "ai-title" {
		field = "aiTitle"
	}
	line, err := json.Marshal(map[string]string{"type": kind, "sessionId": id, field: title})
	if err != nil {
		t.Fatal(err)
	}
	return string(line) + "\n"
}

// Regression: generated names must not overwrite custom names, stale renames
// must not stick, and tool input with title-shaped fields must not be trusted.
func TestClaudeNativeTitleMetadataPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), nativeTestID+".jsonl")
	content := claudeTitleEntry(t, "ai-title", nativeTestID, "Generated title") +
		claudeTitleEntry(t, "custom-title", nativeTestID, "Old custom") +
		strings.Repeat("{\"type\":\"assistant\"}\n", 7000) +
		claudeTitleEntry(t, "custom-title", nativeTestID, "Fix [SDK] | release") +
		claudeTitleEntry(t, "custom-title", "other-id", "Wrong session") +
		claudeTitleEntry(t, "assistant", nativeTestID, "Tool input impostor")
	writeNativeMetadata(t, path, content)
	if got := NativeTitle("claude", nativeTestID, path); got != "Fix [SDK] | release" {
		t.Fatalf("custom title = %q", got)
	}
	writeNativeMetadata(t, path, content+claudeTitleEntry(t, "custom-title", nativeTestID, ""))
	if got := NativeTitle("claude", nativeTestID, path); got != "Generated title" {
		t.Fatalf("cleared custom title = %q", got)
	}
}

// Regression: unrelated transcripts, assistant summaries, oversized titles,
// broken metadata, and symlinked files must gracefully retain the old label.
func TestClaudeNativeTitleUnsupportedMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, nativeTestID+".jsonl")
	for _, content := range []string{
		"{broken",
		claudeTitleEntry(t, "custom-title", "other-id", "Wrong session"),
		claudeTitleEntry(t, "custom-title", nativeTestID, strings.Repeat("x", maxNativeTitleBytes+1)),
		`{"type":"summary","sessionId":"` + nativeTestID + `","summary":"Assistant summary"}`,
	} {
		writeNativeMetadata(t, path, content)
		if got := NativeTitle("claude", nativeTestID, path); got != "" {
			t.Fatalf("unsupported metadata resolved as %q", got)
		}
	}
	writeNativeMetadata(t, path, claudeTitleEntry(t, "custom-title", nativeTestID, "Native"))
	if got := NativeTitle("claude", "other-id", path); got != "" {
		t.Fatalf("mismatched transcript resolved as %q", got)
	}
	link := filepath.Join(t.TempDir(), nativeTestID+".jsonl")
	if err := os.Symlink(path, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if got := NativeTitle("claude", nativeTestID, link); got != "" {
		t.Fatalf("symlink resolved as %q", got)
	}
}
