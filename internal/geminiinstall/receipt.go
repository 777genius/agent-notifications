package geminiinstall

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/geminihooks"
)

const receiptName = "gemini-receipt.json"

// receipt records only our contract, binding and native group digests. Native
// settings, native event payloads and foreign settings never enter this file.
type receipt struct {
	Version      int                    `json:"version"`
	SettingsPath string                 `json:"settingsPath"`
	Binding      string                 `json:"binding"`
	Shell        geminihooks.Shell      `json:"shell"`
	Specs        []geminihooks.HookSpec `json:"specs"`
	Owned        *geminihooks.Receipt   `json:"owned"`
}

func command(binary, root, binding string) []string {
	return []string{binary, "gemini-event", "--control-root", root, "--binding", binding}
}

func hookSpecs(binary, root, binding string) []geminihooks.HookSpec {
	return []geminihooks.HookSpec{
		{Event: "AfterAgent", Name: "agent-notifications-gemini-after-agent",
			Argv: append(command(binary, root, binding), "--event", "AfterAgent"), Timeout: 5000},
		{Event: "Notification", Name: "agent-notifications-gemini-notification", Matcher: "ToolPermission",
			Argv: append(command(binary, root, binding), "--event", "Notification"), Timeout: 5000},
	}
}

// readBounded binds opened regular-file bytes to the existing no-follow kernel
// fingerprint. An empty existing file remains distinct from an absent file.
func readBounded(path string, limit int64) ([]byte, installruntime.Identity, error) {
	if info, err := os.Lstat(path); err == nil && info.Size() > limit {
		return nil, installruntime.Identity{}, errors.New("file exceeded limit")
	}
	before, err := installruntime.Fingerprint(path)
	if err != nil || !before.Exists {
		return nil, before, err
	}
	if before.Link != "" {
		return nil, before, errors.New("symbolic link is not a settings or receipt file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, before, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, before, errors.New("bounded regular file required")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, before, errors.New("file read failed or exceeded limit")
	}
	sum := sha256.Sum256(data)
	after, err := installruntime.Fingerprint(path)
	if err != nil || after != before || hex.EncodeToString(sum[:]) != before.SHA256 ||
		installruntime.IdentityMode(uint32(info.Mode().Perm())) != before.Mode {
		return nil, before, errors.New("file changed while reading")
	}
	return data, before, nil
}

func readReceipt(root string, l installruntime.Ledger) (receipt, []byte, error) {
	var r receipt
	c, registered := l.Consumers[consumerID]
	path := filepath.Join(root, receiptName)
	if !registered || c.Registration != path {
		return r, nil, errors.New("Gemini receipt registration is unavailable")
	}
	data, observed, err := readBounded(path, 16<<10)
	owned, ok := installruntime.OwnedFile(l, path)
	if err != nil || !ok || !observed.Exists || observed != owned {
		return r, nil, errors.New("Gemini receipt is missing or changed")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || r.Version != 1 || !filepath.IsAbs(r.SettingsPath) || filepath.Base(r.SettingsPath) != "settings.json" ||
		r.Binding == "" || len(r.Binding) > 128 || (r.Shell != geminihooks.Bash && r.Shell != geminihooks.PowerShell) ||
		r.Owned == nil || len(r.Owned.Groups) != 2 || len(c.Commands) == 0 ||
		!reflect.DeepEqual(c.Commands, command(c.Commands[0], root, r.Binding)) ||
		!reflect.DeepEqual(r.Specs, hookSpecs(c.Commands[0], root, r.Binding)) {
		return r, nil, errors.New("Gemini receipt contract is invalid")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return r, nil, errors.New("Gemini receipt has trailing data")
	}
	for i, group := range r.Owned.Groups {
		if group.Event != r.Specs[i].Event || group.Name != r.Specs[i].Name {
			return r, nil, errors.New("Gemini receipt selectors changed")
		}
	}
	return r, data, nil
}

func receiptBytes(r receipt) ([]byte, error) {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil || len(data) >= 16<<10 {
		return nil, errors.New("Gemini receipt exceeded its size limit")
	}
	return append(data, '\n'), nil
}
