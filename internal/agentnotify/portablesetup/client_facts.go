package portablesetup

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/777genius/agent-notifications/install/uapinstaller"
)

const clientFactsFile = "client-facts.json"

type clientFact struct {
	BindingID  string `json:"binding_id"`
	ConfigRoot string `json:"config_root"`
	Executable string `json:"executable"`
}

func clientFactsPath(dataRoot string) string { return filepath.Join(dataRoot, clientFactsFile) }

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

func (m Materializer) knownTargets(installationID, selected string) ([]uapinstaller.TargetFacts, error) {
	if installationID == "" {
		return nil, nil
	}
	state, err := m.Store.Load()
	if err != nil {
		return nil, err
	}
	installation, ok := findInstallation(state, installationID)
	if !ok {
		return nil, nil
	}
	facts, err := readClientFacts(filepath.Dir(m.Roots.StateFile))
	if err != nil {
		return nil, err
	}
	var out []uapinstaller.TargetFacts
	for _, binding := range installation.Clients {
		if binding.ClientID == selected {
			continue
		}
		fact, ok := facts[binding.ClientID]
		if !ok || fact.BindingID != binding.ClientBindingID {
			continue
		}
		if info, err := os.Stat(fact.ConfigRoot); err != nil || !info.IsDir() {
			continue
		}
		if m.Roots.RequireLiveProfiles {
			receipt := installation.DataReceipts[binding.DataReceiptID]
			if _, err := os.Stat(filepath.Join(receipt.Locator, "live-profiles.json")); err != nil {
				continue
			}
		}
		out = append(out, uapinstaller.TargetFacts{ClientID: binding.ClientID, BindingID: binding.ClientBindingID, ConfigRoot: fact.ConfigRoot, Executable: fact.Executable})
	}
	return out, nil
}
