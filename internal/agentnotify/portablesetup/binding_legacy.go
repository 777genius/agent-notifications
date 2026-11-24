package portablesetup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/managedstdio"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/packagesnapshot"

	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/agent-notifications/internal/strictjson"
)

// recoverLegacyUninstallBinding handles only a pre-v1.45 confirmed uninstall
// whose consumer was revoked before its historical binding was frozen. The
// selector comes from an exact UAP-managed package snapshot, not today's
// defaults or a directory search.
func recoverLegacyUninstallBinding(ctx context.Context, id Identity, integration portable.Integration, client domain.ClientBinding, receipt domain.DataReceipt, profile string, ledger installruntime.Ledger) (portable.Binding, error) {
	expected, err := expectedStoredBinding(id, integration, client, receipt)
	if err != nil {
		return portable.Binding{}, err
	}
	variants, err := portable.CommittedBindingVariants(ledger, expected)
	if err != nil || len(variants) != 0 {
		return portable.Binding{}, fmt.Errorf("%w: historical consumer is not exclusively absent", ErrPreflight)
	}
	target, err := readUninstallIntentTarget(ledger, expected, client.DataReceiptID, profile)
	if err != nil {
		return portable.Binding{}, err
	}
	if (target.DataReceiptID != "" && target.DataReceiptID != client.DataReceiptID) || target.OldBinding != nil || target.OldConsumerKey != "" || target.NewBinding != nil || target.NewConsumerKey != "" {
		return portable.Binding{}, fmt.Errorf("%w: legacy uninstall intent identity differs", ErrPreflight)
	}
	selector, err := verifiedProjectedSelector(ctx, integration, client, receipt)
	if err != nil {
		return portable.Binding{}, err
	}
	old, found, err := portable.ReadLocatorForRecovery(expected.DataRoot, selector)
	if err != nil {
		return portable.Binding{}, fmt.Errorf("%w: historical locator is foreign or invalid: %v", ErrPreflight, err)
	}
	if !found {
		// v1.44.1's implicit paths are the only locator-free candidates.
		// Explicit custom paths need the original private locator bytes.
		old = expected
		old.GlobalConfig = physicalRoot(filepath.Join(expected.RuntimeRoot, "global", "config.json"))
		old.Primary = "primary"
		name, nameErr := old.Filename()
		if nameErr != nil || name != selector {
			return portable.Binding{}, fmt.Errorf("%w: historical locator absent and selector is not a known default", ErrPreflight)
		}
	}
	same := expected
	same.GlobalConfig, same.Primary = old.GlobalConfig, old.Primary
	if same != old {
		return portable.Binding{}, fmt.Errorf("%w: historical locator UAP identity differs", ErrPreflight)
	}
	name, err := old.Filename()
	if err != nil || name != selector {
		return portable.Binding{}, fmt.Errorf("%w: historical locator selector differs", ErrPreflight)
	}
	return old, nil
}

func verifiedProjectedSelector(ctx context.Context, integration portable.Integration, client domain.ClientBinding, receipt domain.DataReceipt) (string, error) {
	var digest string
	for _, object := range client.NativeObjects {
		if object.Kind != "managed_package_directory" {
			continue
		}
		if digest != "" || object.Path != client.TargetLocator || object.ProtectionClass != "managed" || object.ManagedDigest == "" || client.PhysicalArtifact == "" || !strings.HasSuffix(object.ObjectID, ":"+client.PhysicalArtifact) || (object.ObjectID != "package:claude:"+client.PhysicalArtifact && object.ObjectID != "package:codex:"+client.PhysicalArtifact) {
			return "", fmt.Errorf("%w: managed package ownership differs", ErrPreflight)
		}
		digest = object.ManagedDigest
	}
	if digest == "" {
		return "", fmt.Errorf("%w: managed package digest missing", ErrPreflight)
	}
	snapshot, err := (packagesnapshot.Builder{}).Build(ctx, client.TargetLocator)
	if err != nil {
		return "", fmt.Errorf("%w: managed projection snapshot: %v", ErrPreflight, err)
	}
	defer func() { _ = snapshot.Close() }()
	if snapshot.Digest != digest {
		return "", fmt.Errorf("%w: managed projection digest differs", ErrPreflight)
	}
	file, err := os.Open(filepath.Join(snapshot.Root, ".mcp.json"))
	if err != nil {
		return "", fmt.Errorf("%w: projected MCP config missing", ErrPreflight)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, 65537))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(data) > 65536 || strictjson.Validate(data, strictjson.Budget{Bytes: 65536, Depth: 8, Entries: 128}) != nil {
		return "", fmt.Errorf("%w: projected MCP config invalid", ErrPreflight)
	}
	var document map[string]json.RawMessage
	if json.Unmarshal(data, &document) != nil {
		return "", fmt.Errorf("%w: projected MCP config invalid", ErrPreflight)
	}
	var servers map[string]json.RawMessage
	if integration == portable.Claude {
		servers = document
	} else if json.Unmarshal(document["mcpServers"], &servers) != nil {
		return "", fmt.Errorf("%w: projected MCP servers invalid", ErrPreflight)
	}
	raw, found := servers[portableServerName]
	_, legacy := servers[legacyPortableServerName]
	if found && legacy {
		return "", fmt.Errorf("%w: projected MCP server declared under both keys", ErrPreflight)
	}
	if !found {
		raw, found = servers[legacyPortableServerName]
	}
	var server map[string]json.RawMessage
	if !found || json.Unmarshal(raw, &server) != nil {
		return "", fmt.Errorf("%w: projected MCP server missing", ErrPreflight)
	}
	var args []string
	if json.Unmarshal(server["args"], &args) != nil {
		return "", fmt.Errorf("%w: projected MCP argv invalid", ErrPreflight)
	}
	var command string
	if json.Unmarshal(server["command"], &command) != nil {
		return "", fmt.Errorf("%w: projected MCP argv differs", ErrPreflight)
	}
	var selectorArgs []string
	switch integration {
	case portable.Claude:
		pluginRoot := filepath.Join(client.TargetLocator, ".agentplugins-runtime")
		if len(args) != 10 || command != filepath.Join(pluginRoot, filepath.FromSlash(managedstdio.RelativeDirectory), managedstdio.ExecutableName) ||
			args[0] != managedstdio.Mode || args[1] != pluginRoot || args[2] != receipt.Locator || args[3] != pluginRoot || args[4] != "plugin" || args[5] != "--" || !strings.HasPrefix(args[6], "./") || args[7] != "portable-launch" {
			return "", fmt.Errorf("%w: projected Claude MCP argv differs", ErrPreflight)
		}
		selectorArgs = args[8:]
	case portable.Codex:
		rel, relErr := filepath.Rel(client.TargetLocator, command)
		if relErr != nil || rel == "." || !filepath.IsLocal(rel) || (len(args) != 3 && len(args) != 5) || args[0] != "portable-launch" {
			return "", fmt.Errorf("%w: projected Codex MCP argv differs", ErrPreflight)
		}
		var cwd string
		var env map[string]string
		if json.Unmarshal(server["cwd"], &cwd) != nil || cwd != client.TargetLocator || json.Unmarshal(server["env"], &env) != nil || env["PLUGIN_DATA"] != receipt.Locator || env["PLUGIN_ROOT"] != client.TargetLocator {
			return "", fmt.Errorf("%w: projected Codex MCP roots differ", ErrPreflight)
		}
		selectorArgs = args[1:]
	default:
		return "", fmt.Errorf("%w: unsupported projected MCP client", ErrPreflight)
	}
	if len(selectorArgs) == 4 && integration == portable.Codex && selectorArgs[0] == "--data-root" && selectorArgs[1] == receipt.Locator {
		selectorArgs = selectorArgs[2:]
	} else if len(selectorArgs) != 2 {
		return "", fmt.Errorf("%w: projected MCP selector argv differs", ErrPreflight)
	}
	selector, err := portable.ParseArgs(selectorArgs)
	if err != nil {
		return "", fmt.Errorf("%w: projected MCP selector invalid", ErrPreflight)
	}
	return selector, nil
}

type legacyUninstallBinding struct {
	identity Identity
	client   domain.ClientBinding
	receipt  domain.DataReceipt
	profile  string
	binding  portable.Binding
}

func (m Materializer) removableBindingForRetry(ctx context.Context, req MaterializeRequest, client domain.ClientBinding, receipt domain.DataReceipt, ledger installruntime.Ledger) (portable.Binding, bool, error) {
	b, err := resolveRemovableBinding(req.Identity, req.Integration, client, receipt, req.ClientConfigRoot, ledger)
	if err != nil {
		b, err = recoverLegacyUninstallBinding(ctx, req.Identity, req.Integration, client, receipt, req.ClientConfigRoot, ledger)
		if err != nil {
			return portable.Binding{}, false, err
		}
	}
	if ledger.PendingMutation == nil {
		return b, false, nil
	}
	expected, err := expectedStoredBinding(req.Identity, req.Integration, client, receipt)
	if err != nil {
		return portable.Binding{}, false, err
	}
	target, err := readUninstallIntentTarget(ledger, expected, client.DataReceiptID, req.ClientConfigRoot)
	if err != nil {
		return portable.Binding{}, false, err
	}
	if target.OldBinding == nil {
		if (target.DataReceiptID != "" && target.DataReceiptID != client.DataReceiptID) || target.OldConsumerKey != "" || target.NewBinding != nil || target.NewConsumerKey != "" {
			return portable.Binding{}, false, fmt.Errorf("%w: incomplete legacy uninstall target", ErrPreflight)
		}
		return b, true, nil
	}
	return b, false, nil
}

// freezeLegacyUninstallBindings persists all recovered identities in one
// reservation commit before any locator or UAP client is changed. The caller
// holds the coordinator lease and has already preflighted every target.
func (m Materializer) freezeLegacyUninstallBindings(ctx context.Context, entries []legacyUninstallBinding) (uint64, error) {
	if len(entries) == 0 {
		return 0, nil
	}
	first := entries[0]
	err := m.Kernel.patchIntentLocked(ctx, first.binding.ControlRoot, first.binding.RuntimeRoot, first.binding.Owner, func(intent *Intent, ledger installruntime.Ledger) (bool, error) {
		if intent.Action != "uninstall" || ledger.PendingMutation == nil {
			return false, fmt.Errorf("%w: legacy uninstall reservation missing", ErrPreflight)
		}
		changed := false
		for _, entry := range entries {
			if entry.binding.ControlRoot != first.binding.ControlRoot || entry.binding.RuntimeRoot != first.binding.RuntimeRoot || entry.binding.Owner != first.binding.Owner {
				return false, fmt.Errorf("%w: mixed legacy uninstall roots", ErrPreflight)
			}
			expected, err := expectedStoredBinding(entry.identity, entry.binding.Integration, entry.client, entry.receipt)
			if err != nil {
				return false, err
			}
			if _, err := readUninstallIntentTarget(ledger, expected, entry.client.DataReceiptID, entry.profile); err != nil {
				return false, err
			}
			key, _, _, err := entry.binding.Registration()
			if err != nil {
				return false, err
			}
			var match *IntentTarget
			for i := range intent.Targets {
				if intent.Targets[i].Client == string(entry.binding.Integration) {
					if match != nil {
						return false, fmt.Errorf("%w: duplicate uninstall target", ErrPreflight)
					}
					match = &intent.Targets[i]
				}
			}
			if match == nil || match.InstallationID != entry.binding.InstallationID || match.BindingID != entry.binding.BindingID || match.Profile != entry.profile || (match.DataReceiptID != "" && match.DataReceiptID != entry.client.DataReceiptID) || match.NewBinding != nil || match.NewConsumerKey != "" {
				return false, fmt.Errorf("%w: legacy uninstall target differs", ErrPreflight)
			}
			if match.OldBinding != nil || match.OldConsumerKey != "" {
				return false, fmt.Errorf("%w: legacy uninstall target changed", ErrPreflight)
			}
			b := entry.binding
			match.DataReceiptID = entry.client.DataReceiptID
			match.OldBinding, match.OldConsumerKey = &b, key
			changed = true
		}
		return changed, nil
	})
	if err != nil {
		return 0, err
	}
	snap, err := installruntime.ReadInstalledSnapshot(first.binding.ControlRoot)
	if err != nil || snap.Recovery {
		return 0, fmt.Errorf("%w: frozen uninstall snapshot unavailable", ErrPreflight)
	}
	return snap.Ledger.Generation, nil
}

func frozenUninstallBinding(ledger installruntime.Ledger, expected portable.Binding, receiptID, profile string) (portable.Binding, error) {
	target, err := readUninstallIntentTarget(ledger, expected, receiptID, profile)
	if err != nil {
		return portable.Binding{}, err
	}
	if target.OldBinding == nil || target.NewBinding != nil || target.NewConsumerKey != "" || target.DataReceiptID != receiptID {
		return portable.Binding{}, fmt.Errorf("%w: uninstall intent target differs", ErrPreflight)
	}
	old := *target.OldBinding
	wantKey, _, _, err := old.Registration()
	if err != nil || target.OldConsumerKey != wantKey {
		return portable.Binding{}, fmt.Errorf("%w: uninstall registration differs", ErrPreflight)
	}
	expected.GlobalConfig, expected.Primary = old.GlobalConfig, old.Primary
	if expected != old {
		return portable.Binding{}, fmt.Errorf("%w: uninstall UAP identity differs", ErrPreflight)
	}
	return old, nil
}

func readUninstallIntentTarget(ledger installruntime.Ledger, expected portable.Binding, receiptID, profile string) (IntentTarget, error) {
	pending := ledger.PendingMutation
	if pending == nil || pending.Owner != expected.Owner || pending.IntentRef != IntentPath(expected.ControlRoot) {
		return IntentTarget{}, fmt.Errorf("%w: uninstall reservation missing", ErrPreflight)
	}
	path := IntentPath(expected.ControlRoot)
	wantFile, ok := ledger.Files[path]
	actualFile, err := installruntime.Fingerprint(path)
	if err != nil || !ok || actualFile != wantFile {
		return IntentTarget{}, fmt.Errorf("%w: uninstall intent file differs", ErrPreflight)
	}
	intent, err := ReadIntent(expected.ControlRoot)
	if err != nil || intent.SetupIntentID != pending.ID || intent.Action != "uninstall" {
		return IntentTarget{}, fmt.Errorf("%w: uninstall intent missing", ErrPreflight)
	}
	var target *IntentTarget
	for i := range intent.Targets {
		if intent.Targets[i].Client != string(expected.Integration) {
			continue
		}
		if target != nil {
			return IntentTarget{}, fmt.Errorf("%w: uninstall intent target ambiguous", ErrPreflight)
		}
		target = &intent.Targets[i]
	}
	if target == nil || target.Client != string(expected.Integration) || target.InstallationID != expected.InstallationID || target.BindingID != expected.BindingID || (target.DataReceiptID != "" && target.DataReceiptID != receiptID) || target.Profile != profile {
		return IntentTarget{}, fmt.Errorf("%w: uninstall intent target differs", ErrPreflight)
	}
	return *target, nil
}
