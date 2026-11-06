package sessionname

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
)

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
