package sessionname

import (
	"bytes"
	"io"
	"os"
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
