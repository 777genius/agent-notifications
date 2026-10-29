// Package qoderinstall composes UAP native lifecycle with host-owned runtime and consent.
package qoderinstall

import (
	"encoding/json"
	"errors"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"os"
	"path/filepath"
	"strings"
)

const PluginID = "agent-notifications-qoder@local"
const ConsumerID = "qoder-notifications"
const receiptName = "qoder-receipt.json"
const registryName = "plugins/installed_plugins_v2.json"

type document struct {
	data   []byte
	before installruntime.Identity
}

func readDocument(path string) (document, error) {
	before, err := installruntime.Fingerprint(path)
	if err != nil || !before.Exists {
		return document{before: before}, err
	}
	if before.Link != "" {
		return document{}, errors.New("symbolic link refused")
	}
	st, err := os.Lstat(path)
	if err != nil || !st.Mode().IsRegular() || st.Size() > 32<<20 {
		return document{}, errors.New("bounded regular file required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return document{}, err
	}
	after, err := installruntime.Fingerprint(path)
	if err != nil || after != before {
		return document{}, errors.New("file changed while reading")
	}
	return document{data: data, before: before}, nil
}

func object(data []byte) (map[string]json.RawMessage, error) {
	if len(data) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(data, &result) != nil || result == nil {
		return nil, errors.New("invalid native Qoder JSON")
	}
	return result, nil
}
func nativeEntry(registry []byte) (json.RawMessage, error) {
	doc, err := object(registry)
	if err != nil {
		return nil, err
	}
	plugins, err := object(doc["plugins"])
	if err != nil {
		return nil, err
	}
	return plugins[PluginID], nil
}
func nativeEnabled(settings []byte) (json.RawMessage, error) {
	doc, err := object(settings)
	if err != nil {
		return nil, err
	}
	enabled, err := object(doc["enabledPlugins"])
	if err != nil {
		return nil, err
	}
	return enabled[PluginID], nil
}
func compact(data []byte) string {
	var value any
	if json.Unmarshal(data, &value) != nil {
		return ""
	}
	out, _ := json.Marshal(value)
	return string(out)
}

func mustJSON(v any) []byte { data, _ := json.Marshal(v); return append(data, '\n') }
func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
