package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strings"

	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/strictjson"
)

const maxBootstrapIntent = 64 << 10

// Release builds may set this from the exact source commit via -X. VCS metadata
// remains the source authority for ordinary builds. Never infer it from a tag.
var selectorSourceCommit string

type selectorProvenance struct {
	Version, SourceCommit, SHA256 string
	Stage                         []byte
}

func currentSelectorProvenance() (selectorProvenance, error) {
	p := selectorProvenance{Version: version, SourceCommit: selectorSourceCommit}
	if p.SourceCommit == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				if s.Key == "vcs.revision" {
					p.SourceCommit = s.Value
				}
			}
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return p, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return p, err
	}
	identity, err := installruntime.Fingerprint(exe)
	if err != nil {
		return p, err
	}
	p.SHA256 = identity.SHA256
	p.Stage = []byte(filepath.Dir(exe))
	return p, nil
}
func validSelectorProvenance(p selectorProvenance) bool {
	return p.Version != "" && len(p.Version) <= 128 && validSelectorHex(p.SourceCommit, 40) && validSelectorHex(p.SHA256, 64) && validProductPath(string(p.Stage))
}
func validSelectorHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}

// Open the existing physical private stage. Never mkdir a new authorization
// namespace, follow a leaf symlink, or overwrite an immutable existing intent.
func openIntentStage(path string, p selectorProvenance) (*os.Root, string, error) {
	if !validProductPath(path) || !validSelectorProvenance(p) || filepath.Dir(path) != string(p.Stage) {
		return nil, "", errors.New("intent must belong to the same verified helper private stage")
	}
	parent := filepath.Dir(path)
	physical, err := installruntime.CanonicalPath(parent)
	if err != nil || physical != parent {
		return nil, "", errors.New("physical intent stage required")
	}
	if err := installruntime.CheckPrivateControlRoot(parent); err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(parent)
	return root, filepath.Base(path), err
}

func writeBootstrapIntent(path string, i confirmedBootstrapIntent) error {
	if err := validateBootstrapIntent(i); err != nil {
		return err
	}
	data, err := json.Marshal(i)
	if err != nil {
		return err
	}
	if len(data) > maxBootstrapIntent {
		return errors.New("intent budget exceeded")
	}
	root, leaf, err := openIntentStage(path, i.Provenance)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	if _, err := root.Lstat(leaf); !os.IsNotExist(err) {
		return errors.New("intent already exists or cannot be observed")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temp := ".selector-intent-" + hex.EncodeToString(nonce[:])
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temp) }()
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return err
	}
	// Link is an atomic no-replace publication. Removing the temporary name
	// leaves one immutable regular leaf; concurrent publication cannot clobber it.
	if err := root.Link(temp, leaf); err != nil {
		return err
	}
	if err := root.Remove(temp); err != nil {
		return errors.Join(err, root.Remove(leaf))
	}
	return nil
}

func loadBootstrapIntent(path string, p selectorProvenance) (confirmedBootstrapIntent, error) {
	var i confirmedBootstrapIntent
	root, leaf, err := openIntentStage(path, p)
	if err != nil {
		return i, err
	}
	defer func() { _ = root.Close() }()
	// Use the kernel's held-handle private-file checks: Unix owner/mode and
	// Windows owner/DACL, rather than interpreting Windows synthetic mode bits.
	data, err := readBootstrapIntentDocument(string(p.Stage), root, leaf)
	if err != nil {
		return i, err
	}
	if strictjson.Validate(data, strictjson.Budget{Bytes: maxBootstrapIntent, Depth: 16, Entries: 256}) != nil {
		return i, errors.New("invalid bounded intent JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&i); err != nil {
		return i, err
	}
	// Exact schema shape rejects case aliases and omitted required fields that
	// encoding/json alone would silently accept as zero values.
	canonical, err := json.Marshal(i)
	if err != nil {
		return i, err
	}
	var actual, expected any
	if json.Unmarshal(data, &actual) != nil || json.Unmarshal(canonical, &expected) != nil || !reflect.DeepEqual(actual, expected) {
		return i, errors.New("invalid intent schema shape")
	}
	if !reflect.DeepEqual(i.Provenance, p) {
		return i, errors.New("verified helper provenance differs")
	}
	return i, validateBootstrapIntent(i)
}

func validateBootstrapIntent(i confirmedBootstrapIntent) error {
	invalid := func() error { return errors.New("invalid confirmed intent") }
	if err := validateBootstrapEffectRequest(i.Request); err != nil {
		return err
	}
	if i.Schema != 1 || !validSelectorProvenance(i.Provenance) || i.Request.Operation != "confirm" || i.Request.Mode != "" || i.Request.IntentFile != "" || len(i.Request.Scopes) != 0 {
		return invalid()
	}
	products, err := parseProductCSV(strings.Join(i.Request.Products, ","))
	if err != nil || !reflect.DeepEqual(products, i.Request.Products) || len(i.Units) != len(products) {
		return invalid()
	}
	for key, value := range i.Scopes {
		if !containsProduct(intentScalarKeys, key) || !validProductPath(string(value)) {
			return invalid()
		}
	}
	for _, key := range []string{"home", "control-root", "global-config"} {
		if len(i.Scopes[key]) == 0 {
			return invalid()
		}
	}
	if len(i.MCP.Selected) > 2 || len(i.MCP.Skipped) > 2 || len(i.MCP.Projection.Bindings) > 2 || len(i.MCP.Projection.Direct) > 2 {
		return invalid()
	}
	seen := map[string]bool{}
	for id, profile := range i.MCP.Projection.Profiles {
		key := "claude-config"
		if id == "codex" {
			key = "codex-home"
		}
		if id == "cursor" {
			key = "scope-root"
		}
		if id != "claude" && id != "codex" && id != "cursor" || !containsProduct(products, id) || !bytes.Equal(profile, i.Scopes[key]) {
			return invalid()
		}
	}
	bindingIDs := map[string]bool{}
	for _, b := range i.MCP.Projection.Bindings {
		if !containsProduct(products, b.Client) || b.ID == "" || bindingIDs[b.ID] || len(b.ID) > 256 || len(b.Target) > 4096 || len(b.DataRoot) > 4096 || len(b.Profile) > 4096 {
			return invalid()
		}
		bindingIDs[b.ID] = true
	}
	directIDs := map[string]bool{}
	for _, d := range i.MCP.Projection.Direct {
		if d.ID == "" || directIDs[d.ID] || len(d.ID) > 256 || len(d.Config) > 4096 || len(d.RuntimeRoot) > 4096 || len(d.Commands) > 2 {
			return invalid()
		}
		directIDs[d.ID] = true
		for _, command := range d.Commands {
			if len(command) > 4096 {
				return invalid()
			}
		}
	}
	for _, ids := range [][]string{i.MCP.Selected, i.MCP.Skipped} {
		for _, id := range ids {
			if id != "claude" && id != "codex" && id != "cursor" || !containsProduct(products, id) || seen[id] {
				return invalid()
			}
			seen[id] = true
		}
	}
	for n, id := range products {
		key := id + "-config-dir"
		switch id {
		case "claude":
			key = "claude-config"
		case "codex":
			key = "codex-home"
		case "gemini":
			key = "gemini-config-root"
		case "cursor":
			key = "scope-root"
		}
		executable := id + "-executable"
		if id == "cursor" {
			executable = "client-executable"
		}
		if len(i.Scopes[key]) == 0 || len(i.Scopes[executable]) == 0 {
			return invalid()
		}
		u := i.Units[n]
		portable := id == "claude" || id == "codex" || id == "cursor"
		if u.Product != id || u.Hooks != (portable && id != "cursor") || u.Native == portable || u.MCP != containsProduct(i.MCP.Selected, id) || u.Skill != u.MCP || u.PreservedOff != containsProduct(i.MCP.Skipped, id) || u.Desktop != ((!portable || id == "cursor") && i.Request.Desktop) || u.Webhook != ((!portable || id == "cursor") && i.Request.Webhook) {
			return invalid()
		}
	}
	return nil
}

func removeBootstrapIntent(path string, p selectorProvenance) error {
	root, leaf, err := openIntentStage(path, p)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	err = root.Remove(leaf)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}
func writeIntentScalars(out io.Writer, i confirmedBootstrapIntent) error {
	var b bytes.Buffer
	for _, key := range intentScalarKeys {
		if value, ok := i.Scopes[key]; ok {
			b.WriteString(key)
			b.WriteByte(0)
			b.Write(value)
			b.WriteByte(0)
		}
	}
	if b.Len() > maxBootstrapIntent {
		return fmt.Errorf("intent scalar budget exceeded")
	}
	n, err := out.Write(b.Bytes())
	if err == nil && n != b.Len() {
		err = io.ErrShortWrite
	}
	return err
}

func validateBootstrapEffectRequest(r setupProductsArgs) error {
	args := []string{"confirm", "--products", strings.Join(r.Products, ",")}
	// Frozen scope bytes are validated separately; reparse only effect grammar.
	if containsProduct(r.Products, "cursor") {
		args[0] = "preflight"
		args = append(args, "--intent-file", filepath.Join(os.TempDir(), "TEST-intent"))
	}
	if r.AgentNotify {
		args = append(args, "--agent-notify")
	}
	if r.SkipAgentNotify {
		args = append(args, "--skip-agent-notify")
	}
	if r.Desktop {
		args = append(args, "--desktop")
	}
	if r.Webhook {
		args = append(args, "--webhook")
	}
	c := r.Configure
	if c.Route != nil {
		route := c.Route
		if route.LocalRouting {
			args = append(args, "--app", route.ApplicationPath, "--team-id", route.TeamID)
		} else {
			if route.ApplicationPath != "" || route.TeamID != "" {
				return errors.New("invalid frozen route")
			}
			args = append(args, "--navigation", "none")
		}
		args = append(args, "--allow-unknown-caller", fmt.Sprint(route.AllowUnknownCaller), "--allow-caller-asserted", fmt.Sprint(route.AllowCallerAsserted))
		if c.PreservePolicy {
			args = append(args, "--preserve-policy")
		}
		if c.RequestPermission {
			args = append(args, "--request-permission")
		}
	}
	normalized, err := parseSetupProducts(args)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(normalized.Configure, r.Configure) {
		return errors.New("invalid frozen normalized configure request")
	}
	return nil
}
