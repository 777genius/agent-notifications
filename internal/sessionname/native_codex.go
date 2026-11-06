package sessionname

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

func codexNativeTitle(id, transcript string) string {
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(userHome, ".codex")
	}
	if !filepath.IsAbs(home) {
		return ""
	}
	// A supplied rollout must belong to this home and this thread. In
	// particular, a child rollout with a root hook session_id must not acquire
	// the root's name, and foreign/remote paths must not query local metadata.
	if transcript != "" {
		rel, err := filepath.Rel(home, transcript)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		if err != nil || !filepath.IsAbs(transcript) || len(parts) < 2 ||
			(parts[0] != "sessions" && parts[0] != "archived_sessions") ||
			!strings.HasSuffix(filepath.Base(transcript), "-"+id+".jsonl") {
			return ""
		}
	}
	f, size := openMetadata(filepath.Join(home, "session_index.jsonl"))
	if f == nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	lines := bytes.Split(readWindow(f, size, indexReadBytes, true), []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		var record struct {
			ID   string          `json:"id"`
			Name json.RawMessage `json:"thread_name"`
		}
		if json.Unmarshal(lines[i], &record) == nil && record.ID == id {
			// The newest exact match wins, including empty/invalid names. A
			// cleared rename must not resurrect an older title.
			var name string
			_ = json.Unmarshal(record.Name, &name)
			return CleanNativeTitle(name)
		}
	}
	return ""
}
