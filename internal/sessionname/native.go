package sessionname

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	indexReadBytes      = 1024 * 1024
	transcriptReadBytes = 64 * 1024
	maxNativeTitleBytes = 4096
)

// NativeTitle reads only exact-session metadata on the hook's own host. Native
// on-disk formats are best effort: absent or unsupported metadata returns an
// empty title so callers retain the deterministic session label. No caching
// allows a rename to become visible on the next hook.
func NativeTitle(product, sessionID, transcriptPath string) string {
	if !safeSessionID(sessionID) {
		return ""
	}
	switch product {
	case "codex":
		return codexNativeTitle(sessionID, transcriptPath)
	case "claude":
		return claudeNativeTitle(sessionID, transcriptPath)
	default:
		return ""
	}
}

func CleanNativeTitle(title string) string {
	if len(title) > maxNativeTitleBytes || !utf8.ValidString(title) {
		return ""
	}
	for _, c := range title {
		if unicode.IsControl(c) && !unicode.IsSpace(c) {
			return ""
		}
	}
	return strings.Join(strings.Fields(title), " ")
}

func safeSessionID(id string) bool {
	if id == "" || id == "unknown" || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

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

func claudeNativeTitle(id, transcript string) string {
	if !filepath.IsAbs(transcript) || filepath.Base(transcript) != id+".jsonl" ||
		strings.Contains(filepath.ToSlash(transcript), "/subagents/") {
		return ""
	}
	f, size := openMetadata(transcript)
	if f == nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	head := readWindow(f, size, transcriptReadBytes, false)
	windows := [][]byte{head}
	if size > transcriptReadBytes {
		windows = append(windows, readWindow(f, size, transcriptReadBytes, true))
	}
	var custom, generated string
	for _, window := range windows {
		for _, line := range bytes.Split(window, []byte{'\n'}) {
			var record struct {
				Type      string          `json:"type"`
				SessionID string          `json:"sessionId"`
				Custom    json.RawMessage `json:"customTitle"`
				Generated json.RawMessage `json:"aiTitle"`
			}
			if json.Unmarshal(line, &record) != nil || record.SessionID != id {
				continue
			}
			var title string
			switch record.Type {
			case "custom-title":
				_ = json.Unmarshal(record.Custom, &title)
				custom = CleanNativeTitle(title)
			case "ai-title":
				_ = json.Unmarshal(record.Generated, &title)
				generated = CleanNativeTitle(title)
			}
		}
	}
	if custom != "" {
		return custom
	}
	return generated
}

// Refuse nonregular files before opening, including symlinks and named pipes.
func openMetadata(path string) (*os.File, int64) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, 0
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		_ = f.Close()
		return nil, 0
	}
	return f, opened.Size()
}

// readWindow performs bounded IO and discards lines cut at a window boundary.
func readWindow(f *os.File, size int64, limit int, tail bool) []byte {
	start := int64(0)
	if tail && size > int64(limit) {
		start = size - int64(limit)
	}
	count := size - start
	if count > int64(limit) {
		count = int64(limit)
	}
	if count <= 0 {
		return nil
	}
	data := make([]byte, int(count))
	n, err := f.ReadAt(data, start)
	if err != nil && err != io.EOF {
		return nil
	}
	data = data[:n]
	if start > 0 {
		var previous [1]byte
		if _, err := f.ReadAt(previous[:], start-1); err != nil {
			return nil
		}
		if previous[0] != '\n' {
			if boundary := bytes.IndexByte(data, '\n'); boundary >= 0 {
				data = data[boundary+1:]
			} else {
				return nil
			}
		}
	}
	if start+int64(n) < size {
		if boundary := bytes.LastIndexByte(data, '\n'); boundary >= 0 {
			data = data[:boundary+1]
		} else {
			return nil
		}
	}
	return data
}
