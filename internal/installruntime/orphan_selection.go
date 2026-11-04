package installruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/777genius/agent-notifications/internal/codexcommand"
	"github.com/777genius/agent-notifications/internal/strictjson"
)

// orphanSelection is pure: replay derives the permitted delta independently of
// any checksummed path list. Unknown private reference codecs fail closed.
func orphanSelection(root string, l Ledger, req OrphanRecoveryRequest) ([]string, []string, error) {
	if err := validateOrphanRequest(req); err != nil {
		return nil, nil, err
	}
	if l.ID != req.InstallationID || l.Generation != req.ExpectedGeneration || l.Owner != "existing-installer" || !acceptedLedgerSchema(l.Schema) || l.WriterFloor < 0 || l.WriterFloor > SupportedWriterFloor || l.Generation == ^uint64(0) || l.PolicyGeneration == 0 || l.PolicyGeneration == ^uint64(0) || l.PendingMutation != nil {
		return nil, nil, orphanConflict("installation_fence", root, fmt.Errorf("owner, generation, protocol or reservation conflict"))
	}
	c, ok := l.Consumers[req.ConsumerID]
	if !ok || !reflect.DeepEqual(c, req.Consumer) || c.OpenCode != nil || len(l.Consumers) < 2 || !cleanPhysicalPath(l.RuntimeRoot) || orphanOverlap(l.RuntimeRoot, req.RuntimeRoot) || orphanOverlap(root, req.RuntimeRoot) {
		return nil, nil, orphanConflict("consumer_fence", req.RuntimeRoot, fmt.Errorf("observed consumer differs, is primary/final, private, or overlaps control"))
	}
	wrappers, err := orphanCommandPaths(req.ConsumerID, c)
	if err != nil {
		return nil, nil, err
	}
	if c.Registration != "" {
		if !cleanPhysicalPath(c.Registration) || orphanOverlap(root, c.Registration) || orphanOverlap(l.RuntimeRoot, c.Registration) {
			return nil, nil, orphanConflict("registration", c.Registration, fmt.Errorf("unsupported registration namespace"))
		}
		if _, tracked := l.Files[c.Registration]; tracked && !pathWithinRoot(req.RuntimeRoot, c.Registration) {
			return nil, nil, orphanConflict("registration", c.Registration, fmt.Errorf("external registration is a managed file"))
		}
	}
	for id, retained := range l.Consumers {
		if id == req.ConsumerID {
			continue
		}
		if !cleanPhysicalPath(retained.RuntimeRoot) || orphanOverlap(req.RuntimeRoot, retained.RuntimeRoot) || c.Registration != "" && orphanOverlap(c.Registration, retained.RuntimeRoot) || retained.OpenCode != nil {
			return nil, nil, orphanConflict("shared_ownership", retained.RuntimeRoot, fmt.Errorf("overlapping or private retained consumer"))
		}
		paths, err := orphanRetainedPaths(root, l, id, retained)
		if err != nil {
			return nil, nil, orphanConflict("shared_reference", retained.RuntimeRoot, err)
		}
		for _, path := range paths {
			if orphanOverlap(req.RuntimeRoot, path) || c.Registration != "" && orphanOverlap(c.Registration, path) {
				return nil, nil, orphanConflict("shared_reference", path, fmt.Errorf("retained reference overlaps selected namespace"))
			}
		}
	}
	var selected []string
	for path, id := range l.Files {
		if !cleanPhysicalPath(path) || !meaningfulOrphanIdentity(id) {
			return nil, nil, orphanConflict("file_identity", path, fmt.Errorf("invalid managed identity"))
		}
		if path == req.RuntimeRoot {
			return nil, nil, orphanConflict("file_namespace", path, fmt.Errorf("runtime is a tracked file"))
		}
		if pathWithinRoot(req.RuntimeRoot, path) {
			selected = append(selected, path)
		}
	}
	if len(selected) == 0 || len(selected) > maxOrphanPaths {
		return nil, nil, orphanConflict("file_set", req.RuntimeRoot, fmt.Errorf("empty or oversized orphan file set"))
	}
	sort.Strings(selected)
	if runtime.GOOS == "windows" {
		seen := map[string]bool{}
		for _, path := range selected {
			key := strings.ToLower(path)
			if seen[key] {
				return nil, nil, orphanConflict("file_set", path, fmt.Errorf("case-alias orphan file paths"))
			}
			seen[key] = true
		}
	}
	return selected, wrappers, nil
}

// Only retained consumers use these codecs. Selected-consumer recovery remains
// limited to the existing absence/registration contract, and replay derives the
// same references from its complete before-image through orphanSelection.
func orphanRetainedPaths(root string, l Ledger, id string, c Consumer) ([]string, error) {
	if strings.HasPrefix(id, "portable:") {
		return orphanPortablePaths(root, l, id, c)
	}
	var paths []string
	if c.Registration != "" {
		if !cleanPhysicalPath(c.Registration) {
			return nil, fmt.Errorf("unsupported retained registration")
		}
		paths = append(paths, c.Registration)
	}
	if id == openCodeConsumer {
		// Legacy OpenCode stores argv, not a list of paths. Private modern
		// registrations remain excluded by orphanSelection's ownership fence.
		if c.OpenCode != nil || len(c.Commands) != 4 || c.Commands[1] != "opencode-event" || c.Commands[2] != "--protocol" || c.Commands[3] != "1" ||
			c.Registration == "" || filepath.Base(c.Registration) != "agent-notifications.js" || !orphanOwnedExecutable(l, c.RuntimeRoot, c.Commands[0]) {
			return nil, fmt.Errorf("unsupported legacy OpenCode references")
		}
		binary := "claude-notifications-" + runtime.GOOS + "-" + runtime.GOARCH
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
		if c.Commands[0] != filepath.Join(c.RuntimeRoot, binary) {
			return nil, fmt.Errorf("unsupported legacy OpenCode executable")
		}
		return append(paths, c.Commands[0]), nil
	}
	commands, err := orphanCommandPaths(id, c)
	return append(paths, commands...), err
}

func orphanOwnedExecutable(l Ledger, root, path string) bool {
	id, owned := l.Files[path]
	return owned && meaningfulOrphanIdentity(id) && id.Link == "" && cleanPhysicalPath(path) && pathWithinRoot(root, path) &&
		(id.Mode&0111 != 0 || runtime.GOOS == "windows" && filepath.Ext(path) == ".exe")
}

// A bounded structural projection of portable's immutable v1 wire identity.
// portable imports installruntime, so the kernel cannot import its Binding.
// Exact fields/canonical bytes/hash prevent unknown references being discarded;
// this projection grants no portable setup or delivery authority.
type orphanPortableBinding struct {
	Version        int    `json:"version"`
	Integration    string `json:"integration"`
	InstallationID string `json:"installationID"`
	BindingID      string `json:"bindingID"`
	ScopeID        string `json:"scopeID"`
	ComponentID    string `json:"componentID"`
	Owner          string `json:"owner"`
	ScopeRoot      string `json:"scopeRoot"`
	DataRoot       string `json:"dataRoot"`
	ControlRoot    string `json:"controlRoot"`
	GlobalConfig   string `json:"globalConfig"`
	RuntimeRoot    string `json:"runtimeRoot"`
	Primary        string `json:"primary"`
}

var orphanPortableID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$`)

func orphanPortablePaths(root string, l Ledger, key string, c Consumer) ([]string, error) {
	raw := []byte(c.Registration)
	if strictjson.Validate(raw, strictjson.Budget{Bytes: 16384, Depth: 2, Entries: 13}) != nil || exactOrphanJSON(raw, reflect.TypeOf(orphanPortableBinding{})) != nil {
		return nil, fmt.Errorf("unsupported portable reference codec")
	}
	var b orphanPortableBinding
	if json.Unmarshal(raw, &b) != nil {
		return nil, fmt.Errorf("invalid portable identity")
	}
	canonical, err := json.Marshal(b)
	sum := sha256.Sum256(raw)
	if err != nil || !bytes.Equal(canonical, raw) || key != "portable:"+hex.EncodeToString(sum[:]) || b.Version != 1 ||
		b.Owner != l.Owner || b.ComponentID != l.ID || b.ControlRoot != root || b.RuntimeRoot != c.RuntimeRoot || len(c.Commands) != 1 {
		return nil, fmt.Errorf("portable identity differs from recorded installation")
	}
	switch b.Integration {
	case "claude", "codex", "copilot-vscode", "cursor":
	default:
		return nil, fmt.Errorf("unsupported portable integration")
	}
	for _, id := range []string{b.InstallationID, b.BindingID, b.ScopeID, b.ComponentID} {
		if !orphanPortableID.MatchString(id) {
			return nil, fmt.Errorf("invalid portable identity field")
		}
	}
	paths := []string{b.ScopeRoot, b.DataRoot, b.ControlRoot, b.GlobalConfig, b.RuntimeRoot}
	for _, path := range paths {
		if !cleanPhysicalPath(path) {
			return nil, fmt.Errorf("invalid portable reference path")
		}
	}
	if b.Primary == "" || len(b.Primary) > 512 || strings.ContainsAny(b.Primary, `\:`) || !filepath.IsLocal(filepath.FromSlash(b.Primary)) {
		return nil, fmt.Errorf("invalid portable primary")
	}
	for _, part := range strings.Split(b.Primary, "/") {
		if !orphanPortableID.MatchString(part) || part == "." || part == ".." {
			return nil, fmt.Errorf("invalid portable primary segment")
		}
	}
	primary := filepath.Join(b.RuntimeRoot, filepath.FromSlash(b.Primary))
	if c.Commands[0] != primary || !orphanOwnedExecutable(l, b.RuntimeRoot, primary) {
		return nil, fmt.Errorf("portable primary differs from owned executable")
	}
	return append(paths, primary), nil
}

func meaningfulOrphanIdentity(id Identity) bool {
	if !id.Exists {
		return false
	}
	if id.Link != "" {
		return id.SHA256 == "" && id.Mode == 0 && len(id.Link) <= 4096 && !strings.ContainsRune(id.Link, 0)
	}
	return ownedTransactionBlobName(id.SHA256) && id.Mode & ^uint32(0777) == 0
}

func orphanCommandPaths(id string, c Consumer) ([]string, error) {
	if strings.HasPrefix(id, "codex:") {
		if c.Registration != filepath.Join(filepath.Dir(c.RuntimeRoot), "hooks.json") || id != "codex:"+c.Registration || filepath.Base(c.RuntimeRoot) != codexcommand.InstallDirName || !reflect.DeepEqual(c.Commands, codexcommand.Commands(c.RuntimeRoot)) {
			return nil, orphanConflict("command_codec", c.RuntimeRoot, fmt.Errorf("codex registration must match the exact generated command set"))
		}
		return []string{filepath.Join(c.RuntimeRoot, "bin", "codex-hook-wrapper.cmd"), filepath.Join(c.RuntimeRoot, "bin", "codex-hook-wrapper.sh")}, nil
	}
	paths := make([]string, 0, len(c.Commands))
	if len(c.Commands) > 64 {
		return nil, fmt.Errorf("too many command references")
	}
	for _, command := range c.Commands {
		if !cleanPhysicalPath(command) || !pathWithinRoot(c.RuntimeRoot, command) {
			return nil, orphanConflict("command_codec", c.RuntimeRoot, fmt.Errorf("unsupported opaque command reference"))
		}
		paths = append(paths, command)
	}
	return paths, nil
}

// Use the existing no-follow descriptor-relative walker and fingerprint. Its
// partial anchor chain identifies the first missing component, not just a leaf
// below it. A new directory at that component invalidates the decision too.
func observeOrphanAbsence(path string) (orphanAbsence, error) {
	a := orphanAbsence{Path: path}
	if !cleanPhysicalPath(path) {
		return a, fmt.Errorf("invalid absence path")
	}
	parents, err := pathAnchors(path, false)
	if err != nil {
		return a, err
	}
	base := filepath.VolumeName(path) + string(filepath.Separator)
	if len(parents) > 0 {
		base = parents[len(parents)-1].Path
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return a, fmt.Errorf("invalid absence chain")
	}
	a.Missing = filepath.Join(base, strings.Split(rel, string(filepath.Separator))[0])
	a.Parents = parents
	got, err := Fingerprint(a.Missing)
	if err != nil {
		return a, err
	}
	if got.Exists {
		return a, fmt.Errorf("owned path or first absent component is present")
	}
	again, err := pathAnchors(path, false)
	if err != nil {
		return a, err
	}
	if !reflect.DeepEqual(parents, again) {
		return a, fmt.Errorf("absence parents changed while observing")
	}
	return a, nil
}

func orphanRuntimePaths(files, wrappers []string) []string {
	set := map[string]bool{}
	for _, paths := range [][]string{files, wrappers} {
		for _, p := range paths {
			set[p] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
