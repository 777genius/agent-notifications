package portablesetup

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/777genius/agent-notifications/install/uapinstaller"
	"github.com/777genius/agent-notifications/internal/agentnotify/portable"
	"github.com/777genius/agent-notifications/internal/installruntime"
	"github.com/777genius/plugin-kit-ai/install/integrationctl/agentplugins/domain"
)

const clientFactsFile = "client-facts.json"

type clientFact struct {
	BindingID  string `json:"binding_id"`
	ConfigRoot string `json:"config_root"`
	Executable string `json:"executable"`
}

func clientFactsPath(dataRoot string) string { return filepath.Join(dataRoot, clientFactsFile) }

// Match the public SDK's explicit clean-path admission before any host handoff.
// The SDK revalidates facts again under its own mutation lock.
func validObservedFactPath(path string) bool {
	if !explicitAbs(path) || !utf8.ValidString(path) || len(path) > 4096 || strings.ContainsRune(path, 0) {
		return false
	}
	volume := filepath.VolumeName(path)
	return !strings.HasPrefix(volume, `\\`) && !strings.HasPrefix(volume, "//")
}

func readClientFacts(dataRoot string) (map[string]clientFact, error) {
	body, err := os.ReadFile(clientFactsPath(dataRoot))
	if os.IsNotExist(err) {
		return map[string]clientFact{}, nil
	}
	if err != nil {
		return nil, err
	}
	var facts map[string]clientFact
	if err := json.Unmarshal(body, &facts); err != nil {
		return nil, err
	}
	if facts == nil {
		facts = map[string]clientFact{}
	}
	return facts, nil
}

func recordClientFact(dataRoot string, fact clientFact, clientID string) error {
	if dataRoot == "" || clientID == "" {
		return nil
	}
	facts, err := readClientFacts(dataRoot)
	if err != nil {
		return err
	}
	facts[clientID] = fact
	body, err := json.Marshal(facts)
	if err != nil {
		return err
	}
	return os.WriteFile(clientFactsPath(dataRoot), body, 0600)
}

func (m Materializer) knownTargets(installationID string, selected ...string) ([]uapinstaller.TargetFacts, error) {
	return m.readKnownTargets(installationID, "", false, selected...)
}

// Group updates must prove every unselected live binding before host handoff.
// Other paths keep the existing advisory facts behavior.
func (m Materializer) requiredGroupUpdateKnownTargets(installationID, controlRoot string, selected ...string) ([]uapinstaller.TargetFacts, error) {
	return m.readKnownTargets(installationID, controlRoot, true, selected...)
}

func (m Materializer) readKnownTargets(installationID, controlRoot string, requireAll bool, selected ...string) ([]uapinstaller.TargetFacts, error) {
	if installationID == "" {
		return nil, nil
	}
	state, err := m.Store.Load()
	if err != nil {
		return nil, err
	}
	installation, ok := findInstallation(state, installationID)
	if !ok {
		if requireAll {
			return nil, fmt.Errorf("%w: group update installation is not present", ErrPreflight)
		}
		return nil, nil
	}
	facts, err := readClientFacts(filepath.Dir(m.Roots.StateFile))
	if err != nil {
		return nil, err
	}
	var ledger installruntime.Ledger
	if requireAll {
		snapshot, readErr := installruntime.ReadInstalledSnapshot(controlRoot)
		if readErr != nil {
			return nil, readErr
		}
		ledger = snapshot.Ledger
	}
	var out []uapinstaller.TargetFacts
	for _, binding := range installation.Clients {
		if slices.Contains(selected, binding.ClientID) || requireAll && binding.Materialization == domain.MaterializationAbsent {
			continue
		}
		fact, ok := facts[binding.ClientID]
		if !ok || fact.BindingID != binding.ClientBindingID {
			if requireAll {
				return nil, fmt.Errorf("%w: unselected live client %s has missing or stale binding facts", ErrPreflight, binding.ClientID)
			}
			continue
		}
		if requireAll && !validObservedFactPath(fact.ConfigRoot) {
			return nil, fmt.Errorf("%w: unselected live client %s has invalid profile facts", ErrPreflight, binding.ClientID)
		}
		if requireAll {
			cliRequired := (domain.SelectedDelivery{}).EffectiveTraitsForScope(
				domain.ClientID(binding.ClientID), domain.InstallScope(binding.Scope)).LifecycleKind == domain.LifecycleCLIRegistry
			if (fact.Executable == "" && cliRequired) || (fact.Executable != "" && !validObservedFactPath(fact.Executable)) {
				return nil, fmt.Errorf("%w: unselected live client %s has invalid executable facts", ErrPreflight, binding.ClientID)
			}
		}
		info, statErr := os.Stat(fact.ConfigRoot)
		if statErr != nil || !info.IsDir() {
			if requireAll {
				return nil, fmt.Errorf("%w: unselected live client %s profile unavailable", ErrPreflight, binding.ClientID)
			}
			continue
		}
		if requireAll && !matchesRecordedHostProfile(ledger, installationID, binding, info) {
			return nil, fmt.Errorf("%w: unselected live client %s profile differs from recorded binding", ErrPreflight, binding.ClientID)
		}
		if m.Roots.RequireLiveProfiles {
			receipt := installation.DataReceipts[binding.DataReceiptID]
			if _, err := os.Stat(filepath.Join(receipt.Locator, "live-profiles.json")); err != nil {
				if requireAll {
					return nil, fmt.Errorf("%w: unselected live client %s receipt unavailable", ErrPreflight, binding.ClientID)
				}
				continue
			}
		}
		out = append(out, uapinstaller.TargetFacts{ClientID: binding.ClientID, BindingID: binding.ClientBindingID, ConfigRoot: fact.ConfigRoot, Executable: fact.Executable})
	}
	return out, nil
}

// User-scope CLI clients need not persist NativeProfileRoot in UAP. Their
// immutable host consumer registration already binds the selected ScopeRoot.
// Check its exact original physical directory, not only a mutable facts file.
func matchesRecordedHostProfile(ledger installruntime.Ledger, installationID string, binding domain.ClientBinding, factRoot os.FileInfo) bool {
	found := false
	for key, consumer := range ledger.Consumers {
		if !strings.HasPrefix(key, "portable:") {
			continue
		}
		var recorded portable.Binding
		if json.Unmarshal([]byte(consumer.Registration), &recorded) != nil ||
			recorded.InstallationID != installationID || recorded.BindingID != binding.ClientBindingID ||
			ClientID(recorded.Integration) != binding.ClientID {
			continue
		}
		wantKey, _, _, err := recorded.Registration()
		if err != nil || wantKey != key || !portable.ExactCommittedBinding(ledger, recorded) {
			return false
		}
		root, err := os.Stat(recorded.ScopeRoot)
		if err != nil || !root.IsDir() || !os.SameFile(root, factRoot) {
			return false
		}
		found = true
	}
	return found
}
